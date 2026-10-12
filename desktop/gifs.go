package main

import (
	"context"

	"github.com/kantracity/kantrae2e/client/core"
)

// ---- saved GIFs (core/gifs.go) ---------------------------------------------------

// SavedGIFs returns the saved GIFs, newest first. Their file URLs work like
// any media URL.
func (m *Messenger) SavedGIFs() ([]Message, error) { return m.Saved("gif") }

// Saved returns the saved GIFs ("gif") or stickers ("sticker"), newest first.
func (m *Messenger) Saved(kind string) ([]Message, error) {
	c, err := m.loggedIn()
	if err != nil {
		return nil, err
	}
	ms, err := c.Saved(context.Background(), kind)
	if err != nil {
		return nil, friendly(err)
	}
	out := make([]Message, 0, len(ms))
	for i := range ms {
		out = append(out, *m.toMessage(c, &ms[i]))
	}
	return out, nil
}

// SaveGIF adds the GIF or sticker of a message to the saved ones.
func (m *Messenger) SaveGIF(chatID, id string) error {
	c, err := m.loggedIn()
	if err != nil {
		return err
	}
	_, err = c.SaveGIF(context.Background(), chatID, id)
	return friendly(err)
}

// SaveAs saves the media of a message as a GIF or sticker (a photo as a
// sticker, a short video as a GIF; kind "" keeps its kind).
func (m *Messenger) SaveAs(chatID, id, kind string) error {
	c, err := m.loggedIn()
	if err != nil {
		return err
	}
	_, err = c.SaveAs(context.Background(), chatID, id, kind)
	return friendly(err)
}

// AddSaved adds a file of one's own to the saved GIFs / stickers
// (up.Kind "gif" or "sticker").
func (m *Messenger) AddSaved(up MediaUpload) (*Message, error) {
	c, err := m.loggedIn()
	if err != nil {
		return nil, err
	}
	in, err := mediaInput(up)
	if err != nil {
		return nil, err
	}
	s, err := c.AddSaved(context.Background(), in)
	if err != nil {
		return nil, friendly(err)
	}
	return m.toMessage(c, s), nil
}

// RemoveSavedGIF removes a saved GIF (id from SavedGIFs).
func (m *Messenger) RemoveSavedGIF(id string) error {
	c, err := m.loggedIn()
	if err != nil {
		return err
	}
	return friendly(c.RemoveSavedGIF(context.Background(), id))
}

// SendSavedGIF sends a saved GIF to a chat.
func (m *Messenger) SendSavedGIF(chatID, id, replyTo string) (*Message, error) {
	c, err := m.loggedIn()
	if err != nil {
		return nil, err
	}
	s, err := c.SendSavedGIF(context.Background(), chatID, id, core.SendOptions{ReplyTo: replyTo})
	if err != nil {
		return nil, friendly(err)
	}
	return m.lookup(c, chatID, s.UID, s), nil
}
