package core

// Group info like Telegram's: name, description and photo, and invite
// links. All of it is shared among the members as MLS messages ("meta"),
// so the server never sees it. An invite link carries a random secret in
// the URL fragment (never sent to a server); the delivery service keeps
// only its SHA-256 to let link holders knock, and the members check the
// secret again before adding them.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"strings"

	"github.com/kantracity/kantrae2e/client/store"
	deliveryv1 "github.com/kantracity/kantrae2e/gen/kantra/delivery/v1"
)

var ErrBadInvite = errors.New("invalid invite link")

func fullMeta(g *store.Group) *groupMeta {
	m := &groupMeta{Name: g.Name, About: &g.About, Invite: &g.Invite}
	if g.Avatar != "" {
		var ref MediaRef
		if json.Unmarshal([]byte(g.Avatar), &ref) == nil {
			m.Avatar = &ref
		}
	}
	return m
}

func metaUpdate(m *groupMeta) store.GroupInfoUpdate {
	u := store.GroupInfoUpdate{About: m.About, Invite: m.Invite}
	if m.Avatar != nil {
		s := ""
		if m.Avatar.Hash != "" {
			b, _ := json.Marshal(m.Avatar)
			s = string(b)
		}
		u.Avatar = &s
	}
	return u
}

// sendMeta shares a group info change and applies it here.
func (c *Client) sendMeta(ctx context.Context, groupID string, m *groupMeta) error {
	if GroupKind(groupID) != KindGroup {
		return errors.New("only groups have group info")
	}
	if err := c.refreshToken(ctx); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.requireLogin(); err != nil {
		return err
	}
	g, err := c.st.Group(ctx, groupID)
	if err != nil {
		return err
	}
	if m.Name == "" {
		m.Name, m.KeepName = g.Name, true
	}
	if _, err := c.sendLocked(ctx, groupID, payload{Type: "meta", Meta: m}); err != nil {
		return err
	}
	if !m.KeepName && m.Name != g.Name {
		c.sysLocked(ctx, groupID, SysEvent{Action: "rename", Name: m.Name})
	}
	if m.Avatar != nil {
		act := "photo"
		if m.Avatar.Hash == "" {
			act = "nophoto"
		}
		c.sysLocked(ctx, groupID, SysEvent{Action: act})
	}
	return c.st.Update(ctx, func(tx *store.Tx) error {
		if !m.KeepName {
			if err := tx.SetGroupInfo(groupID, store.GroupInfoUpdate{Name: &m.Name}); err != nil {
				return err
			}
		}
		return tx.SetGroupInfo(groupID, metaUpdate(m))
	})
}

// SetGroupInfo renames a group and sets its description (nil: unchanged).
func (c *Client) SetGroupInfo(ctx context.Context, groupID string, name, about *string) error {
	m := &groupMeta{About: about}
	if name != nil {
		n := strings.TrimSpace(*name)
		if n == "" || len([]rune(n)) > 128 {
			return errors.New("group name must be 1-128 characters")
		}
		m.Name = n
	}
	if about != nil && len([]rune(*about)) > 255 {
		return errors.New("description is too long")
	}
	return c.sendMeta(ctx, groupID, m)
}

// SetGroupPhoto uploads a group photo (encrypted like any media); nil
// removes it.
func (c *Client) SetGroupPhoto(ctx context.Context, groupID string, in *MediaInput) error {
	ref := &MediaRef{}
	if in != nil {
		r, err := c.uploadMedia(ctx, *in)
		if err != nil {
			return err
		}
		ref = r
	}
	return c.sendMeta(ctx, groupID, &groupMeta{Avatar: ref})
}

// InviteLink creates (or with reset, replaces) the group's invite link.
// serverURL is the base of the link (the server this account uses).
func (c *Client) InviteLink(ctx context.Context, groupID string, reset bool) (string, error) {
	g, err := c.st.Group(ctx, groupID)
	if err != nil {
		return "", err
	}
	secret := g.Invite
	if secret == "" || reset {
		b := make([]byte, 16)
		if _, err := rand.Read(b); err != nil {
			return "", err
		}
		secret = hex.EncodeToString(b)
		sum := sha256.Sum256([]byte(secret))
		if err := c.refreshToken(ctx); err != nil {
			return "", err
		}
		if _, err := c.del.SetGroupInvite(ctx, &deliveryv1.SetGroupInviteRequest{GroupId: groupID, SecretHash: sum[:]}); err != nil {
			return "", err
		}
		if err := c.sendMeta(ctx, groupID, &groupMeta{Invite: &secret}); err != nil {
			return "", err
		}
	}
	return c.opts.ServerURL + "/join/" + url.PathEscape(groupID) + "#" + secret, nil
}

// RevokeInviteLink turns the group's invite link off.
func (c *Client) RevokeInviteLink(ctx context.Context, groupID string) error {
	if err := c.refreshToken(ctx); err != nil {
		return err
	}
	if _, err := c.del.SetGroupInvite(ctx, &deliveryv1.SetGroupInviteRequest{GroupId: groupID}); err != nil {
		return err
	}
	empty := ""
	return c.sendMeta(ctx, groupID, &groupMeta{Invite: &empty})
}

// ParseInviteLink extracts the group id and secret of an invite link
// ("https://<server>/join/<group>#<secret>").
func ParseInviteLink(link string) (groupID, secret string, err error) {
	u, err := url.Parse(strings.TrimSpace(link))
	if err != nil {
		return "", "", ErrBadInvite
	}
	rest, ok := strings.CutPrefix(u.Path, "/join/")
	if !ok || rest == "" || u.Fragment == "" || len(u.Fragment) > 128 {
		return "", "", ErrBadInvite
	}
	if GroupKind(rest) != KindGroup {
		return "", "", ErrBadInvite
	}
	return rest, u.Fragment, nil
}

// JoinByLink asks the group's members to add this device (and so this
// account): the first member online does. The group shows up when added.
func (c *Client) JoinByLink(ctx context.Context, link string) (string, error) {
	gid, secret, err := ParseInviteLink(link)
	if err != nil {
		return "", err
	}
	if err := c.refreshToken(ctx); err != nil {
		return "", err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.requireLogin(); err != nil {
		return "", err
	}
	if _, _, err := c.m.GroupInfo([]byte(gid)); err == nil {
		return gid, nil // already a member
	}
	kp, err := c.m.GenerateKeyPackage()
	if err != nil {
		return "", err
	}
	if err := c.persistLocked(ctx, nil); err != nil {
		return "", err
	}
	if _, err := c.del.RequestJoin(ctx, &deliveryv1.RequestJoinRequest{GroupId: gid, KeyPackage: kp, InviteSecret: secret}); err != nil {
		return "", err
	}
	return gid, nil
}

// inviteJoinPayload splits an invite-link join request ("KINV1" + secret +
// "\n" + KeyPackage, assembled by the delivery service).
func inviteJoinPayload(p []byte) (secret string, kp []byte, ok bool) {
	rest, found := strings.CutPrefix(string(p), "KINV1")
	if !found {
		return "", nil, false
	}
	i := strings.IndexByte(rest, '\n')
	if i <= 0 {
		return "", nil, false
	}
	return rest[:i], []byte(rest[i+1:]), true
}
