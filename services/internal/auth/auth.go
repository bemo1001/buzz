// Package auth handles accounts, device sessions and request authentication.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"

	"github.com/buzzit/services/internal/buzz"
	"github.com/buzzit/services/internal/httpx"
	"github.com/buzzit/services/internal/store"
)

const (
	UserTokenPrefix = "bzu_"
	BotTokenPrefix  = "bzb_"
)

// Identity is the authenticated caller.
type Identity struct {
	UserID   string
	DeviceID string // empty for bots
	AppID    string // set when the caller is a Platform app's bot token
}

type ctxKey struct{}

func WithIdentity(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// From returns the caller identity; ok is false for anonymous requests.
func From(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(ctxKey{}).(Identity)
	return id, ok
}

// BotResolver maps a bot token hash to (appID, botUserID). Set by platform.
type BotResolver func(ctx context.Context, tokenHash string) (appID, botUserID string, err error)

type Service struct {
	DB         *sql.DB
	Limiter    buzz.Limiter
	ResolveBot BotResolver
	BcryptCost int
}

func HashToken(tok string) string {
	h := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(h[:])
}

// NewToken returns a random bearer token with the given prefix.
func NewToken(prefix string) string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return prefix + base64.RawURLEncoding.EncodeToString(b)
}

var usernameRe = regexp.MustCompile(`^[a-zA-Z0-9_.]{3,32}$`)

func ValidUsername(u string) bool {
	return usernameRe.MatchString(u) && !strings.HasSuffix(strings.ToLower(u), "_bot")
}

type registerReq struct {
	Username    string `json:"username"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name"`
	DeviceName  string `json:"device_name"`
	Platform    string `json:"platform"`
}

type sessionResp struct {
	Token    string `json:"token"`
	UserID   string `json:"user_id"`
	DeviceID string `json:"device_id"`
}

func (s *Service) Routes(mux *http.ServeMux, authed func(http.HandlerFunc) http.HandlerFunc) {
	mux.HandleFunc("POST /v1/auth/register", httpx.Handler(s.register))
	mux.HandleFunc("POST /v1/auth/login", httpx.Handler(s.login))
	mux.HandleFunc("POST /v1/auth/logout", authed(httpx.Handler(s.logout)))
	mux.HandleFunc("POST /v1/auth/password", authed(httpx.Handler(s.changePassword)))
	mux.HandleFunc("GET /v1/devices", authed(httpx.Handler(s.listDevices)))
	mux.HandleFunc("DELETE /v1/devices/{id}", authed(httpx.Handler(s.deleteDevice)))
	mux.HandleFunc("PUT /v1/devices/current/push-token", authed(httpx.Handler(s.setPushToken)))
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Service) throttle(r *http.Request, action string, n int) error {
	if s.Limiter == nil {
		return nil
	}
	ok, err := s.Limiter.Allow(r.Context(), "auth:"+action+":"+clientIP(r), n, time.Minute)
	if err != nil {
		return err
	}
	if !ok {
		return httpx.ErrRateLimited
	}
	return nil
}

func (s *Service) cost() int {
	if s.BcryptCost == 0 {
		return 12
	}
	return s.BcryptCost
}

func (s *Service) register(w http.ResponseWriter, r *http.Request) error {
	if err := s.throttle(r, "register", 10); err != nil {
		return err
	}
	var req registerReq
	if err := httpx.Decode(w, r, &req); err != nil {
		return err
	}
	req.Username = strings.TrimSpace(req.Username)
	if !ValidUsername(req.Username) {
		return httpx.BadRequest("username must be 3-32 letters, digits, '_' or '.', and must not end in _bot")
	}
	if utf8.RuneCountInString(req.Password) < 8 || len(req.Password) > 72 {
		return httpx.BadRequest("password must be 8-72 characters")
	}
	if req.DisplayName = strings.TrimSpace(req.DisplayName); req.DisplayName == "" {
		req.DisplayName = req.Username
	}
	if utf8.RuneCountInString(req.DisplayName) > 64 {
		return httpx.BadRequest("display name too long")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), s.cost())
	if err != nil {
		return err
	}
	uid := store.NewID("usr")
	now := store.Now()
	var resp sessionResp
	err = store.Tx(r.Context(), s.DB, func(tx *sql.Tx) error {
		var exists int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM users WHERE username = ?`, req.Username).Scan(&exists); err != nil {
			return err
		}
		if exists > 0 {
			return httpx.E(http.StatusConflict, "username_taken", "that username is taken")
		}
		if _, err := tx.Exec(`INSERT INTO users(id, username, display_name, password_hash, created_at) VALUES (?,?,?,?,?)`,
			uid, req.Username, req.DisplayName, string(hash), now); err != nil {
			return err
		}
		resp, err = newDevice(tx, uid, req.DeviceName, req.Platform)
		return err
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusCreated, resp)
	return nil
}

func newDevice(tx *sql.Tx, uid, name, platform string) (sessionResp, error) {
	tok := NewToken(UserTokenPrefix)
	did := store.NewID("dev")
	now := store.Now()
	if len(name) > 64 {
		name = name[:64]
	}
	if len(platform) > 16 {
		platform = platform[:16]
	}
	_, err := tx.Exec(`INSERT INTO devices(id, user_id, name, platform, token_hash, created_at, last_used_at) VALUES (?,?,?,?,?,?,?)`,
		did, uid, name, platform, HashToken(tok), now, now)
	return sessionResp{Token: tok, UserID: uid, DeviceID: did}, err
}

// dummyHash makes failed logins for unknown users take as long as real ones.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("buzzit-timing-equalizer"), bcrypt.MinCost)

func (s *Service) login(w http.ResponseWriter, r *http.Request) error {
	if err := s.throttle(r, "login", 10); err != nil {
		return err
	}
	var req registerReq
	if err := httpx.Decode(w, r, &req); err != nil {
		return err
	}
	var uid string
	var hash sql.NullString
	err := s.DB.QueryRowContext(r.Context(), `SELECT id, password_hash FROM users WHERE username = ? AND is_bot = 0`, strings.TrimSpace(req.Username)).Scan(&uid, &hash)
	if errors.Is(err, sql.ErrNoRows) || !hash.Valid {
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(req.Password))
		return httpx.E(http.StatusUnauthorized, "invalid_credentials", "wrong username or password")
	}
	if err != nil {
		return err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash.String), []byte(req.Password)) != nil {
		return httpx.E(http.StatusUnauthorized, "invalid_credentials", "wrong username or password")
	}
	var resp sessionResp
	err = store.Tx(r.Context(), s.DB, func(tx *sql.Tx) error {
		resp, err = newDevice(tx, uid, req.DeviceName, req.Platform)
		return err
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, resp)
	return nil
}

func (s *Service) logout(w http.ResponseWriter, r *http.Request) error {
	id, _ := From(r.Context())
	if id.DeviceID == "" {
		return httpx.ErrForbidden
	}
	_, err := s.DB.ExecContext(r.Context(), `DELETE FROM devices WHERE id = ?`, id.DeviceID)
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Service) changePassword(w http.ResponseWriter, r *http.Request) error {
	id, _ := From(r.Context())
	var req struct {
		Current string `json:"current_password"`
		New     string `json:"new_password"`
	}
	if err := httpx.Decode(w, r, &req); err != nil {
		return err
	}
	if utf8.RuneCountInString(req.New) < 8 || len(req.New) > 72 {
		return httpx.BadRequest("password must be 8-72 characters")
	}
	var hash sql.NullString
	if err := s.DB.QueryRowContext(r.Context(), `SELECT password_hash FROM users WHERE id = ?`, id.UserID).Scan(&hash); err != nil {
		return err
	}
	if !hash.Valid || bcrypt.CompareHashAndPassword([]byte(hash.String), []byte(req.Current)) != nil {
		return httpx.E(http.StatusUnauthorized, "invalid_credentials", "current password is wrong")
	}
	nh, err := bcrypt.GenerateFromPassword([]byte(req.New), s.cost())
	if err != nil {
		return err
	}
	return store.Tx(r.Context(), s.DB, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`UPDATE users SET password_hash = ? WHERE id = ?`, string(nh), id.UserID); err != nil {
			return err
		}
		// Sign out every other device.
		if _, err := tx.Exec(`DELETE FROM devices WHERE user_id = ? AND id <> ?`, id.UserID, id.DeviceID); err != nil {
			return err
		}
		w.WriteHeader(http.StatusNoContent)
		return nil
	})
}

type device struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Platform   string `json:"platform"`
	CreatedAt  int64  `json:"created_at"`
	LastUsedAt int64  `json:"last_used_at"`
	Current    bool   `json:"current"`
}

func (s *Service) listDevices(w http.ResponseWriter, r *http.Request) error {
	id, _ := From(r.Context())
	rows, err := s.DB.QueryContext(r.Context(), `SELECT id, name, platform, created_at, last_used_at FROM devices WHERE user_id = ? ORDER BY last_used_at DESC`, id.UserID)
	if err != nil {
		return err
	}
	defer rows.Close()
	out := []device{}
	for rows.Next() {
		var d device
		if err := rows.Scan(&d.ID, &d.Name, &d.Platform, &d.CreatedAt, &d.LastUsedAt); err != nil {
			return err
		}
		d.Current = d.ID == id.DeviceID
		out = append(out, d)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"devices": out})
	return rows.Err()
}

func (s *Service) deleteDevice(w http.ResponseWriter, r *http.Request) error {
	id, _ := From(r.Context())
	res, err := s.DB.ExecContext(r.Context(), `DELETE FROM devices WHERE id = ? AND user_id = ?`, r.PathValue("id"), id.UserID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return httpx.ErrNotFound
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Service) setPushToken(w http.ResponseWriter, r *http.Request) error {
	id, _ := From(r.Context())
	if id.DeviceID == "" {
		return httpx.ErrForbidden
	}
	var req struct {
		Token string `json:"token"`
	}
	if err := httpx.Decode(w, r, &req); err != nil {
		return err
	}
	if len(req.Token) > 4096 {
		return httpx.BadRequest("token too long")
	}
	if _, err := s.DB.ExecContext(r.Context(), `UPDATE devices SET push_token = ? WHERE id = ?`, req.Token, id.DeviceID); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// TokenFromRequest reads "Authorization: Bearer <tok>" or, for WebSocket
// upgrades only, the access_token query parameter.
func TokenFromRequest(r *http.Request) string {
	if h := r.Header.Get("Authorization"); h != "" {
		if t, ok := strings.CutPrefix(h, "Bearer "); ok {
			return strings.TrimSpace(t)
		}
		if t, ok := strings.CutPrefix(h, "Bot "); ok {
			return strings.TrimSpace(t)
		}
		return ""
	}
	if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		return r.URL.Query().Get("access_token")
	}
	return ""
}

// Authenticate resolves a token into an Identity.
func (s *Service) Authenticate(ctx context.Context, tok string) (Identity, error) {
	switch {
	case strings.HasPrefix(tok, UserTokenPrefix):
		var id Identity
		err := s.DB.QueryRowContext(ctx, `SELECT id, user_id FROM devices WHERE token_hash = ?`, HashToken(tok)).Scan(&id.DeviceID, &id.UserID)
		if errors.Is(err, sql.ErrNoRows) {
			return Identity{}, httpx.ErrUnauthorized
		}
		if err != nil {
			return Identity{}, err
		}
		now := store.Now()
		// Throttle last-used writes to once a minute per device.
		_, _ = s.DB.ExecContext(ctx, `UPDATE devices SET last_used_at = ? WHERE id = ? AND last_used_at < ?`, now, id.DeviceID, now-60_000)
		return id, nil
	case strings.HasPrefix(tok, BotTokenPrefix) && s.ResolveBot != nil:
		app, bot, err := s.ResolveBot(ctx, HashToken(tok))
		if errors.Is(err, sql.ErrNoRows) {
			return Identity{}, httpx.ErrUnauthorized
		}
		if err != nil {
			return Identity{}, err
		}
		return Identity{UserID: bot, AppID: app}, nil
	}
	return Identity{}, httpx.ErrUnauthorized
}

// Require wraps h so it only runs for authenticated *user* callers
// (bot tokens are rejected; bots use the /v1/bot API).
func (s *Service) Require(h http.HandlerFunc) http.HandlerFunc {
	return s.require(h, false)
}

// RequireBot wraps h so it only runs for Platform bot tokens.
func (s *Service) RequireBot(h http.HandlerFunc) http.HandlerFunc {
	return s.require(h, true)
}

func (s *Service) require(h http.HandlerFunc, bot bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := s.Authenticate(r.Context(), TokenFromRequest(r))
		if err != nil {
			httpx.Fail(w, r, err)
			return
		}
		if bot != (id.AppID != "") {
			httpx.Fail(w, r, httpx.E(http.StatusForbidden, "wrong_token_type", "this endpoint needs a different kind of token"))
			return
		}
		h(w, r.WithContext(WithIdentity(r.Context(), id)))
	}
}
