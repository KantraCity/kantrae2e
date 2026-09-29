package main

import (
	"context"
	"net/http"
	"os"

	"github.com/kantracity/kantrae2e/pkg/db"
	"github.com/kantracity/kantrae2e/pkg/devicestatus"
	"github.com/kantracity/kantrae2e/pkg/server"
	"github.com/kantracity/kantrae2e/services/directory"
)

func main() {
	l := server.Logger("directory-service")
	pool, err := db.Open(context.Background(), server.MustEnv("DATABASE_URL"), directory.Schema, directory.Migrations())
	if err != nil {
		l.Fatal().Err(err).Msg("database")
	}
	defer pool.Close()

	mux := http.NewServeMux()
	devices := devicestatus.FromEnv(os.Getenv)
	if devices == nil {
		l.Warn().Msg("AUTH_INTERNAL_URL/INTERNAL_TOKEN not set: key packages of revoked devices are served")
	}
	mux.Handle(directory.New(pool, server.JWTSecret()).WithDevices(devices).Handler())
	mux.Handle("GET /healthz", server.Health(pool.Ping))
	server.Run(l, server.Env("LISTEN_ADDR", ":8081"), mux)
}
