package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/kantracity/kantrae2e/client/core"
)

// connectedVia waits for the n-th EventConnected and returns its transport.
func connectedVia(t *testing.T, u *user, n int) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if ev := u.eventsOf(core.EventConnected); len(ev) >= n {
			return ev[n-1].Detail
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s: not connected", u.name)
	return ""
}

func TestWebTransportRealtime(t *testing.T) {
	c := newCluster(t)
	c.wt = true
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	alice := c.register("alice")
	bob := c.register("bob")
	dm, err := alice.OpenDirect(ctx, "bob")
	ok(t, err)
	ok(t, bob.Sync(ctx))
	go bob.Listen(ctx)
	if via := connectedVia(t, bob, 1); via != "webtransport" {
		t.Fatalf("bob connected via %q", via)
	}
	_, err = alice.SendText(ctx, dm, "over quic")
	ok(t, err)
	waitFor(t, func() bool { m := texts(t, bob, dm); return len(m) == 1 && m[0] == "over quic" })

	// A network change: the QUIC connection answers a ping and is kept.
	bob.Reconnect()
	time.Sleep(500 * time.Millisecond)
	if n := len(bob.eventsOf(core.EventConnected)); n != 1 {
		t.Fatalf("reconnected %d times: the live QUIC connection should be kept", n-1)
	}
	_, err = alice.SendText(ctx, dm, "after the change")
	ok(t, err)
	waitFor(t, func() bool { return len(texts(t, bob, dm)) == 2 })
	if d := bob.eventsOf(core.EventDisconnected); len(d) != 0 {
		t.Fatalf("disconnected: %v", d[0].Err)
	}

	// UDP blocked (nobody at the endpoint): the WebSocket takes over quickly.
	carol := c.open("carol", "carol.db")
	ok(t, carol.Register(ctx, "carol", "password-carol", "", "carol-laptop"))
	carol.Close()
	c.wtAddr = "127.0.0.1:9" // discard port: nothing answers
	carol = c.openWith("carol", "carol.db", core.Options{WebTransport: true, WebTransportAddr: c.wtAddr})
	t0 := time.Now()
	go carol.Listen(ctx)
	if via := connectedVia(t, carol, 1); via != "websocket" {
		t.Fatalf("carol connected via %q", via)
	}
	if d := time.Since(t0); d > 3*time.Second {
		t.Fatalf("fallback took %s", d)
	}
	dc, err := alice.OpenDirect(ctx, "carol")
	ok(t, err)
	_, err = alice.SendText(ctx, dc, "over tcp")
	ok(t, err)
	waitFor(t, func() bool { return len(texts(t, carol, dc)) == 1 })
}
