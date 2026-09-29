package main

import (
	"context"
	"net/http"

	"github.com/kantracity/kantrae2e/pkg/blobstore"
	"github.com/kantracity/kantrae2e/pkg/db"
	"github.com/kantracity/kantrae2e/pkg/server"
	"github.com/kantracity/kantrae2e/services/media"
)

func main() {
	l := server.Logger("media-service")
	pool, err := db.Open(context.Background(), server.MustEnv("DATABASE_URL"), media.Schema, media.Migrations())
	if err != nil {
		l.Fatal().Err(err).Msg("database")
	}
	defer pool.Close()
	blobs, err := blobstore.FromEnv(media.Bucket)
	if err != nil {
		l.Fatal().Err(err).Msg("object store")
	}

	mux := http.NewServeMux()
	mux.Handle(media.New(pool, blobs, server.JWTSecret()).Handler())
	mux.Handle("GET /healthz", server.Health(pool.Ping, blobs.Check))
	server.Run(l, server.Env("LISTEN_ADDR", ":8084"), mux)
}
