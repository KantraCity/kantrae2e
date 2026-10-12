package e2e

import (
	"context"
	"errors"
	"testing"

	"github.com/kantracity/kantrae2e/client/core"
)

func sysActions(t *testing.T, cl *user, gid string) []string {
	t.Helper()
	ms, err := cl.Messages(context.Background(), gid, 100)
	ok(t, err)
	var out []string
	for i := range ms {
		if e := core.ParseSys(&ms[i]); e != nil {
			out = append(out, e.Action)
		}
	}
	return out
}

func TestChatPrefsAndSystemMessages(t *testing.T) {
	c := newCluster(t)
	ctx := context.Background()
	alice := c.register("alice")
	bob := c.register("bob")
	c.register("carol")
	phone := c.link(alice, "alice-phone", "alice-phone.db", "alice-phone")

	g, err := alice.CreateGroup(ctx, "Клуб")
	ok(t, err)
	ok(t, alice.Invite(ctx, g, "bob", "carol"))
	ok(t, bob.Sync(ctx))
	ok(t, alice.SetGroupInfo(ctx, g, ptr("Клуб 2"), nil))
	ok(t, alice.Remove(ctx, g, "carol"))
	ok(t, bob.Sync(ctx))
	got := sysActions(t, bob, g)
	if len(got) != 3 || got[0] != "add" || got[1] != "rename" || got[2] != "remove" {
		t.Fatalf("bob's system messages: %v", got)
	}
	if a := sysActions(t, alice, g); len(a) != 4 || a[0] != "create" {
		t.Fatalf("alice's system messages: %v", a)
	}
	ms, _ := bob.Messages(ctx, g, 100)
	if e := core.ParseSys(&ms[0]); e == nil || len(e.Users) != 2 {
		t.Fatalf("add event: %+v", e)
	}
	ok(t, bob.LeaveGroup(ctx, g))
	ok(t, alice.Sync(ctx))
	if a := sysActions(t, alice, g); a[len(a)-1] != "leave" {
		t.Fatalf("leave not shown: %v", a)
	}

	// Pinned and muted chats, the same on every device of the account.
	ok(t, alice.PinChat(ctx, g, true))
	ok(t, alice.MuteChat(ctx, g, core.MuteForever))
	ok(t, phone.Sync(ctx))
	prefs, err := phone.ChatPrefs(ctx)
	ok(t, err)
	if p := prefs[g]; p.Pinned == 0 || !p.Muted() {
		t.Fatalf("phone prefs: %+v", p)
	}
	ok(t, phone.MuteChat(ctx, g, 0))
	ok(t, alice.Sync(ctx))
	prefs, _ = alice.ChatPrefs(ctx)
	if p := prefs[g]; p.Pinned == 0 || p.Muted() {
		t.Fatalf("alice prefs after unmute on phone: %+v", p)
	}
	// At most 5 pinned chats.
	for i := 0; i < 4; i++ {
		id, err := alice.CreateGroup(ctx, "g")
		ok(t, err)
		ok(t, alice.PinChat(ctx, id, true))
	}
	extra, err := alice.CreateGroup(ctx, "лишний")
	ok(t, err)
	if err := alice.PinChat(ctx, extra, true); !errors.Is(err, core.ErrTooManyPinned) {
		t.Fatalf("6th pin: %v", err)
	}
	ok(t, alice.PinChat(ctx, g, false))
	ok(t, alice.PinChat(ctx, extra, true))
	// Saved GIFs and scheduled messages are not confused by the settings.
	if s, _ := alice.SavedGIFs(ctx); len(s) != 0 {
		t.Fatalf("saved: %d", len(s))
	}
}

func ptr[T any](v T) *T { return &v }
