package buzz

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newTestServer() (*Server, *MemoryGraph, *LogDispatcher) {
	g := NewMemoryGraph()
	g.AddFriends("alice", "bob")
	d := &LogDispatcher{}
	return &Server{
		Auth:     &Authorizer{Graph: g, Limiter: NewMemoryLimiter(), Policy: DefaultPolicy, Now: time.Now},
		Store:    NewMemoryStore(),
		Dispatch: d,
	}, g, d
}

func do(h http.Handler, method, path, user, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if user != "" {
		req.Header.Set("X-Buzzit-User", user)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestHTTPFlow(t *testing.T) {
	s, g, d := newTestServer()
	h := s.Routes()

	if rec := do(h, "POST", "/v1/buzz", "", `{}`); rec.Code != 401 {
		t.Fatalf("unauth: %d", rec.Code)
	}
	if rec := do(h, "POST", "/v1/buzz", "bob", `{"recipient":"alice","level":"buzz"}`); rec.Code != 403 {
		t.Fatalf("no pass: %d %s", rec.Code, rec.Body)
	}
	g.Grant(Pass{GrantorID: "alice", GranteeID: "bob", MaxLevel: LevelBuzz})
	rec := do(h, "POST", "/v1/buzz", "bob", `{"recipient":"alice","level":"buzz","note":"call me"}`)
	if rec.Code != 202 {
		t.Fatalf("send: %d %s", rec.Code, rec.Body)
	}
	var ev eventResp
	_ = json.Unmarshal(rec.Body.Bytes(), &ev)
	if len(d.Sent) != 1 || ev.Status != StatusSent {
		t.Fatalf("not dispatched: %+v", ev)
	}

	if rec := do(h, "POST", "/v1/buzz/"+ev.ID+"/status", "bob", `{"status":"acked"}`); rec.Code != 404 {
		t.Fatalf("sender must not ack: %d", rec.Code)
	}
	if rec := do(h, "POST", "/v1/buzz/"+ev.ID+"/status", "alice", `{"status":"acked"}`); rec.Code != 200 {
		t.Fatalf("ack: %d", rec.Code)
	}
	do(h, "POST", "/v1/buzz/"+ev.ID+"/status", "alice", `{"status":"rang"}`) // late, must not regress
	rec = do(h, "GET", "/v1/buzz/"+ev.ID, "bob", "")
	_ = json.Unmarshal(rec.Body.Bytes(), &ev)
	if ev.Status != StatusAcked {
		t.Fatalf("status regressed: %s", ev.Status)
	}
	if rec := do(h, "GET", "/v1/buzz/"+ev.ID, "eve", ""); rec.Code != 404 {
		t.Fatalf("third party read: %d", rec.Code)
	}

	for i := 0; i < 2; i++ {
		do(h, "POST", "/v1/buzz", "bob", `{"recipient":"alice","level":"buzz"}`)
	}
	if rec := do(h, "POST", "/v1/buzz", "bob", `{"recipient":"alice","level":"buzz"}`); rec.Code != 429 {
		t.Fatalf("rate limit: %d", rec.Code)
	}
}
