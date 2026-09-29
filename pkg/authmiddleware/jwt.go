// Package authmiddleware issues and validates HS256 JWTs.
//
// Every service validates tokens itself (roadmap 2.6, "minimum path"): the
// gateway is a plain reverse proxy and services never trust gateway headers.
package authmiddleware

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/golang-jwt/jwt/v5"
)

type contextKey string

const (
	userIDKey   contextKey = "user_id"
	deviceIDKey contextKey = "device_id"
)

// Claims carried by a session token. DeviceID is empty for account-level
// tokens (right after Register / Login without a device).
type Claims struct {
	DeviceID string `json:"did,omitempty"`
	jwt.RegisteredClaims
}

// Issue signs a token for userID (and optionally deviceID).
func Issue(secret []byte, userID, deviceID string, ttl time.Duration) (string, error) {
	now := time.Now()
	c := Claims{
		DeviceID: deviceID,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(secret)
}

// Parse validates a raw token and returns its claims.
func Parse(secret []byte, tokenStr string) (*Claims, error) {
	c := &Claims{}
	tok, err := jwt.ParseWithClaims(tokenStr, c, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, jwt.ErrSignatureInvalid
		}
		return secret, nil
	}, jwt.WithValidMethods([]string{"HS256"}), jwt.WithExpirationRequired())
	if err != nil || !tok.Valid {
		return nil, errors.New("invalid token")
	}
	if c.Subject == "" {
		return nil, errors.New("missing sub claim")
	}
	return c, nil
}

func bearer(h http.Header) (string, bool) {
	a := h.Get("Authorization")
	if !strings.HasPrefix(a, "Bearer ") {
		return "", false
	}
	return strings.TrimPrefix(a, "Bearer "), true
}

func withClaims(ctx context.Context, c *Claims) context.Context {
	ctx = context.WithValue(ctx, userIDKey, c.Subject)
	return context.WithValue(ctx, deviceIDKey, c.DeviceID)
}

// JWTMiddleware protects plain HTTP handlers (e.g. the WebSocket endpoint).
func JWTMiddleware(secret []byte) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tok, ok := bearer(r.Header)
			if !ok {
				http.Error(w, "missing bearer token", http.StatusUnauthorized)
				return
			}
			c, err := Parse(secret, tok)
			if err != nil {
				http.Error(w, err.Error(), http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r.WithContext(withClaims(r.Context(), c)))
		})
	}
}

// Interceptor is the connect equivalent of JWTMiddleware. Procedures listed
// in public (full procedure names) skip authentication.
func Interceptor(secret []byte, public ...string) connect.Interceptor {
	skip := map[string]bool{}
	for _, p := range public {
		skip[p] = true
	}
	return connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			if skip[req.Spec().Procedure] {
				return next(ctx, req)
			}
			tok, ok := bearer(req.Header())
			if !ok {
				return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("missing bearer token"))
			}
			c, err := Parse(secret, tok)
			if err != nil {
				return nil, connect.NewError(connect.CodeUnauthenticated, err)
			}
			return next(withClaims(ctx, c), req)
		}
	})
}

func UserIDFromContext(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(userIDKey).(string)
	return id, ok && id != ""
}

func DeviceIDFromContext(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(deviceIDKey).(string)
	return id, ok && id != ""
}

// RequireUser returns the authenticated user id or CodeUnauthenticated.
func RequireUser(ctx context.Context) (string, error) {
	id, ok := UserIDFromContext(ctx)
	if !ok {
		return "", connect.NewError(connect.CodeUnauthenticated, errors.New("no user"))
	}
	return id, nil
}

// RequireDevice returns user and device ids, or CodePermissionDenied when
// the token is not bound to a device.
func RequireDevice(ctx context.Context) (userID, deviceID string, err error) {
	userID, err = RequireUser(ctx)
	if err != nil {
		return "", "", err
	}
	deviceID, ok := DeviceIDFromContext(ctx)
	if !ok {
		return "", "", connect.NewError(connect.CodePermissionDenied, errors.New("device-bound token required"))
	}
	return userID, deviceID, nil
}
