// Package buzz implements the consent, quiet-hours and rate-limit rules that
// decide whether one user may buzz another, and at what level.
package buzz

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Level is the intensity of a buzz. Higher values are more intrusive.
type Level int

const (
	LevelNudge     Level = 1 // normal notification + haptic
	LevelBuzz      Level = 2 // loud sound, strong vibration, breaks Focus/DND where allowed
	LevelEmergency Level = 3 // bypasses silent/DND, repeats until acknowledged
)

func (l Level) String() string {
	switch l {
	case LevelNudge:
		return "nudge"
	case LevelBuzz:
		return "buzz"
	case LevelEmergency:
		return "emergency"
	}
	return fmt.Sprintf("level(%d)", int(l))
}

// ParseLevel converts an API string into a Level.
func ParseLevel(s string) (Level, error) {
	switch s {
	case "nudge":
		return LevelNudge, nil
	case "buzz":
		return LevelBuzz, nil
	case "emergency":
		return LevelEmergency, nil
	}
	return 0, fmt.Errorf("unknown buzz level %q", s)
}

// Status tracks a buzz through its lifecycle.
type Status string

const (
	StatusSent      Status = "sent"
	StatusDelivered Status = "delivered"
	StatusRang      Status = "rang"
	StatusAcked     Status = "acked"
	StatusExpired   Status = "expired"
)

// QuietHours is a daily window (in the recipient's timezone) during which only
// Emergency buzzes are allowed. Start may be after End to wrap midnight.
type QuietHours struct {
	Start, End time.Duration // offset from local midnight
	Location   *time.Location
}

func (q *QuietHours) Contains(t time.Time) bool {
	if q == nil || q.Start == q.End {
		return false
	}
	loc := q.Location
	if loc == nil {
		loc = time.UTC
	}
	lt := t.In(loc)
	mid := time.Date(lt.Year(), lt.Month(), lt.Day(), 0, 0, 0, 0, loc)
	off := lt.Sub(mid)
	if q.Start < q.End {
		return off >= q.Start && off < q.End
	}
	return off >= q.Start || off < q.End
}

// Pass is the recipient's grant ("Buzz Pass") allowing a sender to buzz them
// up to MaxLevel.
type Pass struct {
	GrantorID string // recipient
	GranteeID string // sender
	MaxLevel  Level
	Quiet     *QuietHours
	ExpiresAt time.Time // zero = never
}

// Event is a single buzz.
type Event struct {
	ID          string
	SenderID    string
	RecipientID string
	Level       Level
	Note        string
	Status      Status
	CreatedAt   time.Time
	// AppID is set when the buzz comes from a third-party Platform app.
	AppID string
}

// Errors returned by Authorize. The HTTP layer maps them to status codes.
var (
	ErrNotFriends   = errors.New("buzz: users are not friends")
	ErrBlocked      = errors.New("buzz: sender is blocked")
	ErrNoPass       = errors.New("buzz: recipient has not granted this level")
	ErrQuietHours   = errors.New("buzz: recipient is in quiet hours")
	ErrRateLimited  = errors.New("buzz: rate limit exceeded")
	ErrAppEmergency = errors.New("buzz: third-party apps cannot send emergency buzzes")
	ErrNoteTooLong  = errors.New("buzz: note exceeds 140 characters")
)

// Graph answers social-graph questions (backed by graph-svc in production).
type Graph interface {
	AreFriends(ctx context.Context, a, b string) (bool, error)
	IsBlocked(ctx context.Context, blocker, blocked string) (bool, error)
	Pass(ctx context.Context, grantor, grantee string) (*Pass, error) // nil if none
}

// Limiter enforces rate limits (Redis-backed in production).
type Limiter interface {
	// Allow records a hit for key and reports whether it is within limit
	// hits per window.
	Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, error)
}

// Limit is a rate-limit rule.
type Limit struct {
	N      int
	Window time.Duration
}

// Policy holds per-level rate limits between a sender/recipient pair, plus a
// global per-sender cap.
type Policy struct {
	PerPair   map[Level]Limit
	PerSender Limit
}

// DefaultPolicy matches the product spec.
var DefaultPolicy = Policy{
	PerPair: map[Level]Limit{
		LevelNudge:     {N: 10, Window: 10 * time.Minute},
		LevelBuzz:      {N: 3, Window: 10 * time.Minute},
		LevelEmergency: {N: 3, Window: 24 * time.Hour},
	},
	PerSender: Limit{N: 60, Window: time.Hour},
}

// Authorizer decides whether a buzz may be sent.
type Authorizer struct {
	Graph   Graph
	Limiter Limiter
	Policy  Policy
	Now     func() time.Time
}

// Authorize validates ev against the social graph, the recipient's Buzz Pass,
// quiet hours and rate limits. Rate limits are consumed only when every
// other check passes.
func (a *Authorizer) Authorize(ctx context.Context, ev *Event) error {
	now := time.Now
	if a.Now != nil {
		now = a.Now
	}
	if len([]rune(ev.Note)) > 140 {
		return ErrNoteTooLong
	}
	if ev.Level < LevelNudge || ev.Level > LevelEmergency {
		return fmt.Errorf("buzz: invalid level %d", ev.Level)
	}
	if ev.AppID != "" && ev.Level == LevelEmergency {
		return ErrAppEmergency
	}

	blocked, err := a.Graph.IsBlocked(ctx, ev.RecipientID, ev.SenderID)
	if err != nil {
		return err
	}
	if blocked {
		return ErrBlocked
	}

	// Platform apps are authorized by the user's OAuth grant (checked
	// upstream); user-to-user buzzes require friendship and a pass.
	if ev.AppID == "" {
		ok, err := a.Graph.AreFriends(ctx, ev.SenderID, ev.RecipientID)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNotFriends
		}
	}

	maxLevel := LevelNudge // default granted to every friend / authorized app
	pass, err := a.Graph.Pass(ctx, ev.RecipientID, ev.SenderID)
	if err != nil {
		return err
	}
	t := now()
	if pass != nil && (pass.ExpiresAt.IsZero() || t.Before(pass.ExpiresAt)) {
		maxLevel = pass.MaxLevel
		if ev.Level < LevelEmergency && pass.Quiet.Contains(t) {
			return ErrQuietHours
		}
	}
	if ev.Level > maxLevel {
		return ErrNoPass
	}

	sender := ev.SenderID
	if ev.AppID != "" {
		sender = "app:" + ev.AppID
	}
	if l, ok := a.Policy.PerPair[ev.Level]; ok {
		key := fmt.Sprintf("buzz:pair:%s:%s:%s", sender, ev.RecipientID, ev.Level)
		ok, err := a.Limiter.Allow(ctx, key, l.N, l.Window)
		if err != nil {
			return err
		}
		if !ok {
			return ErrRateLimited
		}
	}
	if a.Policy.PerSender.N > 0 {
		ok, err := a.Limiter.Allow(ctx, "buzz:sender:"+sender, a.Policy.PerSender.N, a.Policy.PerSender.Window)
		if err != nil {
			return err
		}
		if !ok {
			return ErrRateLimited
		}
	}
	return nil
}

// MemoryLimiter is a sliding-window limiter for tests and local dev.
type MemoryLimiter struct {
	mu   sync.Mutex
	hits map[string][]time.Time
	Now  func() time.Time
}

func NewMemoryLimiter() *MemoryLimiter {
	return &MemoryLimiter{hits: map[string][]time.Time{}}
}

func (m *MemoryLimiter) Allow(_ context.Context, key string, limit int, window time.Duration) (bool, error) {
	now := time.Now()
	if m.Now != nil {
		now = m.Now()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cutoff := now.Add(-window)
	h := m.hits[key][:0]
	for _, t := range m.hits[key] {
		if t.After(cutoff) {
			h = append(h, t)
		}
	}
	if len(h) >= limit {
		m.hits[key] = h
		return false, nil
	}
	m.hits[key] = append(h, now)
	return true, nil
}
