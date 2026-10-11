package core

// Device linking (like "linked devices" in Signal/WhatsApp) and recovery.
//
// Every account has a hidden MLS group that only contains its own devices:
// the self-group. A new device starts out *pending*: the server lets it do
// nothing but knock on the self-group (RequestJoin with a KeyPackage). The
// account's other devices show a prompt with a short code derived from the
// new device's key; the same code is shown on the new device. When the user
// confirms, the approving device
//   1. approves the device at auth (server-side gate for everything else),
//   2. adds it to the self-group (MLS Commit + Welcome) and marks its key
//      as verified (the user compared the code),
//   3. sends the account's history key through the self-group (end-to-end
//      encrypted, never seen by the server).
// The new device then restores the backup, joins all chats by itself and
// asks for missing messages (see multidevice.go).
//
// Without another device the recovery phrase (optional, 12 words) does the
// same: it unwraps the history key the server keeps encrypted and proves
// to auth that the user owns the account's secrets.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"connectrpc.com/connect"

	"github.com/kantracity/kantrae2e/client/crypto"
	"github.com/kantracity/kantrae2e/client/mls"
	"github.com/kantracity/kantrae2e/client/store"
	authv1 "github.com/kantracity/kantrae2e/gen/kantra/auth/v1"
)

var (
	ErrPendingApproval = errors.New("this device is waiting for approval on another device")
	ErrNoRecovery      = errors.New("no recovery phrase was set up for this account")
	ErrSelfChat        = errors.New("you cannot start a chat with yourself")
	ErrDirectInvite    = errors.New("members cannot be added to a direct chat")
	ErrPeerStarted     = errors.New("the other person is creating this chat right now, try again in a moment")
)

// Chat kinds, derived from the group id.
const (
	KindGroup  = "group"
	KindDirect = "direct"
	KindSelf   = "self"

	selfPrefix   = "self-"
	directPrefix = "dm-"

	kvApproved      = "device_approved"
	linkRetryPeriod = 2 * time.Minute
	recoveryAD      = "kantra/recovery-wrap/v1"
)

// GroupKind tells direct chats, groups and the hidden self-group apart.
func GroupKind(id string) string {
	switch {
	case strings.HasPrefix(id, selfPrefix):
		return KindSelf
	case strings.HasPrefix(id, directPrefix):
		return KindDirect
	}
	return KindGroup
}

func hashID(prefix, s string) string {
	h := sha256.Sum256([]byte("kantra/" + prefix + s))
	return prefix + hex.EncodeToString(h[:16])
}

func selfGroupID(userID string) string { return hashID(selfPrefix, userID) }

// directGroupID is the same for both participants, so a direct chat exists
// at most once per pair of users.
func directGroupID(a, b string) string {
	ids := []string{a, b}
	sort.Strings(ids)
	return hashID(directPrefix, ids[0]+":"+ids[1])
}

// LinkCode is the short code shown on a new device and on the device that
// approves it: the first two groups of the new device's fingerprint.
func LinkCode(identityBytes, sigKey []byte) string {
	g := strings.Fields(Fingerprint(identityBytes, sigKey))
	return g[0] + " " + g[1]
}

// MyLinkCode is this device's link code.
func (c *Client) MyLinkCode(ctx context.Context) (string, error) {
	a := c.Account()
	if a == nil {
		return "", ErrNotLoggedIn
	}
	pk, err := c.st.Get(ctx, kvSigPublic)
	if err != nil {
		return "", err
	}
	return LinkCode(identity(a.UserID, a.DeviceID), pk), nil
}

// IsApproved reports whether this device has been approved (cached once true).
func (c *Client) IsApproved(ctx context.Context) (bool, error) {
	if v, err := c.st.Get(ctx, kvApproved); err == nil && string(v) == "1" {
		return true, nil
	}
	if c.Account() == nil {
		return false, ErrNotLoggedIn
	}
	if err := c.refreshToken(ctx); err != nil {
		return false, err
	}
	st, err := c.auth.GetAccountStatus(ctx, &authv1.GetAccountStatusRequest{})
	if err != nil {
		return false, err
	}
	if st.DeviceApproved {
		if err = c.st.Update(ctx, func(tx *store.Tx) error { return tx.Set(kvApproved, []byte("1")) }); err == nil {
			c.emit(Event{Type: EventApproved})
		}
	}
	return st.DeviceApproved, err
}

// HasRecovery reports whether the account has a recovery phrase.
func (c *Client) HasRecovery(ctx context.Context) (bool, error) {
	if err := c.refreshToken(ctx); err != nil {
		return false, err
	}
	st, err := c.auth.GetAccountStatus(ctx, &authv1.GetAccountStatusRequest{})
	if err != nil {
		return false, err
	}
	return st.HasRecovery, nil
}

// ---- self-group -------------------------------------------------------------

// ensureSelfGroupLocked makes sure this (approved) device is in the
// account's self-group, creating the group if nobody has yet.
func (c *Client) ensureSelfGroupLocked(ctx context.Context) error {
	gid := selfGroupID(c.acct.UserID)
	if _, _, err := c.m.GroupInfo([]byte(gid)); err == nil {
		return nil
	}
	_, err := c.createGroupLocked(ctx, gid, "", "")
	if connect.CodeOf(err) == connect.CodeAlreadyExists {
		// Another device of ours created it: join by ourselves.
		return c.joinExternalLocked(ctx, gid)
	}
	return err
}

// requestLinkLocked asks the account's other devices to approve this one.
func (c *Client) requestLinkLocked(ctx context.Context, force bool) error {
	gid := selfGroupID(c.acct.UserID)
	if t, ok := c.joinRequested[gid]; ok && !force && time.Since(t) < linkRetryPeriod {
		return nil
	}
	return c.requestJoinLocked(ctx, gid)
}

// RequestLink (re)sends the approval request to the account's devices.
func (c *Client) RequestLink(ctx context.Context) error {
	if err := c.refreshToken(ctx); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.requireLogin(); err != nil {
		return err
	}
	err := c.requestLinkLocked(ctx, true)
	if connect.CodeOf(err) == connect.CodePermissionDenied || connect.CodeOf(err) == connect.CodeNotFound {
		return errors.New("no other device of this account has been online with this version yet: open Kantra on it, or use the recovery phrase")
	}
	return err
}

// LinkRequest is a pending device waiting for approval.
type LinkRequest struct {
	DeviceID   string
	DeviceName string
	Code       string
	At         time.Time
	kp         []byte
}

// handleSelfJoinRequestLocked: a new device of our account wants in.
func (c *Client) handleSelfJoinRequestLocked(groupID, uid, did string, kp []byte) {
	if uid != c.acct.UserID || did == c.acct.DeviceID {
		return
	}
	id, key, err := mls.KeyPackageInfo(kp)
	if err != nil || string(id) != string(identity(uid, did)) {
		c.logf("link request with mismatching key package ignored")
		return
	}
	if _, members, err := c.m.GroupInfo([]byte(groupID)); err != nil || hasIdentity(members, id) {
		return // not in the self-group ourselves, or already linked
	}
	req := &LinkRequest{DeviceID: did, Code: LinkCode(id, key), At: time.Now(), kp: kp}
	c.links[did] = req
	c.emit(Event{Type: EventLinkRequest, DeviceID: did, Detail: req.Code})
}

// PendingLinks lists devices waiting for approval (with their names).
func (c *Client) PendingLinks(ctx context.Context) []LinkRequest {
	c.mu.Lock()
	out := make([]LinkRequest, 0, len(c.links))
	for _, l := range c.links {
		out = append(out, *l)
	}
	c.mu.Unlock()
	if len(out) == 0 {
		return out
	}
	if devs, err := c.Devices(ctx); err == nil {
		names := map[string]string{}
		for _, d := range devs {
			names[d.Id] = d.DeviceName
		}
		for i := range out {
			out[i].DeviceName = names[out[i].DeviceID]
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out
}

// dropResolvedLinksLocked forgets requests of devices already in the self-group.
func (c *Client) dropResolvedLinksLocked(members [][]byte) {
	for did := range c.links {
		if hasIdentity(members, identity(c.acct.UserID, did)) {
			delete(c.links, did)
			c.emit(Event{Type: EventLinkResolved, DeviceID: did})
		}
	}
}

// ApproveLink approves a pending device after the user compared the code:
// it is approved at auth, added to the self-group and receives the history key.
func (c *Client) ApproveLink(ctx context.Context, deviceID string) error {
	if err := c.refreshToken(ctx); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	req := c.links[deviceID]
	if req == nil {
		return errors.New("no pending request from this device")
	}
	if _, err := c.auth.ApproveDevice(ctx, &authv1.ApproveDeviceRequest{DeviceId: deviceID}); err != nil &&
		connect.CodeOf(err) != connect.CodeNotFound { // NotFound: approved meanwhile by another device
		return err
	}
	gid := selfGroupID(c.acct.UserID)
	want := identity(c.acct.UserID, deviceID)
	still := func() (bool, error) {
		_, members, err := c.m.GroupInfo([]byte(gid))
		return err == nil && !hasIdentity(members, want), nil
	}
	if err := c.commitLocked(ctx, gid, commitSpec{adds: [][]byte{req.kp}, addedDevices: []string{deviceID},
		addedUsers: []string{c.acct.UserID}, still: still}); err != nil {
		return err
	}
	// The user compared the code: the new device's key is verified.
	if _, key, err := mls.KeyPackageInfo(req.kp); err == nil {
		_ = c.st.Update(ctx, func(tx *store.Tx) error {
			return tx.PutDeviceKey(store.DeviceKey{UserID: c.acct.UserID, DeviceID: deviceID, Key: key,
				Status: store.KeyVerified, FirstSeen: time.Now().UnixMilli()})
		})
	}
	delete(c.links, deviceID)
	c.emit(Event{Type: EventLinkResolved, DeviceID: deviceID})
	return c.sendAccountKeyLocked(ctx)
}

// sendAccountKeyLocked shares the history key with the self-group.
func (c *Client) sendAccountKeyLocked(ctx context.Context) error {
	key, err := c.historyKey(ctx)
	if errors.Is(err, ErrNoHistoryKey) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = c.sendLocked(ctx, selfGroupID(c.acct.UserID), payload{Type: "account_key", AccountKey: &accountKey{HistoryKey: key}})
	return err
}

// RejectLink revokes a pending device ("it wasn't me").
func (c *Client) RejectLink(ctx context.Context, deviceID string) error {
	if err := c.refreshToken(ctx); err != nil {
		return err
	}
	c.mu.Lock()
	delete(c.links, deviceID)
	c.mu.Unlock()
	_, err := c.auth.RevokeDevice(ctx, &authv1.RevokeDeviceRequest{DeviceId: deviceID})
	return err
}

type accountKey struct {
	HistoryKey []byte `json:"hk"`
}

// handleAccountKeyLocked receives the history key from an own device.
func (c *Client) handleAccountKeyLocked(ctx context.Context, groupID, senderUser string, ak *accountKey) func(*store.Tx) error {
	if GroupKind(groupID) != KindSelf || senderUser != c.acct.UserID || len(ak.HistoryKey) != crypto.KeySize {
		return nil
	}
	if _, err := c.historyKey(ctx); err == nil {
		return nil // already have it
	}
	c.deferLocked(func(ctx context.Context) { c.afterApproval(ctx) })
	key := ak.HistoryKey
	return func(tx *store.Tx) error { return tx.Set(kvHistKey, key) }
}

// afterApproval makes a freshly approved device a full participant: it
// restores the backup, joins all chats and asks for missing messages.
func (c *Client) afterApproval(ctx context.Context) {
	if err := c.st.Update(ctx, func(tx *store.Tx) error { return tx.Set(kvApproved, []byte("1")) }); err != nil {
		return
	}
	restored := 0
	if _, err := c.historyKey(ctx); err == nil {
		n, err := c.RestoreHistory(ctx)
		if err != nil {
			c.logf("restore history: %v", err)
		}
		restored = n
	}
	joined, _, err := c.JoinMyGroups(ctx)
	if err != nil {
		c.logf("join groups: %v", err)
	}
	if err := c.EnsureKeyPackages(ctx); err != nil {
		c.logf("key packages: %v", err)
	}
	c.emit(Event{Type: EventApproved, Count: restored, Detail: fmt.Sprintf("%d", joined)})
}

// ---- recovery phrase ----------------------------------------------------------

// SetupRecovery creates (or replaces) the account's recovery phrase and
// returns it. Show it to the user once; it is not stored anywhere.
func (c *Client) SetupRecovery(ctx context.Context) (string, error) {
	a := c.Account()
	if a == nil {
		return "", ErrNotLoggedIn
	}
	if ok, err := c.IsApproved(ctx); err != nil || !ok {
		if err == nil {
			err = ErrPendingApproval
		}
		return "", err
	}
	key, err := c.historyKey(ctx)
	if errors.Is(err, ErrNoHistoryKey) {
		// Account from before device linking: start a history key now and
		// hand it to the other devices.
		key = crypto.NewKey()
		if err = c.st.Update(ctx, func(tx *store.Tx) error { return tx.Set(kvHistKey, key) }); err == nil {
			c.mu.Lock()
			if err := c.sendAccountKeyLocked(ctx); err != nil {
				c.logf("share history key: %v", err)
			}
			c.mu.Unlock()
		}
	}
	if err != nil {
		return "", err
	}
	phrase, err := crypto.NewRecoveryPhrase()
	if err != nil {
		return "", err
	}
	wrapKey, verifier, err := crypto.RecoveryKeys(phrase, a.UserID)
	if err != nil {
		return "", err
	}
	wrapped, err := crypto.Seal(wrapKey, key, []byte(recoveryAD))
	if err != nil {
		return "", err
	}
	if err := c.refreshToken(ctx); err != nil {
		return "", err
	}
	if _, err := c.auth.SetRecovery(ctx, &authv1.SetRecoveryRequest{Verifier: verifier, WrappedKey: wrapped}); err != nil {
		return "", err
	}
	return phrase, nil
}

// Recover approves this (new) device with the recovery phrase, unwraps the
// history key and makes the device a full participant.
func (c *Client) Recover(ctx context.Context, phrase string) (*LoginResult, error) {
	a := c.Account()
	if a == nil {
		return nil, ErrNotLoggedIn
	}
	wrapKey, verifier, err := crypto.RecoveryKeys(phrase, a.UserID)
	if err != nil {
		return nil, err
	}
	if err := c.refreshToken(ctx); err != nil {
		return nil, err
	}
	r, err := c.auth.Recover(ctx, &authv1.RecoverRequest{Verifier: verifier})
	if connect.CodeOf(err) == connect.CodeNotFound {
		return nil, ErrNoRecovery
	}
	if err != nil {
		return nil, err
	}
	key, err := crypto.Open(wrapKey, r.WrappedKey, []byte(recoveryAD))
	if err != nil {
		return nil, errors.New("recovery data does not match the phrase")
	}
	if err := c.st.Update(ctx, func(tx *store.Tx) error {
		if err := tx.Set(kvHistKey, key); err != nil {
			return err
		}
		return tx.Set(kvApproved, []byte("1"))
	}); err != nil {
		return nil, err
	}
	res := &LoginResult{}
	if res.Restored, err = c.RestoreHistory(ctx); err != nil {
		return res, fmt.Errorf("restore history: %w", err)
	}
	res.Joined, res.Requested, err = c.JoinMyGroups(ctx)
	return res, err
}

// ---- direct chats -----------------------------------------------------------------

// UserInfo is a search result.
type UserInfo struct {
	UserID   string
	Username string
}

// SearchUsers finds users by username prefix (at least 2 characters).
func (c *Client) SearchUsers(ctx context.Context, query string) ([]UserInfo, error) {
	if err := c.refreshToken(ctx); err != nil {
		return nil, err
	}
	r, err := c.auth.SearchUsers(ctx, &authv1.SearchUsersRequest{Query: query})
	if err != nil {
		return nil, err
	}
	out := make([]UserInfo, 0, len(r.Users))
	_ = c.st.Update(ctx, func(tx *store.Tx) error {
		for _, u := range r.Users {
			_ = tx.PutUser(u.UserId, u.Username)
		}
		return nil
	})
	for _, u := range r.Users {
		out = append(out, UserInfo{UserID: u.UserId, Username: u.Username})
	}
	return out, nil
}

// OpenDirect returns the direct chat with a user, creating it (and inviting
// the user) if needed — like opening a person's profile in Telegram.
func (c *Client) OpenDirect(ctx context.Context, username string) (string, error) {
	if err := c.refreshToken(ctx); err != nil {
		return "", err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.requireLogin(); err != nil {
		return "", err
	}
	username = strings.TrimPrefix(strings.TrimSpace(username), "@")
	peer, err := c.resolveUser(ctx, username)
	if err != nil {
		return "", err
	}
	if peer == c.acct.UserID {
		return "", ErrSelfChat
	}
	gid := directGroupID(c.acct.UserID, peer)
	if _, _, err := c.m.GroupInfo([]byte(gid)); err != nil {
		// Not a member locally: maybe it exists already (created by the peer
		// or by another device of ours) — catch up first.
		if err := c.syncLocked(ctx); err != nil {
			return "", err
		}
	}
	if _, _, err := c.m.GroupInfo([]byte(gid)); err != nil {
		_, err := c.createGroupLocked(ctx, gid, "", peer)
		if connect.CodeOf(err) == connect.CodeAlreadyExists {
			if jerr := c.joinExternalLocked(ctx, gid); jerr != nil {
				return "", ErrPeerStarted
			}
		} else if err != nil {
			return "", err
		}
	}
	// Invite the peer if none of their devices is in the chat yet.
	_, members, err := c.m.GroupInfo([]byte(gid))
	if err != nil {
		return "", err
	}
	if !c.otherDevicesOf(members, peer, "") {
		if err := c.inviteLocked(ctx, gid, []string{username}); err != nil {
			return gid, err
		}
	}
	return gid, nil
}

// directPeer returns the other user of a direct chat from the MLS roster.
func (c *Client) directPeer(members [][]byte) string {
	for _, m := range members {
		if u, _ := splitIdentity(m); u != "" && u != c.acct.UserID {
			return u
		}
	}
	return ""
}
