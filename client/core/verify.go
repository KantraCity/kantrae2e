package core

// Key verification ("safety numbers").
//
// Basic MLS credentials are not certified by anyone, so a malicious server
// could hand out a KeyPackage of its own for a user's device, or join a group
// with a forged identity. Defence: every client remembers the signature key
// of each device it meets (trust on first use), users compare device
// fingerprints out of band and mark contacts as verified, and:
//   - a known device showing up with another key is a conflict: sending to
//     groups containing that key is blocked until the user decides;
//   - a KeyPackage with a changed key is refused when inviting;
//   - a new device of a verified contact (or of the own account) raises an
//     event so the user can verify it.

import (
	"context"
	"crypto/sha512"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kantracity/kantrae2e/client/store"
)

var (
	// ErrKeyConflict blocks sending into a group with a device whose key changed.
	ErrKeyConflict = errors.New("a device in this group changed its key; compare fingerprints (keys/trust) or remove the user")
	// ErrKeyChanged refuses a KeyPackage whose key differs from the known one.
	ErrKeyChanged = errors.New("key package key differs from the known key of that device (possible MITM)")
)

const fingerprintIterations = 1024

// Fingerprint is the human-comparable fingerprint of a device: 30 digits in
// 6 groups, derived from its identity and signature public key. Both ends
// compute the same value.
func Fingerprint(identity, sigKey []byte) string {
	h := sha512.New()
	h.Write([]byte("kantra/fingerprint/v1"))
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(identity)))
	h.Write(n[:])
	h.Write(identity)
	h.Write(sigKey)
	sum := h.Sum(nil)
	for i := 0; i < fingerprintIterations; i++ {
		s := sha512.Sum512(append(sum, sigKey...))
		sum = s[:]
	}
	groups := make([]string, 6)
	for i := range groups {
		chunk := sum[i*5 : i*5+5]
		v := uint64(chunk[0])<<32 | uint64(chunk[1])<<24 | uint64(chunk[2])<<16 | uint64(chunk[3])<<8 | uint64(chunk[4])
		groups[i] = fmt.Sprintf("%05d", v%100000)
	}
	return strings.Join(groups, " ")
}

// MyFingerprint returns this device's fingerprint.
func (c *Client) MyFingerprint(ctx context.Context) (string, error) {
	a := c.Account()
	if a == nil {
		return "", ErrNotLoggedIn
	}
	pk, err := c.st.Get(ctx, kvSigPublic)
	if err != nil {
		return "", err
	}
	return Fingerprint(identity(a.UserID, a.DeviceID), pk), nil
}

// KeyInfo describes one known device of a user.
type KeyInfo struct {
	DeviceID            string
	Fingerprint         string
	Status              string // store.KeySeen / KeyVerified / KeyConflict
	ConflictFingerprint string // the new, unconfirmed key (Status conflict)
	ThisDevice          bool
}

// Keys lists the known devices of a user with fingerprints.
func (c *Client) Keys(ctx context.Context, username string) ([]KeyInfo, error) {
	a := c.Account()
	if a == nil {
		return nil, ErrNotLoggedIn
	}
	uid := a.UserID
	if username != a.Username {
		var err error
		if uid, err = c.resolveUser(ctx, username); err != nil {
			return nil, err
		}
	}
	keys, err := c.st.DeviceKeys(ctx, uid)
	if err != nil {
		return nil, err
	}
	out := make([]KeyInfo, 0, len(keys))
	for _, k := range keys {
		ki := KeyInfo{DeviceID: k.DeviceID, Status: k.Status,
			Fingerprint: Fingerprint(identity(uid, k.DeviceID), k.Key),
			ThisDevice:  uid == a.UserID && k.DeviceID == a.DeviceID}
		if k.Status == store.KeyConflict {
			ki.ConflictFingerprint = Fingerprint(identity(uid, k.DeviceID), k.ConflictKey)
		}
		out = append(out, ki)
	}
	return out, nil
}

// Trust marks devices of a user as verified after the fingerprints were
// compared out of band. With deviceID == "" all devices in state "seen" are
// verified; a device in conflict must be named explicitly, which accepts its
// new key. Returns the number of devices updated.
func (c *Client) Trust(ctx context.Context, username, deviceID string) (int, error) {
	a := c.Account()
	if a == nil {
		return 0, ErrNotLoggedIn
	}
	uid := a.UserID
	if username != a.Username {
		var err error
		if uid, err = c.resolveUser(ctx, username); err != nil {
			return 0, err
		}
	}
	keys, err := c.st.DeviceKeys(ctx, uid)
	if err != nil {
		return 0, err
	}
	n := 0
	err = c.st.Update(ctx, func(tx *store.Tx) error {
		for _, k := range keys {
			switch {
			case deviceID == "" && k.Status == store.KeySeen,
				deviceID == k.DeviceID && k.Status != store.KeyConflict:
				k.Status = store.KeyVerified
			case deviceID == k.DeviceID && k.Status == store.KeyConflict:
				k.Key, k.ConflictKey, k.Status = k.ConflictKey, nil, store.KeyVerified
			default:
				continue
			}
			if err := tx.PutDeviceKey(k); err != nil {
				return err
			}
			n++
		}
		return nil
	})
	if err == nil && deviceID != "" && n == 0 {
		err = fmt.Errorf("no known device %s of %s", deviceID, username)
	}
	return n, err
}

// checkKeyPackageLocked verifies the key of a KeyPackage against the known
// key of that device. Returns ErrKeyChanged on mismatch.
func (c *Client) checkKeyPackageLocked(ctx context.Context, userID, deviceID string, key []byte) error {
	k, err := c.st.DeviceKey(ctx, userID, deviceID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if string(k.Key) != string(key) {
		c.emit(Event{Type: EventSecurity, Detail: fmt.Sprintf("refused a key package with a changed key for device %s of %s",
			deviceID, c.Username(ctx, userID))})
		return ErrKeyChanged
	}
	return nil
}

// observeGroupLocked compares the keys of all members of a group with the
// known keys. It returns the writes to persist and the events to emit.
func (c *Client) observeGroupLocked(ctx context.Context, groupID string) (func(*store.Tx) error, []Event) {
	members, err := c.m.Members([]byte(groupID))
	if err != nil {
		return nil, nil
	}
	now := time.Now().UnixMilli()
	var puts []store.DeviceKey
	var events []Event
	verifiedUsers := map[string]bool{}
	hasVerified := func(uid string) bool {
		if v, ok := verifiedUsers[uid]; ok {
			return v
		}
		keys, _ := c.st.DeviceKeys(ctx, uid)
		v := false
		for _, k := range keys {
			v = v || k.Status == store.KeyVerified
		}
		verifiedUsers[uid] = v
		return v
	}
	for _, m := range members {
		uid, did := splitIdentity(m.Identity)
		if uid == "" || (uid == c.acct.UserID && did == c.acct.DeviceID) {
			continue
		}
		k, err := c.st.DeviceKey(ctx, uid, did)
		switch {
		case errors.Is(err, store.ErrNotFound):
			if hasVerified(uid) || uid == c.acct.UserID {
				who := c.Username(ctx, uid)
				if uid == c.acct.UserID {
					who = "your account"
				}
				events = append(events, Event{Type: EventNewDevice, GroupID: groupID,
					Detail: fmt.Sprintf("new unverified device %s of %s", did, who)})
			}
			puts = append(puts, store.DeviceKey{UserID: uid, DeviceID: did, Key: m.SignatureKey, Status: store.KeySeen, FirstSeen: now})
		case err != nil:
			continue
		case string(k.Key) == string(m.SignatureKey):
		case k.Status == store.KeyConflict && string(k.ConflictKey) == string(m.SignatureKey):
		default:
			k.Status, k.ConflictKey = store.KeyConflict, m.SignatureKey
			puts = append(puts, *k)
			events = append(events, Event{Type: EventSecurity, GroupID: groupID,
				Detail: fmt.Sprintf("device %s of %s appeared with a different key: possible MITM, sending is blocked",
					did, c.Username(ctx, uid))})
		}
	}
	if len(puts) == 0 {
		return nil, events
	}
	return func(tx *store.Tx) error {
		for _, k := range puts {
			if err := tx.PutDeviceKey(k); err != nil {
				return err
			}
		}
		return nil
	}, events
}

// conflictInGroupLocked reports whether a member of the group uses a key
// that is in conflict with the known key of its device.
func (c *Client) conflictInGroupLocked(ctx context.Context, groupID string) (bool, error) {
	members, err := c.m.Members([]byte(groupID))
	if err != nil {
		return false, err
	}
	for _, m := range members {
		uid, did := splitIdentity(m.Identity)
		k, err := c.st.DeviceKey(ctx, uid, did)
		if err != nil {
			continue
		}
		if k.Status == store.KeyConflict && string(k.ConflictKey) == string(m.SignatureKey) {
			return true, nil
		}
	}
	return false, nil
}

// rememberOwnKey stores this device's key as verified (so new devices of the
// own account are reported).
func (c *Client) rememberOwnKey(ctx context.Context) error {
	a := c.Account()
	if a == nil {
		return nil
	}
	if _, err := c.st.DeviceKey(ctx, a.UserID, a.DeviceID); err == nil {
		return nil
	}
	pk, err := c.st.Get(ctx, kvSigPublic)
	if err != nil {
		return err
	}
	return c.st.Update(ctx, func(tx *store.Tx) error {
		return tx.PutDeviceKey(store.DeviceKey{UserID: a.UserID, DeviceID: a.DeviceID, Key: pk,
			Status: store.KeyVerified, FirstSeen: time.Now().UnixMilli()})
	})
}
