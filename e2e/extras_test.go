package e2e

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kantracity/kantrae2e/client/core"
)

func TestMessageExtras(t *testing.T) {
	c := newCluster(t)
	ctx := context.Background()
	alice := c.register("alice")
	bob := c.register("bob")
	phone := c.link(alice, "alice-phone", "alice-phone.db", "alice-phone")
	dm, err := alice.OpenDirect(ctx, "bob")
	ok(t, err)
	ok(t, bob.Sync(ctx))

	// Pin for everybody / only for me.
	m1, err := alice.SendText(ctx, dm, "Встреча в Пятницу в 18:00")
	ok(t, err)
	m2, err := alice.SendText(ctx, dm, "второе")
	ok(t, err)
	ok(t, bob.Sync(ctx))
	ok(t, alice.Pin(ctx, dm, m1.UID, true, true))
	ok(t, alice.Pin(ctx, dm, m2.UID, true, false))
	ok(t, bob.Sync(ctx))
	pb, err := bob.Pinned(ctx, dm)
	ok(t, err)
	if len(pb) != 1 || pb[0].UID != m1.UID {
		t.Fatalf("bob's pins: %+v", pb)
	}
	pa, _ := alice.Pinned(ctx, dm)
	if len(pa) != 2 {
		t.Fatalf("alice's pins: %d", len(pa))
	}
	ok(t, bob.Pin(ctx, dm, m1.UID, false, true))
	ok(t, alice.Sync(ctx))
	if pa, _ = alice.Pinned(ctx, dm); len(pa) != 1 || pa[0].UID != m2.UID {
		t.Fatalf("after unpin: %+v", pa)
	}

	// Search: on this device, case-insensitive in Russian.
	found, err := bob.Search(ctx, dm, "пятниц", 10)
	ok(t, err)
	if len(found) != 1 || found[0].UID != m1.UID {
		t.Fatalf("search: %+v", found)
	}

	// Poll and votes.
	if _, err := alice.SendPoll(ctx, dm, core.Poll{Question: "Куда?", Options: []string{"Кино", "Кино"}}); err == nil {
		t.Fatal("poll with one distinct option accepted")
	}
	poll, err := alice.SendPoll(ctx, dm, core.Poll{Question: "Куда идём?", Options: []string{"Кино", "Парк", "Кафе"}})
	ok(t, err)
	ok(t, bob.Sync(ctx))
	pm, err := bob.Message(ctx, dm, poll.UID)
	ok(t, err)
	if x := core.ParseExtra(pm); pm.Kind != "poll" || x == nil || x.Poll == nil || len(x.Poll.Options) != 3 {
		t.Fatalf("poll at bob: %+v", pm)
	}
	if err := bob.Vote(ctx, dm, poll.UID, []int{0, 1}); err == nil {
		t.Fatal("two options in a single-choice poll")
	}
	ok(t, bob.Vote(ctx, dm, poll.UID, []int{1}))
	ok(t, alice.Vote(ctx, dm, poll.UID, []int{1}))
	ok(t, alice.Sync(ctx))
	votes, err := alice.Votes(ctx, dm, poll.UID)
	ok(t, err)
	if len(votes) != 2 || votes[bob.Account().UserID][0] != 1 {
		t.Fatalf("votes: %v", votes)
	}

	// Silent message and link preview (made by the sender).
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(`<html><head><title>Fallback</title><meta property="og:title" content="Kantra — мессенджер">
			<meta property="og:description" content="Сквозное шифрование"></head><body>x</body></html>`))
	}))
	defer site.Close()
	lp, err := core.FetchLinkPreview(ctx, site.URL+"/page")
	ok(t, err)
	if lp.Title != "Kantra — мессенджер" || lp.Desc != "Сквозное шифрование" {
		t.Fatalf("preview %+v", lp)
	}
	sm, err := alice.SendMessage(ctx, dm, "смотри "+site.URL+"/page", core.SendOptions{Silent: true, Link: lp})
	ok(t, err)
	ok(t, bob.Sync(ctx))
	got, _ := bob.Message(ctx, dm, sm.UID)
	if x := core.ParseExtra(got); x == nil || !x.Silent || x.Link == nil || x.Link.Title != lp.Title {
		t.Fatalf("extras at bob: %+v", got)
	}

	// Scheduled: synced to the phone; sent once even if both devices send.
	s, err := alice.Schedule(ctx, dm, "напоминание", time.Now().Add(time.Hour), core.SendOptions{})
	ok(t, err)
	ok(t, phone.Sync(ctx))
	ps, err := phone.ScheduledIn(ctx, dm)
	ok(t, err)
	if len(ps) != 1 || ps[0].Text != "напоминание" {
		t.Fatalf("phone's scheduled: %+v", ps)
	}
	_, err = alice.SendScheduledNow(ctx, s.ID)
	ok(t, err)
	_, _ = phone.SendScheduledNow(ctx, s.ID) // the phone too (before it heard of the deletion)
	ok(t, bob.Sync(ctx))
	n := 0
	for _, x := range texts(t, bob, dm) {
		if x == "напоминание" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("scheduled message arrived %d times", n)
	}
	ok(t, phone.Sync(ctx))
	if ps, _ = phone.ScheduledIn(ctx, dm); len(ps) != 0 {
		t.Fatalf("still scheduled: %+v", ps)
	}
}
