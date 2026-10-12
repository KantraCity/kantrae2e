package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/kantracity/kantrae2e/client/core"
)

func TestGroupInfoAndInviteLinks(t *testing.T) {
	c := newCluster(t)
	ctx := context.Background()
	alice := c.register("alice")
	bob := c.register("bob")
	dave := c.register("dave")

	g, err := alice.CreateGroup(ctx, "Поход")
	ok(t, err)
	ok(t, alice.Invite(ctx, g, "bob"))
	ok(t, bob.Sync(ctx))

	// Name and description, shared through MLS.
	name, about := "Поход 2026", "Собираемся в субботу"
	ok(t, alice.SetGroupInfo(ctx, g, &name, &about))
	ok(t, bob.Sync(ctx))
	gb, err := bob.ResolveGroup(ctx, g)
	ok(t, err)
	if gb.Name != name || gb.About != about {
		t.Fatalf("bob sees %q / %q", gb.Name, gb.About)
	}
	// Only the description changes: the name stays.
	about2 := "В воскресенье"
	ok(t, bob.SetGroupInfo(ctx, g, nil, &about2))
	ok(t, alice.Sync(ctx))
	ga, _ := alice.ResolveGroup(ctx, g)
	if ga.Name != name || ga.About != about2 {
		t.Fatalf("alice sees %q / %q", ga.Name, ga.About)
	}
	// Photo.
	ok(t, alice.SetGroupPhoto(ctx, g, &core.MediaInput{Name: "g.jpg", Data: []byte("jpeg"), Mime: "image/jpeg", Kind: "photo"}))
	ok(t, bob.Sync(ctx))
	if gb, _ = bob.ResolveGroup(ctx, g); !strings.Contains(gb.Avatar, `"hash"`) {
		t.Fatalf("photo not shared: %q", gb.Avatar)
	}

	// Invite link: dave joins through it; bob (online) adds him.
	link, err := alice.InviteLink(ctx, g, false)
	ok(t, err)
	if !strings.Contains(link, "/join/") || !strings.Contains(link, "#") {
		t.Fatalf("link %q", link)
	}
	if again, _ := alice.InviteLink(ctx, g, false); again != link {
		t.Fatal("link changed without reset")
	}
	ok(t, bob.Sync(ctx)) // bob learns the secret
	if _, err := dave.JoinByLink(ctx, strings.Replace(link, "#", "#x", 1)); err == nil {
		t.Fatal("wrong secret accepted by the server")
	}
	gid, err := dave.JoinByLink(ctx, link)
	ok(t, err)
	if gid != g {
		t.Fatalf("gid %s", gid)
	}
	ok(t, bob.Sync(ctx))
	bob.Settle(10 * time.Second)
	ok(t, dave.Sync(ctx))
	dave.Settle(5 * time.Second)
	gd, err := dave.ResolveGroup(ctx, g)
	ok(t, err)
	if !gd.Active || gd.Name != name || gd.About != about2 {
		t.Fatalf("dave's group: %+v", gd)
	}
	_, err = dave.SendText(ctx, g, "привет всем")
	ok(t, err)
	ok(t, alice.Sync(ctx))
	eq(t, texts(t, alice, g)[len(texts(t, alice, g))-1:], "привет всем")

	// Revoked: the old link no longer works.
	ok(t, alice.RevokeInviteLink(ctx, g))
	eve := c.register("eve")
	if _, err := eve.JoinByLink(ctx, link); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("revoked link: %v", err)
	}
}

func TestPresenceAndBlocks(t *testing.T) {
	c := newCluster(t)
	ctx := context.Background()
	alice := c.register("alice")
	bob := c.register("bob")
	carol := c.register("carol")
	aid, bid := alice.Account().UserID, bob.Account().UserID

	// Online while the app is open; last seen after.
	ok(t, bob.SetActive(ctx, true))
	p, err := alice.Presence(ctx, bid)
	ok(t, err)
	if !p[bid].Online || p[bid].LastSeen.IsZero() {
		t.Fatalf("bob: %+v", p[bid])
	}
	ok(t, bob.SetActive(ctx, false))
	p, _ = alice.Presence(ctx, bid)
	if p[bid].Online || p[bid].LastSeen.IsZero() {
		t.Fatalf("bob after closing: %+v", p[bid])
	}
	// Privacy: nobody -> hidden ("recently"); and a hider sees nobody.
	ok(t, bob.SetLastSeenPrivacy(ctx, core.SeenNobody))
	if v, _ := bob.LastSeenPrivacy(ctx); v != core.SeenNobody {
		t.Fatalf("privacy %q", v)
	}
	p, _ = alice.Presence(ctx, bid)
	if !p[bid].Hidden || !p[bid].LastSeen.IsZero() {
		t.Fatalf("hidden bob: %+v", p[bid])
	}
	ok(t, alice.SetActive(ctx, true))
	p, _ = bob.Presence(ctx, aid)
	if !p[aid].Hidden {
		t.Fatalf("bob hides his own but sees alice: %+v", p[aid])
	}
	// "Chats": only people sharing a chat.
	ok(t, bob.SetLastSeenPrivacy(ctx, core.SeenChats))
	dm, err := alice.OpenDirect(ctx, "bob")
	ok(t, err)
	p, _ = alice.Presence(ctx, bid)
	pc, _ := carol.Presence(ctx, bid)
	if p[bid].Hidden || !pc[bid].Hidden {
		t.Fatalf("chats privacy: alice %+v carol %+v", p[bid], pc[bid])
	}

	// Blocks: carol cannot start a chat with bob once he blocked her.
	ok(t, bob.Block(ctx, carol.Account().UserID))
	if l, _ := bob.Blocked(ctx); len(l) != 1 || l[0] != carol.Account().UserID {
		t.Fatalf("blocked list %v", l)
	}
	time.Sleep(10 * time.Millisecond)
	if _, err := carol.OpenDirect(ctx, "bob"); err == nil {
		t.Fatal("blocked user opened a chat")
	}
	ok(t, bob.Unblock(ctx, carol.Account().UserID))

	// In an existing direct chat: after alice blocks bob, he cannot write.
	ok(t, bob.Sync(ctx))
	_, err = bob.SendText(ctx, dm, "до блокировки")
	ok(t, err)
	ok(t, alice.Block(ctx, bid))
	if _, err := bob.SendText(ctx, dm, "после"); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("blocked user wrote: %v", err)
	}
}
