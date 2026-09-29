package directory_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	directoryv1 "github.com/kantracity/kantrae2e/gen/kantra/directory/v1"
	"github.com/kantracity/kantrae2e/gen/kantra/directory/v1/directoryv1connect"
	"github.com/kantracity/kantrae2e/internal/testutil"
	"github.com/kantracity/kantrae2e/pkg/authmiddleware"
	"github.com/kantracity/kantrae2e/services/directory"
)

func client(t *testing.T, url string, hc *http.Client, uid, did string) directoryv1connect.DirectoryServiceClient {
	tok, err := authmiddleware.Issue(testutil.Secret, uid, did, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return directoryv1connect.NewDirectoryServiceClient(hc, url, connect.WithInterceptors(
		connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
			return func(ctx context.Context, r connect.AnyRequest) (connect.AnyResponse, error) {
				r.Header().Set("Authorization", "Bearer "+tok)
				return next(ctx, r)
			}
		})))
}

func TestKeyPackagesAreHandedOutOnce(t *testing.T) {
	pool := testutil.Pool(t, testutil.DatabaseURL(t), directory.Schema, directory.Migrations())
	path, h := directory.New(pool, testutil.Secret).Handler()
	srv := testutil.Serve(t, map[string]http.Handler{path: h})
	ctx := context.Background()

	bob, bobDev := uuid.NewString(), uuid.NewString()
	bobCl := client(t, srv.URL, srv.Client(), bob, bobDev)
	const n = 20
	var kps [][]byte
	for i := 0; i < n; i++ {
		kps = append(kps, []byte(fmt.Sprintf("kp-%02d", i)))
	}
	pub, err := bobCl.PublishKeyPackages(ctx, &directoryv1.PublishKeyPackagesRequest{KeyPackages: kps})
	if err != nil || pub.Available != n {
		t.Fatalf("publish: %v %v", pub, err)
	}

	// Account token without device may not publish.
	if _, err := client(t, srv.URL, srv.Client(), bob, "").PublishKeyPackages(ctx,
		&directoryv1.PublishKeyPackagesRequest{KeyPackages: kps[:1]}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("publish without device: %v", err)
	}

	// 3n concurrent claims: exactly n succeed, all distinct.
	alice := client(t, srv.URL, srv.Client(), uuid.NewString(), uuid.NewString())
	var mu sync.Mutex
	seen := map[string]int{}
	notFound := 0
	var wg sync.WaitGroup
	for i := 0; i < 3*n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := alice.FetchKeyPackage(ctx, &directoryv1.FetchKeyPackageRequest{UserId: bob, DeviceId: bobDev})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case connect.CodeOf(err) == connect.CodeNotFound:
				notFound++
			case err != nil:
				t.Errorf("fetch: %v", err)
			default:
				seen[string(r.KeyPackage)]++
			}
		}()
	}
	wg.Wait()
	if len(seen) != n || notFound != 2*n {
		t.Fatalf("distinct=%d notFound=%d", len(seen), notFound)
	}
	for kp, c := range seen {
		if c != 1 {
			t.Fatalf("%s handed out %d times", kp, c)
		}
	}
	cnt, _ := bobCl.CountKeyPackages(ctx, &directoryv1.CountKeyPackagesRequest{})
	if cnt.Available != 0 {
		t.Fatalf("available=%d", cnt.Available)
	}
}

func TestFetchUserKeyPackages(t *testing.T) {
	pool := testutil.Pool(t, testutil.DatabaseURL(t), directory.Schema, directory.Migrations())
	path, h := directory.New(pool, testutil.Secret).Handler()
	srv := testutil.Serve(t, map[string]http.Handler{path: h})
	ctx := context.Background()

	bob := uuid.NewString()
	d1, d2 := uuid.NewString(), uuid.NewString()
	for _, d := range []string{d1, d2} {
		if _, err := client(t, srv.URL, srv.Client(), bob, d).RefillKeyPackages(ctx,
			&directoryv1.RefillKeyPackagesRequest{KeyPackages: [][]byte{[]byte(d)}}); err != nil {
			t.Fatal(err)
		}
	}
	alice := client(t, srv.URL, srv.Client(), uuid.NewString(), "")
	r, err := alice.FetchUserKeyPackages(ctx, &directoryv1.FetchUserKeyPackagesRequest{UserId: bob})
	if err != nil || len(r.KeyPackages) != 2 {
		t.Fatalf("fetch user: %v %v", r, err)
	}
	for _, kp := range r.KeyPackages {
		if !bytes.Equal(kp.KeyPackage, []byte(kp.DeviceId)) {
			t.Fatalf("mismatch %v", kp)
		}
	}
	if _, err := alice.FetchUserKeyPackages(ctx, &directoryv1.FetchUserKeyPackagesRequest{UserId: bob}); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("drained: %v", err)
	}
}
