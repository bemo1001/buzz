// Package social owns profiles, friendships, blocks and Buzz Passes.
package social

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/buzzit/services/internal/auth"
	"github.com/buzzit/services/internal/buzz"
	"github.com/buzzit/services/internal/httpx"
	"github.com/buzzit/services/internal/realtime"
	"github.com/buzzit/services/internal/store"
)

type Service struct {
	DB  *sql.DB
	Hub *realtime.Hub
}

// User is the public view of a user.
type User struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Bio         string `json:"bio"`
	AvatarID    string `json:"avatar_media_id,omitempty"`
	IsBot       bool   `json:"is_bot"`
	Online      *bool  `json:"online,omitempty"`
	LastSeenAt  int64  `json:"last_seen_at,omitempty"`
}

const userCols = `u.id, u.username, u.display_name, u.bio, COALESCE(u.avatar_media_id,''), u.is_bot, u.show_last_seen, u.last_seen_at`

type scanner interface{ Scan(...any) error }

func (s *Service) scanUser(sc scanner) (User, error) {
	var u User
	var showLastSeen bool
	var lastSeen int64
	if err := sc.Scan(&u.ID, &u.Username, &u.DisplayName, &u.Bio, &u.AvatarID, &u.IsBot, &showLastSeen, &lastSeen); err != nil {
		return u, err
	}
	if showLastSeen && !u.IsBot {
		online := s.Hub != nil && s.Hub.Online(u.ID)
		u.Online = &online
		u.LastSeenAt = lastSeen
	}
	return u, nil
}

// Users loads public profiles by ID (missing IDs are skipped).
func (s *Service) Users(ctx context.Context, ids []string) (map[string]User, error) {
	out := map[string]User{}
	if len(ids) == 0 {
		return out, nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT `+userCols+` FROM users u WHERE u.id IN (`+store.Placeholders(len(ids))+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		u, err := s.scanUser(rows)
		if err != nil {
			return nil, err
		}
		out[u.ID] = u
	}
	return out, rows.Err()
}

func (s *Service) Routes(mux *http.ServeMux, authed func(http.HandlerFunc) http.HandlerFunc) {
	h := func(f func(http.ResponseWriter, *http.Request) error) http.HandlerFunc {
		return authed(httpx.Handler(f))
	}
	mux.HandleFunc("GET /v1/me", h(s.me))
	mux.HandleFunc("PATCH /v1/me", h(s.updateMe))
	mux.HandleFunc("GET /v1/users/search", h(s.search))
	mux.HandleFunc("GET /v1/users/{id}", h(s.getUser))

	mux.HandleFunc("GET /v1/friends", h(s.listFriends))
	mux.HandleFunc("DELETE /v1/friends/{id}", h(s.removeFriend))
	mux.HandleFunc("GET /v1/friends/requests", h(s.listRequests))
	mux.HandleFunc("POST /v1/friends/requests", h(s.sendRequest))
	mux.HandleFunc("POST /v1/friends/requests/{id}/accept", h(s.acceptRequest))
	mux.HandleFunc("DELETE /v1/friends/requests/{id}", h(s.deleteRequest))

	mux.HandleFunc("GET /v1/blocks", h(s.listBlocks))
	mux.HandleFunc("PUT /v1/blocks/{id}", h(s.block))
	mux.HandleFunc("DELETE /v1/blocks/{id}", h(s.unblock))

	mux.HandleFunc("GET /v1/buzz-passes", h(s.listPasses))
	mux.HandleFunc("PUT /v1/buzz-passes/{id}", h(s.putPass))
	mux.HandleFunc("DELETE /v1/buzz-passes/{id}", h(s.deletePass))
}

// ---- profile ---------------------------------------------------------------

type me struct {
	User
	SendReadReceipts bool `json:"send_read_receipts"`
	ShowLastSeen     bool `json:"show_last_seen"`
}

func (s *Service) loadMe(ctx context.Context, uid string) (me, error) {
	var m me
	row := s.DB.QueryRowContext(ctx, `SELECT `+userCols+`, u.send_read_receipts FROM users u WHERE u.id = ?`, uid)
	var showLastSeen bool
	err := row.Scan(&m.ID, &m.Username, &m.DisplayName, &m.Bio, &m.AvatarID, &m.IsBot, &showLastSeen, &m.LastSeenAt, &m.SendReadReceipts)
	m.ShowLastSeen = showLastSeen
	return m, err
}

func (s *Service) me(w http.ResponseWriter, r *http.Request) error {
	id, _ := auth.From(r.Context())
	m, err := s.loadMe(r.Context(), id.UserID)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, m)
	return nil
}

func (s *Service) updateMe(w http.ResponseWriter, r *http.Request) error {
	id, _ := auth.From(r.Context())
	var req struct {
		DisplayName      *string `json:"display_name"`
		Bio              *string `json:"bio"`
		AvatarID         *string `json:"avatar_media_id"`
		SendReadReceipts *bool   `json:"send_read_receipts"`
		ShowLastSeen     *bool   `json:"show_last_seen"`
	}
	if err := httpx.Decode(w, r, &req); err != nil {
		return err
	}
	sets, args := []string{}, []any{}
	if req.DisplayName != nil {
		v := strings.TrimSpace(*req.DisplayName)
		if v == "" || utf8.RuneCountInString(v) > 64 {
			return httpx.BadRequest("display name must be 1-64 characters")
		}
		sets, args = append(sets, "display_name = ?"), append(args, v)
	}
	if req.Bio != nil {
		if utf8.RuneCountInString(*req.Bio) > 280 {
			return httpx.BadRequest("bio must be at most 280 characters")
		}
		sets, args = append(sets, "bio = ?"), append(args, *req.Bio)
	}
	if req.AvatarID != nil {
		if *req.AvatarID == "" {
			sets = append(sets, "avatar_media_id = NULL")
		} else {
			var n int
			if err := s.DB.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM media WHERE id = ? AND owner_id = ? AND mime LIKE 'image/%'`, *req.AvatarID, id.UserID).Scan(&n); err != nil {
				return err
			}
			if n == 0 {
				return httpx.BadRequest("avatar must be an image you uploaded")
			}
			sets, args = append(sets, "avatar_media_id = ?"), append(args, *req.AvatarID)
		}
	}
	if req.SendReadReceipts != nil {
		sets, args = append(sets, "send_read_receipts = ?"), append(args, *req.SendReadReceipts)
	}
	if req.ShowLastSeen != nil {
		sets, args = append(sets, "show_last_seen = ?"), append(args, *req.ShowLastSeen)
	}
	if len(sets) > 0 {
		args = append(args, id.UserID)
		if _, err := s.DB.ExecContext(r.Context(), `UPDATE users SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...); err != nil {
			return err
		}
	}
	return s.me(w, r)
}

func (s *Service) search(w http.ResponseWriter, r *http.Request) error {
	id, _ := auth.From(r.Context())
	q := strings.TrimSpace(strings.TrimPrefix(r.URL.Query().Get("q"), "@"))
	if len(q) < 2 {
		httpx.JSON(w, http.StatusOK, map[string]any{"users": []User{}})
		return nil
	}
	q = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q)
	// Users who blocked the caller are hidden.
	rows, err := s.DB.QueryContext(r.Context(), `SELECT `+userCols+` FROM users u
		WHERE (u.username LIKE ? ESCAPE '\' OR u.display_name LIKE ? ESCAPE '\') AND u.id <> ?
		  AND NOT EXISTS (SELECT 1 FROM blocks b WHERE b.blocker_id = u.id AND b.blocked_id = ?)
		ORDER BY (u.username = ?) DESC, u.username LIMIT 20`, q+"%", "%"+q+"%", id.UserID, id.UserID, q)
	if err != nil {
		return err
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		u, err := s.scanUser(rows)
		if err != nil {
			return err
		}
		out = append(out, u)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"users": out})
	return rows.Err()
}

func (s *Service) getUser(w http.ResponseWriter, r *http.Request) error {
	id, _ := auth.From(r.Context())
	other := r.PathValue("id")
	if blocked, err := s.IsBlocked(r.Context(), other, id.UserID); err != nil {
		return err
	} else if blocked {
		return httpx.ErrNotFound
	}
	u, err := s.scanUser(s.DB.QueryRowContext(r.Context(), `SELECT `+userCols+` FROM users u WHERE u.id = ?`, other))
	if errors.Is(err, sql.ErrNoRows) {
		return httpx.ErrNotFound
	}
	if err != nil {
		return err
	}
	rel, err := s.relationship(r.Context(), id.UserID, other)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"user": u, "relationship": rel})
	return nil
}

// relationship: none | friends | request_sent | request_received | blocked
func (s *Service) relationship(ctx context.Context, me, other string) (string, error) {
	if b, err := s.IsBlocked(ctx, me, other); err != nil || b {
		return "blocked", err
	}
	if f, err := s.AreFriends(ctx, me, other); err != nil || f {
		return "friends", err
	}
	var n int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM friend_requests WHERE from_id = ? AND to_id = ?`, me, other).Scan(&n); err != nil {
		return "", err
	}
	if n > 0 {
		return "request_sent", nil
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM friend_requests WHERE from_id = ? AND to_id = ?`, other, me).Scan(&n); err != nil {
		return "", err
	}
	if n > 0 {
		return "request_received", nil
	}
	return "none", nil
}

// SetOnline records presence changes and tells friends (live only).
func (s *Service) SetOnline(userID string, online bool) {
	ctx := context.Background()
	now := store.Now()
	_, _ = s.DB.ExecContext(ctx, `UPDATE users SET last_seen_at = ? WHERE id = ?`, now, userID)
	var show bool
	if err := s.DB.QueryRowContext(ctx, `SELECT show_last_seen FROM users WHERE id = ?`, userID).Scan(&show); err != nil || !show {
		return
	}
	friends, err := s.FriendIDs(ctx, userID)
	if err != nil || s.Hub == nil {
		return
	}
	s.Hub.Ephemeral("presence", map[string]any{"user_id": userID, "online": online, "last_seen_at": now}, friends...)
}

// ---- friends ---------------------------------------------------------------

func pair(a, b string) (string, string) {
	if a > b {
		return b, a
	}
	return a, b
}

func (s *Service) AreFriends(ctx context.Context, a, b string) (bool, error) {
	x, y := pair(a, b)
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM friendships WHERE user_a = ? AND user_b = ?`, x, y).Scan(&n)
	return n > 0, err
}

func (s *Service) IsBlocked(ctx context.Context, blocker, blocked string) (bool, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM blocks WHERE blocker_id = ? AND blocked_id = ?`, blocker, blocked).Scan(&n)
	return n > 0, err
}

// EitherBlocked reports whether a blocked b or b blocked a.
func (s *Service) EitherBlocked(ctx context.Context, a, b string) (bool, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM blocks WHERE (blocker_id = ? AND blocked_id = ?) OR (blocker_id = ? AND blocked_id = ?)`, a, b, b, a).Scan(&n)
	return n > 0, err
}

func (s *Service) FriendIDs(ctx context.Context, uid string) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT user_b FROM friendships WHERE user_a = ? UNION SELECT user_a FROM friendships WHERE user_b = ?`, uid, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (s *Service) usersList(ctx context.Context, ids []string) ([]User, error) {
	m, err := s.Users(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make([]User, 0, len(ids))
	for _, id := range ids {
		if u, ok := m[id]; ok {
			out = append(out, u)
		}
	}
	return out, nil
}

func (s *Service) listFriends(w http.ResponseWriter, r *http.Request) error {
	id, _ := auth.From(r.Context())
	ids, err := s.FriendIDs(r.Context(), id.UserID)
	if err != nil {
		return err
	}
	users, err := s.usersList(r.Context(), ids)
	if err != nil {
		return err
	}
	// Attach the buzz level each friend has granted *me* (what I can send).
	granted := map[string]int{}
	rows, err := s.DB.QueryContext(r.Context(), `SELECT grantor_id, max_level FROM buzz_passes WHERE grantee_id = ? AND (expires_at = 0 OR expires_at > ?)`, id.UserID, store.Now())
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var g string
		var l int
		if err := rows.Scan(&g, &l); err != nil {
			return err
		}
		granted[g] = l
	}
	type friend struct {
		User
		CanBuzzLevel string `json:"can_buzz_level"`
	}
	out := make([]friend, 0, len(users))
	for _, u := range users {
		lvl := buzz.LevelNudge
		if l, ok := granted[u.ID]; ok {
			lvl = buzz.Level(l)
		}
		out = append(out, friend{u, lvl.String()})
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"friends": out})
	return nil
}

func (s *Service) removeFriend(w http.ResponseWriter, r *http.Request) error {
	id, _ := auth.From(r.Context())
	other := r.PathValue("id")
	a, b := pair(id.UserID, other)
	err := store.Tx(r.Context(), s.DB, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`DELETE FROM friendships WHERE user_a = ? AND user_b = ?`, a, b); err != nil {
			return err
		}
		_, err := tx.Exec(`DELETE FROM buzz_passes WHERE (grantor_id = ? AND grantee_id = ?) OR (grantor_id = ? AND grantee_id = ?)`, id.UserID, other, other, id.UserID)
		return err
	})
	if err != nil {
		return err
	}
	_, _ = s.Hub.Publish(r.Context(), "friend.removed", map[string]string{"user_id": id.UserID}, other)
	_, _ = s.Hub.Publish(r.Context(), "friend.removed", map[string]string{"user_id": other}, id.UserID)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Service) listRequests(w http.ResponseWriter, r *http.Request) error {
	id, _ := auth.From(r.Context())
	load := func(q string) ([]map[string]any, error) {
		rows, err := s.DB.QueryContext(r.Context(), q, id.UserID)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var ids []string
		at := map[string]int64{}
		for rows.Next() {
			var uid string
			var t int64
			if err := rows.Scan(&uid, &t); err != nil {
				return nil, err
			}
			ids = append(ids, uid)
			at[uid] = t
		}
		users, err := s.usersList(r.Context(), ids)
		if err != nil {
			return nil, err
		}
		out := []map[string]any{}
		for _, u := range users {
			out = append(out, map[string]any{"user": u, "created_at": at[u.ID]})
		}
		return out, nil
	}
	in, err := load(`SELECT from_id, created_at FROM friend_requests WHERE to_id = ? ORDER BY created_at DESC`)
	if err != nil {
		return err
	}
	out, err := load(`SELECT to_id, created_at FROM friend_requests WHERE from_id = ? ORDER BY created_at DESC`)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"incoming": in, "outgoing": out})
	return nil
}

func (s *Service) sendRequest(w http.ResponseWriter, r *http.Request) error {
	id, _ := auth.From(r.Context())
	var req struct {
		UserID   string `json:"user_id"`
		Username string `json:"username"`
	}
	if err := httpx.Decode(w, r, &req); err != nil {
		return err
	}
	target := req.UserID
	if target == "" && req.Username != "" {
		err := s.DB.QueryRowContext(r.Context(), `SELECT id FROM users WHERE username = ? AND is_bot = 0`, strings.TrimPrefix(req.Username, "@")).Scan(&target)
		if errors.Is(err, sql.ErrNoRows) {
			return httpx.ErrNotFound
		}
		if err != nil {
			return err
		}
	}
	if target == "" || target == id.UserID {
		return httpx.BadRequest("choose someone else to add")
	}
	var isBot bool
	if err := s.DB.QueryRowContext(r.Context(), `SELECT is_bot FROM users WHERE id = ?`, target).Scan(&isBot); errors.Is(err, sql.ErrNoRows) || isBot {
		return httpx.ErrNotFound
	} else if err != nil {
		return err
	}
	if blocked, err := s.EitherBlocked(r.Context(), id.UserID, target); err != nil {
		return err
	} else if blocked {
		// Same response as success so a block is not revealed.
		httpx.JSON(w, http.StatusAccepted, map[string]string{"status": "request_sent"})
		return nil
	}
	if f, err := s.AreFriends(r.Context(), id.UserID, target); err != nil {
		return err
	} else if f {
		httpx.JSON(w, http.StatusOK, map[string]string{"status": "friends"})
		return nil
	}
	// If they already asked me, this accepts.
	var reverse int
	if err := s.DB.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM friend_requests WHERE from_id = ? AND to_id = ?`, target, id.UserID).Scan(&reverse); err != nil {
		return err
	}
	if reverse > 0 {
		if err := s.befriend(r.Context(), target, id.UserID); err != nil {
			return err
		}
		httpx.JSON(w, http.StatusOK, map[string]string{"status": "friends"})
		return nil
	}
	var pending int
	if err := s.DB.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM friend_requests WHERE from_id = ? AND created_at > ?`, id.UserID, time.Now().Add(-24*time.Hour).UnixMilli()).Scan(&pending); err != nil {
		return err
	}
	if pending >= 100 {
		return httpx.ErrRateLimited
	}
	res, err := s.DB.ExecContext(r.Context(), `INSERT OR IGNORE INTO friend_requests(from_id, to_id, created_at) VALUES (?,?,?)`, id.UserID, target, store.Now())
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		users, _ := s.Users(r.Context(), []string{id.UserID})
		_, _ = s.Hub.Publish(r.Context(), "friend.request", map[string]any{"user": users[id.UserID]}, target)
	}
	httpx.JSON(w, http.StatusAccepted, map[string]string{"status": "request_sent"})
	return nil
}

func (s *Service) befriend(ctx context.Context, requester, accepter string) error {
	a, b := pair(requester, accepter)
	err := store.Tx(ctx, s.DB, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`DELETE FROM friend_requests WHERE (from_id = ? AND to_id = ?) OR (from_id = ? AND to_id = ?)`, requester, accepter, accepter, requester); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT OR IGNORE INTO friendships(user_a, user_b, created_at) VALUES (?,?,?)`, a, b, store.Now())
		return err
	})
	if err != nil {
		return err
	}
	users, _ := s.Users(ctx, []string{requester, accepter})
	_, _ = s.Hub.Publish(ctx, "friend.added", map[string]any{"user": users[accepter]}, requester)
	_, _ = s.Hub.Publish(ctx, "friend.added", map[string]any{"user": users[requester]}, accepter)
	return nil
}

func (s *Service) acceptRequest(w http.ResponseWriter, r *http.Request) error {
	id, _ := auth.From(r.Context())
	from := r.PathValue("id")
	var n int
	if err := s.DB.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM friend_requests WHERE from_id = ? AND to_id = ?`, from, id.UserID).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return httpx.ErrNotFound
	}
	if err := s.befriend(r.Context(), from, id.UserID); err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"status": "friends"})
	return nil
}

// deleteRequest declines an incoming request or cancels an outgoing one.
func (s *Service) deleteRequest(w http.ResponseWriter, r *http.Request) error {
	id, _ := auth.From(r.Context())
	other := r.PathValue("id")
	if _, err := s.DB.ExecContext(r.Context(), `DELETE FROM friend_requests WHERE (from_id = ? AND to_id = ?) OR (from_id = ? AND to_id = ?)`, other, id.UserID, id.UserID, other); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// ---- blocks ----------------------------------------------------------------

func (s *Service) listBlocks(w http.ResponseWriter, r *http.Request) error {
	id, _ := auth.From(r.Context())
	rows, err := s.DB.QueryContext(r.Context(), `SELECT blocked_id FROM blocks WHERE blocker_id = ? ORDER BY created_at DESC`, id.UserID)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var b string
		if err := rows.Scan(&b); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, b)
	}
	rows.Close()
	users, err := s.usersList(r.Context(), ids)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"blocked": users})
	return nil
}

// block also removes the friendship, pending requests and Buzz Passes in
// both directions, so a block instantly kills every buzz right.
func (s *Service) block(w http.ResponseWriter, r *http.Request) error {
	id, _ := auth.From(r.Context())
	other := r.PathValue("id")
	if other == id.UserID {
		return httpx.BadRequest("you cannot block yourself")
	}
	a, b := pair(id.UserID, other)
	err := store.Tx(r.Context(), s.DB, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM users WHERE id = ?`, other).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return httpx.ErrNotFound
		}
		stmts := []struct {
			q    string
			args []any
		}{
			{`INSERT OR IGNORE INTO blocks(blocker_id, blocked_id, created_at) VALUES (?,?,?)`, []any{id.UserID, other, store.Now()}},
			{`DELETE FROM friendships WHERE user_a = ? AND user_b = ?`, []any{a, b}},
			{`DELETE FROM friend_requests WHERE (from_id = ? AND to_id = ?) OR (from_id = ? AND to_id = ?)`, []any{id.UserID, other, other, id.UserID}},
			{`DELETE FROM buzz_passes WHERE (grantor_id = ? AND grantee_id = ?) OR (grantor_id = ? AND grantee_id = ?)`, []any{id.UserID, other, other, id.UserID}},
		}
		for _, st := range stmts {
			if _, err := tx.Exec(st.q, st.args...); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	_, _ = s.Hub.Publish(r.Context(), "friend.removed", map[string]string{"user_id": other}, id.UserID)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Service) unblock(w http.ResponseWriter, r *http.Request) error {
	id, _ := auth.From(r.Context())
	if _, err := s.DB.ExecContext(r.Context(), `DELETE FROM blocks WHERE blocker_id = ? AND blocked_id = ?`, id.UserID, r.PathValue("id")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// ---- Buzz Passes -----------------------------------------------------------

type passJSON struct {
	UserID     string `json:"user_id"`
	MaxLevel   string `json:"max_level"`
	QuietStart *int   `json:"quiet_start_min,omitempty"`
	QuietEnd   *int   `json:"quiet_end_min,omitempty"`
	TZ         string `json:"tz"`
	ExpiresAt  int64  `json:"expires_at,omitempty"`
}

// listPasses returns the passes I granted (who may buzz me, and how hard).
func (s *Service) listPasses(w http.ResponseWriter, r *http.Request) error {
	id, _ := auth.From(r.Context())
	rows, err := s.DB.QueryContext(r.Context(), `SELECT grantee_id, max_level, quiet_start_min, quiet_end_min, tz, expires_at FROM buzz_passes WHERE grantor_id = ?`, id.UserID)
	if err != nil {
		return err
	}
	defer rows.Close()
	out := []passJSON{}
	for rows.Next() {
		var p passJSON
		var lvl int
		var qs, qe sql.NullInt64
		if err := rows.Scan(&p.UserID, &lvl, &qs, &qe, &p.TZ, &p.ExpiresAt); err != nil {
			return err
		}
		p.MaxLevel = buzz.Level(lvl).String()
		if qs.Valid && qe.Valid {
			a, b := int(qs.Int64), int(qe.Int64)
			p.QuietStart, p.QuietEnd = &a, &b
		}
		out = append(out, p)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"passes": out})
	return rows.Err()
}

func (s *Service) putPass(w http.ResponseWriter, r *http.Request) error {
	id, _ := auth.From(r.Context())
	grantee := r.PathValue("id")
	var req passJSON
	if err := httpx.Decode(w, r, &req); err != nil {
		return err
	}
	lvl, err := buzz.ParseLevel(req.MaxLevel)
	if err != nil {
		return httpx.BadRequest(err.Error())
	}
	if f, err := s.AreFriends(r.Context(), id.UserID, grantee); err != nil {
		return err
	} else if !f {
		return httpx.E(http.StatusForbidden, "not_friends", "you can only give a Buzz Pass to friends")
	}
	if req.TZ == "" {
		req.TZ = "UTC"
	}
	if _, err := time.LoadLocation(req.TZ); err != nil {
		return httpx.BadRequest("unknown time zone")
	}
	var qs, qe any
	if (req.QuietStart == nil) != (req.QuietEnd == nil) {
		return httpx.BadRequest("set both quiet_start_min and quiet_end_min, or neither")
	}
	if req.QuietStart != nil {
		if *req.QuietStart < 0 || *req.QuietStart >= 1440 || *req.QuietEnd < 0 || *req.QuietEnd >= 1440 {
			return httpx.BadRequest("quiet hours are minutes after midnight (0-1439)")
		}
		qs, qe = *req.QuietStart, *req.QuietEnd
	}
	_, err = s.DB.ExecContext(r.Context(), `INSERT INTO buzz_passes(grantor_id, grantee_id, max_level, quiet_start_min, quiet_end_min, tz, expires_at, created_at)
		VALUES (?,?,?,?,?,?,?,?)
		ON CONFLICT(grantor_id, grantee_id) DO UPDATE SET max_level = excluded.max_level, quiet_start_min = excluded.quiet_start_min,
		  quiet_end_min = excluded.quiet_end_min, tz = excluded.tz, expires_at = excluded.expires_at`,
		id.UserID, grantee, int(lvl), qs, qe, req.TZ, req.ExpiresAt, store.Now())
	if err != nil {
		return err
	}
	_, _ = s.Hub.Publish(r.Context(), "buzz_pass.changed", map[string]string{"user_id": id.UserID, "max_level": lvl.String()}, grantee)
	req.UserID, req.MaxLevel = grantee, lvl.String()
	httpx.JSON(w, http.StatusOK, req)
	return nil
}

func (s *Service) deletePass(w http.ResponseWriter, r *http.Request) error {
	id, _ := auth.From(r.Context())
	grantee := r.PathValue("id")
	if _, err := s.DB.ExecContext(r.Context(), `DELETE FROM buzz_passes WHERE grantor_id = ? AND grantee_id = ?`, id.UserID, grantee); err != nil {
		return err
	}
	_, _ = s.Hub.Publish(r.Context(), "buzz_pass.changed", map[string]string{"user_id": id.UserID, "max_level": buzz.LevelNudge.String()}, grantee)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// Pass implements buzz.Graph.
func (s *Service) Pass(ctx context.Context, grantor, grantee string) (*buzz.Pass, error) {
	var lvl int
	var qs, qe sql.NullInt64
	var tz string
	var exp int64
	err := s.DB.QueryRowContext(ctx, `SELECT max_level, quiet_start_min, quiet_end_min, tz, expires_at FROM buzz_passes WHERE grantor_id = ? AND grantee_id = ?`, grantor, grantee).
		Scan(&lvl, &qs, &qe, &tz, &exp)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	p := &buzz.Pass{GrantorID: grantor, GranteeID: grantee, MaxLevel: buzz.Level(lvl)}
	if exp > 0 {
		p.ExpiresAt = time.UnixMilli(exp)
	}
	if qs.Valid && qe.Valid {
		loc, err := time.LoadLocation(tz)
		if err != nil {
			loc = time.UTC
		}
		p.Quiet = &buzz.QuietHours{Start: time.Duration(qs.Int64) * time.Minute, End: time.Duration(qe.Int64) * time.Minute, Location: loc}
	}
	return p, nil
}

var _ buzz.Graph = (*Service)(nil)
