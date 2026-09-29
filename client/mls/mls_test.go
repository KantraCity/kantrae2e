package mls

import (
	"bytes"
	"errors"
	"testing"
)

type device struct {
	id     []byte
	sk, pk []byte
	c      *Client
	// persisted state, rebuilt from TakeChanges
	groups map[string]*persistedGroup
	kps    map[string][]byte
}

type persistedGroup struct {
	state  []byte
	epochs map[uint64][]byte
}

func newDevice(t *testing.T, id string) *device {
	t.Helper()
	sk, pk, err := GenerateSignatureKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	c, err := NewClient([]byte(id), sk, pk)
	if err != nil {
		t.Fatal(err)
	}
	return &device{id: []byte(id), sk: sk, pk: pk, c: c,
		groups: map[string]*persistedGroup{}, kps: map[string][]byte{}}
}

// persist applies the change log like a real storage layer would.
func (d *device) persist(t *testing.T) {
	t.Helper()
	chs, err := d.c.TakeChanges()
	if err != nil {
		t.Fatal(err)
	}
	for _, ch := range chs {
		switch ch.Kind {
		case ChangeGroupWrite:
			g := d.groups[string(ch.GroupID)]
			if g == nil {
				g = &persistedGroup{epochs: map[uint64][]byte{}}
				d.groups[string(ch.GroupID)] = g
			}
			g.state = ch.State
			for _, e := range ch.Epochs {
				g.epochs[e.ID] = e.Data
			}
			for id := range g.epochs {
				if id < ch.KeepFrom {
					delete(g.epochs, id)
				}
			}
		case ChangeKeyPackageInsert:
			d.kps[string(ch.KeyPackageID)] = ch.KeyPackageData
		case ChangeKeyPackageDelete:
			delete(d.kps, string(ch.KeyPackageID))
		}
	}
}

// restart simulates a process restart: a fresh client loaded from storage.
func (d *device) restart(t *testing.T) {
	t.Helper()
	d.c.Close()
	c, err := NewClient(d.id, d.sk, d.pk)
	if err != nil {
		t.Fatal(err)
	}
	for gid, g := range d.groups {
		var eps []Epoch
		for id, data := range g.epochs {
			eps = append(eps, Epoch{ID: id, Data: data})
		}
		if err := c.LoadGroup([]byte(gid), g.state, eps); err != nil {
			t.Fatal(err)
		}
	}
	for id, data := range d.kps {
		if err := c.LoadKeyPackage([]byte(id), data); err != nil {
			t.Fatal(err)
		}
	}
	d.c = c
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func ok(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// Phase 1 DoD: two virtual members exchange an encrypted message in-process.
func TestTwoMembersInProcess(t *testing.T) {
	alice, bob := newDevice(t, "alice"), newDevice(t, "bob")
	gid := []byte("g1")

	kp := must(bob.c.GenerateKeyPackage())
	if w, _, _, err := Inspect(kp); err != nil || w != WireKeyPackage {
		t.Fatalf("inspect kp: %d %v", w, err)
	}
	if err := alice.c.CreateGroup(gid); err != nil {
		t.Fatal(err)
	}
	commit, welcome, err := alice.c.CreateCommit(gid, [][]byte{kp}, nil)
	if err != nil || len(commit) == 0 || len(welcome) == 0 {
		t.Fatalf("commit: %v", err)
	}
	if _, ep, g, _ := Inspect(commit); ep != 0 || !bytes.Equal(g, gid) {
		t.Fatalf("commit epoch=%d gid=%q", ep, g)
	}
	if err := alice.c.ApplyPendingCommit(gid); err != nil {
		t.Fatal(err)
	}
	joined := must(bob.c.JoinGroup(welcome))
	if !bytes.Equal(joined, gid) {
		t.Fatalf("joined %q", joined)
	}

	ct := must(alice.c.Encrypt(gid, []byte("hi bob")))
	if bytes.Contains(ct, []byte("hi bob")) {
		t.Fatal("plaintext leaked")
	}
	p := must(bob.c.Process(gid, ct))
	if p.Kind != KindApplication || string(p.Plaintext) != "hi bob" || string(p.Sender) != "alice" || p.Epoch != 1 {
		t.Fatalf("unexpected %+v", p)
	}

	ep, members, err := bob.c.GroupInfo(gid)
	if err != nil || ep != 1 || len(members) != 2 {
		t.Fatalf("info %d %q %v", ep, members, err)
	}
	if _, _, err := bob.c.GroupInfo([]byte("nope")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want not found, got %v", err)
	}
}

// Phase 4 core: state survives a restart through the change log.
func TestRestartKeepsSession(t *testing.T) {
	alice, bob := newDevice(t, "alice"), newDevice(t, "bob")
	gid := []byte("g2")

	kp := must(bob.c.GenerateKeyPackage())
	bob.persist(t)
	bob.restart(t) // key package secrets must survive

	ok(t, alice.c.CreateGroup(gid))
	_, welcome, err := alice.c.CreateCommit(gid, [][]byte{kp}, nil)
	ok(t, err)
	ok(t, alice.c.ApplyPendingCommit(gid))
	alice.persist(t)
	must(bob.c.JoinGroup(welcome))
	bob.persist(t)
	if len(bob.kps) != 0 {
		t.Fatal("used key package not deleted")
	}

	alice.restart(t)
	bob.restart(t)

	for i, text := range []string{"one", "two", "three"} {
		ct := must(alice.c.Encrypt(gid, []byte(text)))
		alice.persist(t)
		p := must(bob.c.Process(gid, ct))
		bob.persist(t)
		if string(p.Plaintext) != text {
			t.Fatalf("msg %d: %q", i, p.Plaintext)
		}
		bob.restart(t)
	}
}

// Phase 6 core: a removed member cannot read later messages.
func TestRemoveMember(t *testing.T) {
	a, b, c := newDevice(t, "a"), newDevice(t, "b"), newDevice(t, "c")
	gid := []byte("g3")
	ok(t, a.c.CreateGroup(gid))
	kps := [][]byte{must(b.c.GenerateKeyPackage()), must(c.c.GenerateKeyPackage())}
	_, welcome, err := a.c.CreateCommit(gid, kps, nil)
	ok(t, err)
	ok(t, a.c.ApplyPendingCommit(gid))
	must(b.c.JoinGroup(welcome))
	must(c.c.JoinGroup(welcome))

	commit, _, err := a.c.CreateCommit(gid, nil, [][]byte{[]byte("c")})
	ok(t, err)
	ok(t, a.c.ApplyPendingCommit(gid))
	if p := must(b.c.Process(gid, commit)); p.Kind != KindCommit || p.Removed || p.Epoch != 2 {
		t.Fatalf("b: %+v", p)
	}
	if p := must(c.c.Process(gid, commit)); !p.Removed {
		t.Fatalf("c not removed: %+v", p)
	}

	ct := must(a.c.Encrypt(gid, []byte("secret")))
	if p := must(b.c.Process(gid, ct)); string(p.Plaintext) != "secret" {
		t.Fatal("b cannot read")
	}
	if p, err := c.c.Process(gid, ct); err == nil {
		t.Fatalf("removed member decrypted: %q", p.Plaintext)
	}
}

// A rejected commit can be cleared and the group keeps working.
func TestClearPendingCommit(t *testing.T) {
	a, b := newDevice(t, "a"), newDevice(t, "b")
	gid := []byte("g4")
	ok(t, a.c.CreateGroup(gid))
	_, _, err := a.c.CreateCommit(gid, [][]byte{must(b.c.GenerateKeyPackage())}, nil)
	ok(t, err)
	ok(t, a.c.ClearPendingCommit(gid))
	if ep, members, _ := a.c.GroupInfo(gid); ep != 0 || len(members) != 1 {
		t.Fatalf("epoch %d members %d", ep, len(members))
	}
}
