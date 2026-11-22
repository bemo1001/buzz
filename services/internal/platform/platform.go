// Package platform is the free Buzzit Platform: developer apps with a bot
// account, user grants (scopes), the Bot API, quotas and signed webhooks.
package platform

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/buzzit/services/internal/auth"
	"github.com/buzzit/services/internal/buzz"
	"github.com/buzzit/services/internal/chat"
	"github.com/buzzit/services/internal/crypt"
	"github.com/buzzit/services/internal/httpx"
	"github.com/buzzit/services/internal/realtime"
	"github.com/buzzit/services/internal/social"
	"github.com/buzzit/services/internal/store"
)

// Scopes a user can grant an app.
const (
	ScopeProfile  = "profile:read"
	ScopeMessages = "messages:send"
	ScopeBuzz     = "buzz:send"
)

var allScopes = []string{ScopeProfile, ScopeMessages, ScopeBuzz}

// Quota is a free-tier daily limit.
type Quota struct {
	MessagesPerDay int
	BuzzesPerDay   int
	BurstPerSecond int
	MaxAppsPerUser int
}

var FreeTier = Quota{MessagesPerDay: 10_000, BuzzesPerDay: 500, BurstPerSecond: 100, MaxAppsPerUser: 10}

type Service struct {
	DB      *sql.DB
	Box     *crypt.Box
	Hub     *realtime.Hub
	Social  *social.Service
	Chat    *chat.Service
	Buzz    *buzz.Server
	Limiter buzz.Limiter
	Quota   Quota
	// AllowPrivateWebhooks permits http:// and private-network webhook URLs (dev only).
	AllowPrivateWebhooks bool
}

type App struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	BotUserID   string `json:"bot_user_id"`
	BotUsername string `json:"bot_username"`
	WebhookURL  string `json:"webhook_url,omitempty"`
	OwnerID     string `json:"owner_id"`
	Tier        string `json:"tier"`
	CreatedAt   int64  `json:"created_at"`
}

func (s *Service) Routes(mux *http.ServeMux, authed, bot func(http.HandlerFunc) http.HandlerFunc) {
	u := func(f func(http.ResponseWriter, *http.Request) error) http.HandlerFunc {
		return authed(httpx.Handler(f))
	}
	b := func(f func(http.ResponseWriter, *http.Request) error) http.HandlerFunc { return bot(httpx.Handler(f)) }

	// Developer management (user tokens)
	mux.HandleFunc("POST /v1/apps", u(s.createApp))
	mux.HandleFunc("GET /v1/apps", u(s.listApps))
	mux.HandleFunc("GET /v1/apps/{id}", u(s.getApp))
	mux.HandleFunc("PATCH /v1/apps/{id}", u(s.updateApp))
	mux.HandleFunc("DELETE /v1/apps/{id}", u(s.deleteApp))
	mux.HandleFunc("POST /v1/apps/{id}/rotate-token", u(s.rotateToken))
	mux.HandleFunc("GET /v1/apps/{id}/usage", u(s.usage))

	// End-user consent (user tokens)
	mux.HandleFunc("POST /v1/apps/{id}/grant", u(s.grant))
	mux.HandleFunc("GET /v1/connected-apps", u(s.connectedApps))
	mux.HandleFunc("DELETE /v1/connected-apps/{id}", u(s.revoke))

	// Bot API (bot tokens)
	mux.HandleFunc("GET /v1/bot/me", b(s.botMe))
	mux.HandleFunc("POST /v1/bot/messages", b(s.botSend))
	mux.HandleFunc("POST /v1/bot/buzz", b(s.botBuzz))
	mux.HandleFunc("GET /v1/bot/buzz/{id}", b(s.botBuzzStatus))
	mux.HandleFunc("GET /v1/bot/sync", bot(httpx.Handler(s.Hub.SyncHandler)))
	mux.HandleFunc("GET /v1/bot/conversations/{id}/messages", b(s.botMessages))
}

// ResolveBot implements auth.BotResolver.
func (s *Service) ResolveBot(ctx context.Context, tokenHash string) (string, string, error) {
	var app, bot string
	err := s.DB.QueryRowContext(ctx, `SELECT id, bot_user_id FROM apps WHERE token_hash = ?`, tokenHash).Scan(&app, &bot)
	return app, bot, err
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

var nonSlug = regexp.MustCompile(`[^a-z0-9_]+`)

func (s *Service) validateWebhook(raw string) error {
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return httpx.BadRequest("webhook_url must be an absolute URL")
	}
	if u.Scheme != "https" && !(s.AllowPrivateWebhooks && u.Scheme == "http") {
		return httpx.BadRequest("webhook_url must use https")
	}
	return nil
}

func (s *Service) loadApp(ctx context.Context, id string) (*App, error) {
	var a App
	err := s.DB.QueryRowContext(ctx, `SELECT a.id, a.name, a.description, a.bot_user_id, u.username, a.webhook_url, a.owner_id, a.tier, a.created_at
		FROM apps a JOIN users u ON u.id = a.bot_user_id WHERE a.id = ?`, id).
		Scan(&a.ID, &a.Name, &a.Description, &a.BotUserID, &a.BotUsername, &a.WebhookURL, &a.OwnerID, &a.Tier, &a.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, httpx.ErrNotFound
	}
	return &a, err
}

func (s *Service) ownApp(r *http.Request) (*App, error) {
	id, _ := auth.From(r.Context())
	a, err := s.loadApp(r.Context(), r.PathValue("id"))
	if err != nil {
		return nil, err
	}
	if a.OwnerID != id.UserID {
		return nil, httpx.ErrNotFound
	}
	return a, nil
}

func (s *Service) createApp(w http.ResponseWriter, r *http.Request) error {
	id, _ := auth.From(r.Context())
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		WebhookURL  string `json:"webhook_url"`
	}
	if err := httpx.Decode(w, r, &req); err != nil {
		return err
	}
	req.Name = strings.TrimSpace(req.Name)
	if n := utf8.RuneCountInString(req.Name); n < 2 || n > 48 {
		return httpx.BadRequest("app name must be 2-48 characters")
	}
	if utf8.RuneCountInString(req.Description) > 500 {
		return httpx.BadRequest("description too long")
	}
	if err := s.validateWebhook(req.WebhookURL); err != nil {
		return err
	}
	var count int
	if err := s.DB.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM apps WHERE owner_id = ?`, id.UserID).Scan(&count); err != nil {
		return err
	}
	if count >= s.Quota.MaxAppsPerUser {
		return httpx.E(http.StatusForbidden, "app_limit", "you have reached the maximum number of apps")
	}
	slug := strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(req.Name), "_"), "_")
	if len(slug) > 20 {
		slug = slug[:20]
	}
	if slug == "" {
		slug = "app"
	}
	appID, botID := store.NewID("app"), store.NewID("usr")
	token := auth.NewToken(auth.BotTokenPrefix)
	secret := "whsec_" + randHex(24)
	now := store.Now()
	var username string
	err := store.Tx(r.Context(), s.DB, func(tx *sql.Tx) error {
		username = slug + "_bot"
		for i := 0; ; i++ {
			var n int
			if err := tx.QueryRow(`SELECT COUNT(*) FROM users WHERE username = ?`, username).Scan(&n); err != nil {
				return err
			}
			if n == 0 {
				break
			}
			username = slug + "_" + randHex(2) + "_bot"
		}
		if _, err := tx.Exec(`INSERT INTO users(id, username, display_name, bio, is_bot, created_at) VALUES (?,?,?,?,1,?)`, botID, username, req.Name, req.Description, now); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO apps(id, owner_id, bot_user_id, name, description, webhook_url, webhook_secret, token_hash, created_at) VALUES (?,?,?,?,?,?,?,?,?)`,
			appID, id.UserID, botID, req.Name, req.Description, req.WebhookURL, secret, auth.HashToken(token), now)
		return err
	})
	if err != nil {
		return err
	}
	a, err := s.loadApp(r.Context(), appID)
	if err != nil {
		return err
	}
	// Secrets are shown once.
	httpx.JSON(w, http.StatusCreated, map[string]any{"app": a, "bot_token": token, "webhook_secret": secret})
	return nil
}

func (s *Service) listApps(w http.ResponseWriter, r *http.Request) error {
	id, _ := auth.From(r.Context())
	rows, err := s.DB.QueryContext(r.Context(), `SELECT id FROM apps WHERE owner_id = ? ORDER BY created_at`, id.UserID)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, a)
	}
	rows.Close()
	out := []*App{}
	for _, a := range ids {
		app, err := s.loadApp(r.Context(), a)
		if err != nil {
			return err
		}
		out = append(out, app)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"apps": out})
	return nil
}

// getApp is public to any signed-in user (for the consent screen); the
// webhook URL is only shown to the owner.
func (s *Service) getApp(w http.ResponseWriter, r *http.Request) error {
	id, _ := auth.From(r.Context())
	a, err := s.loadApp(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	if a.OwnerID != id.UserID {
		a.WebhookURL = ""
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"app": a, "scopes": allScopes})
	return nil
}

func (s *Service) updateApp(w http.ResponseWriter, r *http.Request) error {
	a, err := s.ownApp(r)
	if err != nil {
		return err
	}
	var req struct {
		Description *string `json:"description"`
		WebhookURL  *string `json:"webhook_url"`
	}
	if err := httpx.Decode(w, r, &req); err != nil {
		return err
	}
	if req.Description != nil {
		if utf8.RuneCountInString(*req.Description) > 500 {
			return httpx.BadRequest("description too long")
		}
		if _, err := s.DB.ExecContext(r.Context(), `UPDATE apps SET description = ? WHERE id = ?`, *req.Description, a.ID); err != nil {
			return err
		}
	}
	if req.WebhookURL != nil {
		if err := s.validateWebhook(*req.WebhookURL); err != nil {
			return err
		}
		if _, err := s.DB.ExecContext(r.Context(), `UPDATE apps SET webhook_url = ? WHERE id = ?`, *req.WebhookURL, a.ID); err != nil {
			return err
		}
	}
	a, err = s.loadApp(r.Context(), a.ID)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"app": a})
	return nil
}

func (s *Service) deleteApp(w http.ResponseWriter, r *http.Request) error {
	a, err := s.ownApp(r)
	if err != nil {
		return err
	}
	err = store.Tx(r.Context(), s.DB, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`DELETE FROM apps WHERE id = ?`, a.ID); err != nil {
			return err
		}
		// Removing the bot user cascades its memberships; bot chats disappear.
		_, err := tx.Exec(`DELETE FROM conversations WHERE type = 'bot' AND id IN (SELECT conversation_id FROM members WHERE user_id = ?)`, a.BotUserID)
		if err != nil {
			return err
		}
		_, err = tx.Exec(`DELETE FROM users WHERE id = ?`, a.BotUserID)
		return err
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Service) rotateToken(w http.ResponseWriter, r *http.Request) error {
	a, err := s.ownApp(r)
	if err != nil {
		return err
	}
	token := auth.NewToken(auth.BotTokenPrefix)
	if _, err := s.DB.ExecContext(r.Context(), `UPDATE apps SET token_hash = ? WHERE id = ?`, auth.HashToken(token), a.ID); err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"bot_token": token})
	return nil
}

func today() string { return time.Now().UTC().Format("2006-01-02") }

func (s *Service) usage(w http.ResponseWriter, r *http.Request) error {
	a, err := s.ownApp(r)
	if err != nil {
		return err
	}
	out := map[string]int{"messages": 0, "buzzes": 0}
	rows, err := s.DB.QueryContext(r.Context(), `SELECT kind, count FROM app_usage WHERE app_id = ? AND day = ?`, a.ID, today())
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			return err
		}
		out[k] = n
	}
	var users int
	_ = s.DB.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM app_grants WHERE app_id = ?`, a.ID).Scan(&users)
	httpx.JSON(w, http.StatusOK, map[string]any{"day": today(), "usage": out, "authorized_users": users,
		"limits": map[string]int{"messages": s.Quota.MessagesPerDay, "buzzes": s.Quota.BuzzesPerDay, "burst_per_second": s.Quota.BurstPerSecond}})
	return nil
}

// consume increments today's counter if under limit.
func (s *Service) consume(ctx context.Context, appID, kind string, limit int) error {
	if s.Limiter != nil {
		ok, err := s.Limiter.Allow(ctx, "app:burst:"+appID, s.Quota.BurstPerSecond, time.Second)
		if err != nil {
			return err
		}
		if !ok {
			return httpx.ErrRateLimited
		}
	}
	res, err := s.DB.ExecContext(ctx, `INSERT INTO app_usage(app_id, day, kind, count) VALUES (?,?,?,1)
		ON CONFLICT(app_id, day, kind) DO UPDATE SET count = count + 1 WHERE count < ?`, appID, today(), kind, limit)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return httpx.E(http.StatusTooManyRequests, "quota_exceeded", "daily free-tier quota reached for "+kind)
	}
	return nil
}

// ---- consent ---------------------------------------------------------------

func (s *Service) scopes(ctx context.Context, appID, userID string) ([]string, error) {
	var sc string
	err := s.DB.QueryRowContext(ctx, `SELECT scopes FROM app_grants WHERE app_id = ? AND user_id = ?`, appID, userID).Scan(&sc)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return strings.Fields(sc), err
}

func (s *Service) grant(w http.ResponseWriter, r *http.Request) error {
	id, _ := auth.From(r.Context())
	a, err := s.loadApp(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	var req struct {
		Scopes []string `json:"scopes"`
	}
	if err := httpx.Decode(w, r, &req); err != nil {
		return err
	}
	var scopes []string
	for _, sc := range req.Scopes {
		if !slices.Contains(allScopes, sc) {
			return httpx.BadRequest("unknown scope " + sc)
		}
		if !slices.Contains(scopes, sc) {
			scopes = append(scopes, sc)
		}
	}
	if len(scopes) == 0 {
		return httpx.BadRequest("grant at least one scope")
	}
	if _, err := s.DB.ExecContext(r.Context(), `INSERT INTO app_grants(app_id, user_id, scopes, created_at) VALUES (?,?,?,?)
		ON CONFLICT(app_id, user_id) DO UPDATE SET scopes = excluded.scopes`, a.ID, id.UserID, strings.Join(scopes, " "), store.Now()); err != nil {
		return err
	}
	cid, _, err := s.Chat.EnsureDirect(r.Context(), a.BotUserID, id.UserID, "bot")
	if err != nil {
		return err
	}
	if err := s.enqueue(r.Context(), a.ID, "user.authorized", map[string]any{"user_id": id.UserID, "scopes": scopes, "conversation_id": cid}); err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"app_id": a.ID, "scopes": scopes, "conversation_id": cid})
	return nil
}

func (s *Service) connectedApps(w http.ResponseWriter, r *http.Request) error {
	id, _ := auth.From(r.Context())
	rows, err := s.DB.QueryContext(r.Context(), `SELECT app_id, scopes, created_at FROM app_grants WHERE user_id = ? ORDER BY created_at DESC`, id.UserID)
	if err != nil {
		return err
	}
	type row struct {
		id, scopes string
		at         int64
	}
	var rs []row
	for rows.Next() {
		var x row
		if err := rows.Scan(&x.id, &x.scopes, &x.at); err != nil {
			rows.Close()
			return err
		}
		rs = append(rs, x)
	}
	rows.Close()
	out := []map[string]any{}
	for _, x := range rs {
		a, err := s.loadApp(r.Context(), x.id)
		if err != nil {
			continue
		}
		a.WebhookURL = ""
		out = append(out, map[string]any{"app": a, "scopes": strings.Fields(x.scopes), "granted_at": x.at})
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"apps": out})
	return nil
}

func (s *Service) revoke(w http.ResponseWriter, r *http.Request) error {
	id, _ := auth.From(r.Context())
	appID := r.PathValue("id")
	res, err := s.DB.ExecContext(r.Context(), `DELETE FROM app_grants WHERE app_id = ? AND user_id = ?`, appID, id.UserID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		_ = s.enqueue(r.Context(), appID, "user.revoked", map[string]any{"user_id": id.UserID})
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// ---- Bot API ---------------------------------------------------------------

func (s *Service) botMe(w http.ResponseWriter, r *http.Request) error {
	id, _ := auth.From(r.Context())
	a, err := s.loadApp(r.Context(), id.AppID)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"app": a})
	return nil
}

// canMessage: the user granted messages:send, or opened a chat with the bot.
func (s *Service) canMessage(ctx context.Context, appID, botID, userID string) (string, error) {
	sc, err := s.scopes(ctx, appID, userID)
	if err != nil {
		return "", err
	}
	var cid string
	err = s.DB.QueryRowContext(ctx, `SELECT c.id FROM conversations c WHERE c.type = 'bot' AND c.direct_key = ?`, directKey(botID, userID)).Scan(&cid)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if slices.Contains(sc, ScopeMessages) {
		if cid == "" {
			cid, _, err = s.Chat.EnsureDirect(ctx, botID, userID, "bot")
		}
		return cid, err
	}
	if cid != "" {
		var userSpoke int
		if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages WHERE conversation_id = ? AND sender_id = ?`, cid, userID).Scan(&userSpoke); err != nil {
			return "", err
		}
		if userSpoke > 0 {
			return cid, nil
		}
	}
	return "", httpx.E(http.StatusForbidden, "not_authorized", "the user has not authorized this app to message them")
}

func directKey(a, b string) string {
	if a > b {
		a, b = b, a
	}
	return a + ":" + b
}

func (s *Service) botSend(w http.ResponseWriter, r *http.Request) error {
	id, _ := auth.From(r.Context())
	var req struct {
		To       string `json:"to"`
		Text     string `json:"text"`
		ClientID string `json:"client_id"`
	}
	if err := httpx.Decode(w, r, &req); err != nil {
		return err
	}
	if req.To == "" {
		return httpx.BadRequest("to (user id) is required")
	}
	if blocked, err := s.Social.IsBlocked(r.Context(), req.To, id.UserID); err != nil {
		return err
	} else if blocked {
		return httpx.E(http.StatusForbidden, "not_authorized", "the user has not authorized this app to message them")
	}
	cid, err := s.canMessage(r.Context(), id.AppID, id.UserID, req.To)
	if err != nil {
		return err
	}
	if req.ClientID == "" {
		req.ClientID = r.Header.Get("Idempotency-Key")
	}
	if err := s.consume(r.Context(), id.AppID, "messages", s.Quota.MessagesPerDay); err != nil {
		return err
	}
	m, created, err := s.Chat.Send(r.Context(), id.UserID, cid, chat.SendInput{Kind: "text", Text: req.Text, ClientID: req.ClientID})
	if err != nil {
		return err
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	httpx.JSON(w, status, m)
	return nil
}

func (s *Service) botBuzz(w http.ResponseWriter, r *http.Request) error {
	id, _ := auth.From(r.Context())
	var req struct {
		Recipient string `json:"recipient"`
		Level     string `json:"level"`
		Note      string `json:"note"`
	}
	if err := httpx.Decode(w, r, &req); err != nil {
		return err
	}
	lvl, err := buzz.ParseLevel(req.Level)
	if err != nil {
		return httpx.BadRequest(err.Error())
	}
	sc, err := s.scopes(r.Context(), id.AppID, req.Recipient)
	if err != nil {
		return err
	}
	if !slices.Contains(sc, ScopeBuzz) {
		return httpx.E(http.StatusForbidden, "not_authorized", "the user has not granted buzz:send to this app")
	}
	ev := &buzz.Event{SenderID: id.UserID, RecipientID: req.Recipient, Level: lvl, Note: req.Note, AppID: id.AppID}
	quota := func(ctx context.Context) error { return s.consume(ctx, id.AppID, "buzzes", s.Quota.BuzzesPerDay) }
	if err := s.Buzz.CreateGated(r.Context(), ev, quota); err != nil {
		if code, c, msg := buzz.ErrorStatus(err); code != 0 {
			return httpx.E(code, c, msg)
		}
		return err
	}
	httpx.JSON(w, http.StatusAccepted, buzz.ToJSON(ev))
	return nil
}

func (s *Service) botBuzzStatus(w http.ResponseWriter, r *http.Request) error {
	id, _ := auth.From(r.Context())
	ev, ok, err := s.Buzz.Store.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	if !ok || ev.AppID != id.AppID {
		return httpx.ErrNotFound
	}
	httpx.JSON(w, http.StatusOK, buzz.ToJSON(ev))
	return nil
}

func (s *Service) botMessages(w http.ResponseWriter, r *http.Request) error {
	// Same handler as users get; the bot is a member of its own chats only.
	return s.Chat.ListMessagesHandler(w, r)
}

// ---- hooks -----------------------------------------------------------------

// OnMessage enqueues message.created for user messages in bot chats.
func (s *Service) OnMessage(ctx context.Context, c *chat.Conversation, m *chat.Message) {
	if c.Type != "bot" || m.Kind == "system" {
		return
	}
	for _, mem := range c.Members {
		if !mem.User.IsBot || mem.User.ID == m.SenderID {
			continue
		}
		var appID string
		if err := s.DB.QueryRowContext(ctx, `SELECT id FROM apps WHERE bot_user_id = ?`, mem.User.ID).Scan(&appID); err != nil {
			continue
		}
		_ = s.enqueue(ctx, appID, "message.created", map[string]any{"conversation_id": c.ID, "message": m})
	}
}

// OnBuzzStatus enqueues buzz.acknowledged for app-sent buzzes.
func (s *Service) OnBuzzStatus(ctx context.Context, ev *buzz.Event) {
	if ev.AppID == "" || ev.Status != buzz.StatusAcked {
		return
	}
	_ = s.enqueue(ctx, ev.AppID, "buzz.acknowledged", buzz.ToJSON(ev))
}

func (s *Service) enqueue(ctx context.Context, appID, typ string, data any) error {
	var hook string
	if err := s.DB.QueryRowContext(ctx, `SELECT webhook_url FROM apps WHERE id = ?`, appID).Scan(&hook); err != nil || hook == "" {
		return err
	}
	evID := store.NewID("evt")
	payload, err := json.Marshal(map[string]any{"id": evID, "type": typ, "created_at": time.Now().UTC(), "data": data})
	if err != nil {
		return err
	}
	now := store.Now()
	_, err = s.DB.ExecContext(ctx, `INSERT INTO webhook_outbox(id, app_id, payload, next_attempt_at, created_at) VALUES (?,?,?,?,?)`,
		evID, appID, s.Box.Seal(payload), now, now)
	return err
}
