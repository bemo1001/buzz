// Package crypt encrypts data at rest (message bodies, event payloads) with
// AES-256-GCM. The key lives in the data directory, or comes from
// BUZZIT_DATA_KEY (64 hex characters).
package crypt

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Box struct{ aead cipher.AEAD }

func New(key []byte) (*Box, error) {
	if len(key) != 32 {
		return nil, errors.New("crypt: key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead}, nil
}

// LoadOrCreate reads the key from env or <dir>/data.key, creating it on first run.
func LoadOrCreate(dir string) (*Box, error) {
	if hx := os.Getenv("BUZZIT_DATA_KEY"); hx != "" {
		key, err := hex.DecodeString(strings.TrimSpace(hx))
		if err != nil {
			return nil, fmt.Errorf("crypt: BUZZIT_DATA_KEY: %w", err)
		}
		return New(key)
	}
	path := filepath.Join(dir, "data.key")
	if b, err := os.ReadFile(path); err == nil {
		key, err := hex.DecodeString(strings.TrimSpace(string(b)))
		if err != nil {
			return nil, fmt.Errorf("crypt: %s: %w", path, err)
		}
		return New(key)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, []byte(hex.EncodeToString(key)), 0o600); err != nil {
		return nil, err
	}
	return New(key)
}

// Ephemeral returns a Box with a random key (tests).
func Ephemeral() *Box {
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	b, _ := New(key)
	return b
}

func (b *Box) Seal(plain []byte) []byte {
	nonce := make([]byte, b.aead.NonceSize())
	_, _ = rand.Read(nonce)
	return b.aead.Seal(nonce, nonce, plain, nil)
}

func (b *Box) Open(sealed []byte) ([]byte, error) {
	n := b.aead.NonceSize()
	if len(sealed) < n {
		return nil, errors.New("crypt: ciphertext too short")
	}
	return b.aead.Open(nil, sealed[:n], sealed[n:], nil)
}
