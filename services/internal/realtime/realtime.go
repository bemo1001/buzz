// Package realtime delivers events to users: a persistent per-user event log
// (replayable through /v1/sync) and a live WebSocket hub.
package realtime

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/buzzit/services/internal/auth"
	"github.com/buzzit/services/internal/crypt"
	"github.com/buzzit/services/internal/httpx"
	"github.com/buzzit/services/internal/store"
)

// Event is what clients receive. Seq is 0 for ephemeral events (typing,
// presence) that are never stored.
type Event struct {
	Seq  int64           `json:"seq,omitempty"`
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// Retention of the event log. Clients offline for longer do a full refresh.
const Retention = 30 * 24 * time.Hour

type conn struct {
	deviceID string
	send     chan Event
}

// Hub fans events out to connected devices and records them.
type Hub struct {
	DB  *sql.DB
	Box *crypt.Box

	// OnOnline is called with (userID, online) when a user's first device
	// connects or last device disconnects.
	OnOnline func(userID string, online bool)

	mu    sync.RWMutex
	conns map[string]map[*conn]struct{}
}

func NewHub(db *sql.DB, box *crypt.Box) *Hub {
	return &Hub{DB: db, Box: box, conns: map[string]map[*conn]struct{}{}}
}

// Online reports whether the user has at least one live connection.
func (h *Hub) Online(userID string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.conns[userID]) > 0
}

// Publish stores the event for each user and delivers it live. It returns the
// set of users that had no live connection (candidates for push).
func (h *Hub) Publish(ctx context.Context, typ string, data any, userIDs ...string) (offline []string, err error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	sealed := h.Box.Seal(raw)
	now := store.Now()
	seqs := make(map[string]int64, len(userIDs))
	err = store.Tx(ctx, h.DB, func(tx *sql.Tx) error {
		for _, uid := range userIDs {
			if _, ok := seqs[uid]; ok {
				continue
			}
			var seq int64
			if err := tx.QueryRow(`UPDATE users SET event_seq = event_seq + 1 WHERE id = ? RETURNING event_seq`, uid).Scan(&seq); err != nil {
				if err == sql.ErrNoRows {
					continue
				}
				return err
			}
			if _, err := tx.Exec(`INSERT INTO events(user_id, seq, type, payload, created_at) VALUES (?,?,?,?,?)`, uid, seq, typ, sealed, now); err != nil {
				return err
			}
			seqs[uid] = seq
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for uid, seq := range seqs {
		if !h.deliver(uid, Event{Seq: seq, Type: typ, Data: raw}) {
			offline = append(offline, uid)
		}
	}
	return offline, nil
}

// Ephemeral delivers an event live only (typing, presence).
func (h *Hub) Ephemeral(typ string, data any, userIDs ...string) {
	raw, err := json.Marshal(data)
	if err != nil {
		return
	}
	for _, uid := range userIDs {
		h.deliver(uid, Event{Type: typ, Data: raw})
	}
}

func (h *Hub) deliver(uid string, ev Event) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	cs := h.conns[uid]
	for c := range cs {
		select {
		case c.send <- ev:
		default:
			// Slow client: drop live delivery, it will catch up through /v1/sync.
			slog.Warn("realtime: send buffer full", "user", uid, "device", c.deviceID)
		}
	}
	return len(cs) > 0
}

func (h *Hub) add(uid string, c *conn) {
	h.mu.Lock()
	first := len(h.conns[uid]) == 0
	if h.conns[uid] == nil {
		h.conns[uid] = map[*conn]struct{}{}
	}
	h.conns[uid][c] = struct{}{}
	h.mu.Unlock()
	if first && h.OnOnline != nil {
		h.OnOnline(uid, true)
	}
}

func (h *Hub) remove(uid string, c *conn) {
	h.mu.Lock()
	delete(h.conns[uid], c)
	last := len(h.conns[uid]) == 0
	if last {
		delete(h.conns, uid)
	}
	h.mu.Unlock()
	if last && h.OnOnline != nil {
		h.OnOnline(uid, false)
	}
}

// ClientMessage is what a client may send over the socket.
type ClientMessage struct {
	Type           string `json:"type"` // "typing" | "ping"
	ConversationID string `json:"conversation_id,omitempty"`
}

// Handler serves /v1/ws. onClient handles client messages (e.g. typing).
func (h *Hub) Handler(onClient func(ctx context.Context, id auth.Identity, m ClientMessage)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, _ := auth.From(r.Context())
		ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true}) // token auth, not cookies
		if err != nil {
			return
		}
		ws.SetReadLimit(4096)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		c := &conn{deviceID: id.DeviceID, send: make(chan Event, 256)}
		h.add(id.UserID, c)
		defer h.remove(id.UserID, c)

		var latest int64
		_ = h.DB.QueryRowContext(ctx, `SELECT event_seq FROM users WHERE id = ?`, id.UserID).Scan(&latest)
		hello, _ := json.Marshal(map[string]any{"latest_seq": latest, "server_time": store.Now()})
		c.send <- Event{Type: "hello", Data: hello}

		go func() { // reader
			defer cancel()
			for {
				var m ClientMessage
				_, data, err := ws.Read(ctx)
				if err != nil {
					return
				}
				if json.Unmarshal(data, &m) != nil {
					continue
				}
				if m.Type == "ping" {
					pong, _ := json.Marshal(map[string]int64{"server_time": store.Now()})
					select {
					case c.send <- Event{Type: "pong", Data: pong}:
					default:
					}
					continue
				}
				if onClient != nil {
					onClient(ctx, id, m)
				}
			}
		}()

		keepalive := time.NewTicker(30 * time.Second)
		defer keepalive.Stop()
		for {
			select {
			case <-ctx.Done():
				ws.Close(websocket.StatusNormalClosure, "")
				return
			case ev := <-c.send:
				b, _ := json.Marshal(ev)
				wctx, wc := context.WithTimeout(ctx, 10*time.Second)
				err := ws.Write(wctx, websocket.MessageText, b)
				wc()
				if err != nil {
					return
				}
			case <-keepalive.C:
				pctx, pc := context.WithTimeout(ctx, 10*time.Second)
				err := ws.Ping(pctx)
				pc()
				if err != nil {
					return
				}
			}
		}
	}
}

// SyncHandler serves GET /v1/sync?since=<seq>&limit=<n>.
func (h *Hub) SyncHandler(w http.ResponseWriter, r *http.Request) error {
	id, _ := auth.From(r.Context())
	since := httpx.QueryInt(r, "since", 0, 0, 1<<62)
	limit := httpx.QueryInt(r, "limit", 500, 1, 1000)
	var latest int64
	if err := h.DB.QueryRowContext(r.Context(), `SELECT event_seq FROM users WHERE id = ?`, id.UserID).Scan(&latest); err != nil {
		return err
	}
	var oldest sql.NullInt64
	_ = h.DB.QueryRowContext(r.Context(), `SELECT MIN(seq) FROM events WHERE user_id = ?`, id.UserID).Scan(&oldest)
	// Gap: events the client missed were pruned. It must refetch state.
	reset := since > 0 && oldest.Valid && since < oldest.Int64-1

	rows, err := h.DB.QueryContext(r.Context(), `SELECT seq, type, payload FROM events WHERE user_id = ? AND seq > ? ORDER BY seq LIMIT ?`, id.UserID, since, limit)
	if err != nil {
		return err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var ev Event
		var sealed []byte
		if err := rows.Scan(&ev.Seq, &ev.Type, &sealed); err != nil {
			return err
		}
		raw, err := h.Box.Open(sealed)
		if err != nil {
			return err
		}
		ev.Data = raw
		out = append(out, ev)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"events":   out,
		"latest":   latest,
		"has_more": len(out) == int(limit),
		"reset":    reset,
	})
	return nil
}

// Prune deletes events older than Retention. Run periodically.
func (h *Hub) Prune(ctx context.Context) error {
	_, err := h.DB.ExecContext(ctx, `DELETE FROM events WHERE created_at < ?`, time.Now().Add(-Retention).UnixMilli())
	return err
}
