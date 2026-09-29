// Command s3init creates the object store buckets (idempotent). It replaces
// the manual `mc mb` step of the roadmap in docker-compose.
package main

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/kantracity/kantrae2e/pkg/server"
)

func main() {
	l := server.Logger("s3init")
	c, err := minio.New(server.Env("S3_ENDPOINT", "minio:9000"), &minio.Options{
		Creds:  credentials.NewStaticV4(server.MustEnv("S3_ACCESS_KEY"), server.MustEnv("S3_SECRET_KEY"), ""),
		Secure: server.Env("S3_USE_SSL", "false") == "true",
	})
	if err != nil {
		l.Fatal().Err(err).Msg("client")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	for _, b := range strings.Split(server.Env("S3_BUCKETS", "history,media"), ",") {
		for {
			exists, err := c.BucketExists(ctx, b)
			if err == nil && !exists {
				err = c.MakeBucket(ctx, b, minio.MakeBucketOptions{})
			}
			if err == nil {
				l.Info().Str("bucket", b).Msg("ready")
				break
			}
			if ctx.Err() != nil {
				l.Error().Err(err).Str("bucket", b).Msg("giving up")
				os.Exit(1)
			}
			l.Warn().Err(err).Msg("object store not ready, retrying")
			time.Sleep(2 * time.Second)
		}
	}
}
