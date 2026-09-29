package main

import (
	"context"
	"net/http"
	"time"

	"github.com/kantracity/kantrae2e/pkg/db"
	"github.com/kantracity/kantrae2e/pkg/server"
	"github.com/kantracity/kantrae2e/services/auth"
)

func main() {
	l := server.Logger("auth-service")
	ttl, err := time.ParseDuration(server.Env("TOKEN_TTL", "24h"))
	if err != nil {
		l.Fatal().Err(err).Msg("bad TOKEN_TTL")
	}
	pool, err := db.Open(context.Background(), server.MustEnv("DATABASE_URL"), auth.Schema, auth.Migrations())
	if err != nil {
		l.Fatal().Err(err).Msg("database")
	}
	defer pool.Close()

	mux := http.NewServeMux()
	mux.Handle(auth.New(pool, server.JWTSecret(), ttl).Handler())
	mux.Handle("GET /healthz", server.Health(pool.Ping))
	server.Run(l, server.Env("LISTEN_ADDR", ":8080"), mux)
}
