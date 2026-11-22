// Package httpx holds small HTTP helpers shared by all API packages.
package httpx

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
)

// Error is an API error with an HTTP status and a stable machine code.
type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func E(status int, code, msg string) *Error { return &Error{status, code, msg} }

var (
	ErrUnauthorized = E(http.StatusUnauthorized, "unauthenticated", "authentication required")
	ErrForbidden    = E(http.StatusForbidden, "forbidden", "you are not allowed to do this")
	ErrNotFound     = E(http.StatusNotFound, "not_found", "not found")
	ErrRateLimited  = E(http.StatusTooManyRequests, "rate_limited", "too many requests, slow down")
)

func BadRequest(msg string) *Error { return E(http.StatusBadRequest, "invalid_request", msg) }

// JSON writes v as JSON with the given status.
func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

// Fail writes err as a JSON error. Unknown errors become 500 and are logged.
func Fail(w http.ResponseWriter, r *http.Request, err error) {
	var e *Error
	if !errors.As(err, &e) {
		slog.Error("internal error", "path", r.URL.Path, "err", err)
		e = E(http.StatusInternalServerError, "internal", "internal error")
	}
	if e.Status == http.StatusTooManyRequests {
		w.Header().Set("Retry-After", "60")
	}
	JSON(w, e.Status, map[string]any{"error": map[string]string{"code": e.Code, "message": e.Message}})
}

// Decode reads a JSON body (max 64 KiB) into v.
func Decode(w http.ResponseWriter, r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	if err := dec.Decode(v); err != nil {
		return BadRequest("invalid JSON body: " + err.Error())
	}
	return nil
}

// Handler adapts a function returning an error into an http.HandlerFunc.
func Handler(f func(w http.ResponseWriter, r *http.Request) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := f(w, r); err != nil {
			Fail(w, r, err)
		}
	}
}

// QueryInt parses an integer query parameter with a default and bounds.
func QueryInt(r *http.Request, name string, def, min, max int64) int64 {
	v, err := strconv.ParseInt(r.URL.Query().Get(name), 10, 64)
	if err != nil {
		return def
	}
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}
