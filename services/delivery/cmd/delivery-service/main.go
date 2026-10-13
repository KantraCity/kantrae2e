package main

import (
	"context"
	"net"
	"net/http"
	"os"
	"strconv"

	"github.com/kantracity/kantrae2e/pkg/db"
	"github.com/kantracity/kantrae2e/pkg/devicestatus"
	"github.com/kantracity/kantrae2e/pkg/server"
	"github.com/kantracity/kantrae2e/services/delivery"
)

func main() {
	l := server.Logger("delivery-service")
	pool, err := db.Open(context.Background(), server.MustEnv("DATABASE_URL"), delivery.Schema, delivery.Migrations())
	if err != nil {
		l.Fatal().Err(err).Msg("database")
	}
	defer pool.Close()

	devices := devicestatus.FromEnv(os.Getenv)
	if devices == nil {
		l.Warn().Msg("AUTH_INTERNAL_URL/INTERNAL_TOKEN not set: revoked devices are not rejected")
	}
	svc := delivery.New(pool, server.JWTSecret(), l).WithDevices(devices)
	mux := http.NewServeMux()
	mux.Handle(svc.Handler())
	mux.Handle(delivery.WSPath, svc.WSHandler())
	mux.Handle("GET /healthz", server.Health(pool.Ping))
	// Realtime over WebTransport (QUIC) on its own UDP port; clients find
	// it (port, certificate hash) at RealtimeInfoPath.
	wtPort := 0
	if addr := os.Getenv("WT_ADDR"); addr != "" {
		_, p, _ := net.SplitHostPort(addr)
		wtPort, _ = strconv.Atoi(server.Env("WT_PUBLIC_PORT", p))
		wt := svc.WebTransportServer(addr)
		go func() {
			l.Info().Str("addr", addr).Msg("webtransport listening")
			if err := wt.ListenAndServe(); err != nil {
				l.Error().Err(err).Msg("webtransport stopped")
			}
		}()
	}
	mux.Handle("GET "+delivery.RealtimeInfoPath, svc.RealtimeInfoHandler(wtPort))
	server.Run(l, server.Env("LISTEN_ADDR", ":8082"), mux)
}
