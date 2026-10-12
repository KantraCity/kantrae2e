package main

import (
	"context"

	"github.com/kantracity/kantrae2e/client/core"
)

// ---- saved GIFs (core/gifs.go) ---------------------------------------------------

// SavedGIFs returns the saved GIFs, newest first. Their file URLs work like
// any media URL.
func (m *Messenger) SavedGIFs() ([]Message, error) {
	c, err := m.loggedIn()
	if err != nil {
		return nil, err
	}
	ms, err := c.SavedGIFs(context.Background())
	if err != nil {
		return nil, friendly(err)
	}
	out := make([]Message, 0, len(ms))
	for i := range ms {
		out = append(out, *m.toMessage(c, &ms[i]))
	}
	return out, nil
}

// SaveGIF adds the GIF of a message to the saved GIFs.
func (m *Messenger) SaveGIF(chatID, id string) error {
	c, err := m.loggedIn()
	if err != nil {
		return err
	}
	_, err = c.SaveGIF(context.Background(), chatID, id)
	return friendly(err)
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
