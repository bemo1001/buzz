// Package webhook signs and verifies Buzzit Platform webhook deliveries.
//
// Header format: Buzzit-Signature: t=<unix>,v1=<hex hmac-sha256>
// The signed payload is "<t>.<raw body>".
package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const Header = "Buzzit-Signature"

// DefaultTolerance is how old a signature may be before it is rejected
// (replay protection).
const DefaultTolerance = 5 * time.Minute

var (
	ErrMalformed = errors.New("webhook: malformed signature header")
	ErrMismatch  = errors.New("webhook: signature mismatch")
	ErrExpired   = errors.New("webhook: signature timestamp outside tolerance")
)

func mac(secret []byte, ts int64, body []byte) string {
	h := hmac.New(sha256.New, secret)
	fmt.Fprintf(h, "%d.", ts)
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

// Sign returns the header value for body at time t.
func Sign(secret, body []byte, t time.Time) string {
	ts := t.Unix()
	return fmt.Sprintf("t=%d,v1=%s", ts, mac(secret, ts, body))
}

// Verify checks header against body. now is the receiver's clock.
func Verify(secret, body []byte, header string, now time.Time, tolerance time.Duration) error {
	var ts int64
	var sigs []string
	for _, part := range strings.Split(header, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			return ErrMalformed
		}
		switch k {
		case "t":
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return ErrMalformed
			}
			ts = n
		case "v1":
			sigs = append(sigs, v)
		}
	}
	if ts == 0 || len(sigs) == 0 {
		return ErrMalformed
	}
	if d := now.Sub(time.Unix(ts, 0)); d > tolerance || d < -tolerance {
		return ErrExpired
	}
	want := mac(secret, ts, body)
	for _, s := range sigs { // multiple v1 allowed during secret rotation
		if hmac.Equal([]byte(s), []byte(want)) {
			return nil
		}
	}
	return ErrMismatch
}
