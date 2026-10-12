package core

// Deleting chats, like in Telegram:
//   - ClearHistory empties a chat (for this account, or in a direct chat for
//     both people);
//   - DeleteChat empties it and removes it from the list until a new
//     message arrives (for both people: the other person's copy too);
//   - LeaveGroup deletes a group and leaves it: the other members' apps
//     remove this account's devices from the MLS group.
// Every device of the account applies the same (a "clear"/"leave" payload in
// the chat itself, which all own devices are members of), and the history
// backup carries the deletions (message tombstones, chat flags).

import (
	"context"
	"errors"
	"time"

	"github.com/kantracity/kantrae2e/client/store"
)

// EventChatCleared: a chat was emptied, hidden or left (GroupID).
const EventChatCleared EventType = "chat_cleared"

var ErrNotGroup = errors.New("only a group can be left")

// ChatHidden reports whether a chat is out of the chat list: left, or
// deleted and no message has arrived since (last = newest message time).
func ChatHidden(g store.Group, last int64) bool {
	return g.LeftAt > 0 || (g.HiddenAt > 0 && last <= g.HiddenAt)
}

// ClearHistory deletes all messages of a chat. forAll (direct chats only)
// deletes them for the other person too.
func (c *Client) ClearHistory(ctx context.Context, groupID string, forAll bool) error {
	return c.clearChat(ctx, groupID, forAll, false)
}

// DeleteChat deletes a chat's messages and removes it from the chat list.
// forAll (direct chats only) deletes it for the other person too.
func (c *Client) DeleteChat(ctx context.Context, groupID string, forAll bool) error {
	return c.clearChat(ctx, groupID, forAll, true)
}

func (c *Client) clearChat(ctx context.Context, groupID string, forAll, hide bool) error {
	if GroupKind(groupID) == KindSelf {
		return ErrNoMessage
	}
	if GroupKind(groupID) != KindDirect {
		forAll = false
	}
	at := time.Now().UnixMilli()
	local := func(tx *store.Tx) error {
		if _, err := tx.ClearMessages(groupID, at); err != nil {
			return err
		}
		if hide {
			return tx.SetChatFlags(groupID, at, 0)
		}
		return tx.ResetChatFlags(groupID, true, false) // emptied, but stays in the list
	}
	g, err := c.st.Group(ctx, groupID)
	if err != nil {
		return err
	}
	if !g.Active || g.LeftAt > 0 {
		// Read-only chat (not a member here): only this device.
		if err := c.st.Update(ctx, local); err != nil {
			return err
		}
		c.scheduleBackup()
		return nil
	}
	return c.sendControl(ctx, groupID, payload{Type: "clear", UpTo: at, All: forAll, Hide: hide}, local)
}

// LeaveGroup deletes a group and leaves it.
func (c *Client) LeaveGroup(ctx context.Context, groupID string) error {
	if GroupKind(groupID) != KindGroup {
		return ErrNotGroup
	}
	at := time.Now().UnixMilli()
	local := func(tx *store.Tx) error {
		if _, err := tx.ClearMessages(groupID, at); err != nil {
			return err
		}
		return tx.SetChatFlags(groupID, at, at)
	}
	g, err := c.st.Group(ctx, groupID)
	if err != nil {
		return err
	}
	if !g.Active {
		if err := c.st.Update(ctx, local); err != nil {
			return err
		}
		c.scheduleBackup()
		return nil
	}
	return c.sendControl(ctx, groupID, payload{Type: "leave", UpTo: at}, local)
}

// UnhideChat puts a deleted chat back into the list (the user opened it).
func (c *Client) UnhideChat(ctx context.Context, groupID string) error {
	return c.st.Update(ctx, func(tx *store.Tx) error { return tx.ResetChatFlags(groupID, true, false) })
}

// applyChatControlLocked handles "clear" and "leave" from another device of
// ours or another member. Caller holds c.mu.
func (c *Client) applyChatControlLocked(groupID, senderUser string, pl *payload) (func(*store.Tx) error, *Event) {
	ev := &Event{Type: EventChatCleared, GroupID: groupID}
	mine := senderUser == c.userID()
	switch pl.Type {
	case "clear":
		if !mine && !(pl.All && GroupKind(groupID) == KindDirect) {
			return func(*store.Tx) error { return nil }, &Event{Type: EventGroupUpdated, GroupID: groupID}
		}
		return func(tx *store.Tx) error {
			if _, err := tx.ClearMessages(groupID, pl.UpTo); err != nil {
				return err
			}
			if pl.Hide {
				return tx.SetChatFlags(groupID, pl.UpTo, 0)
			}
			if mine {
				return tx.ResetChatFlags(groupID, true, false)
			}
			return nil
		}, ev
	case "leave":
		if mine {
			return func(tx *store.Tx) error {
				if _, err := tx.ClearMessages(groupID, pl.UpTo); err != nil {
					return err
				}
				return tx.SetChatFlags(groupID, pl.UpTo, pl.UpTo)
			}, ev
		}
		// Someone left: remove their devices (any member may; the first
		// commit wins, the others find nobody left to remove).
		if GroupKind(groupID) == KindGroup && c.userID() != "" {
			c.deferLocked(func(ctx context.Context) { c.removeLeaver(ctx, groupID, senderUser) })
		}
		return func(*store.Tx) error { return nil }, &Event{Type: EventGroupUpdated, GroupID: groupID}
	}
	return nil, nil
}

func (c *Client) removeLeaver(ctx context.Context, groupID, uid string) {
	if err := c.refreshToken(ctx); err != nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.requireLogin() != nil {
		return
	}
	if g, err := c.st.Group(ctx, groupID); err != nil || g.LeftAt > 0 || !g.Active {
		return // we are not in it any more ourselves
	}
	if err := c.syncLocked(ctx); err != nil {
		c.logf("remove leaver: %v", err)
		return
	}
	ids, devices, err := c.devicesOfLocked(groupID, uid)
	if err != nil || len(ids) == 0 {
		return
	}
	still := func() (bool, error) {
		now, _, err := c.devicesOfLocked(groupID, uid)
		return len(now) > 0, err
	}
	if err := c.commitLocked(ctx, groupID, commitSpec{removeIDs: ids, removedDevices: devices, still: still}); err != nil {
		c.logf("remove leaver from %s: %v", groupID, err)
	}
}
