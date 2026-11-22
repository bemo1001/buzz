package buzz

import (
	"context"
	"errors"
	"testing"
	"time"
)

func setup(now time.Time) (*Authorizer, *MemoryGraph) {
	g := NewMemoryGraph()
	g.AddFriends("alice", "bob")
	lim := NewMemoryLimiter()
	lim.Now = func() time.Time { return now }
	return &Authorizer{Graph: g, Limiter: lim, Policy: DefaultPolicy, Now: func() time.Time { return now }}, g
}

func TestAuthorize(t *testing.T) {
	ctx := context.Background()
	noon := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)

	t.Run("friends can nudge by default", func(t *testing.T) {
		a, _ := setup(noon)
		if err := a.Authorize(ctx, &Event{SenderID: "bob", RecipientID: "alice", Level: LevelNudge}); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("strangers cannot buzz", func(t *testing.T) {
		a, _ := setup(noon)
		err := a.Authorize(ctx, &Event{SenderID: "eve", RecipientID: "alice", Level: LevelNudge})
		if !errors.Is(err, ErrNotFriends) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("buzz requires pass", func(t *testing.T) {
		a, g := setup(noon)
		ev := &Event{SenderID: "bob", RecipientID: "alice", Level: LevelBuzz}
		if err := a.Authorize(ctx, ev); !errors.Is(err, ErrNoPass) {
			t.Fatalf("got %v", err)
		}
		g.Grant(Pass{GrantorID: "alice", GranteeID: "bob", MaxLevel: LevelBuzz})
		if err := a.Authorize(ctx, ev); err != nil {
			t.Fatal(err)
		}
		ev.Level = LevelEmergency
		if err := a.Authorize(ctx, ev); !errors.Is(err, ErrNoPass) {
			t.Fatalf("emergency should need its own grant, got %v", err)
		}
	})
	t.Run("expired pass falls back to nudge", func(t *testing.T) {
		a, g := setup(noon)
		g.Grant(Pass{GrantorID: "alice", GranteeID: "bob", MaxLevel: LevelEmergency, ExpiresAt: noon.Add(-time.Minute)})
		if err := a.Authorize(ctx, &Event{SenderID: "bob", RecipientID: "alice", Level: LevelBuzz}); !errors.Is(err, ErrNoPass) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("blocked kills everything", func(t *testing.T) {
		a, g := setup(noon)
		g.Grant(Pass{GrantorID: "alice", GranteeID: "bob", MaxLevel: LevelEmergency})
		g.Block("alice", "bob")
		if err := a.Authorize(ctx, &Event{SenderID: "bob", RecipientID: "alice", Level: LevelEmergency}); !errors.Is(err, ErrBlocked) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("quiet hours allow only emergency", func(t *testing.T) {
		night := time.Date(2026, 11, 22, 23, 30, 0, 0, time.UTC)
		a, g := setup(night)
		g.Grant(Pass{GrantorID: "alice", GranteeID: "bob", MaxLevel: LevelEmergency,
			Quiet: &QuietHours{Start: 22 * time.Hour, End: 7 * time.Hour}})
		if err := a.Authorize(ctx, &Event{SenderID: "bob", RecipientID: "alice", Level: LevelBuzz}); !errors.Is(err, ErrQuietHours) {
			t.Fatalf("got %v", err)
		}
		if err := a.Authorize(ctx, &Event{SenderID: "bob", RecipientID: "alice", Level: LevelEmergency}); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("rate limit per pair", func(t *testing.T) {
		a, g := setup(noon)
		g.Grant(Pass{GrantorID: "alice", GranteeID: "bob", MaxLevel: LevelBuzz})
		for i := 0; i < 3; i++ {
			if err := a.Authorize(ctx, &Event{SenderID: "bob", RecipientID: "alice", Level: LevelBuzz}); err != nil {
				t.Fatal(i, err)
			}
		}
		if err := a.Authorize(ctx, &Event{SenderID: "bob", RecipientID: "alice", Level: LevelBuzz}); !errors.Is(err, ErrRateLimited) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("apps can never emergency", func(t *testing.T) {
		a, _ := setup(noon)
		err := a.Authorize(ctx, &Event{SenderID: "app-owner", RecipientID: "alice", Level: LevelEmergency, AppID: "doorbell"})
		if !errors.Is(err, ErrAppEmergency) {
			t.Fatalf("got %v", err)
		}
		if err := a.Authorize(ctx, &Event{SenderID: "app-owner", RecipientID: "alice", Level: LevelNudge, AppID: "doorbell"}); err != nil {
			t.Fatal(err)
		}
	})
}

func TestQuietHoursWrap(t *testing.T) {
	q := &QuietHours{Start: 22 * time.Hour, End: 7 * time.Hour}
	for h, want := range map[int]bool{21: false, 22: true, 3: true, 7: false, 12: false} {
		if got := q.Contains(time.Date(2026, 1, 1, h, 0, 0, 0, time.UTC)); got != want {
			t.Errorf("hour %d: got %v want %v", h, got, want)
		}
	}
}
