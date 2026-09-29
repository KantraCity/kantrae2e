package main

import (
	"context"
	"net/http"
	"os"

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
	server.Run(l, server.Env("LISTEN_ADDR", ":8082"), mux)
}
