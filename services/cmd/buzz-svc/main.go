// buzz-svc: authorizes and dispatches buzzes.
//
// Local dev uses in-memory adapters seeded with demo users; production wires
// graph-svc (gRPC), Redis (rate limits), Postgres (events) and Kafka (dispatch).
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/buzzit/services/internal/buzz"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	addr := os.Getenv("BUZZ_ADDR")
	if addr == "" {
		addr = ":8081"
	}

	graph := buzz.NewMemoryGraph()
	if os.Getenv("BUZZ_DEMO_SEED") != "0" {
		graph.AddFriends("alice", "bob")
		graph.Grant(buzz.Pass{GrantorID: "alice", GranteeID: "bob", MaxLevel: buzz.LevelEmergency})
	}
	srv := &buzz.Server{
		Auth:  &buzz.Authorizer{Graph: graph, Limiter: buzz.NewMemoryLimiter(), Policy: buzz.DefaultPolicy},
		Store: buzz.NewMemoryStore(),
		Dispatch: &buzz.LogDispatcher{Log: func(ev *buzz.Event) {
			log.Info("buzz dispatched", "id", ev.ID, "from", ev.SenderID, "to", ev.RecipientID, "level", ev.Level.String())
		}},
	}

	hs := &http.Server{Addr: addr, Handler: srv.Routes(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		log.Info("buzz-svc listening", "addr", addr)
		if err := hs.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("listen", "err", err)
			os.Exit(1)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = hs.Shutdown(shutdown)
}
