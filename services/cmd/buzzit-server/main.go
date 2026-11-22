// buzzit-server runs the whole Buzzit backend as one process:
// accounts, friends, chats, media, realtime, Buzz and the Platform API.
//
//	buzzit-server --addr :8080 --data ./data
package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/buzzit/services/internal/server"
)

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	maxUpload, _ := strconv.ParseInt(env("BUZZIT_MAX_UPLOAD_MB", "50"), 10, 64)
	addr := flag.String("addr", env("BUZZIT_ADDR", ":8080"), "listen address")
	data := flag.String("data", env("BUZZIT_DATA", "./data"), "data directory (database, key, media)")
	fcm := flag.String("fcm-service-account", env("BUZZIT_FCM_SERVICE_ACCOUNT", ""), "optional Firebase service-account JSON for push")
	tlsCert := flag.String("tls-cert", env("BUZZIT_TLS_CERT", ""), "TLS certificate file (optional; else run behind a TLS proxy)")
	tlsKey := flag.String("tls-key", env("BUZZIT_TLS_KEY", ""), "TLS key file")
	devWebhooks := flag.Bool("dev-allow-private-webhooks", env("BUZZIT_DEV_PRIVATE_WEBHOOKS", "") == "1", "allow http:// and private-network webhook URLs (development only)")
	flag.Int64Var(&maxUpload, "max-upload-mb", maxUpload, "maximum upload size in MB")
	flag.Parse()

	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	app, err := server.New(server.Config{
		DataDir: *data, MaxUploadMB: maxUpload, FCMServiceAccount: *fcm, AllowPrivateWebhooks: *devWebhooks,
	})
	if err != nil {
		slog.Error("startup", "err", err)
		os.Exit(1)
	}
	defer app.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	app.Run(ctx)

	hs := &http.Server{
		Addr:              *addr,
		Handler:           app.Handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}
	go func() {
		slog.Info("buzzit-server listening", "addr", *addr, "data", *data, "tls", *tlsCert != "")
		var err error
		if *tlsCert != "" {
			err = hs.ListenAndServeTLS(*tlsCert, *tlsKey)
		} else {
			err = hs.ListenAndServe()
		}
		if err != nil && err != http.ErrServerClosed {
			slog.Error("listen", "err", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	slog.Info("shutting down")
	shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = hs.Shutdown(shutdown)
}
