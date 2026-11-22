// Package push sends wake-up notifications to devices that are not connected.
// FCM is optional: without it, the Android app keeps its own connection open.
package push

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// Message is a data-only push. Data-only is required so the Android app,
// not the OS, decides how loud to be (alarm stream for buzzes).
type Message struct {
	Data     map[string]string
	High     bool          // high priority (buzzes, messages)
	TTL      time.Duration // drop if undeliverable for this long
	Collapse string        // optional collapse key
}

type Pusher interface {
	// Send delivers to device tokens. It returns tokens the provider reports
	// as invalid, so the caller can forget them.
	Send(ctx context.Context, tokens []string, m Message) (invalid []string)
}

// Noop drops all pushes.
type Noop struct{}

func (Noop) Send(context.Context, []string, Message) []string { return nil }

// FCM sends through the Firebase Cloud Messaging HTTP v1 API using a
// service-account key file.
type FCM struct {
	projectID string
	email     string
	key       *rsa.PrivateKey
	tokenURL  string
	client    *http.Client

	mu     sync.Mutex
	token  string
	expiry time.Time
}

// NewFCMFromFile loads a Google service-account JSON key.
func NewFCMFromFile(path string) (*FCM, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var sa struct {
		ProjectID   string `json:"project_id"`
		ClientEmail string `json:"client_email"`
		PrivateKey  string `json:"private_key"`
		TokenURI    string `json:"token_uri"`
	}
	if err := json.Unmarshal(b, &sa); err != nil {
		return nil, fmt.Errorf("push: service account: %w", err)
	}
	blk, _ := pem.Decode([]byte(sa.PrivateKey))
	if blk == nil {
		return nil, errors.New("push: service account private_key is not PEM")
	}
	k, err := x509.ParsePKCS8PrivateKey(blk.Bytes)
	if err != nil {
		return nil, err
	}
	rk, ok := k.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("push: service account key is not RSA")
	}
	if sa.TokenURI == "" {
		sa.TokenURI = "https://oauth2.googleapis.com/token"
	}
	return &FCM{projectID: sa.ProjectID, email: sa.ClientEmail, key: rk, tokenURL: sa.TokenURI, client: &http.Client{Timeout: 10 * time.Second}}, nil
}

func b64(v []byte) string { return base64.RawURLEncoding.EncodeToString(v) }

func (f *FCM) accessToken(ctx context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.token != "" && time.Until(f.expiry) > time.Minute {
		return f.token, nil
	}
	now := time.Now()
	hdr, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT"})
	claims, _ := json.Marshal(map[string]any{
		"iss": f.email, "scope": "https://www.googleapis.com/auth/firebase.messaging",
		"aud": f.tokenURL, "iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
	})
	unsigned := b64(hdr) + "." + b64(claims)
	sum := sha256.Sum256([]byte(unsigned))
	sig, err := rsa.SignPKCS1v15(nil, f.key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	form := url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"}, "assertion": {unsigned + "." + b64(sig)}}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, f.tokenURL, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := f.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var tr struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return "", fmt.Errorf("push: token exchange: %s: %s", resp.Status, b)
	}
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return "", err
	}
	f.token, f.expiry = tr.AccessToken, now.Add(time.Duration(tr.ExpiresIn)*time.Second)
	return f.token, nil
}

func (f *FCM) Send(ctx context.Context, tokens []string, m Message) []string {
	if len(tokens) == 0 {
		return nil
	}
	at, err := f.accessToken(ctx)
	if err != nil {
		slog.Error("push: fcm auth", "err", err)
		return nil
	}
	priority := "NORMAL"
	if m.High {
		priority = "HIGH"
	}
	ttl := m.TTL
	if ttl == 0 {
		ttl = time.Hour
	}
	var invalid []string
	for _, tok := range tokens {
		android := map[string]any{"priority": priority, "ttl": fmt.Sprintf("%ds", int(ttl.Seconds()))}
		if m.Collapse != "" {
			android["collapse_key"] = m.Collapse
		}
		payload, _ := json.Marshal(map[string]any{"message": map[string]any{"token": tok, "data": m.Data, "android": android}})
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost,
			"https://fcm.googleapis.com/v1/projects/"+f.projectID+"/messages:send", bytes.NewReader(payload))
		req.Header.Set("Authorization", "Bearer "+at)
		req.Header.Set("Content-Type", "application/json")
		resp, err := f.client.Do(req)
		if err != nil {
			slog.Warn("push: fcm send", "err", err)
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		switch {
		case resp.StatusCode == http.StatusOK:
		case resp.StatusCode == http.StatusNotFound || bytes.Contains(body, []byte("UNREGISTERED")):
			invalid = append(invalid, tok)
		default:
			slog.Warn("push: fcm rejected", "status", resp.Status, "body", string(body))
		}
	}
	return invalid
}
