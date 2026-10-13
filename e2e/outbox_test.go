package e2e

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kantracity/kantrae2e/client/core"
	"github.com/kantracity/kantrae2e/client/store"
)

// flaky is a network that can be switched off.
type flaky struct {
	down atomic.Bool
	next http.RoundTripper
}

func (f *flaky) RoundTrip(r *http.Request) (*http.Response, error) {
	if f.down.Load() {
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("network is unreachable")}
	}
	return f.next.RoundTrip(r)
}

// Messages written without a connection wait in the outbox, keep their
// order, leave when the network is back (also after a restart), and an
// unsent message can be deleted without ever reaching anyone.
func TestOutbox(t *testing.T) {
	c := newCluster(t)
	ctx := context.Background()
	net1 := &flaky{next: c.hc.Transport}
	alice := c.openWith("alice", "alice.db", core.Options{HTTPClient: &http.Client{Transport: net1, Timeout: time.Minute}})
	ok(t, alice.Register(ctx, "alice", "password-alice", "", "alice-laptop"))
	bob := c.register("bob")
	dm, err := alice.OpenDirect(ctx, "bob")
	ok(t, err)
	ok(t, bob.Sync(ctx))

	net1.down.Store(true)
	for _, text := range []string{"офлайн 1", "офлайн 2"} {
		m, err := alice.SendText(ctx, dm, text)
		ok(t, err)
		if m.Pending != store.PendingWaiting {
			t.Fatalf("%q: pending %d", text, m.Pending)
		}
	}
	oops, err := alice.SendText(ctx, dm, "передумал")
	ok(t, err)
	ok(t, alice.DeleteMessage(ctx, dm, oops.UID, true))

	runCtx, stop := context.WithCancel(ctx)
	go alice.RunOutbox(runCtx)
	net1.down.Store(false)
	alice.KickOutbox()
	waitTexts(t, bob, dm, []string{"офлайн 1", "офлайн 2"})
	ms, _ := alice.Messages(ctx, dm, 10)
	for _, m := range ms {
		if m.Pending != 0 {
			t.Fatalf("still pending after sending: %+v", m)
		}
	}
	stop()

	// Written offline, app closed, opened again with the network back.
	net1.down.Store(true)
	_, err = alice.SendText(ctx, dm, "после перезапуска")
	ok(t, err)
	ok(t, alice.Close())
	again := c.open("alice-again", "alice.db")
	go again.RunOutbox(ctx)
	waitTexts(t, bob, dm, []string{"офлайн 1", "офлайн 2", "после перезапуска"})
}

func waitTexts(t *testing.T, u *user, group string, want []string) {
	t.Helper()
	ctx := context.Background()
	var got []string
	for i := 0; i < 100; i++ {
		ok(t, u.Sync(ctx))
		got = texts(t, u, group)
		if len(got) == len(want) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if len(got) != len(want) {
		t.Fatalf("%s has %q, want %q", u.name, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s has %q, want %q", u.name, got, want)
		}
	}
}
