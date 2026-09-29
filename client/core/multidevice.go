package core

// Seamless multi-device support:
//
//  1. A new device joins every group of its account by itself with an MLS
//     External Commit, built from the GroupInfo the delivery service keeps for
//     each group (every Commit uploads the GroupInfo of its new epoch). The
//     server only allows this for users that already have a device in the
//     group; all members re-check it and remove a device whose user was not
//     in the group before.
//  2. Fallback when no current GroupInfo exists: the device sends a
//     JOIN_REQUEST with a fresh KeyPackage and any online member adds it.
//  3. History: the seed-phrase backup is uploaded continuously (see
//     scheduleBackup) and restored on login. Messages that are newer than the
//     backup are re-sent by an online member ("history sharing") inside the
//     current MLS epoch, but only from the time that member first saw the
//     requesting user in the group, so new users never see older history.
//     Shared messages are stored with origin "shared": their authenticity
//     rests on the member who re-sent them.

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/kantracity/kantrae2e/client/mls"
	"github.com/kantracity/kantrae2e/client/store"
	authv1 "github.com/kantracity/kantrae2e/gen/kantra/auth/v1"
	deliveryv1 "github.com/kantracity/kantrae2e/gen/kantra/delivery/v1"
)

const (
	maxJoinRetries      = 5
	joinRequestInterval = 10 * time.Minute
	historyShareLimit   = 5000      // messages per request
	historyBatchBytes   = 256 << 10 // JSON bytes per share message
)

type historyRequest struct {
	ID    string `json:"id"`
	Since int64  `json:"since"` // newest message the device already has (unix ms)
}

type sharedMessage struct {
	UID          string `json:"uid"`
	SenderUser   string `json:"su"`
	SenderDevice string `json:"sd"`
	Kind         string `json:"k"`
	Body         string `json:"b"`
	SentAt       int64  `json:"ts"`
}

type historyShare struct {
	Req       string          `json:"req"`
	To        string          `json:"to"` // device id of the requester
	GroupName string          `json:"name,omitempty"`
	Messages  []sharedMessage `json:"msgs"`
}

// jitter spreads responses of several online members; the user's own devices
// answer first.
func jitter(own bool) time.Duration {
	if own {
		return time.Duration(rand.IntN(200)) * time.Millisecond
	}
	return 400*time.Millisecond + time.Duration(rand.IntN(800))*time.Millisecond
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

// otherDevicesOf reports whether members contain a device of userID other
// than exceptDevice.
func (c *Client) otherDevicesOf(members [][]byte, userID, exceptDevice string) bool {
	for _, m := range members {
		if u, d := splitIdentity(m); u == userID && d != exceptDevice {
			return true
		}
	}
	return false
}

func hasIdentity(members [][]byte, id []byte) bool {
	for _, m := range members {
		if string(m) == string(id) {
			return true
		}
	}
	return false
}

// seenWrites records the users of members as seen in the group at ts
// (keeps earlier timestamps), except skipUser.
func (c *Client) seenWrites(groupID string, members [][]byte, ts int64, skipUser string) func(*store.Tx) error {
	users := map[string]bool{}
	for _, m := range members {
		if u, _ := splitIdentity(m); u != "" && u != skipUser {
			users[u] = true
		}
	}
	return func(tx *store.Tx) error {
		for u := range users {
			if err := tx.SeenMember(groupID, u, ts); err != nil {
				return err
			}
		}
		return nil
	}
}

// ---- joining ------------------------------------------------------------------

// JoinMyGroups makes this device a member of every group its account is in.
func (c *Client) JoinMyGroups(ctx context.Context) (joined, requested int, err error) {
	if err := c.refreshToken(ctx); err != nil {
		return 0, 0, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.requireLogin(); err != nil {
		return 0, 0, err
	}
	joined, requested, err = c.joinMyGroupsLocked(ctx)
	c.flushDeferred()
	return joined, requested, err
}

func (c *Client) joinMyGroupsLocked(ctx context.Context) (joined, requested int, err error) {
	r, err := c.del.ListMyGroups(ctx, &deliveryv1.ListMyGroupsRequest{})
	if err != nil {
		return 0, 0, err
	}
	for _, g := range r.Groups {
		if g.DeviceIsMember {
			continue
		}
		jerr := c.joinExternalLocked(ctx, g.GroupId)
		if jerr == nil {
			joined++
			continue
		}
		c.logf("external join of %s failed (%v), asking members to add this device", g.GroupId, jerr)
		if t, ok := c.joinRequested[g.GroupId]; ok && time.Since(t) < joinRequestInterval {
			continue
		}
		if err := c.requestJoinLocked(ctx, g.GroupId); err != nil {
			c.logf("join request for %s: %v", g.GroupId, err)
			continue
		}
		requested++
	}
	return joined, requested, nil
}

func (c *Client) joinExternalLocked(ctx context.Context, groupID string) error {
	for attempt := 0; ; attempt++ {
		gi, err := c.del.GetGroupInfo(ctx, &deliveryv1.GetGroupInfoRequest{GroupId: groupID})
		if err != nil {
			return err
		}
		gid, commit, newGI, err := c.m.ExternalJoin(gi.GroupInfo)
		if err != nil {
			return err
		}
		drop := func() {
			_ = c.m.ForgetGroup(gid)
			_ = c.persistLocked(ctx, func(tx *store.Tx) error { return tx.DeleteMLSGroup(gid) })
		}
		if string(gid) != groupID {
			drop()
			return fmt.Errorf("GroupInfo is for another group")
		}
		_, err = c.del.ExternalJoin(ctx, &deliveryv1.ExternalJoinRequest{
			GroupId: groupID, Epoch: gi.Epoch, Commit: commit, GroupInfo: newGI})
		if err != nil {
			drop()
			if connect.CodeOf(err) == connect.CodeAborted && attempt < maxJoinRetries {
				continue // someone committed meanwhile: fetch the new GroupInfo
			}
			return err
		}
		_, members, _ := c.m.GroupInfo(gid)
		seen := c.seenWrites(groupID, members, time.Now().UnixMilli(), "")
		observe, events := c.observeGroupLocked(ctx, groupID)
		err = c.persistLocked(ctx, func(tx *store.Tx) error {
			if err := seen(tx); err != nil {
				return err
			}
			if observe != nil {
				if err := observe(tx); err != nil {
					return err
				}
			}
			return tx.UpsertGroup(store.Group{ID: groupID, Epoch: gi.Epoch + 1, Active: true, CreatedAt: time.Now().Unix()})
		})
		if err != nil {
			return err
		}
		delete(c.joinRequested, groupID)
		c.emit(Event{Type: EventGroupJoined, GroupID: groupID})
		for _, e := range events {
			c.emit(e)
		}
		c.deferHistoryRequestLocked(groupID)
		return nil
	}
}

func (c *Client) requestJoinLocked(ctx context.Context, groupID string) error {
	kp, err := c.m.GenerateKeyPackage()
	if err != nil {
		return err
	}
	if err := c.persistLocked(ctx, nil); err != nil { // keep the KeyPackage secrets
		return err
	}
	if _, err := c.del.RequestJoin(ctx, &deliveryv1.RequestJoinRequest{GroupId: groupID, KeyPackage: kp}); err != nil {
		return err
	}
	c.joinRequested[groupID] = time.Now()
	return nil
}

// handleJoinRequestLocked: another device of a member asks to be added.
func (c *Client) handleJoinRequestLocked(e *deliveryv1.Envelope) {
	uid, did := e.SenderUserId, e.SenderDeviceId
	if did == c.acct.DeviceID || uid == "" {
		return
	}
	want := identity(uid, did)
	kpID, kpKey, err := mls.KeyPackageInfo(e.Payload)
	if err != nil || string(kpID) != string(want) {
		c.logf("join request with mismatching key package in %s ignored", e.GroupId)
		return
	}
	if c.checkKeyPackageLocked(context.Background(), uid, did, kpKey) != nil {
		return // known device with a different key: refuse (event emitted)
	}
	_, members, err := c.m.GroupInfo([]byte(e.GroupId))
	if err != nil || hasIdentity(members, want) || !c.otherDevicesOf(members, uid, did) {
		return // not our group, already added, or the user is not a member
	}
	groupID, kp := e.GroupId, e.Payload
	own := uid == c.acct.UserID
	c.deferLocked(func(ctx context.Context) {
		if !sleepCtx(ctx, jitter(own)) {
			return
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		if err := c.syncLocked(ctx); err != nil {
			c.logf("auto-invite sync: %v", err)
			return
		}
		still := func() (bool, error) {
			_, members, err := c.m.GroupInfo([]byte(groupID))
			if err != nil {
				return false, nil
			}
			return !hasIdentity(members, want) && c.otherDevicesOf(members, uid, did), nil
		}
		err := c.commitLocked(ctx, groupID, commitSpec{adds: [][]byte{kp}, addedDevices: []string{did},
			addedUsers: []string{uid}, still: still})
		if err != nil {
			c.logf("auto-invite of %s into %s: %v", did, groupID, err)
		}
	})
}

// checkExternalJoinLocked reports whether an External Commit by joiner is
// legitimate: its user must have had another device in the group. Otherwise
// the joiner is removed in the background.
func (c *Client) checkExternalJoinLocked(groupID string, joiner []byte, members [][]byte) bool {
	uid, did := splitIdentity(joiner)
	if c.otherDevicesOf(members, uid, did) {
		return true
	}
	c.logf("SECURITY: unexpected external join of %s into %s", joiner, groupID)
	id := append([]byte(nil), joiner...)
	c.deferLocked(func(ctx context.Context) {
		c.mu.Lock()
		defer c.mu.Unlock()
		still := func() (bool, error) {
			_, members, err := c.m.GroupInfo([]byte(groupID))
			return err == nil && hasIdentity(members, id), nil
		}
		if err := c.commitLocked(ctx, groupID, commitSpec{removeIDs: [][]byte{id}, removedDevices: []string{did}, still: still}); err != nil {
			c.logf("removing unexpected member: %v", err)
		}
	})
	return false
}

// ---- history sharing ------------------------------------------------------------

func (c *Client) deferHistoryRequestLocked(groupID string) {
	c.deferLocked(func(ctx context.Context) {
		c.mu.Lock()
		defer c.mu.Unlock()
		since, err := c.st.LastMessageTime(ctx, groupID)
		if err != nil {
			return
		}
		_, err = c.sendLocked(ctx, groupID, payload{Type: "history_request",
			HistReq: &historyRequest{ID: uuid.NewString(), Since: since}})
		if err != nil {
			c.logf("history request in %s: %v", groupID, err)
		}
	})
}

func (c *Client) handleHistoryRequestLocked(groupID, uid, did string, req *historyRequest) {
	if did == c.acct.DeviceID || req.ID == "" {
		return
	}
	_, members, err := c.m.GroupInfo([]byte(groupID))
	if err != nil || !c.otherDevicesOf(members, uid, did) {
		return // only additional devices of existing members get history
	}
	own := uid == c.acct.UserID
	c.deferLocked(func(ctx context.Context) {
		if !sleepCtx(ctx, jitter(own)) {
			return
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		// Process what arrived meanwhile: maybe someone answered already.
		if err := c.syncLocked(ctx); err != nil {
			c.logf("history share sync: %v", err)
		}
		if c.answered[req.ID] {
			return
		}
		from := req.Since
		if !own {
			fs, err := c.st.FirstSeen(ctx, groupID, uid)
			if err != nil {
				return // we cannot tell since when this user may read
			}
			from = max(from, fs)
		}
		msgs, err := c.st.MessagesSince(ctx, groupID, from, historyShareLimit)
		if err != nil {
			return
		}
		name := ""
		if g, err := c.st.Group(ctx, groupID); err == nil {
			name = g.Name
		}
		c.answered[req.ID] = true
		batch := &historyShare{Req: req.ID, To: did, GroupName: name}
		size := 0
		sent := false
		send := func() bool {
			if len(batch.Messages) == 0 && sent {
				return true // an empty answer is only sent to say "nothing missing"
			}
			sent = true
			if _, err := c.sendLocked(ctx, groupID, payload{Type: "history_share", HistShare: batch}); err != nil {
				c.logf("history share in %s: %v", groupID, err)
				return false
			}
			batch = &historyShare{Req: req.ID, To: did}
			size = 0
			return true
		}
		for _, m := range msgs {
			sm := sharedMessage{UID: m.UID, SenderUser: m.SenderUser, SenderDevice: m.SenderDevice,
				Kind: m.Kind, Body: m.Body, SentAt: m.SentAt}
			batch.Messages = append(batch.Messages, sm)
			size += len(m.Body) + 200
			if size >= historyBatchBytes && !send() {
				return
			}
		}
		send()
	})
}

func (c *Client) handleHistoryShareLocked(ctx context.Context, groupID, sharer string, sh *historyShare) (func(*store.Tx) error, *Event) {
	if sh.To != c.acct.DeviceID || c.answered[sh.Req] {
		c.answered[sh.Req] = true
		return nil, nil // for another device, or a duplicate answer
	}
	c.answered[sh.Req] = true
	setName := ""
	if g, err := c.st.Group(ctx, groupID); err == nil && g.Name == "" {
		setName = sh.GroupName
	}
	ev := &Event{Type: EventHistory, GroupID: groupID}
	write := func(tx *store.Tx) error {
		if setName != "" {
			if err := tx.UpsertGroup(store.Group{ID: groupID, Name: setName, Active: true}); err != nil {
				return err
			}
		}
		for _, m := range sh.Messages {
			if !storable(m.Kind) || m.UID == "" {
				continue
			}
			isNew, err := tx.InsertSharedMessage(store.Message{UID: m.UID, GroupID: groupID, SenderUser: m.SenderUser,
				SenderDevice: m.SenderDevice, Kind: m.Kind, Body: m.Body, SentAt: m.SentAt,
				Outgoing: m.SenderUser == c.acct.UserID}, sharer)
			if err != nil {
				return err
			}
			if isNew {
				ev.Count++
			}
		}
		return nil
	}
	c.scheduleBackup()
	return write, ev
}

// ---- devices --------------------------------------------------------------------

// Devices lists the account's devices.
func (c *Client) Devices(ctx context.Context) ([]*authv1.Device, error) {
	if err := c.refreshToken(ctx); err != nil {
		return nil, err
	}
	r, err := c.auth.ListDevices(ctx, &authv1.ListDevicesRequest{})
	if err != nil {
		return nil, err
	}
	return r.Devices, nil
}

// RevokeDevice revokes another device of this account (e.g. a lost phone)
// and removes it from every group this device is in. It returns the number
// of groups it was removed from.
func (c *Client) RevokeDevice(ctx context.Context, deviceID string) (int, error) {
	if err := c.refreshToken(ctx); err != nil {
		return 0, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.requireLogin(); err != nil {
		return 0, err
	}
	if deviceID == c.acct.DeviceID {
		return 0, errors.New("cannot revoke the current device")
	}
	if _, err := c.auth.RevokeDevice(ctx, &authv1.RevokeDeviceRequest{DeviceId: deviceID}); err != nil {
		return 0, err
	}
	if err := c.syncLocked(ctx); err != nil {
		return 0, err
	}
	groups, err := c.st.Groups(ctx)
	if err != nil {
		return 0, err
	}
	target := identity(c.acct.UserID, deviceID)
	removed := 0
	for _, g := range groups {
		if !g.Active {
			continue
		}
		_, members, err := c.m.GroupInfo([]byte(g.ID))
		if err != nil || !hasIdentity(members, target) {
			continue
		}
		if err := c.commitLocked(ctx, g.ID, commitSpec{removeIDs: [][]byte{target}, removedDevices: []string{deviceID}}); err != nil {
			return removed, fmt.Errorf("remove from %s: %w", g.ID, err)
		}
		removed++
	}
	return removed, nil
}
