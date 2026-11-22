package platform

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"syscall"
	"time"

	"github.com/buzzit/services/internal/webhook"
)

const maxAttempts = 10

// errPrivate is returned when a webhook resolves to a non-public address.
var errPrivate = errors.New("webhook host resolves to a private address")

// safeClient refuses to connect to loopback, private and link-local
// addresses (SSRF protection), unless allowPrivate is set.
func safeClient(allowPrivate bool) *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	if !allowPrivate {
		dialer.Control = func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip := net.ParseIP(host)
			if ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
				ip.IsUnspecified() || ip.IsMulticast() || ip.Equal(net.IPv4bcast) || isCGNAT(ip) {
				return errPrivate
			}
			return nil
		}
	}
	tr := &http.Transport{
		DialContext:         dialer.DialContext,
		TLSHandshakeTimeout: 5 * time.Second,
		MaxIdleConns:        50,
		IdleConnTimeout:     60 * time.Second,
		// Webhooks must not go through the server's outbound proxy, which
		// would bypass the address check above.
		Proxy: nil,
	}
	return &http.Client{
		Timeout:   10 * time.Second,
		Transport: tr,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse // no redirects
		},
	}
}

func isCGNAT(ip net.IP) bool {
	v4 := ip.To4()
	return v4 != nil && v4[0] == 100 && v4[1]&0xc0 == 64
}

// RunWebhooks delivers queued webhooks until ctx is cancelled.
func (s *Service) RunWebhooks(ctx context.Context) {
	client := safeClient(s.AllowPrivateWebhooks)
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.deliverDue(ctx, client)
		}
	}
}

// DeliverNow runs one delivery pass (tests).
func (s *Service) DeliverNow(ctx context.Context) {
	s.deliverDue(ctx, safeClient(s.AllowPrivateWebhooks))
}

type job struct {
	id, appID, url, secret string
	payload                []byte
	attempts               int
}

func (s *Service) deliverDue(ctx context.Context, client *http.Client) {
	rows, err := s.DB.QueryContext(ctx, `SELECT o.id, o.app_id, a.webhook_url, a.webhook_secret, o.payload, o.attempts
		FROM webhook_outbox o JOIN apps a ON a.id = o.app_id
		WHERE o.status = 'pending' AND o.next_attempt_at <= ? ORDER BY o.next_attempt_at LIMIT 50`, time.Now().UnixMilli())
	if err != nil {
		slog.Error("webhooks: query", "err", err)
		return
	}
	var jobs []job
	for rows.Next() {
		var j job
		var sealed []byte
		if err := rows.Scan(&j.id, &j.appID, &j.url, &j.secret, &sealed, &j.attempts); err != nil {
			continue
		}
		if j.payload, err = s.Box.Open(sealed); err != nil {
			continue
		}
		jobs = append(jobs, j)
	}
	rows.Close()

	for _, j := range jobs {
		err := s.post(ctx, client, j)
		if err == nil {
			_, _ = s.DB.ExecContext(ctx, `UPDATE webhook_outbox SET status = 'delivered', attempts = attempts + 1, last_error = '' WHERE id = ?`, j.id)
			continue
		}
		attempts := j.attempts + 1
		if attempts >= maxAttempts || j.url == "" {
			_, _ = s.DB.ExecContext(ctx, `UPDATE webhook_outbox SET status = 'failed', attempts = ?, last_error = ? WHERE id = ?`, attempts, err.Error(), j.id)
			continue
		}
		// Exponential backoff: 10s, 20s, 40s ... capped at 1h.
		backoff := min(time.Duration(10<<j.attempts)*time.Second, time.Hour)
		_, _ = s.DB.ExecContext(ctx, `UPDATE webhook_outbox SET attempts = ?, last_error = ?, next_attempt_at = ? WHERE id = ?`,
			attempts, err.Error(), time.Now().Add(backoff).UnixMilli(), j.id)
	}
	// Keep the outbox small.
	_, _ = s.DB.ExecContext(ctx, `DELETE FROM webhook_outbox WHERE status <> 'pending' AND created_at < ?`, time.Now().Add(-7*24*time.Hour).UnixMilli())
}

func (s *Service) post(ctx context.Context, client *http.Client, j job) error {
	if j.url == "" {
		return errors.New("no webhook url")
	}
	if err := s.validateWebhook(j.url); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, j.url, bytes.NewReader(j.payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Buzzit-Webhooks/1.0")
	req.Header.Set("Buzzit-Delivery", j.id)
	req.Header.Set(webhook.Header, webhook.Sign([]byte(j.secret), j.payload, time.Now()))
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("endpoint returned %s", resp.Status)
	}
	return nil
}
