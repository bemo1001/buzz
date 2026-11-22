package buzz

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"
)

// Dispatcher hands an authorized buzz to the delivery pipeline (Kafka topic
// "buzz.outbound" in production; FCM/APNs fan-out happens in delivery-svc).
type Dispatcher interface {
	Dispatch(ev *Event) error
}

// Store persists buzz events and their status transitions.
type Store interface {
	Save(ev *Event) error
	Get(id string) (*Event, bool)
	SetStatus(id string, s Status) error
}

// Server exposes the buzz HTTP API. Authentication is done by the gateway,
// which injects the caller identity in X-Buzzit-User / X-Buzzit-App.
type Server struct {
	Auth     *Authorizer
	Store    Store
	Dispatch Dispatcher
}

type sendReq struct {
	Recipient string `json:"recipient"`
	Level     string `json:"level"`
	Note      string `json:"note,omitempty"`
}

type eventResp struct {
	ID        string    `json:"id"`
	Recipient string    `json:"recipient"`
	Level     string    `json:"level"`
	Status    Status    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

func toResp(ev *Event) eventResp {
	return eventResp{ev.ID, ev.RecipientID, ev.Level.String(), ev.Status, ev.CreatedAt}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, codeStr, msg string) {
	writeJSON(w, code, map[string]any{"error": map[string]string{"code": codeStr, "message": msg}})
}

func newID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return "bz_" + hex.EncodeToString(b)
}

func (s *Server) Routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/buzz", s.send)
	mux.HandleFunc("GET /v1/buzz/{id}", s.get)
	mux.HandleFunc("POST /v1/buzz/{id}/status", s.status)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	return mux
}

func (s *Server) send(w http.ResponseWriter, r *http.Request) {
	user := r.Header.Get("X-Buzzit-User")
	if user == "" {
		writeErr(w, http.StatusUnauthorized, "unauthenticated", "missing caller identity")
		return
	}
	var req sendReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	lvl, err := ParseLevel(req.Level)
	if err != nil || req.Recipient == "" {
		writeErr(w, http.StatusBadRequest, "invalid_request", "recipient and a valid level are required")
		return
	}
	ev := &Event{
		ID: newID(), SenderID: user, RecipientID: req.Recipient, Level: lvl,
		Note: req.Note, Status: StatusSent, CreatedAt: time.Now().UTC(),
		AppID: r.Header.Get("X-Buzzit-App"),
	}
	if err := s.Auth.Authorize(r.Context(), ev); err != nil {
		switch {
		case errors.Is(err, ErrRateLimited):
			w.Header().Set("Retry-After", "60")
			writeErr(w, http.StatusTooManyRequests, "rate_limited", err.Error())
		case errors.Is(err, ErrNotFriends), errors.Is(err, ErrBlocked):
			// Do not reveal block status: same response as not-friends.
			writeErr(w, http.StatusForbidden, "not_allowed", "you cannot buzz this user")
		case errors.Is(err, ErrNoPass), errors.Is(err, ErrAppEmergency):
			writeErr(w, http.StatusForbidden, "level_not_granted", err.Error())
		case errors.Is(err, ErrQuietHours):
			writeErr(w, http.StatusConflict, "quiet_hours", err.Error())
		case errors.Is(err, ErrNoteTooLong):
			writeErr(w, http.StatusBadRequest, "invalid_request", err.Error())
		default:
			writeErr(w, http.StatusInternalServerError, "internal", "internal error")
		}
		return
	}
	if err := s.Store.Save(ev); err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	if err := s.Dispatch.Dispatch(ev); err != nil {
		writeErr(w, http.StatusServiceUnavailable, "dispatch_failed", "try again")
		return
	}
	writeJSON(w, http.StatusAccepted, toResp(ev))
}

func (s *Server) get(w http.ResponseWriter, r *http.Request) {
	user := r.Header.Get("X-Buzzit-User")
	ev, ok := s.Store.Get(r.PathValue("id"))
	if !ok || (ev.SenderID != user && ev.RecipientID != user) {
		writeErr(w, http.StatusNotFound, "not_found", "buzz not found")
		return
	}
	writeJSON(w, http.StatusOK, toResp(ev))
}

// status is called by the recipient's device: delivered -> rang -> acked.
func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	user := r.Header.Get("X-Buzzit-User")
	ev, ok := s.Store.Get(r.PathValue("id"))
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
	order := map[Status]int{StatusSent: 0, StatusDelivered: 1, StatusRang: 2, StatusAcked: 3}
	next, valid := order[body.Status]
	if !valid || body.Status == StatusSent {
		writeErr(w, http.StatusBadRequest, "invalid_status", "status must be delivered, rang or acked")
		return
	}
	if next <= order[ev.Status] { // idempotent, never go backwards
		writeJSON(w, http.StatusOK, toResp(ev))
		return
	}
	if err := s.Store.SetStatus(ev.ID, body.Status); err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	ev, _ = s.Store.Get(ev.ID)
	writeJSON(w, http.StatusOK, toResp(ev))
}

// MemoryStore is an in-memory Store for tests and local dev.
type MemoryStore struct {
	mu sync.RWMutex
	m  map[string]*Event
}

func NewMemoryStore() *MemoryStore { return &MemoryStore{m: map[string]*Event{}} }

func (s *MemoryStore) Save(ev *Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *ev
	s.m[ev.ID] = &cp
	return nil
}

func (s *MemoryStore) Get(id string) (*Event, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ev, ok := s.m[id]
	if !ok {
		return nil, false
	}
	cp := *ev
	return &cp, true
}

func (s *MemoryStore) SetStatus(id string, st Status) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ev, ok := s.m[id]; ok {
		ev.Status = st
	}
	return nil
}

// LogDispatcher records dispatched events (local dev / tests).
type LogDispatcher struct {
	mu   sync.Mutex
	Sent []*Event
	Log  func(ev *Event)
}

func (d *LogDispatcher) Dispatch(ev *Event) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.Sent = append(d.Sent, ev)
	if d.Log != nil {
		d.Log(ev)
	}
	return nil
}
