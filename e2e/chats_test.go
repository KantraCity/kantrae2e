package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/kantracity/kantrae2e/client/core"
)

// chatHidden reports whether u's chat list would leave out the chat.
func chatHidden(t *testing.T, u *user, gid string) bool {
	t.Helper()
	ctx := context.Background()
	gs, err := u.Groups(ctx)
	ok(t, err)
	for _, g := range gs {
		if g.ID == gid {
			var last int64
			if ms, _ := u.Messages(ctx, gid, 1); len(ms) == 1 {
				last = ms[0].SentAt
			}
			return core.ChatHidden(g, last)
		}
	}
	return true
}

func TestDeleteChats(t *testing.T) {
	c := newCluster(t)
	ctx := context.Background()
	alice := c.register("alice")
	bob := c.register("bob")
	carol := c.register("carol")
	phone := c.link(alice, "alice-phone", "alice-phone.db", "alice-phone")

	dm, err := alice.OpenDirect(ctx, "bob")
	ok(t, err)
	_, err = alice.SendText(ctx, dm, "привет")
	ok(t, err)
	ok(t, bob.Sync(ctx))
	_, err = bob.SendText(ctx, dm, "и тебе")
	ok(t, err)
	ok(t, phone.Sync(ctx))
	phone.Settle(10 * time.Second)
	ok(t, alice.Sync(ctx))
	alice.Settle(10 * time.Second)
	ok(t, phone.Sync(ctx))
	eq(t, texts(t, phone, dm), "привет", "и тебе")

	// Delete for me: gone on both of alice's devices, bob keeps everything.
	ok(t, alice.DeleteChat(ctx, dm, false))
	if !chatHidden(t, alice, dm) {
		t.Fatal("deleted chat still listed")
	}
	eq(t, texts(t, alice, dm))
	ok(t, phone.Sync(ctx))
	ok(t, bob.Sync(ctx))
	if !chatHidden(t, phone, dm) || len(texts(t, phone, dm)) != 0 {
		t.Fatalf("phone: hidden=%v texts=%q", chatHidden(t, phone, dm), texts(t, phone, dm))
	}
	eq(t, texts(t, bob, dm), "привет", "и тебе")
	if chatHidden(t, bob, dm) {
		t.Fatal("bob lost the chat")
	}

	// A new message brings the chat back, with only the new message.
	time.Sleep(5 * time.Millisecond)
	_, err = bob.SendText(ctx, dm, "ау")
	ok(t, err)
	ok(t, alice.Sync(ctx))
	if chatHidden(t, alice, dm) {
		t.Fatal("chat not back after a new message")
	}
	eq(t, texts(t, alice, dm), "ау")

	// Clear history for both.
	ok(t, alice.ClearHistory(ctx, dm, true))
	ok(t, bob.Sync(ctx))
	ok(t, phone.Sync(ctx))
	eq(t, texts(t, bob, dm))
	eq(t, texts(t, phone, dm))
	if chatHidden(t, bob, dm) || chatHidden(t, alice, dm) {
		t.Fatal("clearing history hid the chat")
	}

	// Delete for both: bob's chat disappears too.
	_, err = bob.SendText(ctx, dm, "ещё раз")
	ok(t, err)
	ok(t, alice.Sync(ctx))
	ok(t, alice.DeleteChat(ctx, dm, true))
	ok(t, bob.Sync(ctx))
	if !chatHidden(t, bob, dm) || len(texts(t, bob, dm)) != 0 {
		t.Fatalf("bob after delete for both: hidden=%v %q", chatHidden(t, bob, dm), texts(t, bob, dm))
	}
	// bob can only clear for himself in a group, not delete others' copies.

	// Leaving a group: the others remove carol's devices; carol gets nothing new.
	g, err := alice.CreateGroup(ctx, "Поход")
	ok(t, err)
	ok(t, alice.Invite(ctx, g, "bob", "carol"))
	ok(t, bob.Sync(ctx))
	ok(t, carol.Sync(ctx))
	ok(t, phone.Sync(ctx))
	_, err = carol.SendText(ctx, g, "я тут")
	ok(t, err)
	ok(t, carol.LeaveGroup(ctx, g))
	if !chatHidden(t, carol, g) {
		t.Fatal("left group still listed")
	}
	ok(t, alice.Sync(ctx))
	alice.Settle(10 * time.Second) // the deferred removal commit
	ok(t, bob.Sync(ctx))
	bob.Settle(10 * time.Second)
	ok(t, alice.Sync(ctx))
	members, err := alice.Members(ctx, g)
	ok(t, err)
	for _, m := range members {
		if m.Username == "carol" {
			t.Fatalf("carol still a member: %+v", members)
		}
	}
	_, err = alice.SendText(ctx, g, "без carol")
	ok(t, err)
	ok(t, carol.Sync(ctx))
	if got := texts(t, carol, g); len(got) != 0 {
		t.Fatalf("carol sees %q after leaving", got)
	}
	if len(carol.eventsOf(core.EventRemoved)) == 0 {
		t.Fatal("carol not removed")
	}
	if err := alice.LeaveGroup(ctx, dm); err == nil {
		t.Fatal("left a direct chat")
	}
}

func TestSavedGIFs(t *testing.T) {
	c := newCluster(t)
	ctx := context.Background()
	alice := c.register("alice")
	bob := c.register("bob")
	phone := c.link(alice, "alice-phone", "alice-phone.db", "alice-phone")

	dm, err := alice.OpenDirect(ctx, "bob")
	ok(t, err)
	ok(t, bob.Sync(ctx))
	gif := []byte("GIF89a fake animation bytes")
	sent, err := bob.SendMedia(ctx, dm, core.MediaInput{Name: "cat.gif", Data: gif, Mime: "image/gif", Kind: "gif", Width: 200, Height: 150}, core.SendOptions{})
	ok(t, err)
	_, err = bob.SendText(ctx, dm, "не гиф")
	ok(t, err)
	ok(t, alice.Sync(ctx))

	if _, err := alice.SaveGIF(ctx, dm, sent.UID); err != nil {
		t.Fatal(err)
	}
	ms, _ := alice.Messages(ctx, dm, 10)
	if _, err := alice.SaveGIF(ctx, dm, ms[len(ms)-1].UID); err == nil {
		t.Fatal("saved a text message as a GIF")
	}
	// Saving again keeps one copy.
	_, err = alice.SaveGIF(ctx, dm, sent.UID)
	ok(t, err)
	saved, err := alice.SavedGIFs(ctx)
	ok(t, err)
	if len(saved) != 1 {
		t.Fatalf("saved %d", len(saved))
	}

	// The phone has it too, and it never shows up as a chat.
	ok(t, phone.Sync(ctx))
	onPhone, err := phone.SavedGIFs(ctx)
	ok(t, err)
	if len(onPhone) != 1 {
		t.Fatalf("phone saved %d", len(onPhone))
	}
	_, data, err := phone.DownloadMedia(ctx, &onPhone[0])
	ok(t, err)
	if string(data) != string(gif) {
		t.Fatal("saved GIF content differs")
	}
	gs, _ := phone.Groups(ctx)
	for _, g := range gs {
		if core.GroupKind(g.ID) == core.KindSelf {
			t.Fatal("self-group listed")
		}
	}

	// Sending it from the phone reuses the uploaded file.
	back, err := phone.SendSavedGIF(ctx, dm, onPhone[0].UID, core.SendOptions{})
	ok(t, err)
	ok(t, bob.Sync(ctx))
	got, err := bob.Message(ctx, dm, back.UID)
	ok(t, err)
	r1, _ := core.ParseMedia(got)
	r0, _ := core.ParseMedia(sent)
	if r1 == nil || r0 == nil || r1.Hash != r0.Hash || !core.IsGIF(r1) {
		t.Fatalf("sent GIF: %+v", r1)
	}

	// Removing on the phone removes it on the laptop.
	ok(t, phone.RemoveSavedGIF(ctx, onPhone[0].UID))
	ok(t, alice.Sync(ctx))
	saved, _ = alice.SavedGIFs(ctx)
	if len(saved) != 0 {
		t.Fatalf("still saved: %d", len(saved))
	}
}

func TestCallSignals(t *testing.T) {
	c := newCluster(t)
	ctx := context.Background()
	alice := c.register("alice")
	bob := c.register("bob")
	dm, err := alice.OpenDirect(ctx, "bob")
	ok(t, err)
	ok(t, bob.Sync(ctx))
	ok(t, alice.SendSignal(ctx, dm, `{"t":"invite","call":"c1"}`))
	ok(t, bob.Sync(ctx))
	ev := bob.eventsOf(core.EventSignal)
	if len(ev) != 1 || ev[0].Detail != `{"t":"invite","call":"c1"}` || ev[0].UserID != alice.Account().UserID ||
		ev[0].DeviceID != alice.Account().DeviceID || ev[0].GroupID != dm || ev[0].Count == 0 {
		t.Fatalf("signal events %+v", ev)
	}
	// Not a message, on either side.
	eq(t, texts(t, bob, dm))
	eq(t, texts(t, alice, dm))
	if len(alice.eventsOf(core.EventSignal)) != 0 {
		t.Fatal("own signal echoed")
	}
	// The server hands out no call servers unless configured.
	if s, err := alice.IceServers(ctx); err != nil || len(s) != 0 {
		t.Fatalf("ice servers: %v %v", s, err)
	}
}
