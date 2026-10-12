package core

// Saved GIFs and stickers (no packs: whatever you liked, you keep). They
// are media messages in the account's self-group (which only this
// account's devices are in): every device gets them live, the history
// backup carries them to new devices, and saving or sending one never
// uploads the file again (the encrypted reference is re-sent). Removing
// one is a deletion in the self-group. A file of your own can be added
// directly (AddSaved).

import (
	"context"
	"errors"

	"github.com/kantracity/kantrae2e/client/store"
)

// MaxSavedGIFs: the oldest ones drop out of the panel beyond this.
const MaxSavedGIFs = 200

var ErrNotGIF = errors.New("not a GIF or sticker")

// IsGIF reports whether a media reference is an animation.
func IsGIF(ref *MediaRef) bool { return ref != nil && ref.Kind == "gif" }

// Savable kinds of media (the emoji panel's GIF and sticker tabs).
func savable(ref *MediaRef) bool { return ref != nil && (ref.Kind == "gif" || ref.Kind == "sticker") }

// SelfGroupID is the id of the account's own group (saved GIFs live there).
func (c *Client) SelfGroupID() string {
	if id := c.userID(); id != "" {
		return selfGroupID(id)
	}
	return ""
}

// SavedGIFs returns the saved GIFs, newest first.
func (c *Client) SavedGIFs(ctx context.Context) ([]store.Message, error) { return c.Saved(ctx, "gif") }

// Saved returns the saved media of a kind ("gif" or "sticker"), newest first.
func (c *Client) Saved(ctx context.Context, kind string) ([]store.Message, error) {
	self := c.SelfGroupID()
	if self == "" {
		return nil, ErrNotLoggedIn
	}
	ms, err := c.st.MessagesOfKind(ctx, self, "media", MaxSavedGIFs*2)
	if err != nil {
		return nil, err
	}
	var out []store.Message
	seen := map[string]bool{}
	for i := len(ms) - 1; i >= 0 && len(out) < MaxSavedGIFs; i-- {
		ref, err := ParseMedia(&ms[i])
		if err != nil || ref.Kind != kind || seen[ref.Hash] {
			continue
		}
		seen[ref.Hash] = true
		out = append(out, ms[i])
	}
	return out, nil
}

// SaveGIF adds the GIF or sticker of a message to the saved ones (already
// saved: it moves to the front).
func (c *Client) SaveGIF(ctx context.Context, groupID, uid string) (*store.Message, error) {
	return c.SaveAs(ctx, groupID, uid, "")
}

// SaveAs saves the media of a message to the saved GIFs or stickers. kind ""
// keeps the message's kind (a GIF or a sticker); "sticker" also takes a
// photo, "gif" a short video. The file is not uploaded again.
func (c *Client) SaveAs(ctx context.Context, groupID, uid, kind string) (*store.Message, error) {
	m, err := c.ownMessage(ctx, groupID, uid)
	if err != nil {
		return nil, err
	}
	ref, err := ParseMedia(m)
	if err != nil {
		return nil, ErrNotGIF
	}
	if kind == "" {
		kind = ref.Kind
	}
	ok := ref.Kind == kind && savable(ref) ||
		kind == "sticker" && ref.Kind == "photo" ||
		kind == "gif" && ref.Kind == "video" && ref.Duration <= 60 && ref.Size <= 20<<20
	if !ok {
		return nil, ErrNotGIF
	}
	self := c.SelfGroupID()
	saved, err := c.Saved(ctx, kind)
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
	keep.Kind = kind
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
	if err != nil || !savable(ref) {
		return nil, ErrNotGIF
	}
	return c.sendMediaRef(ctx, groupID, ref, opt)
}

// AddSaved uploads a file of one's own straight into the saved GIFs or
// stickers (in.Kind "gif" or "sticker").
func (c *Client) AddSaved(ctx context.Context, in MediaInput) (*store.Message, error) {
	if in.Kind != "gif" && in.Kind != "sticker" {
		return nil, ErrNotGIF
	}
	in.Caption = ""
	return c.SendMedia(ctx, c.SelfGroupID(), in, SendOptions{})
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
	return c.sendLocked(ctx, groupID, payload{Type: "media", ReplyTo: opt.ReplyTo, Media: ref, Extra: opt.extra()})
}
