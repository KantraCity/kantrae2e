package devicestatus

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"

	authv1 "github.com/kantracity/kantrae2e/gen/kantra/auth/v1"
	"github.com/kantracity/kantrae2e/gen/kantra/auth/v1/authv1connect"
)

type fakeAuth struct {
	active  map[string]bool
	pending map[string]bool
	calls   atomic.Int32
	down    atomic.Bool
}

func (f *fakeAuth) Blocked(context.Context, *authv1.BlockedRequest) (*authv1.BlockedResponse, error) {
	return &authv1.BlockedResponse{}, nil
}

func (f *fakeAuth) DeviceStatus(_ context.Context, r *authv1.DeviceStatusRequest) (*authv1.DeviceStatusResponse, error) {
	f.calls.Add(1)
	if f.down.Load() {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("down"))
	}
	out := &authv1.DeviceStatusResponse{}
	for _, id := range r.DeviceIds {
		if f.active[id] {
			out.ActiveDeviceIds = append(out.ActiveDeviceIds, id)
			if !f.pending[id] {
				out.ApprovedDeviceIds = append(out.ApprovedDeviceIds, id)
			}
		}
	}
	return out, nil
}

func TestCaching(t *testing.T) {
	f := &fakeAuth{active: map[string]bool{"a": true, "p": true}, pending: map[string]bool{"p": true}}
	mux := http.NewServeMux()
	mux.Handle(authv1connect.NewAuthInternalServiceHandler(f))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	now := time.Unix(0, 0)
	c := New(srv.Client(), srv.URL, "tok")
	c.now = func() time.Time { return now }
	ctx := context.Background()

	st, err := c.Status(ctx, "a", "r", "p")
	if err != nil || !st["a"].Approved || st["r"].Active || !st["p"].Active || st["p"].Approved {
		t.Fatalf("%v %v", st, err)
	}
	c.Status(ctx, "a", "r")
	if f.calls.Load() != 1 {
		t.Fatalf("not cached: %d calls", f.calls.Load())
	}
	// Pending answers are not cached: approval is visible immediately.
	f.pending["p"] = false
	if st, _ := c.Status(ctx, "p"); !st["p"].Approved {
		t.Fatal("approval not picked up")
	}

	// "a" gets revoked: visible after the TTL, then cached forever.
	f.active["a"] = false
	now = now.Add(ActiveTTL + time.Second)
	if st, _ := c.Status(ctx, "a"); st["a"].Active {
		t.Fatal("revocation not picked up")
	}
	f.down.Store(true)
	now = now.Add(time.Hour)
	if st, err := c.Status(ctx, "a", "r"); err != nil || st["a"].Active || st["r"].Active {
		t.Fatalf("revoked devices while auth is down: %v %v", st, err)
	}
	// Unknown device while auth is down: fail closed.
	if _, err := c.Status(ctx, "new"); err == nil {
		t.Fatal("unknown device allowed while auth is down")
	}
}
