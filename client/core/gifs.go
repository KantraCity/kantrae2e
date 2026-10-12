package core

// Saved GIFs, like Telegram's. They are media messages in the account's
// self-group (which only this account's devices are in): every device gets
// them live, the history backup carries them to new devices, and saving or
// sending one never uploads the file again (the encrypted reference is
// re-sent). Removing one is a deletion in the self-group.

import (
	"context"
	"errors"

	"github.com/kantracity/kantrae2e/client/store"
)

// MaxSavedGIFs: the oldest ones drop out of the panel beyond this.
const MaxSavedGIFs = 200

var ErrNotGIF = errors.New("not a GIF")

// IsGIF reports whether a media reference is an animation.
func IsGIF(ref *MediaRef) bool { return ref != nil && ref.Kind == "gif" }

// SelfGroupID is the id of the account's own group (saved GIFs live there).
func (c *Client) SelfGroupID() string {
	if id := c.userID(); id != "" {
		return selfGroupID(id)
	}
	return ""
}

// SavedGIFs returns the saved GIFs, newest first.
func (c *Client) SavedGIFs(ctx context.Context) ([]store.Message, error) {
	self := c.SelfGroupID()
	if self == "" {
		return nil, ErrNotLoggedIn
	}
	ms, err := c.st.Messages(ctx, self, MaxSavedGIFs*2)
	if err != nil {
		return nil, err
	}
	var out []store.Message
	seen := map[string]bool{}
	for i := len(ms) - 1; i >= 0 && len(out) < MaxSavedGIFs; i-- {
		ref, err := ParseMedia(&ms[i])
		if err != nil || !IsGIF(ref) || seen[ref.Hash] {
			continue
		}
		seen[ref.Hash] = true
		out = append(out, ms[i])
	}
	return out, nil
}

// SaveGIF adds the GIF of a message to the saved GIFs (already saved: it
// moves to the front).
func (c *Client) SaveGIF(ctx context.Context, groupID, uid string) (*store.Message, error) {
	m, err := c.ownMessage(ctx, groupID, uid)
	if err != nil {
		return nil, err
	}
	ref, err := ParseMedia(m)
	if err != nil || !IsGIF(ref) {
		return nil, ErrNotGIF
	}
	self := c.SelfGroupID()
	saved, err := c.SavedGIFs(ctx)
	if err != nil {
		return nil, err
	}
	for _, s := range saved {
		if r, _ := ParseMedia(&s); r != nil && r.Hash == ref.Hash {
			_ = c.DeleteMessage(ctx, self, s.UID, true) // re-saved below, as the newest
		}
	}
	keep := *ref
	keep.Caption = ""
	return c.sendMediaRef(ctx, self, &keep, SendOptions{})
}

// RemoveSavedGIF removes a GIF from the saved ones (on every device).
func (c *Client) RemoveSavedGIF(ctx context.Context, uid string) error {
	return c.DeleteMessage(ctx, c.SelfGroupID(), uid, true)
}

// SendSavedGIF sends a saved GIF to a chat.
func (c *Client) SendSavedGIF(ctx context.Context, groupID, uid string, opt SendOptions) (*store.Message, error) {
	m, err := c.ownMessage(ctx, c.SelfGroupID(), uid)
	if err != nil {
		return nil, err
	}
	ref, err := ParseMedia(m)
	if err != nil || !IsGIF(ref) {
		return nil, ErrNotGIF
	}
	return c.sendMediaRef(ctx, groupID, ref, opt)
}

func (c *Client) sendMediaRef(ctx context.Context, groupID string, ref *MediaRef, opt SendOptions) (*store.Message, error) {
	if err := c.refreshToken(ctx); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.requireLogin(); err != nil {
		return nil, err
	}
	return c.sendLocked(ctx, groupID, payload{Type: "media", ReplyTo: opt.ReplyTo, Media: ref})
}
