package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/kantracity/kantrae2e/client/mls"
	"github.com/kantracity/kantrae2e/client/store"
	authv1 "github.com/kantracity/kantrae2e/gen/kantra/auth/v1"
	deliveryv1 "github.com/kantracity/kantrae2e/gen/kantra/delivery/v1"
	directoryv1 "github.com/kantracity/kantrae2e/gen/kantra/directory/v1"
)

// payload is the plaintext inside every MLS application message.
type payload struct {
	V     int        `json:"v"`
	ID    string     `json:"id"`
	Type  string     `json:"t"` // "text", "media", "meta", "history_request", "history_share"
	Body  string     `json:"body,omitempty"`
	TS    int64      `json:"ts"`
	Media *MediaRef  `json:"media,omitempty"`
	Meta  *groupMeta `json:"meta,omitempty"`
	// History sharing (see multidevice.go).
	HistReq   *historyRequest `json:"hreq,omitempty"`
	HistShare *historyShare   `json:"hshare,omitempty"`
	// Device linking (see linking.go).
	AccountKey *accountKey `json:"akey,omitempty"`
	// Message features (see messages.go).
	ReplyTo string `json:"re,omitempty"`    // text/media: uid answered
	Fwd     string `json:"fwd,omitempty"`   // text/media: forwarded from (author's name)
	Target  string `json:"tgt,omitempty"`   // edit/delete/reaction: uid
	Emoji   string `json:"emoji,omitempty"` // reaction ("" removes)
	UpTo    int64  `json:"upto,omitempty"`  // read, clear: up to (unix ms)
	// clear (see chats.go): for everyone (direct chats), and hide the chat.
	All  bool `json:"all,omitempty"`
	Hide bool `json:"hide,omitempty"`
	// Message extras (extras.go): link preview, poll, silent.
	Extra *Extra `json:"x,omitempty"`
	// pin: unpin instead; vote: the chosen options of a poll.
	Unpin   bool  `json:"unpin,omitempty"`
	Options []int `json:"opts,omitempty"`
}

// storable reports whether a payload type is a user-visible message.
func storable(t string) bool {
	return t == "text" || t == "media" || t == "poll" || t == "sys" || t == "sched" || t == "cpref"
}

// selfOnly: stored kinds that belong only in the self-group.
func selfOnly(t string) bool { return t == "sched" || t == "cpref" }

// groupMeta: group info shared among the members (groupinfo.go). Nil
// pointers leave a field unchanged; Name is always set by older clients.
type groupMeta struct {
	Name   string    `json:"name"`
	About  *string   `json:"about,omitempty"`
	Avatar *MediaRef `json:"avatar,omitempty"` // Hash "" removes the photo
	Invite *string   `json:"invite,omitempty"` // "" revokes the link
	// Keep: the name is not part of this change (only other fields are).
	KeepName bool `json:"keep_name,omitempty"`
}

// maxCommitRetries bounds re-attempts after epoch conflicts.
const maxCommitRetries = 5

func (c *Client) requireLogin() error {
	if c.m == nil || c.acct == nil {
		return ErrNotLoggedIn
	}
	return nil
}

// CreateGroup creates a group chat (MLS epoch 0) with only this device.
func (c *Client) CreateGroup(ctx context.Context, name string) (string, error) {
	if err := c.refreshToken(ctx); err != nil {
		return "", err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.requireLogin(); err != nil {
		return "", err
	}
	gid, err := c.createGroupLocked(ctx, uuid.NewString(), name, "")
	if err == nil {
		_ = c.st.Update(ctx, c.sysLocal(gid, uuid.NewString(), c.acct.UserID, time.Now().UnixMilli(), SysEvent{Action: "create", Name: name}))
	}
	return gid, err
}

func (c *Client) createGroupLocked(ctx context.Context, gid, name, peer string) (string, error) {
	if err := c.m.CreateGroup([]byte(gid)); err != nil {
		return "", err
	}
	gi, err := c.m.GroupInfoMessage([]byte(gid))
	if err == nil {
		_, err = c.del.CreateGroup(ctx, &deliveryv1.CreateGroupRequest{GroupId: gid, GroupInfo: gi})
	}
	if err != nil {
		_ = c.m.ForgetGroup([]byte(gid))
		_ = c.persistLocked(ctx, func(tx *store.Tx) error { return tx.DeleteMLSGroup([]byte(gid)) })
		return "", err
	}
	now := time.Now()
	err = c.persistLocked(ctx, func(tx *store.Tx) error {
		if err := tx.SeenMember(gid, c.acct.UserID, now.UnixMilli()); err != nil {
			return err
		}
		return tx.UpsertGroup(store.Group{ID: gid, Name: name, Peer: peer, Active: true, CreatedAt: now.Unix()})
	})
	return gid, err
}

// resolveUser returns the user id of a username and caches the mapping.
func (c *Client) resolveUser(ctx context.Context, username string) (string, error) {
	r, err := c.auth.LookupUser(ctx, &authv1.LookupUserRequest{Username: username})
	if err != nil {
		return "", fmt.Errorf("lookup %q: %w", username, err)
	}
	err = c.putProfiles(ctx, []*authv1.User{lookupUser(r)})
	return r.UserId, err
}

func lookupUser(r *authv1.LookupUserResponse) *authv1.User {
	return &authv1.User{UserId: r.UserId, Username: r.Username, DisplayName: r.DisplayName, Bio: r.Bio, AvatarId: r.AvatarId}
}

// Username resolves a user id to a username (cached locally).
func (c *Client) Username(ctx context.Context, userID string) string {
	if n, err := c.st.Username(ctx, userID); err == nil {
		return n
	}
	r, err := c.auth.LookupUser(ctx, &authv1.LookupUserRequest{UserId: userID})
	if err != nil {
		return userID
	}
	_ = c.putProfiles(ctx, []*authv1.User{lookupUser(r)})
	return r.Username
}

// Invite adds all devices (that published KeyPackages) of the given users.
func (c *Client) Invite(ctx context.Context, groupID string, usernames ...string) error {
	if err := c.refreshToken(ctx); err != nil {
		return err
	}
	if GroupKind(groupID) != KindGroup {
		return ErrDirectInvite
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.requireLogin(); err != nil {
		return err
	}
	users, err := c.inviteLocked(ctx, groupID, usernames)
	if err == nil {
		c.sysLocked(ctx, groupID, SysEvent{Action: "add", Users: users})
	}
	return err
}

// inviteLocked adds all devices of users; it returns the ids of the users added.
func (c *Client) inviteLocked(ctx context.Context, groupID string, usernames []string) ([]string, error) {
	_, current, err := c.m.GroupInfo([]byte(groupID))
	if errors.Is(err, mls.ErrNotFound) {
		return nil, ErrNotMember
	}
	if err != nil {
		return nil, err
	}
	inGroup := map[string]bool{}
	for _, id := range current {
		inGroup[string(id)] = true
	}
	var kps [][]byte
	var devices, users []string
	for _, u := range usernames {
		uid, err := c.resolveUser(ctx, u)
		if err != nil {
			return nil, err
		}
		r, err := c.dir.FetchUserKeyPackages(ctx, &directoryv1.FetchUserKeyPackagesRequest{UserId: uid})
		if connect.CodeOf(err) == connect.CodeNotFound {
			return nil, fmt.Errorf("%s: %w", u, ErrNoKeyPackages)
		}
		if err != nil {
			return nil, err
		}
		for _, dk := range r.KeyPackages {
			// The KeyPackage must belong to the device the directory claims,
			// with the key we know for it (if any).
			id, key, err := mls.KeyPackageInfo(dk.KeyPackage)
			if err != nil || !bytes.Equal(id, identity(uid, dk.DeviceId)) {
				return nil, fmt.Errorf("directory returned a key package with unexpected identity %q", id)
			}
			if err := c.checkKeyPackageLocked(ctx, uid, dk.DeviceId, key); err != nil {
				return nil, fmt.Errorf("%s: %w", u, err)
			}
			if inGroup[string(id)] {
				continue // device already a member (its KeyPackage is wasted)
			}
			kps = append(kps, dk.KeyPackage)
			devices = append(devices, dk.DeviceId)
			users = append(users, uid)
		}
	}
	if len(kps) == 0 {
		return nil, fmt.Errorf("all devices of %s are already members", strings.Join(usernames, ", "))
	}
	if err := c.commitLocked(ctx, groupID, commitSpec{adds: kps, addedDevices: devices, addedUsers: users}); err != nil {
		return nil, err
	}
	var added []string
	for _, u := range users {
		if len(added) == 0 || added[len(added)-1] != u {
			added = append(added, u)
		}
	}
	// Tell the new members the group's info (name, description, photo,
	// invite link).
	if g, err := c.st.Group(ctx, groupID); err == nil {
		_, err = c.sendLocked(ctx, groupID, payload{Type: "meta", Meta: fullMeta(g)})
		return added, err
	}
	return added, nil
}

// Remove removes all devices of a user from the group.
func (c *Client) Remove(ctx context.Context, groupID, username string) error {
	if err := c.refreshToken(ctx); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.requireLogin(); err != nil {
		return err
	}
	uid, err := c.resolveUser(ctx, username)
	if err != nil {
		return err
	}
	if err := c.syncLocked(ctx); err != nil {
		return err
	}
	ids, devices, err := c.devicesOfLocked(groupID, uid)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return fmt.Errorf("%s is not a member", username)
	}
	if err := c.commitLocked(ctx, groupID, commitSpec{removeIDs: ids, removedDevices: devices}); err != nil {
		return err
	}
	c.sysLocked(ctx, groupID, SysEvent{Action: "remove", Users: []string{uid}})
	return nil
}

// devicesOfLocked lists the member identities (and device ids) of a user.
func (c *Client) devicesOfLocked(groupID, uid string) ([][]byte, []string, error) {
	_, members, err := c.m.GroupInfo([]byte(groupID))
	if err != nil {
		return nil, nil, err
	}
	var ids [][]byte
	var devices []string
	seen := map[string]bool{}
	for _, m := range members {
		if u, d := splitIdentity(m); u == uid && !seen[string(m)] {
			seen[string(m)] = true
			ids = append(ids, m)
			devices = append(devices, d)
		}
	}
	return ids, devices, nil
}

// commitSpec describes the membership change of a Commit.
type commitSpec struct {
	adds           [][]byte // KeyPackages
	addedDevices   []string // same order as adds
	addedUsers     []string // same order as adds
	removeIDs      [][]byte // credential identities
	removedDevices []string
	// still, if set, is re-checked before every attempt; false aborts
	// silently (e.g. someone else already added the device).
	still func() (bool, error)
}

// commitLocked creates a Commit, gets it accepted by the delivery service
// (which enforces epoch order) and only then applies it locally. On an epoch
// conflict the pending commit is discarded, missed commits are processed and
// the commit is rebuilt on top of the new epoch.
func (c *Client) commitLocked(ctx context.Context, groupID string, spec commitSpec) error {
	gid := []byte(groupID)
	for attempt := 0; ; attempt++ {
		if spec.still != nil {
			ok, err := spec.still()
			if err != nil || !ok {
				return err
			}
		}
		epoch, _, err := c.m.GroupInfo(gid)
		if errors.Is(err, mls.ErrNotFound) {
			return ErrNotMember
		}
		if err != nil {
			return err
		}
		commit, welcome, groupInfo, err := c.m.CreateCommit(gid, spec.adds, spec.removeIDs)
		if err != nil {
			return err
		}
		_, err = c.del.SendCommit(ctx, &deliveryv1.SendCommitRequest{
			GroupId: groupID, Epoch: epoch, Commit: commit, Welcome: welcome, GroupInfo: groupInfo,
			AddedDeviceIds: spec.addedDevices, AddedUserIds: spec.addedUsers, RemovedDeviceIds: spec.removedDevices,
		})
		if err != nil {
			if cerr := c.m.ClearPendingCommit(gid); cerr != nil {
				return cerr
			}
			if connect.CodeOf(err) == connect.CodeAborted && attempt < maxCommitRetries {
				c.logf("epoch conflict in %s at epoch %d, catching up", groupID, epoch)
				if err := c.syncLocked(ctx); err != nil {
					return err
				}
				continue
			}
			return err
		}
		if err := c.m.ApplyPendingCommit(gid); err != nil {
			return err
		}
		observe, events := c.observeGroupLocked(ctx, groupID)
		defer func() {
			for _, e := range events {
				c.emit(e)
			}
		}()
		now := time.Now().UnixMilli()
		return c.persistLocked(ctx, func(tx *store.Tx) error {
			if observe != nil {
				if err := observe(tx); err != nil {
					return err
				}
			}
			for _, u := range spec.addedUsers {
				if err := tx.SeenMember(groupID, u, now); err != nil {
					return err
				}
			}
			return tx.UpsertGroup(store.Group{ID: groupID, Epoch: epoch + 1, Active: true, CreatedAt: time.Now().Unix()})
		})
	}
}

// SendText sends a text message to a group.
func (c *Client) SendText(ctx context.Context, groupID, text string) (*store.Message, error) {
	return c.SendMessage(ctx, groupID, text, SendOptions{})
}

// SendMessage sends a text message, optionally as a reply.
func (c *Client) SendMessage(ctx context.Context, groupID, text string, opt SendOptions) (*store.Message, error) {
	if strings.TrimSpace(text) == "" {
		return nil, errors.New("empty message")
	}
	if err := c.refreshToken(ctx); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.requireLogin(); err != nil {
		return nil, err
	}
	return c.sendLocked(ctx, groupID, payload{Type: "text", Body: text, ReplyTo: opt.ReplyTo, Extra: opt.extra()})
}

func (c *Client) sendLocked(ctx context.Context, groupID string, p payload) (*store.Message, error) {
	p.V, p.TS = 1, time.Now().UnixMilli()
	if p.ID == "" { // scheduled messages carry their own id (dedup across devices)
		p.ID = uuid.NewString()
	}
	plain, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	gid := []byte(groupID)
	if conflict, err := c.conflictInGroupLocked(ctx, groupID); err == nil && conflict {
		return nil, ErrKeyConflict
	}
	for attempt := 0; ; attempt++ {
		epoch, _, err := c.m.GroupInfo(gid)
		if errors.Is(err, mls.ErrNotFound) {
			return nil, ErrNotMember
		}
		if err != nil {
			return nil, err
		}
		ct, err := c.m.Encrypt(gid, plain)
		if err != nil {
			return nil, err
		}
		// The sender ratchet advanced: persist before the message leaves.
		if err := c.persistLocked(ctx, nil); err != nil {
			return nil, err
		}
		_, err = c.del.SendApplication(ctx, &deliveryv1.SendApplicationRequest{GroupId: groupID, Epoch: epoch, Payload: ct})
		if connect.CodeOf(err) == connect.CodeFailedPrecondition && attempt == 0 {
			// Our epoch is too old: catch up and re-encrypt once.
			if err := c.syncLocked(ctx); err != nil {
				return nil, err
			}
			continue
		}
		if err != nil {
			return nil, err
		}
		break
	}
	msg := c.localMessage(groupID, p)
	if !storable(p.Type) {
		return msg, nil
	}
	if err := c.st.Update(ctx, func(tx *store.Tx) error { return tx.InsertMessage(*msg) }); err != nil {
		return nil, err
	}
	c.scheduleBackup()
	return msg, nil
}

// localMessage converts an outgoing payload to a stored message.
func (c *Client) localMessage(groupID string, p payload) *store.Message {
	m := &store.Message{UID: p.ID, GroupID: groupID, SenderUser: c.acct.UserID, SenderDevice: c.acct.DeviceID,
		Kind: p.Type, Body: p.Body, SentAt: p.TS, Outgoing: true, ReplyTo: p.ReplyTo, FwdFrom: p.Fwd, Extra: p.Extra.json()}
	if p.Media != nil {
		b, _ := json.Marshal(p.Media)
		m.Body = string(b)
	}
	return m
}

// MessagesOfKind returns the newest stored messages of one kind ("text",
// "media", …), oldest first.
func (c *Client) MessagesOfKind(ctx context.Context, groupID, kind string, limit int) ([]store.Message, error) {
	return c.st.MessagesOfKind(ctx, groupID, kind, limit)
}

// Groups lists known groups.
// The account's hidden self-group is not listed.
func (c *Client) Groups(ctx context.Context) ([]store.Group, error) {
	all, err := c.st.Groups(ctx)
	out := all[:0]
	for _, g := range all {
		if GroupKind(g.ID) != KindSelf {
			out = append(out, g)
		}
	}
	return out, err
}

// Messages returns the last limit messages of a group.
func (c *Client) Messages(ctx context.Context, groupID string, limit int) ([]store.Message, error) {
	return c.st.Messages(ctx, groupID, limit)
}

// Member is one device in a group.
type Member struct {
	UserID   string
	Username string
	DeviceID string
}

// Members lists the group members according to the local MLS state.
func (c *Client) Members(ctx context.Context, groupID string) ([]Member, error) {
	c.mu.Lock()
	if err := c.requireLogin(); err != nil {
		c.mu.Unlock()
		return nil, err
	}
	_, ids, err := c.m.GroupInfo([]byte(groupID))
	c.mu.Unlock()
	if errors.Is(err, mls.ErrNotFound) {
		return nil, ErrNotMember
	}
	if err != nil {
		return nil, err
	}
	out := make([]Member, 0, len(ids))
	for _, id := range ids {
		u, d := splitIdentity(id)
		out = append(out, Member{UserID: u, DeviceID: d, Username: c.Username(ctx, u)})
	}
	return out, nil
}

// ResolveGroup finds a group by id, id prefix or exact name.
func (c *Client) ResolveGroup(ctx context.Context, ref string) (*store.Group, error) {
	gs, err := c.st.Groups(ctx)
	if err != nil {
		return nil, err
	}
	var found []store.Group
	for _, g := range gs {
		if GroupKind(g.ID) == KindSelf {
			continue
		}
		if strings.HasPrefix(ref, "@") && g.Peer != "" {
			if n, err := c.st.Username(ctx, g.Peer); err == nil && "@"+n == ref {
				found = append(found, g)
			}
			continue
		}
		if g.ID == ref || g.Name == ref || (len(ref) >= 4 && strings.HasPrefix(g.ID, ref)) {
			found = append(found, g)
		}
	}
	switch len(found) {
	case 0:
		return nil, fmt.Errorf("no group %q", ref)
	case 1:
		return &found[0], nil
	default:
		return nil, fmt.Errorf("group %q is ambiguous", ref)
	}
}
