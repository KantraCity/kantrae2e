// Package server holds the bootstrap shared by all services: config from
// environment, logging, health checks, h2c HTTP server, graceful shutdown.
package server

import (
	"bufio"
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// Env returns the environment variable or def.
func Env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// MustEnv returns the environment variable or exits.
func MustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatal().Str("var", key).Msg("missing required environment variable")
	}
	return v
}

// JWTSecret reads JWT_SECRET and enforces a minimum length.
func JWTSecret() []byte {
	s := MustEnv("JWT_SECRET")
	if len(s) < 32 {
		log.Fatal().Msg("JWT_SECRET must be at least 32 bytes")
	}
	return []byte(s)
}

// Logger configures zerolog for a service.
func Logger(service string) zerolog.Logger {
	zerolog.TimeFieldFormat = time.RFC3339Nano
	l := zerolog.New(os.Stdout).With().Timestamp().Str("service", service).Logger()
	if strings.EqualFold(os.Getenv("LOG_FORMAT"), "console") {
		l = l.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	}
	log.Logger = l
	return l
}

// Checker is a readiness probe (e.g. a DB ping).
type Checker func(ctx context.Context) error

// Health returns a handler for GET /healthz running all checks.
func Health(checks ...Checker) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		for _, c := range checks {
			if err := c(ctx); err != nil {
				http.Error(w, "unhealthy: "+err.Error(), http.StatusServiceUnavailable)
				return
			}
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("ok"))
	})
}

// AccessLog logs method, path, status and duration. Never logs bodies.
func AccessLog(l zerolog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		if r.URL.Path == "/healthz" {
			return
		}
		l.Info().Str("method", r.Method).Str("path", r.URL.Path).
			Int("status", sw.status).Dur("dur", time.Since(start)).Msg("request")
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// Unwrap lets http.ResponseController reach the underlying writer
// (needed for WebSocket hijacking and flushing).
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// Hijack supports WebSocket upgrades through the access log wrapper.
func (w *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	w.status = http.StatusSwitchingProtocols
	return http.NewResponseController(w.ResponseWriter).Hijack()
}

// Run serves handler on addr (HTTP/1.1 and cleartext HTTP/2) until SIGINT or
// SIGTERM, then shuts down gracefully.
func Run(l zerolog.Logger, addr string, handler http.Handler) {
	var protocols http.Protocols
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)
	srv := &http.Server{
		Addr:              addr,
		Handler:           AccessLog(l, handler),
		Protocols:         &protocols,
		ReadHeaderTimeout: 10 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		l.Info().Str("addr", addr).Msg("listening")
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			l.Fatal().Err(err).Msg("server failed")
		}
	}()
	<-ctx.Done()
	l.Info().Msg("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}
