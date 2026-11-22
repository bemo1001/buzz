// Package server assembles every Buzzit module into one HTTP server.
package server

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"github.com/buzzit/services/internal/auth"
	"github.com/buzzit/services/internal/buzz"
	"github.com/buzzit/services/internal/chat"
	"github.com/buzzit/services/internal/crypt"
	"github.com/buzzit/services/internal/httpx"
	"github.com/buzzit/services/internal/media"
	"github.com/buzzit/services/internal/platform"
	"github.com/buzzit/services/internal/push"
	"github.com/buzzit/services/internal/realtime"
	"github.com/buzzit/services/internal/social"
	"github.com/buzzit/services/internal/store"
)

type Config struct {
	DataDir              string // holds buzzit.db, data.key and media/
	MaxUploadMB          int64
	FCMServiceAccount    string // optional path to a Firebase service-account JSON
	AllowPrivateWebhooks bool   // dev only
	BcryptCost           int    // 0 = default (12)
	InMemory             bool   // tests: in-memory database
}

type App struct {
	DB       *sql.DB
	Hub      *realtime.Hub
	Auth     *auth.Service
	Social   *social.Service
	Chat     *chat.Service
	Media    *media.Service
	Buzz     *buzz.Server
	Platform *platform.Service
	Push     push.Pusher
	Handler  http.Handler
}

func New(cfg Config) (*App, error) {
	if cfg.MaxUploadMB == 0 {
		cfg.MaxUploadMB = 50
	}
	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		return nil, err
	}
	dbPath := filepath.Join(cfg.DataDir, "buzzit.db")
	if cfg.InMemory {
		dbPath = ":memory:"
	}
	db, err := store.Open(dbPath)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	box, err := crypt.LoadOrCreate(cfg.DataDir)
	if err != nil {
		return nil, err
	}

	var pusher push.Pusher = push.Noop{}
	if cfg.FCMServiceAccount != "" {
		f, err := push.NewFCMFromFile(cfg.FCMServiceAccount)
		if err != nil {
			return nil, err
		}
		pusher = f
		slog.Info("push: FCM enabled")
	}

	limiter := buzz.NewMemoryLimiter()
	hub := realtime.NewHub(db, box)
	soc := &social.Service{DB: db, Hub: hub}
	hub.OnOnline = soc.SetOnline
	a := &App{DB: db, Hub: hub, Social: soc, Push: pusher}

	a.Auth = &auth.Service{DB: db, Limiter: limiter, BcryptCost: cfg.BcryptCost}
	a.Chat = &chat.Service{DB: db, Box: box, Hub: hub, Social: soc, Notify: a}
	a.Media = &media.Service{DB: db, Dir: filepath.Join(cfg.DataDir, "media"), MaxSize: cfg.MaxUploadMB << 20}
	a.Buzz = &buzz.Server{
		Auth:     &buzz.Authorizer{Graph: soc, Limiter: limiter, Policy: buzz.DefaultPolicy},
		Store:    &buzz.SQLStore{DB: db},
		Dispatch: a,
		Identity: func(r *http.Request) (string, string) {
			id, _ := auth.From(r.Context())
			return id.UserID, id.AppID
		},
	}
	a.Platform = &platform.Service{DB: db, Box: box, Hub: hub, Social: soc, Chat: a.Chat, Buzz: a.Buzz,
		Limiter: limiter, Quota: platform.FreeTier, AllowPrivateWebhooks: cfg.AllowPrivateWebhooks}
	a.Auth.ResolveBot = a.Platform.ResolveBot
	a.Chat.OnMessage = a.Platform.OnMessage
	a.Buzz.OnStatus = a.onBuzzStatus

	mux := http.NewServeMux()
	user, bot := a.Auth.Require, a.Auth.RequireBot
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := db.PingContext(r.Context()); err != nil {
			http.Error(w, "db unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /v1/ws", user(hub.Handler(a.onClientMessage)))
	mux.HandleFunc("GET /v1/bot/ws", bot(hub.Handler(nil)))
	mux.HandleFunc("GET /v1/sync", user(httpx.Handler(hub.SyncHandler)))
	a.Auth.Routes(mux, user)
	soc.Routes(mux, user)
	a.Chat.Routes(mux, user)
	a.Media.Routes(mux, user)
	a.Buzz.Routes(mux, user)
	a.Platform.Routes(mux, user, bot)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { httpx.Fail(w, r, httpx.ErrNotFound) })

	a.Handler = middleware(mux)
	return a, nil
}

// Run starts background jobs until ctx is done.
func (a *App) Run(ctx context.Context) {
	go a.Platform.RunWebhooks(ctx)
	go func() {
		t := time.NewTicker(time.Hour)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if err := a.Hub.Prune(ctx); err != nil {
					slog.Error("prune events", "err", err)
				}
			}
		}
	}()
}

func (a *App) Close() error { return a.DB.Close() }

func (a *App) onClientMessage(ctx context.Context, id auth.Identity, m realtime.ClientMessage) {
	if m.Type == "typing" && m.ConversationID != "" {
		a.Chat.Typing(ctx, id.UserID, m.ConversationID)
	}
}

// ---- push & dispatch -------------------------------------------------------

func (a *App) pushTokens(ctx context.Context, userIDs []string) []string {
	if _, noop := a.Push.(push.Noop); noop || len(userIDs) == 0 {
		return nil
	}
	args := make([]any, len(userIDs))
	for i, u := range userIDs {
		args[i] = u
	}
	rows, err := a.DB.QueryContext(ctx, `SELECT push_token FROM devices WHERE push_token <> '' AND user_id IN (`+store.Placeholders(len(userIDs))+`)`, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var t string
		if rows.Scan(&t) == nil {
			out = append(out, t)
		}
	}
	return out
}

func (a *App) push(ctx context.Context, users []string, m push.Message) {
	tokens := a.pushTokens(ctx, users)
	if len(tokens) == 0 {
		return
	}
	go func() {
		c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		for _, bad := range a.Push.Send(c, tokens, m) {
			_, _ = a.DB.ExecContext(c, `UPDATE devices SET push_token = '' WHERE push_token = ?`, bad)
		}
	}()
}

// NotifyMessage implements chat.Notifier. Pushes carry no message content:
// the app wakes up and fetches over its own connection.
func (a *App) NotifyMessage(ctx context.Context, offline []string, c *chat.Conversation, m *chat.Message, _ social.User) {
	a.push(ctx, offline, push.Message{
		Data: map[string]string{"type": "sync", "conversation_id": c.ID},
		High: true, TTL: 24 * time.Hour, Collapse: "sync",
	})
}

// Dispatch implements buzz.Dispatcher.
func (a *App) Dispatch(ctx context.Context, ev *buzz.Event) error {
	users, err := a.Social.Users(ctx, []string{ev.SenderID})
	if err != nil {
		return err
	}
	sender := users[ev.SenderID]
	if _, err := a.Hub.Publish(ctx, "buzz", map[string]any{"buzz": buzz.ToJSON(ev), "sender": sender}, ev.RecipientID); err != nil {
		return err
	}
	// Always push buzzes, even to online users: the app may be in the
	// background with the socket suspended. Clients de-duplicate by ID.
	a.push(ctx, []string{ev.RecipientID}, push.Message{
		Data: map[string]string{"type": "buzz", "buzz_id": ev.ID, "level": ev.Level.String(), "sender_name": sender.DisplayName, "note": ev.Note},
		High: true, TTL: 5 * time.Minute,
	})
	return nil
}

func (a *App) onBuzzStatus(ctx context.Context, ev *buzz.Event) {
	targets := []string{ev.RecipientID} // the recipient's other devices stop ringing
	if ev.AppID == "" {
		targets = append(targets, ev.SenderID)
	}
	_, _ = a.Hub.Publish(ctx, "buzz.status", buzz.ToJSON(ev), targets...)
	a.Platform.OnBuzzStatus(ctx, ev)
}

// ---- middleware ------------------------------------------------------------

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) { w.status = code; w.ResponseWriter.WriteHeader(code) }

// Unwrap lets http.ResponseController / websocket reach the underlying writer.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("panic", "path", r.URL.Path, "err", rec, "stack", string(debug.Stack()))
				httpx.Fail(sw, r, httpx.E(http.StatusInternalServerError, "internal", "internal error"))
			}
			if r.URL.Path != "/healthz" && !strings.HasSuffix(r.URL.Path, "/ws") {
				slog.Info("http", "method", r.Method, "path", r.URL.Path, "status", sw.status, "ms", time.Since(start).Milliseconds())
			}
		}()
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(sw, r)
	})
}
