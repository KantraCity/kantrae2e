package blobstore

import "github.com/kantracity/kantrae2e/pkg/server"

// FromEnv builds an S3 store from S3_ENDPOINT, S3_ACCESS_KEY, S3_SECRET_KEY,
// S3_USE_SSL and the given bucket (overridable with S3_BUCKET).
func FromEnv(defaultBucket string) (Store, error) {
	return NewS3(S3Config{
		Endpoint:  server.Env("S3_ENDPOINT", "minio:9000"),
		AccessKey: server.MustEnv("S3_ACCESS_KEY"),
		SecretKey: server.MustEnv("S3_SECRET_KEY"),
		UseSSL:    server.Env("S3_USE_SSL", "false") == "true",
		Bucket:    server.Env("S3_BUCKET", defaultBucket),
	})
}
