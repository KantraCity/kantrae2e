package core

// Telegram-like message features. Everything except "typing" travels as an
// MLS application message, so the server sees none of it:
//   - replies and forwards are fields of text/media messages;
//   - edit, delete (for everyone), reaction and read receipts are their own
//     payload types referring to a message by its uid;
//   - "typing" is an ephemeral server signal (delivery.Typing): not
//     encrypted, never stored — it only says "someone in this group is
//     typing", which the server sees from the traffic anyway.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/kantracity/kantrae2e/client/store"
	deliveryv1 "github.com/kantracity/kantrae2e/gen/kantra/delivery/v1"
)

// SendOptions for text and media messages.
type SendOptions struct {
	ReplyTo string       // uid of the message answered
	Silent  bool         // no notification for the others
	Link    *LinkPreview // preview of the first link (text messages)
}

func (o SendOptions) extra() *Extra {
	if !o.Silent && o.Link == nil {
		return nil
	}
	return &Extra{Silent: o.Silent, Link: o.Link}
}

var (
	ErrNotYours    = errors.New("only the author can do this with a message")
	ErrNoMessage   = errors.New("message not found")
	ErrNotEditable = errors.New("only text messages can be edited")
)

// Events of this file.
const (
	// EventMessageUpdated: edited, deleted or reacted (GroupID, Detail = uid).
	EventMessageUpdated EventType = "message_updated"
	// EventRead: another member read the group up to a time (GroupID).
	EventRead EventType = "read"
	// EventTyping: UserID is typing in GroupID.
	EventTyping EventType = "typing"
)

func (c *Client) ownMessage(ctx context.Context, groupID, uid string) (*store.Message, error) {
	m, err := c.st.Message(ctx, groupID, uid)
	if errors.Is(err, store.ErrNotFound) || (err == nil && m.Deleted) {
		return nil, ErrNoMessage
	}
	return m, err
}

// sendControl sends a non-storable payload and applies it locally.
func (c *Client) sendControl(ctx context.Context, groupID string, p payload, local func(*store.Tx) error) error {
	if err := c.refreshToken(ctx); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.requireLogin(); err != nil {
		return err
	}
	if _, err := c.sendLocked(ctx, groupID, p); err != nil {
		return err
	}
	if local == nil {
		return nil
	}
	if err := c.st.Update(ctx, local); err != nil {
		return err
	}
	c.scheduleBackup()
	return nil
}

// EditMessage changes the text of one's own text message.
func (c *Client) EditMessage(ctx context.Context, groupID, uid, text string) error {
	if strings.TrimSpace(text) == "" {
		return errors.New("empty message")
	}
	m, err := c.ownMessage(ctx, groupID, uid)
	if err != nil {
		return err
	}
	a := c.Account()
	if a == nil || m.SenderUser != a.UserID {
		return ErrNotYours
	}
	if m.Kind != "text" {
		return ErrNotEditable
	}
	at := time.Now().UnixMilli()
	return c.sendControl(ctx, groupID, payload{Type: "edit", Target: uid, Body: text}, func(tx *store.Tx) error {
		_, err := tx.EditMessage(groupID, uid, a.UserID, text, at)
		return err
	})
}

// canDeleteForAll: one's own messages anywhere; any message of a direct chat
// (as in Telegram's private chats).
func canDeleteForAll(groupID, deleter, author string) bool {
	return deleter == author || GroupKind(groupID) == KindDirect
}

// DeleteMessage deletes a message on this device, or for everyone.
func (c *Client) DeleteMessage(ctx context.Context, groupID, uid string, forAll bool) error {
	m, err := c.ownMessage(ctx, groupID, uid)
	if err != nil {
		return err
	}
	// Never sent: it only exists here.
	if m.Pending != 0 {
		_, err := c.cancelUnsent(ctx, groupID, uid)
		return err
	}
	if !forAll {
		return c.st.Update(ctx, func(tx *store.Tx) error {
			_, err := tx.DeleteMessage(groupID, uid, "")
			return err
		})
	}
	a := c.Account()
	if a == nil || !canDeleteForAll(groupID, a.UserID, m.SenderUser) {
		return ErrNotYours
	}
	return c.sendControl(ctx, groupID, payload{Type: "delete", Target: uid}, func(tx *store.Tx) error {
		_, err := tx.DeleteMessage(groupID, uid, "")
		return err
	})
}

// React sets this user's reaction to a message ("" removes it).
func (c *Client) React(ctx context.Context, groupID, uid, emoji string) error {
	if _, err := c.ownMessage(ctx, groupID, uid); err != nil {
		return err
	}
	if len(emoji) > 32 {
		return errors.New("bad reaction")
	}
	a := c.Account()
	if a == nil {
		return ErrNotLoggedIn
	}
	return c.sendControl(ctx, groupID, payload{Type: "reaction", Target: uid, Emoji: emoji}, func(tx *store.Tx) error {
		return tx.SetReaction(groupID, uid, a.UserID, emoji)
	})
}

// Forward sends a copy of a message to another chat ("Forwarded from …").
// Media is not uploaded again: the reference (with its key) is re-sent.
func (c *Client) Forward(ctx context.Context, fromGroup, uid, toGroup string) (*store.Message, error) {
	m, err := c.ownMessage(ctx, fromGroup, uid)
	if err != nil {
		return nil, err
	}
	from := m.FwdFrom
	if from == "" {
		from = DisplayName(c.CachedProfile(ctx, m.SenderUser))
	}
	p := payload{Type: m.Kind, Fwd: from}
	if m.Kind == "media" {
		ref, err := ParseMedia(m)
		if err != nil {
			return nil, err
		}
		p.Media = ref
	} else {
		p.Body = m.Body
	}
	if err := c.refreshForSend(ctx); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.requireLogin(); err != nil {
		return nil, err
	}
	return c.sendLocked(ctx, toGroup, p)
}

// readSent remembers the last read receipt sent per group (this process).
var readSent sync.Map // groupID -> int64

// MarkRead tells the other members that this user has read the group up to
// its newest message from someone else (one receipt per new message).
func (c *Client) MarkRead(ctx context.Context, groupID string) error {
	a := c.Account()
	if a == nil {
		return ErrNotLoggedIn
	}
	ms, err := c.st.Messages(ctx, groupID, 30)
	if err != nil {
		return err
	}
	var upto int64
	for _, m := range ms {
		if m.SenderUser != a.UserID && m.SentAt > upto && m.Origin == "" {
			upto = m.SentAt
		}
	}
	if upto == 0 {
		return nil
	}
	key := a.UserID + "/" + groupID
	if prev, ok := readSent.Load(key); ok && prev.(int64) >= upto {
		return nil
	}
	marks, _ := c.st.ReadMarks(ctx, groupID)
	if marks[a.UserID] >= upto {
		readSent.Store(key, upto)
		return nil
	}
	err = c.sendControl(ctx, groupID, payload{Type: "read", UpTo: upto}, func(tx *store.Tx) error {
		return tx.SetReadMark(groupID, a.UserID, upto)
	})
	if err == nil {
		readSent.Store(key, upto)
	}
	return err
}

// Typing signals "typing…" to the other members (best effort; call at most
// every few seconds while the user types).
func (c *Client) Typing(ctx context.Context, groupID string) error {
	if err := c.refreshToken(ctx); err != nil {
		return err
	}
	_, err := c.del.Typing(ctx, &deliveryv1.TypingRequest{GroupId: groupID})
	return err
}

// Reactions returns the reactions in a group: message uid -> user -> emoji.
func (c *Client) Reactions(ctx context.Context, groupID string) (map[string]map[string]string, error) {
	rs, err := c.st.Reactions(ctx, groupID)
	if err != nil {
		return nil, err
	}
	out := map[string]map[string]string{}
	for _, r := range rs {
		if out[r.MsgUID] == nil {
			out[r.MsgUID] = map[string]string{}
		}
		out[r.MsgUID][r.UserID] = r.Emoji
	}
	return out, nil
}

// ReadMarks returns up to when each member has read the group (unix ms).
func (c *Client) ReadMarks(ctx context.Context, groupID string) (map[string]int64, error) {
	return c.st.ReadMarks(ctx, groupID)
}

// Message returns one message.
func (c *Client) Message(ctx context.Context, groupID, uid string) (*store.Message, error) {
	return c.st.Message(ctx, groupID, uid)
}

// applyControlLocked handles edit/delete/reaction/read from another member
// (or another device of ours). It returns the writes and the event.
func (c *Client) applyControlLocked(groupID, senderUser string, pl *payload) (func(*store.Tx) error, *Event) {
	ev := &Event{Type: EventMessageUpdated, GroupID: groupID, Detail: pl.Target}
	switch pl.Type {
	case "edit":
		return func(tx *store.Tx) error {
			_, err := tx.EditMessage(groupID, pl.Target, senderUser, pl.Body, pl.TS)
			return err
		}, ev
	case "delete":
		// Own messages anywhere, any message in a direct chat: enforced
		// in the UPDATE itself (author "" = any author).
		author := senderUser
		if GroupKind(groupID) == KindDirect {
			author = ""
		}
		return func(tx *store.Tx) error {
			_, err := tx.DeleteMessage(groupID, pl.Target, author)
			return err
		}, ev
	case "reaction":
		return func(tx *store.Tx) error { return tx.SetReaction(groupID, pl.Target, senderUser, pl.Emoji) }, ev
	case "pin":
		at := pl.TS
		if pl.Unpin {
			at = 0
		}
		return func(tx *store.Tx) error { return tx.SetPinned(groupID, pl.Target, at) }, ev
	case "vote":
		opts, _ := json.Marshal(pl.Options)
		return func(tx *store.Tx) error { return tx.SetVote(groupID, pl.Target, senderUser, string(opts)) }, ev
	case "read":
		// From another device of ours, it means "read here too" (UserID).
		return func(tx *store.Tx) error { return tx.SetReadMark(groupID, senderUser, pl.UpTo) },
			&Event{Type: EventRead, GroupID: groupID, UserID: senderUser, Count: int(pl.UpTo / 1000)}
	}
	return nil, nil
}
