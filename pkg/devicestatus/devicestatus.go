// Package devicestatus tells services whether a device has been revoked, by
// asking auth-service's internal API. Results are cached: revocations are
// permanent (cached forever), "active" answers for ActiveTTL, so a revoked
// device is locked out within ActiveTTL.
package devicestatus

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"connectrpc.com/connect"

	authv1 "github.com/kantracity/kantrae2e/gen/kantra/auth/v1"
	"github.com/kantracity/kantrae2e/gen/kantra/auth/v1/authv1connect"
	"github.com/kantracity/kantrae2e/pkg/authmiddleware"
)

// ActiveTTL bounds how long a revoked device may still be treated as active.
const ActiveTTL = 30 * time.Second

// Checker reports which devices are active (exist and are not revoked).
// A nil Checker treats every device as active.
type Checker interface {
	Active(ctx context.Context, deviceIDs ...string) (map[string]bool, error)
}

type entry struct {
	active bool
	at     time.Time
}

// Client queries auth-service with caching.
type Client struct {
	// TTL of cached "active" answers (default ActiveTTL; 0 disables caching
	// of positive answers).
	TTL   time.Duration
	c     authv1connect.AuthInternalServiceClient
	now   func() time.Time
	mu    sync.Mutex
	cache map[string]entry
}

// New creates a client for the auth internal API at baseURL.
func New(hc *http.Client, baseURL, token string) *Client {
	auth := connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, r connect.AnyRequest) (connect.AnyResponse, error) {
			r.Header().Set("Authorization", "Internal "+token)
			return next(ctx, r)
		}
	})
	return &Client{
		TTL:   ActiveTTL,
		c:     authv1connect.NewAuthInternalServiceClient(hc, baseURL, connect.WithInterceptors(auth)),
		now:   time.Now,
		cache: map[string]entry{},
	}
}

func (c *Client) Active(ctx context.Context, deviceIDs ...string) (map[string]bool, error) {
	out := make(map[string]bool, len(deviceIDs))
	var ask []string
	now := c.now()
	c.mu.Lock()
	for _, id := range deviceIDs {
		e, ok := c.cache[id]
		switch {
		case ok && !e.active:
			out[id] = false // revocation is permanent
		case ok && now.Sub(e.at) < c.TTL:
			out[id] = true
		default:
			ask = append(ask, id)
		}
	}
	c.mu.Unlock()
	if len(ask) == 0 {
		return out, nil
	}
	r, err := c.c.DeviceStatus(ctx, &authv1.DeviceStatusRequest{DeviceIds: ask})
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		// Auth unavailable: fall back to stale positive answers, deny the rest.
		for _, id := range ask {
			e, ok := c.cache[id]
			if !ok {
				return nil, err
			}
			out[id] = e.active
		}
		return out, nil
	}
	active := map[string]bool{}
	for _, id := range r.ActiveDeviceIds {
		active[id] = true
	}
	for _, id := range ask {
		c.cache[id] = entry{active: active[id], at: now}
		out[id] = active[id]
	}
	return out, nil
}

// ErrRevoked is returned to revoked devices.
var ErrRevoked = errors.New("device revoked")

// RequireActive fails with CodePermissionDenied if the device is revoked.
func RequireActive(ctx context.Context, ch Checker, deviceID string) error {
	if ch == nil || deviceID == "" {
		return nil
	}
	st, err := ch.Active(ctx, deviceID)
	if err != nil {
		return connect.NewError(connect.CodeUnavailable, err)
	}
	if !st[deviceID] {
		return connect.NewError(connect.CodePermissionDenied, ErrRevoked)
	}
	return nil
}

// Interceptor rejects requests of revoked devices. It must run after
// authmiddleware.Interceptor (which puts the device id in the context).
func Interceptor(ch Checker) connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			if did, ok := authmiddleware.DeviceIDFromContext(ctx); ok {
				if err := RequireActive(ctx, ch, did); err != nil {
					return nil, err
				}
			}
			return next(ctx, req)
		}
	}
}

// Static is a Checker for tests: devices listed in Revoked are revoked.
type Static struct {
	mu      sync.Mutex
	Revoked map[string]bool
}

func (s *Static) Revoke(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Revoked == nil {
		s.Revoked = map[string]bool{}
	}
	s.Revoked[id] = true
}

func (s *Static) Active(_ context.Context, ids ...string) (map[string]bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]bool{}
	for _, id := range ids {
		out[id] = !s.Revoked[id]
	}
	return out, nil
}

// FromEnv builds a client from AUTH_INTERNAL_URL and INTERNAL_TOKEN, or
// returns nil (checks disabled) when they are not set.
func FromEnv(getenv func(string) string) Checker {
	url, tok := getenv("AUTH_INTERNAL_URL"), getenv("INTERNAL_TOKEN")
	if url == "" || tok == "" {
		return nil
	}
	return New(&http.Client{Timeout: 5 * time.Second}, url, tok)
}
