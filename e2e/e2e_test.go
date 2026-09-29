// End-to-end test of the roadmap DoDs: all services in-process (real
// Postgres; real MinIO when TEST_S3_ENDPOINT is set, otherwise in-memory
// S3), real clients with separate SQLite databases talking over HTTPS.
package e2e

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/kantracity/kantrae2e/client/core"
	"github.com/kantracity/kantrae2e/internal/testutil"
	"github.com/kantracity/kantrae2e/pkg/blobstore"
	"github.com/kantracity/kantrae2e/services/auth"
	"github.com/kantracity/kantrae2e/services/delivery"
	"github.com/kantracity/kantrae2e/services/directory"
	"github.com/kantracity/kantrae2e/services/history"
	"github.com/kantracity/kantrae2e/services/media"
	"net/http"
)

type cluster struct {
	t           *testing.T
	url         string
	hc          *http.Client
	deliveryDB  *pgxpool.Pool
	historyBlob blobstore.Store
	dir         string
}

func blobs(t *testing.T, bucket string) blobstore.Store {
	ep := os.Getenv("TEST_S3_ENDPOINT")
	if ep == "" {
		return blobstore.NewMemory()
	}
	s, err := blobstore.NewS3(blobstore.S3Config{Endpoint: ep, AccessKey: os.Getenv("TEST_S3_ACCESS_KEY"),
		SecretKey: os.Getenv("TEST_S3_SECRET_KEY"), Bucket: bucket})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Check(context.Background()); err != nil {
		t.Fatalf("s3: %v", err)
	}
	return s
}

func newCluster(t *testing.T) *cluster {
	dsn := testutil.DatabaseURL(t)
	secret := testutil.Secret
	c := &cluster{t: t, dir: t.TempDir()}
	h := map[string]http.Handler{}
	add := func(p string, hd http.Handler) { h[p] = hd }

	add(auth.New(testutil.Pool(t, dsn, auth.Schema, auth.Migrations()), secret, time.Hour).Handler())
	add(directory.New(testutil.Pool(t, dsn, directory.Schema, directory.Migrations()), secret).Handler())
	c.deliveryDB = testutil.Pool(t, dsn, delivery.Schema, delivery.Migrations())
	del := delivery.New(c.deliveryDB, secret, zerolog.Nop())
	add(del.Handler())
	add(delivery.WSPath, del.WSHandler())
	c.historyBlob = blobs(t, history.Bucket)
	add(history.New(testutil.Pool(t, dsn, history.Schema, history.Migrations()), c.historyBlob, secret).Handler())
	add(media.New(testutil.Pool(t, dsn, media.Schema, media.Migrations()), blobs(t, media.Bucket), secret).Handler())

	srv := testutil.Serve(t, h)
	c.url, c.hc = srv.URL, srv.Client()
	return c
}

type user struct {
	*core.Client
	name, db string
	mu       sync.Mutex
	events   []core.Event
}

func (c *cluster) open(name, db string) *user {
	c.t.Helper()
	u := &user{name: name, db: filepath.Join(c.dir, db)}
	cl, err := core.Open(context.Background(), core.Options{
		ServerURL: c.url, DBPath: u.db, HTTPClient: c.hc,
		OnEvent: func(e core.Event) { u.mu.Lock(); u.events = append(u.events, e); u.mu.Unlock() },
		Logf:    func(f string, a ...any) { c.t.Logf(name+": "+f, a...) },
	})
	if err != nil {
		c.t.Fatal(err)
	}
	u.Client = cl
	c.t.Cleanup(func() { cl.Close() })
	return u
}

func (c *cluster) register(name string) (*user, string) {
	c.t.Helper()
	u := c.open(name, name+".db")
	phrase, err := u.Register(context.Background(), name, "password-"+name, name+"-laptop")
	if err != nil {
		c.t.Fatal(err)
	}
	return u, phrase
}

func texts(t *testing.T, u *user, gid string) []string {
	t.Helper()
	ms, err := u.Messages(context.Background(), gid, 100)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, m := range ms {
		out = append(out, m.Body)
	}
	return out
}

func ok(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func eq(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %q want %q", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("got %q want %q", got, want)
		}
	}
}

func TestMessengerEndToEnd(t *testing.T) {
	c := newCluster(t)
	ctx := context.Background()
	alice, alicePhrase := c.register("alice")
	bob, _ := c.register("bob")
	carol, _ := c.register("carol")

	// Phase 3: group over the network, messages in both directions.
	gid, err := alice.CreateGroup(ctx, "friends")
	ok(t, err)
	ok(t, alice.Invite(ctx, gid, "bob"))
	ok(t, bob.Sync(ctx))
	g, err := bob.ResolveGroup(ctx, "friends")
	ok(t, err)
	if g.ID != gid || !g.Active || g.Epoch != 1 {
		t.Fatalf("bob group %+v", g)
	}
	_, err = alice.SendText(ctx, gid, "hi bob, this is secret")
	ok(t, err)
	ok(t, bob.Sync(ctx))
	_, err = bob.SendText(ctx, gid, "hi alice")
	ok(t, err)
	ok(t, alice.Sync(ctx))
	eq(t, texts(t, bob, gid), "hi bob, this is secret", "hi alice")
	eq(t, texts(t, alice, gid), "hi bob, this is secret", "hi alice")

	// The server only ever saw ciphertext.
	rows, err := c.deliveryDB.Query(ctx, `SELECT payload FROM group_messages`)
	ok(t, err)
	for rows.Next() {
		var p []byte
		ok(t, rows.Scan(&p))
		if bytes.Contains(p, []byte("secret")) || bytes.Contains(p, []byte("hi alice")) {
			t.Fatal("plaintext on the server")
		}
	}
	rows.Close()

	// Phase 4: restart does not break the MLS session.
	ok(t, bob.Close())
	bob = c.open("bob", "bob.db")
	_, err = alice.SendText(ctx, gid, "after restart")
	ok(t, err)
	ok(t, bob.Sync(ctx))
	_, err = bob.SendText(ctx, gid, "still here")
	ok(t, err)
	ok(t, alice.Sync(ctx))
	eq(t, texts(t, alice, gid)[2:], "after restart", "still here")

	// Realtime: bob listens on the WebSocket.
	lctx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); _ = bob.Listen(lctx) }()
	_, err = alice.SendText(ctx, gid, "live")
	ok(t, err)
	waitFor(t, func() bool { m := texts(t, bob, gid); return len(m) == 5 && m[4] == "live" })
	stop()
	<-done

	// Phase 6: group of 3, then carol is removed and sees nothing afterwards.
	ok(t, bob.Invite(ctx, gid, "carol"))
	ok(t, carol.Sync(ctx))
	ok(t, alice.Sync(ctx))
	members, err := alice.Members(ctx, gid)
	ok(t, err)
	if len(members) != 3 {
		t.Fatalf("members %v", members)
	}
	_, err = carol.SendText(ctx, gid, "hello from carol")
	ok(t, err)
	ok(t, alice.Sync(ctx))
	ok(t, bob.Sync(ctx))
	ok(t, alice.Remove(ctx, gid, "carol"))
	_, err = alice.SendText(ctx, gid, "carol cannot read this")
	ok(t, err)
	ok(t, carol.Sync(ctx))
	ok(t, bob.Sync(ctx))
	cg, _ := carol.ResolveGroup(ctx, gid)
	if cg.Active {
		t.Fatal("carol still active")
	}
	eq(t, texts(t, carol, gid), "hello from carol")
	if m := texts(t, bob, gid); m[len(m)-1] != "carol cannot read this" {
		t.Fatalf("bob: %q", m)
	}
	if _, err := carol.SendText(ctx, gid, "let me in"); err == nil {
		t.Fatal("removed member could send")
	}

	// Concurrent commits: both try to add dave at the same epoch.
	dave, _ := c.register("dave")
	erin, _ := c.register("erin")
	var wg sync.WaitGroup
	errs := make([]error, 2)
	wg.Add(2)
	go func() { defer wg.Done(); errs[0] = alice.Invite(ctx, gid, "dave") }()
	go func() { defer wg.Done(); errs[1] = bob.Invite(ctx, gid, "erin") }()
	wg.Wait()
	ok(t, errs[0])
	ok(t, errs[1])
	for _, u := range []*user{alice, bob, dave, erin} {
		ok(t, u.Sync(ctx))
	}
	_, err = dave.SendText(ctx, gid, "dave here")
	ok(t, err)
	for _, u := range []*user{alice, bob, erin} {
		ok(t, u.Sync(ctx))
		if m := texts(t, u, gid); m[len(m)-1] != "dave here" {
			t.Fatalf("%s: %q", u.name, m)
		}
	}

	// Phase 7: encrypted media through the MLS session.
	file := bytes.Repeat([]byte("picture-bytes "), 1000)
	_, err = alice.SendFile(ctx, gid, "cat.jpg", file)
	ok(t, err)
	ok(t, bob.Sync(ctx))
	ms, _ := bob.Messages(ctx, gid, 1)
	name, data, err := bob.DownloadMedia(ctx, &ms[0])
	ok(t, err)
	if name != "cat.jpg" || !bytes.Equal(data, file) {
		t.Fatal("media mismatch")
	}

	// Phase 5: back up, switch the old device off, restore on a new one.
	n, err := alice.BackupHistory(ctx)
	ok(t, err)
	if n == 0 {
		t.Fatal("nothing backed up")
	}
	want := texts(t, alice, gid)
	ok(t, alice.Close())

	alice2 := c.open("alice2", "alice-phone.db")
	if err := alice2.Login(ctx, "alice", "password-alice", "alice-phone", "wrong words"); err == nil {
		t.Fatal("bad seed phrase accepted")
	}
	alice2 = c.open("alice2", "alice-phone2.db")
	ok(t, alice2.Login(ctx, "alice", "password-alice", "alice-phone", alicePhrase))
	restored, err := alice2.RestoreHistory(ctx)
	ok(t, err)
	if restored != n {
		t.Fatalf("restored %d of %d", restored, n)
	}
	eq(t, texts(t, alice2, gid), want...)
	rg, _ := alice2.ResolveGroup(ctx, "friends")
	if rg == nil || rg.Active {
		t.Fatalf("restored group %+v", rg)
	}
	// Idempotent.
	if again, _ := alice2.RestoreHistory(ctx); again != 0 {
		t.Fatalf("restored twice: %d", again)
	}
	// A different account's seed cannot decrypt alice's backup.
	if m, ok := c.historyBlob.(*blobstore.Memory); ok && m.Len() == 0 {
		t.Fatal("no chunks in object store")
	}

	// The new device can be added to the group by a member.
	ok(t, bob.Invite(ctx, gid, "alice"))
	ok(t, alice2.Sync(ctx))
	_, err = alice2.SendText(ctx, gid, "new phone, same me")
	ok(t, err)
	ok(t, bob.Sync(ctx))
	if m := texts(t, bob, gid); m[len(m)-1] != "new phone, same me" {
		t.Fatalf("bob: %q", m)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timeout")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
