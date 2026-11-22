package buzz

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"
)

// Dispatcher delivers an authorized buzz to the recipient's devices
// (realtime + high-priority push).
type Dispatcher interface {
	Dispatch(ctx context.Context, ev *Event) error
}

// Store persists buzz events and their status transitions.
type Store interface {
	Save(ctx context.Context, ev *Event) error
	Get(ctx context.Context, id string) (*Event, bool, error)
	SetStatus(ctx context.Context, id string, s Status) error
}

// Server exposes the buzz HTTP API.
type Server struct {
	Auth     *Authorizer
	Store    Store
	Dispatch Dispatcher
	// Identity returns the authenticated caller. Defaults to the
	// X-Buzzit-User / X-Buzzit-App headers, which is only safe in tests.
	Identity func(r *http.Request) (userID, appID string)
	// OnStatus is called after a status change (e.g. to notify the sender).
	OnStatus func(ctx context.Context, ev *Event)
}

// EventJSON is the wire format of a buzz.
type EventJSON struct {
	ID          string    `json:"id"`
	SenderID    string    `json:"sender_id"`
	RecipientID string    `json:"recipient"`
	Level       string    `json:"level"`
	Note        string    `json:"note,omitempty"`
	Status      Status    `json:"status"`
	AppID       string    `json:"app_id,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

func ToJSON(ev *Event) EventJSON {
	return EventJSON{ev.ID, ev.SenderID, ev.RecipientID, ev.Level.String(), ev.Note, ev.Status, ev.AppID, ev.CreatedAt}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, codeStr, msg string) {
	if code == http.StatusTooManyRequests {
		w.Header().Set("Retry-After", "60")
	}
	writeJSON(w, code, map[string]any{"error": map[string]string{"code": codeStr, "message": msg}})
}

func NewID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return "bz_" + hex.EncodeToString(b)
}

func (s *Server) identity(r *http.Request) (string, string) {
	if s.Identity != nil {
		return s.Identity(r)
	}
	return r.Header.Get("X-Buzzit-User"), r.Header.Get("X-Buzzit-App")
}

// Routes registers the user-facing buzz endpoints. wrap applies auth.
func (s *Server) Routes(mux *http.ServeMux, wrap func(http.HandlerFunc) http.HandlerFunc) {
	if wrap == nil {
		wrap = func(h http.HandlerFunc) http.HandlerFunc { return h }
	}
	mux.HandleFunc("POST /v1/buzz", wrap(s.send))
	mux.HandleFunc("GET /v1/buzz/{id}", wrap(s.get))
	mux.HandleFunc("POST /v1/buzz/{id}/status", wrap(s.status))
}

// ErrorStatus maps an Authorize error to an HTTP status and code.
func ErrorStatus(err error) (int, string, string) {
	switch {
	case errors.Is(err, ErrRateLimited):
		return http.StatusTooManyRequests, "rate_limited", err.Error()
	case errors.Is(err, ErrNotFriends), errors.Is(err, ErrBlocked):
		// Do not reveal block status: same response as not-friends.
		return http.StatusForbidden, "not_allowed", "you cannot buzz this user"
	case errors.Is(err, ErrNoPass), errors.Is(err, ErrAppEmergency):
		return http.StatusForbidden, "level_not_granted", err.Error()
	case errors.Is(err, ErrQuietHours):
		return http.StatusConflict, "quiet_hours", err.Error()
	case errors.Is(err, ErrNoteTooLong):
		return http.StatusBadRequest, "invalid_request", err.Error()
	}
	return 0, "", ""
}

// Create authorizes, stores and dispatches a buzz.
func (s *Server) Create(ctx context.Context, ev *Event) error { return s.CreateGated(ctx, ev, nil) }

// CreateGated is Create with a gate (e.g. a quota check) that runs only
// after authorization succeeds, so rejected buzzes cost nothing.
func (s *Server) CreateGated(ctx context.Context, ev *Event, gate func(context.Context) error) error {
	if ev.ID == "" {
		ev.ID = NewID()
	}
	ev.Status = StatusSent
	ev.CreatedAt = time.Now().UTC()
	if err := s.Auth.Authorize(ctx, ev); err != nil {
		return err
	}
	if gate != nil {
		if err := gate(ctx); err != nil {
			return err
		}
	}
	if err := s.Store.Save(ctx, ev); err != nil {
		return err
	}
	return s.Dispatch.Dispatch(ctx, ev)
}

func (s *Server) send(w http.ResponseWriter, r *http.Request) {
	user, app := s.identity(r)
	if user == "" {
		writeErr(w, http.StatusUnauthorized, "unauthenticated", "missing caller identity")
		return
	}
	var req struct {
		Recipient string `json:"recipient"`
		Level     string `json:"level"`
		Note      string `json:"note,omitempty"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	lvl, err := ParseLevel(req.Level)
	if err != nil || req.Recipient == "" || req.Recipient == user {
		writeErr(w, http.StatusBadRequest, "invalid_request", "recipient and a valid level are required")
		return
	}
	ev := &Event{SenderID: user, RecipientID: req.Recipient, Level: lvl, Note: req.Note, AppID: app}
	if err := s.Create(r.Context(), ev); err != nil {
		if code, c, msg := ErrorStatus(err); code != 0 {
			writeErr(w, code, c, msg)
			return
		}
		writeErr(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	writeJSON(w, http.StatusAccepted, ToJSON(ev))
}

func (s *Server) get(w http.ResponseWriter, r *http.Request) {
	user, _ := s.identity(r)
	ev, ok, err := s.Store.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	if !ok || (ev.SenderID != user && ev.RecipientID != user) {
		writeErr(w, http.StatusNotFound, "not_found", "buzz not found")
		return
	}
	writeJSON(w, http.StatusOK, ToJSON(ev))
}

var statusOrder = map[Status]int{StatusSent: 0, StatusDelivered: 1, StatusRang: 2, StatusAcked: 3}

// status is called by the recipient's device: delivered -> rang -> acked.
func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	user, _ := s.identity(r)
	ev, ok, err := s.Store.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	if !ok || ev.RecipientID != user {
		writeErr(w, http.StatusNotFound, "not_found", "buzz not found")
		return
	}
	var body struct {
		Status Status `json:"status"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	next, valid := statusOrder[body.Status]
	if !valid || body.Status == StatusSent {
		writeErr(w, http.StatusBadRequest, "invalid_status", "status must be delivered, rang or acked")
		return
	}
	if next <= statusOrder[ev.Status] { // idempotent, never go backwards
		writeJSON(w, http.StatusOK, ToJSON(ev))
		return
	}
	if err := s.Store.SetStatus(r.Context(), ev.ID, body.Status); err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	ev.Status = body.Status
	if s.OnStatus != nil {
		s.OnStatus(r.Context(), ev)
	}
	writeJSON(w, http.StatusOK, ToJSON(ev))
}

// MemoryStore is an in-memory Store for tests.
type MemoryStore struct {
	mu sync.RWMutex
	m  map[string]*Event
}

func NewMemoryStore() *MemoryStore { return &MemoryStore{m: map[string]*Event{}} }

func (s *MemoryStore) Save(_ context.Context, ev *Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *ev
	s.m[ev.ID] = &cp
	return nil
}

func (s *MemoryStore) Get(_ context.Context, id string) (*Event, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ev, ok := s.m[id]
	if !ok {
		return nil, false, nil
	}
	cp := *ev
	return &cp, true, nil
}

func (s *MemoryStore) SetStatus(_ context.Context, id string, st Status) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ev, ok := s.m[id]; ok {
		ev.Status = st
	}
	return nil
}

// LogDispatcher records dispatched events (tests).
type LogDispatcher struct {
	mu   sync.Mutex
	Sent []*Event
}

func (d *LogDispatcher) Dispatch(_ context.Context, ev *Event) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.Sent = append(d.Sent, ev)
	return nil
}
