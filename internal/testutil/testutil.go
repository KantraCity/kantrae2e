// Package testutil provides throwaway Postgres databases for tests.
// Tests are skipped unless TEST_DATABASE_URL points to a server where the
// user may CREATE DATABASE, e.g.
// postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable
package testutil

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kantracity/kantrae2e/pkg/db"
)

var Secret = []byte("test-secret-test-secret-test-secret!")

// DatabaseURL creates a fresh database and returns its DSN.
func DatabaseURL(t testing.TB) string {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	name := "kantra_t_" + hex.EncodeToString(b)
	if _, err := conn.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("create db: %v", err)
	}
	t.Cleanup(func() {
		c, err := pgx.Connect(context.Background(), base)
		if err == nil {
			_, _ = c.Exec(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
			c.Close(context.Background())
		}
	})
	u, _ := url.Parse(base)
	u.Path = "/" + name
	return u.String()
}

// Pool opens dsn for a service schema and applies its migrations.
func Pool(t testing.TB, dsn, schema string, migrations fs.FS) *pgxpool.Pool {
	t.Helper()
	p, err := db.Open(context.Background(), dsn, schema, migrations)
	if err != nil {
		t.Fatalf("db open %s: %v", schema, err)
	}
	t.Cleanup(p.Close)
	return p
}

// Serve starts an HTTP/2-capable test server for a connect handler.
func Serve(t testing.TB, handlers map[string]http.Handler) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	for p, h := range handlers {
		mux.Handle(p, h)
	}
	srv := httptest.NewUnstartedServer(mux)
	srv.EnableHTTP2 = true
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}
