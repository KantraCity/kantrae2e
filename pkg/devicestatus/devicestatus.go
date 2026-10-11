// Package devicestatus tells services whether a device is revoked or still
// pending approval, by asking auth-service's internal API. Results are
// cached: revocations are permanent (cached forever), approved devices for
// TTL; pending answers are not cached so an approval takes effect at once.
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

// State of one device.
type State struct {
	Active   bool // exists and not revoked
	Approved bool // active and approved by another device or the recovery key
}

// Checker reports device states. A nil Checker treats every device as
// active and approved.
type Checker interface {
	Status(ctx context.Context, deviceIDs ...string) (map[string]State, error)
}

type entry struct {
	st State
	at time.Time
}

// Client queries auth-service with caching.
type Client struct {
	// TTL of cached "approved" answers (default ActiveTTL; 0 disables).
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

func (c *Client) Status(ctx context.Context, deviceIDs ...string) (map[string]State, error) {
	out := make(map[string]State, len(deviceIDs))
	var ask []string
	now := c.now()
	c.mu.Lock()
	for _, id := range deviceIDs {
		e, ok := c.cache[id]
		switch {
		case ok && !e.st.Active:
			out[id] = e.st // revocation is permanent
		case ok && e.st.Approved && now.Sub(e.at) < c.TTL:
			out[id] = e.st
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
		// Auth unavailable: fall back to stale answers, deny unknown devices.
		for _, id := range ask {
			e, ok := c.cache[id]
			if !ok {
				return nil, err
			}
			out[id] = e.st
		}
		return out, nil
	}
	st := map[string]State{}
	for _, id := range r.ActiveDeviceIds {
		st[id] = State{Active: true}
	}
	for _, id := range r.ApprovedDeviceIds {
		st[id] = State{Active: true, Approved: true}
	}
	for _, id := range ask {
		c.cache[id] = entry{st: st[id], at: now}
		out[id] = st[id]
	}
	return out, nil
}

var (
	// ErrRevoked is returned to revoked devices.
	ErrRevoked = errors.New("device revoked")
	// ErrPending is returned to devices that still await approval.
	ErrPending = errors.New("device not approved yet: confirm it on another device or with the recovery key")
)

func get(ctx context.Context, ch Checker, deviceID string) (State, error) {
	if ch == nil || deviceID == "" {
		return State{Active: true, Approved: true}, nil
	}
	st, err := ch.Status(ctx, deviceID)
	if err != nil {
		return State{}, connect.NewError(connect.CodeUnavailable, err)
	}
	return st[deviceID], nil
}

// RequireActive fails with CodePermissionDenied if the device is revoked.
func RequireActive(ctx context.Context, ch Checker, deviceID string) error {
	st, err := get(ctx, ch, deviceID)
	if err != nil {
		return err
	}
	if !st.Active {
		return connect.NewError(connect.CodePermissionDenied, ErrRevoked)
	}
	return nil
}

// IsApproved reports whether the device is active and approved.
func IsApproved(ctx context.Context, ch Checker, deviceID string) (bool, error) {
	st, err := get(ctx, ch, deviceID)
	return st.Approved, err
}

// Interceptor rejects requests of revoked devices, and of pending devices
// except for the listed procedures. It must run after
// authmiddleware.Interceptor (which puts the device id in the context).
func Interceptor(ch Checker, allowPending ...string) connect.UnaryInterceptorFunc {
	allow := map[string]bool{}
	for _, p := range allowPending {
		allow[p] = true
	}
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			if did, ok := authmiddleware.DeviceIDFromContext(ctx); ok {
				st, err := get(ctx, ch, did)
				if err != nil {
					return nil, err
				}
				if !st.Active {
					return nil, connect.NewError(connect.CodePermissionDenied, ErrRevoked)
				}
				if !st.Approved && !allow[req.Spec().Procedure] {
					return nil, connect.NewError(connect.CodePermissionDenied, ErrPending)
				}
			}
			return next(ctx, req)
		}
	}
}

// Filter returns the ids whose state satisfies keep.
func Filter(ctx context.Context, ch Checker, ids []string, keep func(State) bool) ([]string, error) {
	if ch == nil || len(ids) == 0 {
		return ids, nil
	}
	st, err := ch.Status(ctx, ids...)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, err)
	}
	var out []string
	for _, id := range ids {
		if keep(st[id]) {
			out = append(out, id)
		}
	}
	return out, nil
}

// Static is a Checker for tests: devices are active and approved unless
// listed in Revoked or Pending.
type Static struct {
	mu      sync.Mutex
	Revoked map[string]bool
	Pending map[string]bool
}

func (s *Static) Revoke(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Revoked == nil {
		s.Revoked = map[string]bool{}
	}
	s.Revoked[id] = true
}

func (s *Static) SetPending(id string, pending bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Pending == nil {
		s.Pending = map[string]bool{}
	}
	s.Pending[id] = pending
}

func (s *Static) Status(_ context.Context, ids ...string) (map[string]State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]State{}
	for _, id := range ids {
		active := !s.Revoked[id]
		out[id] = State{Active: active, Approved: active && !s.Pending[id]}
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
