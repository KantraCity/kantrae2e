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
	Type  string     `json:"t"` // "text", "media", "meta"
	Body  string     `json:"body,omitempty"`
	TS    int64      `json:"ts"`
	Media *MediaRef  `json:"media,omitempty"`
	Meta  *groupMeta `json:"meta,omitempty"`
}

type groupMeta struct {
	Name string `json:"name"`
}

// maxCommitRetries bounds re-attempts after epoch conflicts.
const maxCommitRetries = 5

func (c *Client) requireLogin() error {
	if c.m == nil || c.acct == nil {
		return ErrNotLoggedIn
	}
	return nil
}

// CreateGroup creates an MLS group (epoch 0) with only this device.
func (c *Client) CreateGroup(ctx context.Context, name string) (string, error) {
	if err := c.refreshToken(ctx); err != nil {
		return "", err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.requireLogin(); err != nil {
		return "", err
	}
	gid := uuid.NewString()
	if _, err := c.del.CreateGroup(ctx, &deliveryv1.CreateGroupRequest{GroupId: gid}); err != nil {
		return "", err
	}
	if err := c.m.CreateGroup([]byte(gid)); err != nil {
		return "", err
	}
	err := c.persistLocked(ctx, func(tx *store.Tx) error {
		return tx.UpsertGroup(store.Group{ID: gid, Name: name, Active: true, CreatedAt: time.Now().Unix()})
	})
	return gid, err
}

// resolveUser returns the user id of a username and caches the mapping.
func (c *Client) resolveUser(ctx context.Context, username string) (string, error) {
	r, err := c.auth.LookupUser(ctx, &authv1.LookupUserRequest{Username: username})
	if err != nil {
		return "", fmt.Errorf("lookup %q: %w", username, err)
	}
	err = c.st.Update(ctx, func(tx *store.Tx) error { return tx.PutUser(r.UserId, r.Username) })
	return r.UserId, err
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
	_ = c.st.Update(ctx, func(tx *store.Tx) error { return tx.PutUser(r.UserId, r.Username) })
	return r.Username
}

// Invite adds all devices (that published KeyPackages) of the given users.
func (c *Client) Invite(ctx context.Context, groupID string, usernames ...string) error {
	if err := c.refreshToken(ctx); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.requireLogin(); err != nil {
		return err
	}
	_, current, err := c.m.GroupInfo([]byte(groupID))
	if errors.Is(err, mls.ErrNotFound) {
		return ErrNotMember
	}
	if err != nil {
		return err
	}
	inGroup := map[string]bool{}
	for _, id := range current {
		inGroup[string(id)] = true
	}
	var kps [][]byte
	var devices []string
	for _, u := range usernames {
		uid, err := c.resolveUser(ctx, u)
		if err != nil {
			return err
		}
		r, err := c.dir.FetchUserKeyPackages(ctx, &directoryv1.FetchUserKeyPackagesRequest{UserId: uid})
		if connect.CodeOf(err) == connect.CodeNotFound {
			return fmt.Errorf("%s: %w", u, ErrNoKeyPackages)
		}
		if err != nil {
			return err
		}
		for _, dk := range r.KeyPackages {
			// The KeyPackage must belong to the device the directory claims.
			id, err := mls.KeyPackageIdentity(dk.KeyPackage)
			if err != nil || !bytes.Equal(id, identity(uid, dk.DeviceId)) {
				return fmt.Errorf("directory returned a key package with unexpected identity %q", id)
			}
			if inGroup[string(id)] {
				continue // device already a member (its KeyPackage is wasted)
			}
			kps = append(kps, dk.KeyPackage)
			devices = append(devices, dk.DeviceId)
		}
	}
	if len(kps) == 0 {
		return fmt.Errorf("all devices of %s are already members", strings.Join(usernames, ", "))
	}
	if err := c.commitLocked(ctx, groupID, kps, nil, devices, nil); err != nil {
		return err
	}
	// Tell the new members the group's name.
	g, err := c.st.Group(ctx, groupID)
	if err == nil && g.Name != "" {
		_, err = c.sendLocked(ctx, groupID, payload{Type: "meta", Meta: &groupMeta{Name: g.Name}})
	}
	return err
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
	_, members, err := c.m.GroupInfo([]byte(groupID))
	if err != nil {
		return err
	}
	var ids [][]byte
	var devices []string
	for _, m := range members {
		if u, d := splitIdentity(m); u == uid {
			ids = append(ids, m)
			devices = append(devices, d)
		}
	}
	if len(ids) == 0 {
		return fmt.Errorf("%s is not a member", username)
	}
	return c.commitLocked(ctx, groupID, nil, ids, nil, devices)
}

// commitLocked creates a Commit, gets it accepted by the delivery service
// (which enforces epoch order) and only then applies it locally. On an epoch
// conflict the pending commit is discarded, missed commits are processed and
// the commit is rebuilt on top of the new epoch.
func (c *Client) commitLocked(ctx context.Context, groupID string, kps, removeIDs [][]byte, added, removed []string) error {
	gid := []byte(groupID)
	for attempt := 0; ; attempt++ {
		epoch, _, err := c.m.GroupInfo(gid)
		if errors.Is(err, mls.ErrNotFound) {
			return ErrNotMember
		}
		if err != nil {
			return err
		}
		commit, welcome, err := c.m.CreateCommit(gid, kps, removeIDs)
		if err != nil {
			return err
		}
		_, err = c.del.SendCommit(ctx, &deliveryv1.SendCommitRequest{
			GroupId: groupID, Epoch: epoch, Commit: commit, Welcome: welcome,
			AddedDeviceIds: added, RemovedDeviceIds: removed,
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
		return c.persistLocked(ctx, func(tx *store.Tx) error {
			return tx.UpsertGroup(store.Group{ID: groupID, Epoch: epoch + 1, Active: true, CreatedAt: time.Now().Unix()})
		})
	}
}

// SendText sends a text message to a group.
func (c *Client) SendText(ctx context.Context, groupID, text string) (*store.Message, error) {
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
	return c.sendLocked(ctx, groupID, payload{Type: "text", Body: text})
}

func (c *Client) sendLocked(ctx context.Context, groupID string, p payload) (*store.Message, error) {
	p.V, p.ID, p.TS = 1, uuid.NewString(), time.Now().UnixMilli()
	plain, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	gid := []byte(groupID)
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
	if msg == nil {
		return nil, nil
	}
	err = c.st.Update(ctx, func(tx *store.Tx) error {
		if p.Type == "meta" {
			return nil
		}
		return tx.InsertMessage(*msg)
	})
	return msg, err
}

// localMessage converts an outgoing payload to a stored message.
func (c *Client) localMessage(groupID string, p payload) *store.Message {
	m := &store.Message{UID: p.ID, GroupID: groupID, SenderUser: c.acct.UserID, SenderDevice: c.acct.DeviceID,
		Kind: p.Type, Body: p.Body, SentAt: p.TS, Outgoing: true}
	if p.Media != nil {
		b, _ := json.Marshal(p.Media)
		m.Body = string(b)
	}
	return m
}

// Groups lists known groups.
func (c *Client) Groups(ctx context.Context) ([]store.Group, error) { return c.st.Groups(ctx) }

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
