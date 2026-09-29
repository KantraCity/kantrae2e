package auth_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect"

	authv1 "github.com/kantracity/kantrae2e/gen/kantra/auth/v1"
	"github.com/kantracity/kantrae2e/gen/kantra/auth/v1/authv1connect"
	"github.com/kantracity/kantrae2e/internal/testutil"
	"github.com/kantracity/kantrae2e/pkg/authmiddleware"
	"github.com/kantracity/kantrae2e/services/auth"
)

func bearer(tok string) connect.ClientOption {
	return connect.WithInterceptors(connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, r connect.AnyRequest) (connect.AnyResponse, error) {
			r.Header().Set("Authorization", "Bearer "+tok)
			return next(ctx, r)
		}
	}))
}

func TestAuthFlow(t *testing.T) {
	dsn := testutil.DatabaseURL(t)
	pool := testutil.Pool(t, dsn, auth.Schema, auth.Migrations())
	path, h := auth.New(pool, testutil.Secret, time.Hour).Handler()
	srv := testutil.Serve(t, map[string]http.Handler{path: h})
	ctx := context.Background()
	anon := authv1connect.NewAuthServiceClient(srv.Client(), srv.URL)

	reg, err := anon.Register(ctx, &authv1.RegisterRequest{Username: "alice", Password: "correct horse"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := anon.Register(ctx, &authv1.RegisterRequest{Username: "alice", Password: "whatever1"}); connect.CodeOf(err) != connect.CodeAlreadyExists {
		t.Fatalf("duplicate: %v", err)
	}
	if _, err := anon.Login(ctx, &authv1.LoginRequest{Username: "alice", Password: "wrong-pass"}); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("wrong password: %v", err)
	}
	if _, err := anon.ListDevices(ctx, &authv1.ListDevicesRequest{}); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("no token: %v", err)
	}

	cl := authv1connect.NewAuthServiceClient(srv.Client(), srv.URL, bearer(reg.Token))
	dev, err := cl.RegisterDevice(ctx, &authv1.RegisterDeviceRequest{DeviceName: "laptop"})
	if err != nil {
		t.Fatal(err)
	}
	c, err := authmiddleware.Parse(testutil.Secret, dev.Token)
	if err != nil || c.Subject != reg.UserId || c.DeviceID != dev.DeviceId {
		t.Fatalf("device token claims %+v %v", c, err)
	}

	login, err := anon.Login(ctx, &authv1.LoginRequest{Username: "alice", Password: "correct horse", DeviceId: dev.DeviceId})
	if err != nil {
		t.Fatal(err)
	}
	if c, _ := authmiddleware.Parse(testutil.Secret, login.Token); c.DeviceID != dev.DeviceId {
		t.Fatal("login token not device-bound")
	}

	list, err := cl.ListDevices(ctx, &authv1.ListDevicesRequest{})
	if err != nil || len(list.Devices) != 1 {
		t.Fatalf("list: %v %v", list, err)
	}
	devCl := authv1connect.NewAuthServiceClient(srv.Client(), srv.URL, bearer(dev.Token))
	if r, err := devCl.RefreshToken(ctx, &authv1.RefreshTokenRequest{}); err != nil || r.Token == "" {
		t.Fatalf("refresh: %v", err)
	}
	if _, err := cl.RevokeDevice(ctx, &authv1.RevokeDeviceRequest{DeviceId: dev.DeviceId}); err != nil {
		t.Fatal(err)
	}
	if _, err := anon.Login(ctx, &authv1.LoginRequest{Username: "alice", Password: "correct horse", DeviceId: dev.DeviceId}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("revoked device login: %v", err)
	}
	if _, err := devCl.RefreshToken(ctx, &authv1.RefreshTokenRequest{}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("refresh revoked: %v", err)
	}
	lu, err := cl.LookupUser(ctx, &authv1.LookupUserRequest{Username: "alice"})
	if err != nil || lu.UserId != reg.UserId {
		t.Fatalf("lookup: %v %v", lu, err)
	}
	lu, err = cl.LookupUser(ctx, &authv1.LookupUserRequest{UserId: reg.UserId})
	if err != nil || lu.Username != "alice" {
		t.Fatalf("reverse lookup: %v %v", lu, err)
	}
}
