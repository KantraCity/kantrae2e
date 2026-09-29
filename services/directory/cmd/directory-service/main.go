package main

import (
	"context"
	"net/http"

	"github.com/kantracity/kantrae2e/pkg/db"
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
	mux.Handle(directory.New(pool, server.JWTSecret()).Handler())
	mux.Handle("GET /healthz", server.Health(pool.Ping))
	server.Run(l, server.Env("LISTEN_ADDR", ":8081"), mux)
}
