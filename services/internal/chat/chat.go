// Package chat implements conversations (direct, group, bot) and messages.
package chat

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/buzzit/services/internal/auth"
	"github.com/buzzit/services/internal/crypt"
	"github.com/buzzit/services/internal/httpx"
	"github.com/buzzit/services/internal/realtime"
	"github.com/buzzit/services/internal/social"
	"github.com/buzzit/services/internal/store"
)

const (
	MaxGroupMembers = 256
	MaxTextLen      = 8000
	EditWindow      = 15 * time.Minute
)

// Notifier is told about new messages so it can push to offline users.
type Notifier interface {
	NotifyMessage(ctx context.Context, offline []string, conv *Conversation, msg *Message, sender social.User)
}

type Service struct {
	DB     *sql.DB
	Box    *crypt.Box
	Hub    *realtime.Hub
	Social *social.Service
	Notify Notifier
	// OnMessage runs after every message is stored (used by Platform webhooks).
	OnMessage func(ctx context.Context, conv *Conversation, msg *Message)
}

type MediaRef struct {
	ID       string `json:"id"`
	Mime     string `json:"mime"`
	Size     int64  `json:"size"`
	Filename string `json:"filename"`
}

type Reaction struct {
	UserID string `json:"user_id"`
	Emoji  string `json:"emoji"`
}

type Message struct {
	ID             string     `json:"id"`
	ConversationID string     `json:"conversation_id"`
	Seq            int64      `json:"seq"`
	SenderID       string     `json:"sender_id"`
	ClientID       string     `json:"client_id,omitempty"`
	Kind           string     `json:"kind"`
	Text           string     `json:"text,omitempty"`
	Media          *MediaRef  `json:"media,omitempty"`
	ReplyTo        string     `json:"reply_to,omitempty"`
	Reactions      []Reaction `json:"reactions"`
	Deleted        bool       `json:"deleted"`
	EditedAt       int64      `json:"edited_at,omitempty"`
	CreatedAt      int64      `json:"created_at"`
	// System messages describe membership/title changes.
	System *SystemInfo `json:"system,omitempty"`
}

type SystemInfo struct {
	Event   string   `json:"event"` // group_created | members_added | member_removed | member_left | title_changed
	ActorID string   `json:"actor_id"`
	Targets []string `json:"targets,omitempty"`
	Title   string   `json:"title,omitempty"`
}

type body struct {
	Text   string      `json:"t,omitempty"`
	System *SystemInfo `json:"s,omitempty"`
}

type Member struct {
	User        social.User `json:"user"`
	Role        string      `json:"role"`
	LastReadSeq int64       `json:"last_read_seq"`
}

type Conversation struct {
	ID          string   `json:"id"`
	Type        string   `json:"type"`
	Title       string   `json:"title"`
	AvatarID    string   `json:"avatar_media_id,omitempty"`
	CreatedBy   string   `json:"created_by"`
	CreatedAt   int64    `json:"created_at"`
	LastSeq     int64    `json:"last_seq"`
	Members     []Member `json:"members"`
	LastMessage *Message `json:"last_message,omitempty"`
	UnreadCount int      `json:"unread_count"`
	MutedUntil  int64    `json:"muted_until"`
	MyRole      string   `json:"my_role"`

	memberIDs []string
}

// MemberIDs returns the IDs of all members.
func (c *Conversation) MemberIDs() []string { return c.memberIDs }

func (s *Service) Routes(mux *http.ServeMux, authed func(http.HandlerFunc) http.HandlerFunc) {
	h := func(f func(http.ResponseWriter, *http.Request) error) http.HandlerFunc {
		return authed(httpx.Handler(f))
	}
	mux.HandleFunc("GET /v1/conversations", h(s.list))
	mux.HandleFunc("POST /v1/conversations", h(s.create))
	mux.HandleFunc("GET /v1/conversations/{id}", h(s.get))
	mux.HandleFunc("PATCH /v1/conversations/{id}", h(s.update))
	mux.HandleFunc("PUT /v1/conversations/{id}/settings", h(s.settings))
	mux.HandleFunc("POST /v1/conversations/{id}/members", h(s.addMembers))
	mux.HandleFunc("DELETE /v1/conversations/{id}/members/{uid}", h(s.removeMember))
	mux.HandleFunc("PATCH /v1/conversations/{id}/members/{uid}", h(s.setRole))
	mux.HandleFunc("GET /v1/conversations/{id}/messages", h(s.listMessages))
	mux.HandleFunc("POST /v1/conversations/{id}/messages", h(s.send))
	mux.HandleFunc("PATCH /v1/conversations/{id}/messages/{mid}", h(s.edit))
	mux.HandleFunc("DELETE /v1/conversations/{id}/messages/{mid}", h(s.deleteMessage))
	mux.HandleFunc("PUT /v1/conversations/{id}/messages/{mid}/reaction", h(s.react))
	mux.HandleFunc("DELETE /v1/conversations/{id}/messages/{mid}/reaction", h(s.unreact))
	mux.HandleFunc("POST /v1/conversations/{id}/read", h(s.markRead))
}

// ---- loading ---------------------------------------------------------------

func (s *Service) role(ctx context.Context, convID, uid string) (string, error) {
	var role string
	err := s.DB.QueryRowContext(ctx, `SELECT role FROM members WHERE conversation_id = ? AND user_id = ?`, convID, uid).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return "", httpx.ErrNotFound
	}
	return role, err
}

// Load returns a conversation as seen by uid (who must be a member).
func (s *Service) Load(ctx context.Context, convID, uid string) (*Conversation, error) {
	c := &Conversation{}
	var avatar sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT c.id, c.type, c.title, c.avatar_media_id, c.created_by, c.created_at, c.last_seq, m.role, m.muted_until
		FROM conversations c JOIN members m ON m.conversation_id = c.id AND m.user_id = ? WHERE c.id = ?`, uid, convID).
		Scan(&c.ID, &c.Type, &c.Title, &avatar, &c.CreatedBy, &c.CreatedAt, &c.LastSeq, &c.MyRole, &c.MutedUntil)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, httpx.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	c.AvatarID = avatar.String
	if err := s.loadMembers(ctx, c); err != nil {
		return nil, err
	}
	if c.LastSeq > 0 {
		msgs, err := s.messages(ctx, `m.conversation_id = ? AND m.seq = ?`, []any{c.ID, c.LastSeq}, 1)
		if err != nil {
			return nil, err
		}
		if len(msgs) > 0 {
			c.LastMessage = &msgs[0]
		}
	}
	var lastRead int64
	for _, m := range c.Members {
		if m.User.ID == uid {
			lastRead = m.LastReadSeq
		}
	}
	err = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages WHERE conversation_id = ? AND seq > ? AND sender_id <> ? AND deleted = 0 AND kind <> 'system'`, c.ID, lastRead, uid).Scan(&c.UnreadCount)
	return c, err
}

func (s *Service) loadMembers(ctx context.Context, c *Conversation) error {
	rows, err := s.DB.QueryContext(ctx, `SELECT user_id, role, last_read_seq FROM members WHERE conversation_id = ? ORDER BY joined_at`, c.ID)
	if err != nil {
		return err
	}
	type row struct {
		id, role string
		read     int64
	}
	var rs []row
	for rows.Next() {
		var x row
		if err := rows.Scan(&x.id, &x.role, &x.read); err != nil {
			rows.Close()
			return err
		}
		rs = append(rs, x)
		c.memberIDs = append(c.memberIDs, x.id)
	}
	rows.Close()
	users, err := s.Social.Users(ctx, c.memberIDs)
	if err != nil {
		return err
	}
	c.Members = c.Members[:0]
	for _, x := range rs {
		c.Members = append(c.Members, Member{User: users[x.id], Role: x.role, LastReadSeq: x.read})
	}
	return nil
}

func (s *Service) messages(ctx context.Context, where string, args []any, limit int) ([]Message, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT m.id, m.conversation_id, m.seq, m.sender_id, m.client_id, m.kind, m.body, m.reply_to, m.deleted, m.edited_at, m.created_at,
		  md.id, md.mime, md.size, md.filename
		FROM messages m LEFT JOIN media md ON md.id = m.media_id AND m.deleted = 0
		WHERE `+where+` ORDER BY m.seq DESC LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	idx := map[string]int{}
	for rows.Next() {
		var m Message
		var sealed []byte
		var reply, mid, mmime, mname sql.NullString
		var msize sql.NullInt64
		if err := rows.Scan(&m.ID, &m.ConversationID, &m.Seq, &m.SenderID, &m.ClientID, &m.Kind, &sealed, &reply, &m.Deleted, &m.EditedAt, &m.CreatedAt,
			&mid, &mmime, &msize, &mname); err != nil {
			return nil, err
		}
		m.ReplyTo = reply.String
		if mid.Valid {
			m.Media = &MediaRef{ID: mid.String, Mime: mmime.String, Size: msize.Int64, Filename: mname.String}
		}
		if !m.Deleted && len(sealed) > 0 {
			raw, err := s.Box.Open(sealed)
			if err != nil {
				return nil, err
			}
			var b body
			if err := json.Unmarshal(raw, &b); err != nil {
				return nil, err
			}
			m.Text, m.System = b.Text, b.System
		}
		m.Reactions = []Reaction{}
		idx[m.ID] = len(out)
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return out, nil
	}
	ids := make([]any, 0, len(out))
	for _, m := range out {
		ids = append(ids, m.ID)
	}
	rr, err := s.DB.QueryContext(ctx, `SELECT message_id, user_id, emoji FROM reactions WHERE message_id IN (`+store.Placeholders(len(ids))+`) ORDER BY created_at`, ids...)
	if err != nil {
		return nil, err
	}
	defer rr.Close()
	for rr.Next() {
		var mid string
		var r Reaction
		if err := rr.Scan(&mid, &r.UserID, &r.Emoji); err != nil {
			return nil, err
		}
		out[idx[mid]].Reactions = append(out[idx[mid]].Reactions, r)
	}
	return out, rr.Err()
}

func (s *Service) message(ctx context.Context, convID, mid string) (*Message, error) {
	msgs, err := s.messages(ctx, `m.conversation_id = ? AND m.id = ?`, []any{convID, mid}, 1)
	if err != nil {
		return nil, err
	}
	if len(msgs) == 0 {
		return nil, httpx.ErrNotFound
	}
	return &msgs[0], nil
}

// ---- conversations ---------------------------------------------------------

func (s *Service) list(w http.ResponseWriter, r *http.Request) error {
	id, _ := auth.From(r.Context())
	limit := httpx.QueryInt(r, "limit", 200, 1, 500)
	rows, err := s.DB.QueryContext(r.Context(), `SELECT c.id FROM conversations c JOIN members m ON m.conversation_id = c.id
		WHERE m.user_id = ? ORDER BY MAX(c.last_message_at, c.created_at) DESC LIMIT ?`, id.UserID, limit)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var cid string
		if err := rows.Scan(&cid); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, cid)
	}
	rows.Close()
	out := make([]*Conversation, 0, len(ids))
	for _, cid := range ids {
		c, err := s.Load(r.Context(), cid, id.UserID)
		if err != nil {
			return err
		}
		out = append(out, c)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"conversations": out})
	return nil
}

func (s *Service) get(w http.ResponseWriter, r *http.Request) error {
	id, _ := auth.From(r.Context())
	c, err := s.Load(r.Context(), r.PathValue("id"), id.UserID)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, c)
	return nil
}

func directKey(a, b string) string {
	if a > b {
		a, b = b, a
	}
	return a + ":" + b
}

// EnsureDirect returns (creating if needed) the 1:1 conversation of a and b.
// typ is "direct" or "bot".
func (s *Service) EnsureDirect(ctx context.Context, a, b, typ string) (string, bool, error) {
	key := directKey(a, b)
	var cid string
	err := s.DB.QueryRowContext(ctx, `SELECT id FROM conversations WHERE direct_key = ?`, key).Scan(&cid)
	if err == nil {
		return cid, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", false, err
	}
	cid = store.NewID("cnv")
	now := store.Now()
	err = store.Tx(ctx, s.DB, func(tx *sql.Tx) error {
		res, err := tx.Exec(`INSERT OR IGNORE INTO conversations(id, type, direct_key, created_by, created_at) VALUES (?,?,?,?,?)`, cid, typ, key, a, now)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 { // lost a race
			return tx.QueryRow(`SELECT id FROM conversations WHERE direct_key = ?`, key).Scan(&cid)
		}
		for _, u := range []string{a, b} {
			if _, err := tx.Exec(`INSERT INTO members(conversation_id, user_id, role, joined_at) VALUES (?,?,'member',?)`, cid, u, now); err != nil {
				return err
			}
		}
		return nil
	})
	return cid, err == nil, err
}

func (s *Service) create(w http.ResponseWriter, r *http.Request) error {
	id, _ := auth.From(r.Context())
	var req struct {
		Type      string   `json:"type"`
		UserID    string   `json:"user_id"`
		Title     string   `json:"title"`
		MemberIDs []string `json:"member_ids"`
		AvatarID  string   `json:"avatar_media_id"`
	}
	if err := httpx.Decode(w, r, &req); err != nil {
		return err
	}
	switch req.Type {
	case "direct":
		if req.UserID == "" || req.UserID == id.UserID {
			return httpx.BadRequest("user_id is required")
		}
		users, err := s.Social.Users(r.Context(), []string{req.UserID})
		if err != nil {
			return err
		}
		target, exists := users[req.UserID]
		if !exists {
			return httpx.ErrNotFound
		}
		typ := "direct"
		if target.IsBot {
			typ = "bot" // anyone can open a chat with a Platform bot
		} else {
			ok, err := s.Social.AreFriends(r.Context(), id.UserID, req.UserID)
			if err != nil {
				return err
			}
			if !ok {
				return httpx.E(http.StatusForbidden, "not_friends", "add this person as a friend first")
			}
		}
		cid, created, err := s.EnsureDirect(r.Context(), id.UserID, req.UserID, typ)
		if err != nil {
			return err
		}
		c, err := s.Load(r.Context(), cid, id.UserID)
		if err != nil {
			return err
		}
		if created {
			oc, err := s.Load(r.Context(), cid, req.UserID)
			if err == nil {
				_, _ = s.Hub.Publish(r.Context(), "conversation.updated", oc, req.UserID)
			}
			httpx.JSON(w, http.StatusCreated, c)
		} else {
			httpx.JSON(w, http.StatusOK, c)
		}
		return nil

	case "group":
		title := strings.TrimSpace(req.Title)
		if title == "" || utf8.RuneCountInString(title) > 100 {
			return httpx.BadRequest("group title must be 1-100 characters")
		}
		members := uniq(append([]string{id.UserID}, req.MemberIDs...))
		if len(members) > MaxGroupMembers {
			return httpx.BadRequest("too many members")
		}
		if err := s.checkFriends(r.Context(), id.UserID, members); err != nil {
			return err
		}
		cid := store.NewID("cnv")
		now := store.Now()
		err := store.Tx(r.Context(), s.DB, func(tx *sql.Tx) error {
			var avatar any
			if req.AvatarID != "" {
				avatar = req.AvatarID
			}
			if _, err := tx.Exec(`INSERT INTO conversations(id, type, title, avatar_media_id, created_by, created_at) VALUES (?,'group',?,?,?,?)`, cid, title, avatar, id.UserID, now); err != nil {
				return err
			}
			for _, u := range members {
				role := "member"
				if u == id.UserID {
					role = "owner"
				}
				if _, err := tx.Exec(`INSERT INTO members(conversation_id, user_id, role, joined_at) VALUES (?,?,?,?)`, cid, u, role, now); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
		if _, err := s.systemMessage(r.Context(), cid, id.UserID, SystemInfo{Event: "group_created", ActorID: id.UserID, Title: title}); err != nil {
			return err
		}
		c, err := s.Load(r.Context(), cid, id.UserID)
		if err != nil {
			return err
		}
		s.broadcastConversation(r.Context(), cid, members)
		httpx.JSON(w, http.StatusCreated, c)
		return nil
	}
	return httpx.BadRequest("type must be 'direct' or 'group'")
}

func uniq(ids []string) []string {
	seen := map[string]bool{}
	out := ids[:0:0]
	for _, id := range ids {
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// checkFriends ensures every id (other than actor) is a friend of actor.
func (s *Service) checkFriends(ctx context.Context, actor string, ids []string) error {
	for _, u := range ids {
		if u == actor {
			continue
		}
		ok, err := s.Social.AreFriends(ctx, actor, u)
		if err != nil {
			return err
		}
		if !ok {
			return httpx.E(http.StatusForbidden, "not_friends", "you can only add friends to a group")
		}
	}
	return nil
}

// broadcastConversation sends each user their own view of the conversation.
func (s *Service) broadcastConversation(ctx context.Context, cid string, users []string) {
	for _, u := range users {
		if c, err := s.Load(ctx, cid, u); err == nil {
			_, _ = s.Hub.Publish(ctx, "conversation.updated", c, u)
		}
	}
}

func isAdmin(role string) bool { return role == "owner" || role == "admin" }

func (s *Service) update(w http.ResponseWriter, r *http.Request) error {
	id, _ := auth.From(r.Context())
	cid := r.PathValue("id")
	c, err := s.Load(r.Context(), cid, id.UserID)
	if err != nil {
		return err
	}
	if c.Type != "group" || !isAdmin(c.MyRole) {
		return httpx.ErrForbidden
	}
	var req struct {
		Title    *string `json:"title"`
		AvatarID *string `json:"avatar_media_id"`
	}
	if err := httpx.Decode(w, r, &req); err != nil {
		return err
	}
	if req.Title != nil {
		t := strings.TrimSpace(*req.Title)
		if t == "" || utf8.RuneCountInString(t) > 100 {
			return httpx.BadRequest("group title must be 1-100 characters")
		}
		if _, err := s.DB.ExecContext(r.Context(), `UPDATE conversations SET title = ? WHERE id = ?`, t, cid); err != nil {
			return err
		}
		if _, err := s.systemMessage(r.Context(), cid, id.UserID, SystemInfo{Event: "title_changed", ActorID: id.UserID, Title: t}); err != nil {
			return err
		}
	}
	if req.AvatarID != nil {
		var v any
		if *req.AvatarID != "" {
			v = *req.AvatarID
		}
		if _, err := s.DB.ExecContext(r.Context(), `UPDATE conversations SET avatar_media_id = ? WHERE id = ?`, v, cid); err != nil {
			return err
		}
	}
	s.broadcastConversation(r.Context(), cid, c.memberIDs)
	return s.get(w, r)
}

func (s *Service) settings(w http.ResponseWriter, r *http.Request) error {
	id, _ := auth.From(r.Context())
	var req struct {
		MutedUntil int64 `json:"muted_until"` // 0 = unmuted, -1 = forever
	}
	if err := httpx.Decode(w, r, &req); err != nil {
		return err
	}
	if req.MutedUntil < 0 {
		req.MutedUntil = 1 << 62
	}
	res, err := s.DB.ExecContext(r.Context(), `UPDATE members SET muted_until = ? WHERE conversation_id = ? AND user_id = ?`, req.MutedUntil, r.PathValue("id"), id.UserID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return httpx.ErrNotFound
	}
	return s.get(w, r)
}

func (s *Service) addMembers(w http.ResponseWriter, r *http.Request) error {
	id, _ := auth.From(r.Context())
	cid := r.PathValue("id")
	c, err := s.Load(r.Context(), cid, id.UserID)
	if err != nil {
		return err
	}
	if c.Type != "group" || !isAdmin(c.MyRole) {
		return httpx.ErrForbidden
	}
	var req struct {
		UserIDs []string `json:"user_ids"`
	}
	if err := httpx.Decode(w, r, &req); err != nil {
		return err
	}
	existing := map[string]bool{}
	for _, m := range c.memberIDs {
		existing[m] = true
	}
	var add []string
	for _, u := range uniq(req.UserIDs) {
		if !existing[u] {
			add = append(add, u)
		}
	}
	if len(add) == 0 {
		return s.get(w, r)
	}
	if len(c.memberIDs)+len(add) > MaxGroupMembers {
		return httpx.BadRequest("group is full")
	}
	if err := s.checkFriends(r.Context(), id.UserID, add); err != nil {
		return err
	}
	now := store.Now()
	err = store.Tx(r.Context(), s.DB, func(tx *sql.Tx) error {
		for _, u := range add {
			// New members start "read" up to now, so history isn't all unread.
			if _, err := tx.Exec(`INSERT INTO members(conversation_id, user_id, role, joined_at, last_read_seq) VALUES (?,?,'member',?,?)`, cid, u, now, c.LastSeq); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if _, err := s.systemMessage(r.Context(), cid, id.UserID, SystemInfo{Event: "members_added", ActorID: id.UserID, Targets: add}); err != nil {
		return err
	}
	s.broadcastConversation(r.Context(), cid, append(c.memberIDs, add...))
	return s.get(w, r)
}

func (s *Service) removeMember(w http.ResponseWriter, r *http.Request) error {
	id, _ := auth.From(r.Context())
	cid, target := r.PathValue("id"), r.PathValue("uid")
	if target == "me" {
		target = id.UserID
	}
	c, err := s.Load(r.Context(), cid, id.UserID)
	if err != nil {
		return err
	}
	if c.Type != "group" {
		return httpx.BadRequest("you can only leave or remove people from groups")
	}
	targetRole, err := s.role(r.Context(), cid, target)
	if err != nil {
		return err
	}
	self := target == id.UserID
	if !self && (!isAdmin(c.MyRole) || targetRole == "owner" || (targetRole == "admin" && c.MyRole != "owner")) {
		return httpx.ErrForbidden
	}
	err = store.Tx(r.Context(), s.DB, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`DELETE FROM members WHERE conversation_id = ? AND user_id = ?`, cid, target); err != nil {
			return err
		}
		if self && targetRole == "owner" {
			// Hand ownership to the longest-standing admin, else member.
			_, err := tx.Exec(`UPDATE members SET role = 'owner' WHERE conversation_id = ?1 AND user_id = (
				SELECT user_id FROM members WHERE conversation_id = ?1 ORDER BY (role = 'admin') DESC, joined_at LIMIT 1)`, cid)
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	remaining := make([]string, 0, len(c.memberIDs))
	for _, m := range c.memberIDs {
		if m != target {
			remaining = append(remaining, m)
		}
	}
	if len(remaining) == 0 {
		_, err := s.DB.ExecContext(r.Context(), `DELETE FROM conversations WHERE id = ?`, cid)
		if err != nil {
			return err
		}
	} else {
		ev := SystemInfo{Event: "member_removed", ActorID: id.UserID, Targets: []string{target}}
		if self {
			ev = SystemInfo{Event: "member_left", ActorID: id.UserID}
		}
		if _, err := s.systemMessage(r.Context(), cid, id.UserID, ev); err != nil {
			return err
		}
		s.broadcastConversation(r.Context(), cid, remaining)
	}
	_, _ = s.Hub.Publish(r.Context(), "conversation.removed", map[string]string{"conversation_id": cid}, target)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Service) setRole(w http.ResponseWriter, r *http.Request) error {
	id, _ := auth.From(r.Context())
	cid, target := r.PathValue("id"), r.PathValue("uid")
	c, err := s.Load(r.Context(), cid, id.UserID)
	if err != nil {
		return err
	}
	if c.Type != "group" || c.MyRole != "owner" || target == id.UserID {
		return httpx.ErrForbidden
	}
	var req struct {
		Role string `json:"role"`
	}
	if err := httpx.Decode(w, r, &req); err != nil {
		return err
	}
	if req.Role != "admin" && req.Role != "member" && req.Role != "owner" {
		return httpx.BadRequest("role must be owner, admin or member")
	}
	if _, err := s.role(r.Context(), cid, target); err != nil {
		return err
	}
	err = store.Tx(r.Context(), s.DB, func(tx *sql.Tx) error {
		if req.Role == "owner" { // transfer ownership
			if _, err := tx.Exec(`UPDATE members SET role = 'admin' WHERE conversation_id = ? AND user_id = ?`, cid, id.UserID); err != nil {
				return err
			}
		}
		_, err := tx.Exec(`UPDATE members SET role = ? WHERE conversation_id = ? AND user_id = ?`, req.Role, cid, target)
		return err
	})
	if err != nil {
		return err
	}
	s.broadcastConversation(r.Context(), cid, c.memberIDs)
	return s.get(w, r)
}

// ---- messages --------------------------------------------------------------

// ListMessagesHandler is the message-history handler (also used by the Bot API).
func (s *Service) ListMessagesHandler(w http.ResponseWriter, r *http.Request) error {
	return s.listMessages(w, r)
}

func (s *Service) listMessages(w http.ResponseWriter, r *http.Request) error {
	id, _ := auth.From(r.Context())
	cid := r.PathValue("id")
	if _, err := s.role(r.Context(), cid, id.UserID); err != nil {
		return err
	}
	limit := int(httpx.QueryInt(r, "limit", 50, 1, 200))
	before := httpx.QueryInt(r, "before_seq", 1<<62, 1, 1<<62)
	after := httpx.QueryInt(r, "after_seq", 0, 0, 1<<62)
	msgs, err := s.messages(r.Context(), `m.conversation_id = ? AND m.seq < ? AND m.seq > ?`, []any{cid, before, after}, limit+1)
	if err != nil {
		return err
	}
	more := len(msgs) > limit
	if more {
		msgs = msgs[:limit]
	}
	if msgs == nil {
		msgs = []Message{}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"messages": msgs, "has_more": more})
	return nil
}

// SendInput is a new message.
type SendInput struct {
	ClientID string `json:"client_id"`
	Kind     string `json:"kind"`
	Text     string `json:"text"`
	MediaID  string `json:"media_id"`
	ReplyTo  string `json:"reply_to"`
}

var mediaKinds = map[string]bool{"image": true, "file": true, "voice": true, "video": true}

// Send stores a message from sender in conversation cid and fans it out.
func (s *Service) Send(ctx context.Context, sender, cid string, in SendInput) (*Message, bool, error) {
	if in.Kind == "" {
		in.Kind = "text"
	}
	in.Text = strings.TrimRight(in.Text, " \n\t")
	switch {
	case in.Kind == "text":
		if strings.TrimSpace(in.Text) == "" {
			return nil, false, httpx.BadRequest("message is empty")
		}
		in.MediaID = ""
	case mediaKinds[in.Kind]:
		if in.MediaID == "" {
			return nil, false, httpx.BadRequest("media_id is required for " + in.Kind)
		}
	default:
		return nil, false, httpx.BadRequest("kind must be text, image, file, voice or video")
	}
	if utf8.RuneCountInString(in.Text) > MaxTextLen {
		return nil, false, httpx.BadRequest("message is too long")
	}
	if len(in.ClientID) > 64 {
		return nil, false, httpx.BadRequest("client_id too long")
	}

	c, err := s.Load(ctx, cid, sender)
	if err != nil {
		return nil, false, err
	}
	if c.Type == "direct" || c.Type == "bot" {
		for _, m := range c.memberIDs {
			if m == sender {
				continue
			}
			blocked, err := s.Social.EitherBlocked(ctx, sender, m)
			if err != nil {
				return nil, false, err
			}
			if blocked {
				return nil, false, httpx.E(http.StatusForbidden, "cannot_message", "you can't message this person")
			}
		}
	}
	if in.ClientID != "" {
		var mid string
		err := s.DB.QueryRowContext(ctx, `SELECT id FROM messages WHERE conversation_id = ? AND sender_id = ? AND client_id = ?`, cid, sender, in.ClientID).Scan(&mid)
		if err == nil { // idempotent retry
			m, err := s.message(ctx, cid, mid)
			return m, false, err
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, false, err
		}
	}
	if in.MediaID != "" {
		var n int
		if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM media WHERE id = ? AND owner_id = ?`, in.MediaID, sender).Scan(&n); err != nil {
			return nil, false, err
		}
		if n == 0 {
			return nil, false, httpx.BadRequest("media not found; upload it first")
		}
	}
	if in.ReplyTo != "" {
		var n int
		if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages WHERE id = ? AND conversation_id = ?`, in.ReplyTo, cid).Scan(&n); err != nil {
			return nil, false, err
		}
		if n == 0 {
			in.ReplyTo = ""
		}
	}
	raw, _ := json.Marshal(body{Text: in.Text})
	mid, err := s.insert(ctx, cid, sender, in, s.Box.Seal(raw))
	if err != nil {
		return nil, false, err
	}
	m, err := s.message(ctx, cid, mid)
	if err != nil {
		return nil, false, err
	}
	s.fanOut(ctx, c, m)
	return m, true, nil
}

func (s *Service) insert(ctx context.Context, cid, sender string, in SendInput, sealed []byte) (string, error) {
	mid := store.NewID("msg")
	now := store.Now()
	err := store.Tx(ctx, s.DB, func(tx *sql.Tx) error {
		var seq int64
		if err := tx.QueryRow(`UPDATE conversations SET last_seq = last_seq + 1, last_message_at = ? WHERE id = ? RETURNING last_seq`, now, cid).Scan(&seq); err != nil {
			return err
		}
		var media, reply any
		if in.MediaID != "" {
			media = in.MediaID
		}
		if in.ReplyTo != "" {
			reply = in.ReplyTo
		}
		if _, err := tx.Exec(`INSERT INTO messages(id, conversation_id, seq, sender_id, client_id, kind, body, media_id, reply_to, created_at) VALUES (?,?,?,?,?,?,?,?,?,?)`,
			mid, cid, seq, sender, in.ClientID, in.Kind, sealed, media, reply, now); err != nil {
			return err
		}
		// Sending implies having read everything up to here.
		_, err := tx.Exec(`UPDATE members SET last_read_seq = ? WHERE conversation_id = ? AND user_id = ?`, seq, cid, sender)
		return err
	})
	return mid, err
}

func (s *Service) fanOut(ctx context.Context, c *Conversation, m *Message) {
	offline, err := s.Hub.Publish(ctx, "message.new", m, c.memberIDs...)
	if err != nil {
		return
	}
	if s.Notify != nil && m.Kind != "system" {
		var targets []string
		now := store.Now()
		muted := map[string]bool{}
		rows, err := s.DB.QueryContext(ctx, `SELECT user_id FROM members WHERE conversation_id = ? AND muted_until > ?`, c.ID, now)
		if err == nil {
			for rows.Next() {
				var u string
				if rows.Scan(&u) == nil {
					muted[u] = true
				}
			}
			rows.Close()
		}
		for _, u := range offline {
			if u != m.SenderID && !muted[u] {
				targets = append(targets, u)
			}
		}
		if len(targets) > 0 {
			users, _ := s.Social.Users(ctx, []string{m.SenderID})
			s.Notify.NotifyMessage(ctx, targets, c, m, users[m.SenderID])
		}
	}
	if s.OnMessage != nil {
		s.OnMessage(ctx, c, m)
	}
}

func (s *Service) systemMessage(ctx context.Context, cid, actor string, info SystemInfo) (*Message, error) {
	raw, _ := json.Marshal(body{System: &info})
	mid, err := s.insert(ctx, cid, actor, SendInput{Kind: "system"}, s.Box.Seal(raw))
	if err != nil {
		return nil, err
	}
	m, err := s.message(ctx, cid, mid)
	if err != nil {
		return nil, err
	}
	c, err := s.Load(ctx, cid, actor)
	if err != nil {
		return m, nil // actor may have just left
	}
	s.fanOut(ctx, c, m)
	return m, nil
}

func (s *Service) send(w http.ResponseWriter, r *http.Request) error {
	id, _ := auth.From(r.Context())
	var in SendInput
	if err := httpx.Decode(w, r, &in); err != nil {
		return err
	}
	if in.Kind == "system" {
		return httpx.BadRequest("invalid kind")
	}
	m, created, err := s.Send(r.Context(), id.UserID, r.PathValue("id"), in)
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

func (s *Service) publishUpdate(ctx context.Context, cid string, m *Message) {
	c, err := s.Load(ctx, cid, m.SenderID)
	var members []string
	if err == nil {
		members = c.memberIDs
	} else {
		rows, err := s.DB.QueryContext(ctx, `SELECT user_id FROM members WHERE conversation_id = ?`, cid)
		if err != nil {
			return
		}
		for rows.Next() {
			var u string
			if rows.Scan(&u) == nil {
				members = append(members, u)
			}
		}
		rows.Close()
	}
	_, _ = s.Hub.Publish(ctx, "message.updated", m, members...)
}

func (s *Service) edit(w http.ResponseWriter, r *http.Request) error {
	id, _ := auth.From(r.Context())
	cid, mid := r.PathValue("id"), r.PathValue("mid")
	if _, err := s.role(r.Context(), cid, id.UserID); err != nil {
		return err
	}
	m, err := s.message(r.Context(), cid, mid)
	if err != nil {
		return err
	}
	if m.SenderID != id.UserID || m.Deleted || m.Kind == "system" {
		return httpx.ErrForbidden
	}
	if time.Since(time.UnixMilli(m.CreatedAt)) > EditWindow {
		return httpx.E(http.StatusForbidden, "edit_window_passed", "messages can only be edited for 15 minutes")
	}
	var req struct {
		Text string `json:"text"`
	}
	if err := httpx.Decode(w, r, &req); err != nil {
		return err
	}
	req.Text = strings.TrimRight(req.Text, " \n\t")
	if (m.Kind == "text" && strings.TrimSpace(req.Text) == "") || utf8.RuneCountInString(req.Text) > MaxTextLen {
		return httpx.BadRequest("invalid text")
	}
	raw, _ := json.Marshal(body{Text: req.Text})
	if _, err := s.DB.ExecContext(r.Context(), `UPDATE messages SET body = ?, edited_at = ? WHERE id = ?`, s.Box.Seal(raw), store.Now(), mid); err != nil {
		return err
	}
	m, err = s.message(r.Context(), cid, mid)
	if err != nil {
		return err
	}
	s.publishUpdate(r.Context(), cid, m)
	httpx.JSON(w, http.StatusOK, m)
	return nil
}

func (s *Service) deleteMessage(w http.ResponseWriter, r *http.Request) error {
	id, _ := auth.From(r.Context())
	cid, mid := r.PathValue("id"), r.PathValue("mid")
	role, err := s.role(r.Context(), cid, id.UserID)
	if err != nil {
		return err
	}
	m, err := s.message(r.Context(), cid, mid)
	if err != nil {
		return err
	}
	if m.Kind == "system" || (m.SenderID != id.UserID && !isAdmin(role)) {
		return httpx.ErrForbidden
	}
	if !m.Deleted {
		err = store.Tx(r.Context(), s.DB, func(tx *sql.Tx) error {
			if _, err := tx.Exec(`UPDATE messages SET deleted = 1, body = NULL, media_id = NULL WHERE id = ?`, mid); err != nil {
				return err
			}
			_, err := tx.Exec(`DELETE FROM reactions WHERE message_id = ?`, mid)
			return err
		})
		if err != nil {
			return err
		}
		if m, err = s.message(r.Context(), cid, mid); err != nil {
			return err
		}
		s.publishUpdate(r.Context(), cid, m)
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Service) react(w http.ResponseWriter, r *http.Request) error {
	id, _ := auth.From(r.Context())
	cid, mid := r.PathValue("id"), r.PathValue("mid")
	if _, err := s.role(r.Context(), cid, id.UserID); err != nil {
		return err
	}
	var req struct {
		Emoji string `json:"emoji"`
	}
	if err := httpx.Decode(w, r, &req); err != nil {
		return err
	}
	if req.Emoji == "" || len(req.Emoji) > 32 || utf8.RuneCountInString(req.Emoji) > 8 {
		return httpx.BadRequest("emoji must be a single emoji")
	}
	m, err := s.message(r.Context(), cid, mid)
	if err != nil {
		return err
	}
	if m.Deleted || m.Kind == "system" {
		return httpx.ErrForbidden
	}
	if _, err := s.DB.ExecContext(r.Context(), `INSERT INTO reactions(message_id, user_id, emoji, created_at) VALUES (?,?,?,?)
		ON CONFLICT(message_id, user_id) DO UPDATE SET emoji = excluded.emoji, created_at = excluded.created_at`, mid, id.UserID, req.Emoji, store.Now()); err != nil {
		return err
	}
	if m, err = s.message(r.Context(), cid, mid); err != nil {
		return err
	}
	s.publishUpdate(r.Context(), cid, m)
	httpx.JSON(w, http.StatusOK, m)
	return nil
}

func (s *Service) unreact(w http.ResponseWriter, r *http.Request) error {
	id, _ := auth.From(r.Context())
	cid, mid := r.PathValue("id"), r.PathValue("mid")
	if _, err := s.role(r.Context(), cid, id.UserID); err != nil {
		return err
	}
	if _, err := s.DB.ExecContext(r.Context(), `DELETE FROM reactions WHERE message_id = ? AND user_id = ?`, mid, id.UserID); err != nil {
		return err
	}
	m, err := s.message(r.Context(), cid, mid)
	if err != nil {
		return err
	}
	s.publishUpdate(r.Context(), cid, m)
	httpx.JSON(w, http.StatusOK, m)
	return nil
}

func (s *Service) markRead(w http.ResponseWriter, r *http.Request) error {
	id, _ := auth.From(r.Context())
	cid := r.PathValue("id")
	var req struct {
		Seq int64 `json:"seq"`
	}
	if err := httpx.Decode(w, r, &req); err != nil {
		return err
	}
	c, err := s.Load(r.Context(), cid, id.UserID)
	if err != nil {
		return err
	}
	if req.Seq > c.LastSeq {
		req.Seq = c.LastSeq
	}
	res, err := s.DB.ExecContext(r.Context(), `UPDATE members SET last_read_seq = ? WHERE conversation_id = ? AND user_id = ? AND last_read_seq < ?`, req.Seq, cid, id.UserID, req.Seq)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		var receipts bool
		_ = s.DB.QueryRowContext(r.Context(), `SELECT send_read_receipts FROM users WHERE id = ?`, id.UserID).Scan(&receipts)
		targets := []string{id.UserID} // always sync my other devices
		if receipts {
			targets = c.memberIDs
		}
		_, _ = s.Hub.Publish(r.Context(), "read", map[string]any{"conversation_id": cid, "user_id": id.UserID, "seq": req.Seq}, targets...)
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// Typing handles a client "typing" WebSocket message.
func (s *Service) Typing(ctx context.Context, uid, cid string) {
	rows, err := s.DB.QueryContext(ctx, `SELECT user_id FROM members WHERE conversation_id = ? AND user_id <> ?
		AND EXISTS (SELECT 1 FROM members WHERE conversation_id = ? AND user_id = ?)`, cid, uid, cid, uid)
	if err != nil {
		return
	}
	var others []string
	for rows.Next() {
		var u string
		if rows.Scan(&u) == nil {
			others = append(others, u)
		}
	}
	rows.Close()
	s.Hub.Ephemeral("typing", map[string]string{"conversation_id": cid, "user_id": uid}, others...)
}
