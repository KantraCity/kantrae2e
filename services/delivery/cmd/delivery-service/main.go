package main

import (
	"context"
	"net/http"

	"github.com/kantracity/kantrae2e/pkg/db"
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

	svc := delivery.New(pool, server.JWTSecret(), l)
	mux := http.NewServeMux()
	mux.Handle(svc.Handler())
	mux.Handle(delivery.WSPath, svc.WSHandler())
	mux.Handle("GET /healthz", server.Health(pool.Ping))
	server.Run(l, server.Env("LISTEN_ADDR", ":8082"), mux)
}
