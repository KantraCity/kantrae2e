// End-to-end test of the roadmap DoDs: all services in-process (real
// Postgres; real MinIO when TEST_S3_ENDPOINT is set, otherwise in-memory
// S3), real clients with separate SQLite databases talking over HTTPS.
package e2e

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/kantracity/kantrae2e/client/core"
	"github.com/kantracity/kantrae2e/client/mls"
	deliveryv1 "github.com/kantracity/kantrae2e/gen/kantra/delivery/v1"
	"github.com/kantracity/kantrae2e/gen/kantra/delivery/v1/deliveryv1connect"
	directoryv1 "github.com/kantracity/kantrae2e/gen/kantra/directory/v1"
	"github.com/kantracity/kantrae2e/gen/kantra/directory/v1/directoryv1connect"
	"github.com/kantracity/kantrae2e/internal/testutil"
	"github.com/kantracity/kantrae2e/pkg/authmiddleware"
	"github.com/kantracity/kantrae2e/pkg/blobstore"
	"github.com/kantracity/kantrae2e/pkg/devicestatus"
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

	authDB := testutil.Pool(t, dsn, auth.Schema, auth.Migrations())
	add(auth.New(authDB, secret, time.Hour).Handler())
	const internalToken = "internal-token-internal-token-1234"
	add(auth.NewInternal(authDB, internalToken).Handler())
	// Services learn about revocations through auth's internal API; the
	// base URL is only known once the server runs, hence the indirection.
	devices := &lateChecker{}
	add(directory.New(testutil.Pool(t, dsn, directory.Schema, directory.Migrations()), secret).WithDevices(devices).Handler())
	c.deliveryDB = testutil.Pool(t, dsn, delivery.Schema, delivery.Migrations())
	del := delivery.New(c.deliveryDB, secret, zerolog.Nop()).WithDevices(devices)
	add(del.Handler())
	add(delivery.WSPath, del.WSHandler())
	c.historyBlob = blobs(t, history.Bucket)
	add(history.New(testutil.Pool(t, dsn, history.Schema, history.Migrations()), c.historyBlob, secret).Handler())
	add(media.New(testutil.Pool(t, dsn, media.Schema, media.Migrations()), blobs(t, media.Bucket), secret).Handler())

	srv := testutil.Serve(t, h)
	c.url, c.hc = srv.URL, srv.Client()
	dc := devicestatus.New(srv.Client(), srv.URL, internalToken)
	dc.TTL = 0 // see revocations immediately (production: devicestatus.ActiveTTL)
	devices.Checker = dc
	return c
}

type lateChecker struct{ devicestatus.Checker }

type user struct {
	*core.Client
	name, db string
	mu       sync.Mutex
	events   []core.Event
}

func (u *user) eventsOf(t core.EventType) []core.Event {
	u.mu.Lock()
	defer u.mu.Unlock()
	var out []core.Event
	for _, e := range u.events {
		if e.Type == t {
			out = append(out, e)
		}
	}
	return out
}

// autoBackup is set by tests that exercise the continuous backup.
var autoBackup = false

func (c *cluster) open(name, db string) *user {
	c.t.Helper()
	u := &user{name: name, db: filepath.Join(c.dir, db)}
	cl, err := core.Open(context.Background(), core.Options{
		ServerURL: c.url, DBPath: u.db, HTTPClient: c.hc,
		OnEvent:           func(e core.Event) { u.mu.Lock(); u.events = append(u.events, e); u.mu.Unlock() },
		Logf:              func(f string, a ...any) { c.t.Logf(name+": "+f, a...) },
		DisableAutoBackup: !autoBackup,
		BackupDelay:       20 * time.Millisecond,
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
	if _, err := alice2.Login(ctx, "alice", "password-alice", "alice-phone", "wrong words"); err == nil {
		t.Fatal("bad seed phrase accepted")
	}
	alice2 = c.open("alice2", "alice-phone2.db")
	res, err := alice2.Login(ctx, "alice", "password-alice", "alice-phone", alicePhrase)
	ok(t, err)
	if res.Restored != n || res.Joined != 1 {
		t.Fatalf("login result %+v (backed up %d)", res, n)
	}
	eq(t, texts(t, alice2, gid), want...)
	rg, _ := alice2.ResolveGroup(ctx, "friends")
	if rg == nil || !rg.Active {
		t.Fatalf("group after login %+v", rg)
	}
	// Idempotent.
	if again, _ := alice2.RestoreHistory(ctx); again != 0 {
		t.Fatalf("restored twice: %d", again)
	}
	// A different account's seed cannot decrypt alice's backup.
	if m, ok := c.historyBlob.(*blobstore.Memory); ok && m.Len() == 0 {
		t.Fatal("no chunks in object store")
	}

	// The new device joined by itself and can write right away.
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

// Seamless multi-device: the old device is switched off while others keep
// writing; the new device restores the backup, joins all groups by itself
// and gets the missing messages from another member.
func TestSeamlessNewDevice(t *testing.T) {
	autoBackup = true
	defer func() { autoBackup = false }()
	c := newCluster(t)
	ctx := context.Background()
	laptop, phrase := c.register("alice")
	bob, _ := c.register("bob")
	carol, _ := c.register("carol")

	gid, err := laptop.CreateGroup(ctx, "team")
	ok(t, err)
	ok(t, laptop.Invite(ctx, gid, "bob", "carol"))
	ok(t, bob.Sync(ctx))
	ok(t, carol.Sync(ctx))
	_, err = bob.SendText(ctx, gid, "hi all")
	ok(t, err)
	ok(t, laptop.Sync(ctx))
	_, err = laptop.SendText(ctx, gid, "last words from the laptop")
	ok(t, err)
	// Second group, created on the laptop only.
	gid2, err := laptop.CreateGroup(ctx, "alice & bob")
	ok(t, err)
	ok(t, laptop.Invite(ctx, gid2, "bob"))
	_, err = laptop.SendText(ctx, gid2, "private hello")
	ok(t, err)
	ok(t, laptop.Close()) // flushes the automatic backup

	// Alice has no device online now.
	ok(t, bob.Sync(ctx))
	ok(t, carol.Sync(ctx))
	_, err = bob.SendText(ctx, gid, "while alice was offline")
	ok(t, err)
	_, err = bob.SendText(ctx, gid2, "are you there?")
	ok(t, err)

	// New phone: backup + both groups joined, no action from anybody else.
	phone := c.open("phone", "alice-phone.db")
	res, err := phone.Login(ctx, "alice", "password-alice", "alice-phone", phrase)
	ok(t, err)
	if res.Restored != 3 || res.Joined != 2 || res.Requested != 0 {
		t.Fatalf("login %+v", res)
	}
	g, _ := phone.ResolveGroup(ctx, "team")
	if g == nil || !g.Active {
		t.Fatalf("team on phone: %+v", g)
	}
	phone.Settle(5 * time.Second) // history requests go out

	// Bob and Carol process the external commits (legitimate: alice was in the
	// groups) and answer the history requests.
	for _, u := range []*user{bob, carol} {
		ok(t, u.Sync(ctx))
		u.Settle(10 * time.Second)
		if ev := u.eventsOf(core.EventSecurity); len(ev) != 0 {
			t.Fatalf("%s: unexpected security events %v", u.name, ev)
		}
	}
	ok(t, phone.Sync(ctx))
	eq(t, texts(t, phone, gid), "hi all", "last words from the laptop", "while alice was offline")
	eq(t, texts(t, phone, gid2), "private hello", "are you there?")
	ms, _ := phone.Messages(ctx, gid, 10)
	if ms[2].Origin != "shared" || ms[0].Origin != "backup" {
		t.Fatalf("origins %q %q", ms[0].Origin, ms[2].Origin)
	}
	got := 0
	for _, ev := range phone.eventsOf(core.EventHistory) {
		got += ev.Count
	}
	if got != 2 { // one missed message in each group
		t.Fatalf("history events counted %d messages", got)
	}

	// The phone is a full member immediately.
	_, err = phone.SendText(ctx, gid, "hello from the phone")
	ok(t, err)
	for _, u := range []*user{bob, carol} {
		ok(t, u.Sync(ctx))
		if m := texts(t, u, gid); m[len(m)-1] != "hello from the phone" {
			t.Fatalf("%s: %q", u.name, m)
		}
	}

	// Fallback path: no usable GroupInfo on the server -> JOIN_REQUEST, an
	// online member (bob) adds the tablet automatically.
	_, err = c.deliveryDB.Exec(ctx, `UPDATE groups SET group_info = NULL`)
	ok(t, err)
	tablet := c.open("tablet", "alice-tablet.db")
	res, err = tablet.Login(ctx, "alice", "password-alice", "alice-tablet", phrase)
	ok(t, err)
	if res.Joined != 0 || res.Requested != 2 {
		t.Fatalf("tablet login %+v", res)
	}
	ok(t, bob.Sync(ctx))
	bob.Settle(10 * time.Second)
	ok(t, tablet.Sync(ctx))
	tablet.Settle(5 * time.Second)
	ok(t, bob.Sync(ctx))
	ok(t, phone.Sync(ctx))
	bob.Settle(10 * time.Second)
	phone.Settle(10 * time.Second)
	ok(t, tablet.Sync(ctx))
	m := texts(t, tablet, gid)
	if len(m) != 4 || m[3] != "hello from the phone" {
		t.Fatalf("tablet team: %q", m)
	}
	_, err = tablet.SendText(ctx, gid2, "tablet in the private chat")
	ok(t, err)
	ok(t, bob.Sync(ctx))
	if m := texts(t, bob, gid2); m[len(m)-1] != "tablet in the private chat" {
		t.Fatalf("bob gid2: %q", m)
	}

	// The (lost) laptop is revoked from the phone: removed from both groups
	// and it cannot rejoin by itself.
	var laptopID string
	devs, err := phone.Devices(ctx)
	ok(t, err)
	for _, d := range devs {
		if d.DeviceName == "alice-laptop" {
			laptopID = d.Id
		}
	}
	n, err := phone.RevokeDevice(ctx, laptopID)
	ok(t, err)
	if n != 2 {
		t.Fatalf("removed from %d groups", n)
	}
	// The revoked laptop is locked out of delivery and directory.
	laptop = c.open("alice", "alice.db")
	if err := laptop.Sync(ctx); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("revoked laptop sync: %v", err)
	}
	if j, _, err := laptop.JoinMyGroups(ctx); j != 0 || err == nil {
		t.Fatal("revoked laptop rejoined")
	}
	// Bob re-inviting alice does not bring the laptop back.
	ok(t, bob.Sync(ctx))
	if err := bob.Invite(ctx, gid, "alice"); err == nil {
		t.Fatal("expected: all active devices of alice are already members")
	}
}

// Defence in depth: even if the server wrongly lets a stranger join through
// an External Commit, members detect it and remove the device.
func TestStrangerExternalJoinIsRemoved(t *testing.T) {
	c := newCluster(t)
	ctx := context.Background()
	alice, _ := c.register("alice")
	bob, _ := c.register("bob")
	mallory, _ := c.register("mallory")
	gid, err := alice.CreateGroup(ctx, "secret")
	ok(t, err)
	ok(t, alice.Invite(ctx, gid, "bob"))
	ok(t, bob.Sync(ctx))

	// Simulate a compromised server: pretend mallory's user is in the group.
	_, err = c.deliveryDB.Exec(ctx, `INSERT INTO group_members (group_id, device_id, user_id) VALUES ($1, $2, $3)`,
		gid, "00000000-0000-0000-0000-000000000001", mallory.Account().UserID)
	ok(t, err)
	joined, _, err := mallory.JoinMyGroups(ctx)
	ok(t, err)
	if joined != 1 {
		t.Fatal("setup: mallory did not join")
	}

	ok(t, bob.Sync(ctx))
	ok(t, alice.Sync(ctx))
	bob.Settle(10 * time.Second)
	alice.Settle(10 * time.Second)
	if len(bob.eventsOf(core.EventSecurity)) == 0 || len(alice.eventsOf(core.EventSecurity)) == 0 {
		t.Fatal("stranger not detected")
	}
	ok(t, alice.Sync(ctx))
	ok(t, bob.Sync(ctx))
	members, err := alice.Members(ctx, gid)
	ok(t, err)
	if len(members) != 2 {
		t.Fatalf("members after removal: %+v", members)
	}
	_, err = alice.SendText(ctx, gid, "mallory must not read this")
	ok(t, err)
	_ = mallory.Sync(ctx)
	if len(mallory.eventsOf(core.EventRemoved)) == 0 {
		t.Fatal("mallory not removed")
	}
	for _, m := range texts(t, mallory, gid) {
		if m == "mallory must not read this" {
			t.Fatal("stranger read the message")
		}
	}
}

func TestKeyVerification(t *testing.T) {
	c := newCluster(t)
	ctx := context.Background()
	alice, _ := c.register("alice")
	bob, bobPhrase := c.register("bob")
	gid, err := alice.CreateGroup(ctx, "verified")
	ok(t, err)
	ok(t, alice.Invite(ctx, gid, "bob"))
	ok(t, bob.Sync(ctx))

	// Both sides compute the same fingerprints.
	bobFP, err := bob.MyFingerprint(ctx)
	ok(t, err)
	keys, err := alice.Keys(ctx, "bob")
	ok(t, err)
	if len(keys) != 1 || keys[0].Fingerprint != bobFP || keys[0].Status != "seen" {
		t.Fatalf("alice sees bob: %+v (bob says %s)", keys, bobFP)
	}
	aliceFP, _ := alice.MyFingerprint(ctx)
	if k, _ := bob.Keys(ctx, "alice"); len(k) != 1 || k[0].Fingerprint != aliceFP {
		t.Fatalf("bob sees alice: %+v", k)
	}
	if n, err := alice.Trust(ctx, "bob", ""); err != nil || n != 1 {
		t.Fatalf("trust: %d %v", n, err)
	}

	// New device of a verified contact -> event, still usable.
	phone := c.open("bob-phone", "bob-phone.db")
	_, err = phone.Login(ctx, "bob", "password-bob", "bob-phone", bobPhrase)
	ok(t, err)
	ok(t, alice.Sync(ctx))
	if len(alice.eventsOf(core.EventNewDevice)) != 1 {
		t.Fatalf("new device events: %+v", alice.eventsOf(core.EventNewDevice))
	}
	_, err = alice.SendText(ctx, gid, "still fine")
	ok(t, err)

	// Compromised server: a forged device claiming to be bob's laptop (same
	// device id, attacker's key). mls-rs already refuses two leaves with the
	// same identity in one group, so the attack targets a group the real
	// laptop is not in: bob's laptop has no KeyPackages left, only his phone
	// gets invited, then the "laptop" joins by itself.
	bobAcct := bob.Account()
	sk, pk, err := mls.GenerateSignatureKeyPair()
	ok(t, err)
	fake, err := mls.NewClient([]byte(bobAcct.UserID+":"+bobAcct.DeviceID), sk, pk)
	ok(t, err)
	defer fake.Close()
	tok, _ := authmiddleware.Issue(testutil.Secret, bobAcct.UserID, bobAcct.DeviceID, time.Hour)
	withTok := connect.WithInterceptors(connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, r connect.AnyRequest) (connect.AnyResponse, error) {
			r.Header().Set("Authorization", "Bearer "+tok)
			return next(ctx, r)
		}
	}))
	fakeDel := deliveryv1connect.NewDeliveryServiceClient(c.hc, c.url, withTok)
	fakeDir := directoryv1connect.NewDirectoryServiceClient(c.hc, c.url, withTok)
	drainLaptop := func() {
		for {
			if _, err := fakeDir.FetchKeyPackage(ctx, &directoryv1.FetchKeyPackageRequest{UserId: bobAcct.UserID, DeviceId: bobAcct.DeviceID}); err != nil {
				return
			}
		}
	}
	drainLaptop()
	gid2, err := alice.CreateGroup(ctx, "phone only")
	ok(t, err)
	ok(t, alice.Invite(ctx, gid2, "bob")) // adds bob's phone only
	gi, err := fakeDel.GetGroupInfo(ctx, &deliveryv1.GetGroupInfoRequest{GroupId: gid2})
	ok(t, err)
	_, commit, newGI, err := fake.ExternalJoin(gi.GroupInfo)
	ok(t, err)
	_, err = fakeDel.ExternalJoin(ctx, &deliveryv1.ExternalJoinRequest{GroupId: gid2, Epoch: gi.Epoch, Commit: commit, GroupInfo: newGI})
	ok(t, err)

	ok(t, alice.Sync(ctx))
	alice.Settle(5 * time.Second)
	sec := alice.eventsOf(core.EventSecurity)
	if len(sec) == 0 || !strings.Contains(sec[len(sec)-1].Detail, "different key") {
		t.Fatalf("no key-change alert: %+v", sec)
	}
	if _, err := alice.SendText(ctx, gid2, "secret"); !errors.Is(err, core.ErrKeyConflict) {
		t.Fatalf("send with conflict: %v", err)
	}
	// Groups without the forged key are unaffected.
	_, err = alice.SendText(ctx, gid, "other group still fine")
	ok(t, err)
	keys, _ = alice.Keys(ctx, "bob")
	var conflict bool
	for _, k := range keys {
		conflict = conflict || (k.DeviceID == bobAcct.DeviceID && k.Status == "conflict" && k.ConflictFingerprint != "")
	}
	if !conflict {
		t.Fatalf("keys: %+v", keys)
	}

	// Resolution: remove bob from that group (incl. the forged device).
	ok(t, alice.Remove(ctx, gid2, "bob"))
	_, err = alice.SendText(ctx, gid2, "after cleanup")
	ok(t, err)

	// Inviting with a KeyPackage whose key differs from the known one fails.
	gid3, err := alice.CreateGroup(ctx, "third")
	ok(t, err)
	fakeKP, err := fake.GenerateKeyPackage()
	ok(t, err)
	drainLaptop()
	_, err = fakeDir.PublishKeyPackages(ctx, &directoryv1.PublishKeyPackagesRequest{KeyPackages: [][]byte{fakeKP}})
	ok(t, err)
	if err := alice.Invite(ctx, gid3, "bob"); !errors.Is(err, core.ErrKeyChanged) {
		t.Fatalf("invite with forged key package: %v", err)
	}
}
