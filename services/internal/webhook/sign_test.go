package webhook

import (
	"errors"
	"testing"
	"time"
)

func TestSignVerify(t *testing.T) {
	secret := []byte("whsec_test")
	body := []byte(`{"type":"buzz.acknowledged"}`)
	now := time.Unix(1_790_000_000, 0)
	h := Sign(secret, body, now)

	if err := Verify(secret, body, h, now.Add(time.Minute), DefaultTolerance); err != nil {
		t.Fatal(err)
	}
	if err := Verify(secret, []byte(`{"type":"tampered"}`), h, now, DefaultTolerance); !errors.Is(err, ErrMismatch) {
		t.Fatalf("tampered body: %v", err)
	}
	if err := Verify([]byte("other"), body, h, now, DefaultTolerance); !errors.Is(err, ErrMismatch) {
		t.Fatalf("wrong secret: %v", err)
	}
	if err := Verify(secret, body, h, now.Add(10*time.Minute), DefaultTolerance); !errors.Is(err, ErrExpired) {
		t.Fatalf("replay: %v", err)
	}
	if err := Verify(secret, body, "garbage", now, DefaultTolerance); !errors.Is(err, ErrMalformed) {
		t.Fatalf("malformed: %v", err)
	}
}
