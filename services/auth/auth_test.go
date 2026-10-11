package auth_test

import (
	"bytes"
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

func TestInternalDeviceStatus(t *testing.T) {
	pool := testutil.Pool(t, testutil.DatabaseURL(t), auth.Schema, auth.Migrations())
	path, h := auth.New(pool, testutil.Secret, time.Hour).Handler()
	ipath, ih := auth.NewInternal(pool, "internal-token-internal-token-1234").Handler()
	srv := testutil.Serve(t, map[string]http.Handler{path: h, ipath: ih})
	ctx := context.Background()

	anon := authv1connect.NewAuthServiceClient(srv.Client(), srv.URL)
	reg, err := anon.Register(ctx, &authv1.RegisterRequest{Username: "alice", Password: "correct horse"})
	if err != nil {
		t.Fatal(err)
	}
	cl := authv1connect.NewAuthServiceClient(srv.Client(), srv.URL, bearer(reg.Token))
	d1, _ := cl.RegisterDevice(ctx, &authv1.RegisterDeviceRequest{DeviceName: "a"})
	d2, _ := cl.RegisterDevice(ctx, &authv1.RegisterDeviceRequest{DeviceName: "b"})
	if _, err := cl.RevokeDevice(ctx, &authv1.RevokeDeviceRequest{DeviceId: d2.DeviceId}); err != nil {
		t.Fatal(err)
	}

	internalWith := func(tok string) authv1connect.AuthInternalServiceClient {
		return authv1connect.NewAuthInternalServiceClient(srv.Client(), srv.URL, connect.WithInterceptors(
			connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
				return func(ctx context.Context, r connect.AnyRequest) (connect.AnyResponse, error) {
					r.Header().Set("Authorization", tok)
					return next(ctx, r)
				}
			})))
	}
	req := &authv1.DeviceStatusRequest{DeviceIds: []string{d1.DeviceId, d2.DeviceId, "not-a-uuid"}}
	// A user JWT is not enough.
	if _, err := internalWith("Bearer "+reg.Token).DeviceStatus(ctx, req); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("jwt accepted: %v", err)
	}
	r, err := internalWith("Internal internal-token-internal-token-1234").DeviceStatus(ctx, req)
	if err != nil || len(r.ActiveDeviceIds) != 1 || r.ActiveDeviceIds[0] != d1.DeviceId {
		t.Fatalf("status: %v %v", r, err)
	}
}

func TestLinkingRecoveryAndSearch(t *testing.T) {
	pool := testutil.Pool(t, testutil.DatabaseURL(t), auth.Schema, auth.Migrations())
	path, h := auth.New(pool, testutil.Secret, time.Hour).Handler()
	srv := testutil.Serve(t, map[string]http.Handler{path: h})
	ctx := context.Background()
	anon := authv1connect.NewAuthServiceClient(srv.Client(), srv.URL)

	reg, err := anon.Register(ctx, &authv1.RegisterRequest{Username: "alice_w", Password: "correct horse"})
	if err != nil {
		t.Fatal(err)
	}
	acct := authv1connect.NewAuthServiceClient(srv.Client(), srv.URL, bearer(reg.Token))
	first, _ := acct.RegisterDevice(ctx, &authv1.RegisterDeviceRequest{DeviceName: "laptop"})
	second, _ := acct.RegisterDevice(ctx, &authv1.RegisterDeviceRequest{DeviceName: "phone"})
	laptop := authv1connect.NewAuthServiceClient(srv.Client(), srv.URL, bearer(first.Token))
	phone := authv1connect.NewAuthServiceClient(srv.Client(), srv.URL, bearer(second.Token))

	st, _ := laptop.GetAccountStatus(ctx, &authv1.GetAccountStatusRequest{})
	if !st.DeviceApproved || st.HasRecovery {
		t.Fatalf("first device: %+v", st)
	}
	st, _ = phone.GetAccountStatus(ctx, &authv1.GetAccountStatusRequest{})
	if st.DeviceApproved {
		t.Fatal("second device approved automatically")
	}
	// A pending device cannot approve anything or set recovery.
	if _, err := phone.ApproveDevice(ctx, &authv1.ApproveDeviceRequest{DeviceId: second.DeviceId}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("self-approval: %v", err)
	}
	verifier := bytes.Repeat([]byte{7}, 32)
	if _, err := phone.SetRecovery(ctx, &authv1.SetRecoveryRequest{Verifier: verifier, WrappedKey: []byte("w")}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("pending set recovery: %v", err)
	}
	if _, err := laptop.ApproveDevice(ctx, &authv1.ApproveDeviceRequest{DeviceId: second.DeviceId}); err != nil {
		t.Fatal(err)
	}
	if st, _ := phone.GetAccountStatus(ctx, &authv1.GetAccountStatusRequest{}); !st.DeviceApproved {
		t.Fatal("not approved")
	}

	// Recovery: set from an approved device, use from a new pending one.
	if _, err := laptop.SetRecovery(ctx, &authv1.SetRecoveryRequest{Verifier: verifier, WrappedKey: []byte("wrapped")}); err != nil {
		t.Fatal(err)
	}
	third, _ := acct.RegisterDevice(ctx, &authv1.RegisterDeviceRequest{DeviceName: "tablet"})
	tablet := authv1connect.NewAuthServiceClient(srv.Client(), srv.URL, bearer(third.Token))
	if _, err := tablet.Recover(ctx, &authv1.RecoverRequest{Verifier: bytes.Repeat([]byte{8}, 32)}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("wrong verifier: %v", err)
	}
	r, err := tablet.Recover(ctx, &authv1.RecoverRequest{Verifier: verifier})
	if err != nil || string(r.WrappedKey) != "wrapped" {
		t.Fatalf("recover: %v %v", r, err)
	}
	if st, _ := tablet.GetAccountStatus(ctx, &authv1.GetAccountStatusRequest{}); !st.DeviceApproved || !st.HasRecovery {
		t.Fatalf("after recover: %+v", st)
	}

	// Search by username prefix (case-insensitive, LIKE wildcards escaped).
	for _, u := range []string{"alina", "bob", "al_x"} {
		if _, err := anon.Register(ctx, &authv1.RegisterRequest{Username: u, Password: "password1"}); err != nil {
			t.Fatal(err)
		}
	}
	res, err := laptop.SearchUsers(ctx, &authv1.SearchUsersRequest{Query: "@AL"})
	if err != nil || len(res.Users) != 3 {
		t.Fatalf("search al: %v %v", res, err)
	}
	res, _ = laptop.SearchUsers(ctx, &authv1.SearchUsersRequest{Query: "al_"})
	if len(res.Users) != 1 || res.Users[0].Username != "al_x" {
		t.Fatalf("underscore not escaped: %v", res.Users)
	}
	res, _ = laptop.SearchUsers(ctx, &authv1.SearchUsersRequest{Query: "a"})
	if len(res.Users) != 0 {
		t.Fatal("one-letter search should return nothing")
	}
}
