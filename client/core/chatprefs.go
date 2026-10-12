package core

// Per-account chat settings, like Telegram: chats pinned to the top of the
// list (at most MaxPinnedChats) and muted chats. They are kept as "cpref"
// items in the account's self-group, so every device of the account has the
// same settings and the history backup carries them; the newest item of a
// chat wins and older ones are deleted.

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/kantracity/kantrae2e/client/store"
)

const (
	MaxPinnedChats = 5
	// MuteForever: "muted until" for chats muted with no end (year 9999;
	// fits a JavaScript number).
	MuteForever int64 = 253402300799000
)

var ErrTooManyPinned = errors.New("at most 5 chats can be pinned")

// ChatPref: the settings of one chat (unix ms; 0 = off).
type ChatPref struct {
	Pinned     int64 `json:"pin,omitempty"`  // pinned at
	MutedUntil int64 `json:"mute,omitempty"` // notifications off until
	uid        string
	at         int64
}

// Muted reports whether the chat's notifications are off now.
func (p ChatPref) Muted() bool { return p.MutedUntil > time.Now().UnixMilli() }

// ChatPrefs returns the settings of all chats that have any.
func (c *Client) ChatPrefs(ctx context.Context) (map[string]ChatPref, error) {
	all, err := c.chatPrefItems(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]ChatPref, len(all))
	for g, items := range all {
		out[g] = items[len(items)-1]
	}
	return out, nil
}

// chatPrefItems: the cpref items of every chat, oldest first.
func (c *Client) chatPrefItems(ctx context.Context) (map[string][]ChatPref, error) {
	self := c.SelfGroupID()
	if self == "" {
		return nil, ErrNotLoggedIn
	}
	ms, err := c.st.MessagesOfKind(ctx, self, "cpref", 5000)
	if err != nil {
		return nil, err
	}
	out := map[string][]ChatPref{}
	for _, m := range ms {
		var p struct {
			ChatPref
			G string `json:"g"`
		}
		if json.Unmarshal([]byte(m.Body), &p) != nil || p.G == "" {
			continue
		}
		p.ChatPref.uid, p.ChatPref.at = m.UID, m.SentAt
		out[p.G] = append(out[p.G], p.ChatPref)
	}
	return out, nil
}

// PinChat pins a chat to the top of the list (or unpins it).
func (c *Client) PinChat(ctx context.Context, groupID string, pin bool) error {
	return c.setChatPref(ctx, groupID, func(p *ChatPref, all map[string]ChatPref) error {
		if !pin {
			p.Pinned = 0
			return nil
		}
		if p.Pinned > 0 {
			return nil
		}
		n := 0
		for g, o := range all {
			if g != groupID && o.Pinned > 0 {
				n++
			}
		}
		if n >= MaxPinnedChats {
			return ErrTooManyPinned
		}
		p.Pinned = time.Now().UnixMilli()
		return nil
	})
}

// MuteChat turns a chat's notifications off until a time (unix ms;
// MuteForever; 0 turns them back on).
func (c *Client) MuteChat(ctx context.Context, groupID string, until int64) error {
	return c.setChatPref(ctx, groupID, func(p *ChatPref, _ map[string]ChatPref) error {
		p.MutedUntil = until
		return nil
	})
}

func (c *Client) setChatPref(ctx context.Context, groupID string, change func(*ChatPref, map[string]ChatPref) error) error {
	if GroupKind(groupID) == KindSelf || groupID == "" {
		return ErrNoMessage
	}
	items, err := c.chatPrefItems(ctx)
	if err != nil {
		return err
	}
	cur := map[string]ChatPref{}
	for g, l := range items {
		cur[g] = l[len(l)-1]
	}
	p := cur[groupID]
	if err := change(&p, cur); err != nil {
		return err
	}
	body, _ := json.Marshal(struct {
		G string `json:"g"`
		ChatPref
	}{groupID, p})
	if err := c.refreshToken(ctx); err != nil {
		return err
	}
	c.mu.Lock()
	err = c.requireLogin()
	if err == nil {
		_, err = c.sendLocked(ctx, selfGroupID(c.acct.UserID), payload{Type: "cpref", Body: string(body)})
	}
	c.mu.Unlock()
	if err != nil {
		return err
	}
	// The older items of this chat are not needed any more.
	for _, old := range items[groupID] {
		_ = c.DeleteMessage(ctx, c.SelfGroupID(), old.uid, true)
	}
	return nil
}

// ---- system messages in groups ---------------------------------------------

// SysEvent is the body of a "sys" message: what happened in a group. The
// one who did it is the message's (authenticated) sender.
type SysEvent struct {
	Action string   `json:"a"`           // create, add, join (by link), leave, remove, rename, photo
	Users  []string `json:"u,omitempty"` // add, join, remove: user ids
	Name   string   `json:"n,omitempty"` // create, rename: the group's name
}

// ParseSys reads the event of a "sys" message.
func ParseSys(m *store.Message) *SysEvent {
	if m.Kind != "sys" {
		return nil
	}
	var e SysEvent
	if json.Unmarshal([]byte(m.Body), &e) != nil || e.Action == "" {
		return nil
	}
	return &e
}

// sysLocked tells the group's members what happened (best effort).
func (c *Client) sysLocked(ctx context.Context, groupID string, e SysEvent) {
	if GroupKind(groupID) != KindGroup {
		return
	}
	b, _ := json.Marshal(e)
	if _, err := c.sendLocked(ctx, groupID, payload{Type: "sys", Body: string(b)}); err != nil {
		c.logf("system message in %s: %v", groupID, err)
	}
}

// sysLocal stores a system message on this device only.
func (c *Client) sysLocal(groupID, id, sender string, at int64, e SysEvent) func(*store.Tx) error {
	b, _ := json.Marshal(e)
	return func(tx *store.Tx) error {
		return tx.InsertMessage(store.Message{UID: id, GroupID: groupID, SenderUser: sender, Kind: "sys", Body: string(b),
			SentAt: at, Outgoing: sender == c.userID()})
	}
}
