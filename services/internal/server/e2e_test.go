package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/buzzit/services/internal/webhook"
)

type client struct {
	t      *testing.T
	base   string
	token  string
	userID string

	ws     *websocket.Conn
	mu     sync.Mutex
	events []map[string]any
	notify chan struct{}
}

func newApp(t *testing.T) (*App, *httptest.Server) {
	t.Helper()
	app, err := New(Config{DataDir: t.TempDir(), InMemory: true, BcryptCost: 4, AllowPrivateWebhooks: true, MaxUploadMB: 1})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(app.Handler)
	t.Cleanup(func() { srv.Close(); app.Close() })
	return app, srv
}

func (c *client) do(method, path string, body any, want int) map[string]any {
	c.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.base+path, rd)
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		c.t.Fatalf("%s %s: got %d want %d: %s", method, path, resp.StatusCode, want, raw)
	}
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return out
}

func register(t *testing.T, base, username string) *client {
	c := &client{t: t, base: base, notify: make(chan struct{}, 1000)}
	r := c.do("POST", "/v1/auth/register", map[string]string{"username": username, "password": "correct horse battery", "display_name": strings.ToUpper(username[:1]) + username[1:]}, 201)
	c.token, c.userID = r["token"].(string), r["user_id"].(string)
	return c
}

// connect opens the WebSocket and records every event.
func (c *client) connect(ctx context.Context) {
	c.t.Helper()
	url := "ws" + strings.TrimPrefix(c.base, "http") + "/v1/ws"
	ws, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + c.token}}})
	if err != nil {
		c.t.Fatal(err)
	}
	go func() {
		for {
			_, data, err := ws.Read(ctx)
			if err != nil {
				return
			}
			var ev map[string]any
			_ = json.Unmarshal(data, &ev)
			c.mu.Lock()
			c.events = append(c.events, ev)
			c.mu.Unlock()
			c.notify <- struct{}{}
		}
	}()
	c.waitFor("hello", nil)
	c.ws = ws
}

func (c *client) send(m map[string]string) {
	b, _ := json.Marshal(m)
	if err := c.ws.Write(context.Background(), websocket.MessageText, b); err != nil {
		c.t.Fatal(err)
	}
}

// waitFor returns the first unseen event of type typ matching pred.
func (c *client) waitFor(typ string, pred func(data map[string]any) bool) map[string]any {
	c.t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		c.mu.Lock()
		for i, ev := range c.events {
			if ev["type"] == typ {
				data, _ := ev["data"].(map[string]any)
				if pred == nil || pred(data) {
					c.events = append(c.events[:i:i], c.events[i+1:]...)
					c.mu.Unlock()
					return data
				}
			}
		}
		c.mu.Unlock()
		select {
		case <-c.notify:
		case <-deadline:
			c.t.Fatalf("%s: timed out waiting for %q event", c.userID, typ)
		}
	}
}

func TestEndToEnd(t *testing.T) {
	app, srv := newApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	alice := register(t, srv.URL, "alice")
	bob := register(t, srv.URL, "bob")
	alice.connect(ctx)
	bob.connect(ctx)

	// Duplicate usernames are rejected (case-insensitive).
	(&client{t: t, base: srv.URL}).do("POST", "/v1/auth/register", map[string]string{"username": "ALICE", "password": "whatever123"}, 409)
	// Wrong password.
	(&client{t: t, base: srv.URL}).do("POST", "/v1/auth/login", map[string]string{"username": "alice", "password": "nope-nope"}, 401)

	// --- friends ---------------------------------------------------------
	bob.do("POST", "/v1/friends/requests", map[string]string{"username": "alice"}, 202)
	alice.waitFor("friend.request", nil)
	alice.do("POST", "/v1/friends/requests/"+bob.userID+"/accept", nil, 200)
	bob.waitFor("friend.added", nil)
	friends := bob.do("GET", "/v1/friends", nil, 200)["friends"].([]any)
	if len(friends) != 1 || friends[0].(map[string]any)["can_buzz_level"] != "nudge" {
		t.Fatalf("friends: %v", friends)
	}

	// --- direct chat -----------------------------------------------------
	conv := bob.do("POST", "/v1/conversations", map[string]string{"type": "direct", "user_id": alice.userID}, 201)
	cid := conv["id"].(string)
	alice.waitFor("conversation.updated", nil)
	// Opening it again returns the same conversation.
	if again := alice.do("POST", "/v1/conversations", map[string]string{"type": "direct", "user_id": bob.userID}, 200); again["id"] != cid {
		t.Fatal("direct conversation not reused")
	}

	msg := bob.do("POST", "/v1/conversations/"+cid+"/messages", map[string]string{"text": "hey alice 👋", "client_id": "c1"}, 201)
	got := alice.waitFor("message.new", func(d map[string]any) bool { return d["id"] == msg["id"] })
	if got["text"] != "hey alice 👋" {
		t.Fatalf("message text: %v", got)
	}
	// Retrying with the same client_id is idempotent.
	if dup := bob.do("POST", "/v1/conversations/"+cid+"/messages", map[string]string{"text": "hey alice 👋", "client_id": "c1"}, 200); dup["id"] != msg["id"] {
		t.Fatal("idempotent send created a new message")
	}

	convs := alice.do("GET", "/v1/conversations", nil, 200)["conversations"].([]any)
	if n := convs[0].(map[string]any)["unread_count"].(float64); n != 1 {
		t.Fatalf("unread = %v", n)
	}
	alice.do("POST", "/v1/conversations/"+cid+"/read", map[string]any{"seq": msg["seq"]}, 204)
	bob.waitFor("read", func(d map[string]any) bool { return d["user_id"] == alice.userID })

	alice.send(map[string]string{"type": "typing", "conversation_id": cid})
	bob.waitFor("typing", func(d map[string]any) bool { return d["user_id"] == alice.userID })

	mid := msg["id"].(string)
	bob.do("PATCH", "/v1/conversations/"+cid+"/messages/"+mid, map[string]string{"text": "hey alice!"}, 200)
	alice.waitFor("message.updated", func(d map[string]any) bool { return d["text"] == "hey alice!" })
	alice.do("PUT", "/v1/conversations/"+cid+"/messages/"+mid+"/reaction", map[string]string{"emoji": "❤️"}, 200)
	alice.do("PATCH", "/v1/conversations/"+cid+"/messages/"+mid, map[string]string{"text": "hacked"}, 403) // not the sender

	hist := alice.do("GET", "/v1/conversations/"+cid+"/messages", nil, 200)["messages"].([]any)
	if r := hist[0].(map[string]any)["reactions"].([]any); len(r) != 1 {
		t.Fatalf("reactions: %v", r)
	}

	// Message bodies are encrypted at rest.
	var sealed []byte
	if err := app.DB.QueryRow(`SELECT body FROM messages WHERE id = ?`, mid).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("hey alice")) {
		t.Fatal("message body stored in plaintext")
	}

	// --- media -----------------------------------------------------------
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", "pic.png")
	fw.Write([]byte("\x89PNG\r\n\x1a\n" + strings.Repeat("x", 100)))
	mw.Close()
	req, _ := http.NewRequest("POST", srv.URL+"/v1/media", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+bob.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 201 {
		t.Fatalf("upload: %v %v", err, resp.Status)
	}
	var med map[string]any
	json.NewDecoder(resp.Body).Decode(&med)
	resp.Body.Close()
	if med["mime"] != "image/png" {
		t.Fatalf("mime sniffing: %v", med)
	}
	mediaID := med["id"].(string)
	alice.do("GET", "/v1/media/"+mediaID, nil, 404) // not shared yet
	bob.do("POST", "/v1/conversations/"+cid+"/messages", map[string]string{"kind": "image", "media_id": mediaID}, 201)
	alice.do("GET", "/v1/media/"+mediaID, nil, 200)

	// --- buzz ------------------------------------------------------------
	bob.do("POST", "/v1/buzz", map[string]string{"recipient": alice.userID, "level": "emergency"}, 403)
	alice.do("PUT", "/v1/buzz-passes/"+bob.userID, map[string]any{"max_level": "emergency", "tz": "Europe/Paris"}, 200)
	bob.waitFor("buzz_pass.changed", nil)
	bz := bob.do("POST", "/v1/buzz", map[string]string{"recipient": alice.userID, "level": "emergency", "note": "call me now"}, 202)
	in := alice.waitFor("buzz", nil)
	if in["buzz"].(map[string]any)["id"] != bz["id"] || in["sender"].(map[string]any)["username"] != "bob" {
		t.Fatalf("buzz event: %v", in)
	}
	alice.do("POST", "/v1/buzz/"+bz["id"].(string)+"/status", map[string]string{"status": "acked"}, 200)
	bob.waitFor("buzz.status", func(d map[string]any) bool { return d["status"] == "acked" })

	// --- sync replay -----------------------------------------------------
	sync := alice.do("GET", "/v1/sync?since=0", nil, 200)
	if len(sync["events"].([]any)) < 5 {
		t.Fatalf("sync returned too few events: %v", sync)
	}

	// --- groups ----------------------------------------------------------
	carol := register(t, srv.URL, "carol")
	carol.connect(ctx)
	carol.do("POST", "/v1/friends/requests", map[string]string{"user_id": alice.userID}, 202)
	alice.do("POST", "/v1/friends/requests/"+carol.userID+"/accept", nil, 200)
	grp := alice.do("POST", "/v1/conversations", map[string]any{"type": "group", "title": "Weekend", "member_ids": []string{bob.userID, carol.userID}}, 201)
	gid := grp["id"].(string)
	if len(grp["members"].([]any)) != 3 || grp["my_role"] != "owner" {
		t.Fatalf("group: %v", grp)
	}
	carol.do("POST", "/v1/conversations/"+gid+"/messages", map[string]string{"text": "hi all"}, 201)
	bob.waitFor("message.new", func(d map[string]any) bool { return d["text"] == "hi all" })
	carol.do("DELETE", "/v1/conversations/"+gid+"/members/"+bob.userID, nil, 403) // not admin
	bob.do("DELETE", "/v1/conversations/"+gid+"/members/me", nil, 204)
	bob.do("GET", "/v1/conversations/"+gid, nil, 404)

	// --- block -----------------------------------------------------------
	alice.do("PUT", "/v1/blocks/"+bob.userID, nil, 204)
	bob.do("POST", "/v1/conversations/"+cid+"/messages", map[string]string{"text": "hello?"}, 403)
	bob.do("POST", "/v1/buzz", map[string]string{"recipient": alice.userID, "level": "nudge"}, 403)
	alice.do("DELETE", "/v1/blocks/"+bob.userID, nil, 204)

	// --- Platform --------------------------------------------------------
	hooks := make(chan *http.Request, 10)
	bodies := make(chan []byte, 10)
	hookSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		hooks <- r
		bodies <- b
	}))
	defer hookSrv.Close()

	created := carol.do("POST", "/v1/apps", map[string]string{"name": "Door Bell", "description": "Rings you when someone is at the door", "webhook_url": hookSrv.URL}, 201)
	appInfo := created["app"].(map[string]any)
	appID, secret := appInfo["id"].(string), created["webhook_secret"].(string)
	if appInfo["bot_username"] != "door_bell_bot" {
		t.Fatalf("bot username: %v", appInfo["bot_username"])
	}
	bot := &client{t: t, base: srv.URL, token: created["bot_token"].(string)}
	bot.do("GET", "/v1/bot/me", nil, 200)
	bot.do("GET", "/v1/me", nil, 403) // bot tokens can't use user endpoints

	bot.do("POST", "/v1/bot/messages", map[string]string{"to": alice.userID, "text": "Ding dong"}, 403) // no consent yet
	alice.do("POST", "/v1/apps/"+appID+"/grant", map[string]any{"scopes": []string{"messages:send", "buzz:send"}}, 200)
	expectHook := func(typ string) map[string]any {
		t.Helper()
		app.Platform.DeliverNow(ctx)
		select {
		case r := <-hooks:
			b := <-bodies
			if err := webhook.Verify([]byte(secret), b, r.Header.Get(webhook.Header), time.Now(), webhook.DefaultTolerance); err != nil {
				t.Fatalf("webhook signature: %v", err)
			}
			var ev map[string]any
			json.Unmarshal(b, &ev)
			if ev["type"] != typ {
				t.Fatalf("webhook type %v, want %s", ev["type"], typ)
			}
			return ev
		case <-time.After(5 * time.Second):
			t.Fatalf("no %s webhook", typ)
		}
		return nil
	}
	expectHook("user.authorized")

	bot.do("POST", "/v1/bot/messages", map[string]string{"to": alice.userID, "text": "Ding dong, someone is at the door"}, 201)
	alice.waitFor("message.new", func(d map[string]any) bool { return d["text"] == "Ding dong, someone is at the door" })

	bot.do("POST", "/v1/bot/buzz", map[string]string{"recipient": alice.userID, "level": "emergency"}, 403) // never for apps
	abz := bot.do("POST", "/v1/bot/buzz", map[string]string{"recipient": alice.userID, "level": "nudge", "note": "Door"}, 202)
	alice.waitFor("buzz", func(d map[string]any) bool { return d["buzz"].(map[string]any)["id"] == abz["id"] })
	alice.do("POST", "/v1/buzz/"+abz["id"].(string)+"/status", map[string]string{"status": "acked"}, 200)
	expectHook("buzz.acknowledged")

	// The user replies in the bot chat -> message.created webhook.
	botConv := alice.do("POST", "/v1/conversations", map[string]string{"type": "direct", "user_id": appInfo["bot_user_id"].(string)}, 200)
	alice.do("POST", "/v1/conversations/"+botConv["id"].(string)+"/messages", map[string]string{"text": "who is it?"}, 201)
	ev := expectHook("message.created")
	if ev["data"].(map[string]any)["message"].(map[string]any)["text"] != "who is it?" {
		t.Fatalf("message.created payload: %v", ev)
	}

	usage := carol.do("GET", "/v1/apps/"+appID+"/usage", nil, 200)
	if u := usage["usage"].(map[string]any); u["messages"].(float64) != 1 || u["buzzes"].(float64) != 1 {
		t.Fatalf("usage: %v", usage)
	}

	alice.do("DELETE", "/v1/connected-apps/"+appID, nil, 204)
	expectHook("user.revoked")
	bot.do("POST", "/v1/bot/buzz", map[string]string{"recipient": alice.userID, "level": "nudge"}, 403)

	// --- sessions --------------------------------------------------------
	alice.do("POST", "/v1/auth/logout", nil, 204)
	alice.do("GET", "/v1/me", nil, 401)
	fmt.Println("e2e ok")
}
