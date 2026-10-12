package main

import (
	"context"
	"errors"
	"time"

	"github.com/kantracity/kantrae2e/client/core"
)

// ---- deleting chats (core/chats.go) ---------------------------------------------

// ClearHistory deletes all messages of a chat; forAll (direct chats) also
// for the other person.
func (m *Messenger) ClearHistory(chatID string, forAll bool) error {
	c, err := m.loggedIn()
	if err != nil {
		return err
	}
	return friendly(c.ClearHistory(context.Background(), chatID, forAll))
}

// DeleteChat deletes a chat (removes it from the list until a new message
// arrives); forAll (direct chats) deletes it for the other person too.
func (m *Messenger) DeleteChat(chatID string, forAll bool) error {
	c, err := m.loggedIn()
	if err != nil {
		return err
	}
	if err := c.DeleteChat(context.Background(), chatID, forAll); err != nil {
		return friendly(err)
	}
	m.forgetChat(chatID)
	return nil
}

// LeaveGroup deletes a group and leaves it.
func (m *Messenger) LeaveGroup(chatID string) error {
	c, err := m.loggedIn()
	if err != nil {
		return err
	}
	if err := c.LeaveGroup(context.Background(), chatID); err != nil {
		return friendly(err)
	}
	m.forgetChat(chatID)
	return nil
}

// forgetChat drops the unread counter and notification of a deleted chat.
func (m *Messenger) forgetChat(chatID string) {
	m.mu.Lock()
	s := m.active
	if s != nil {
		delete(s.unread, chatID)
		if m.activeChat == chatID {
			m.activeChat = ""
		}
	}
	m.mu.Unlock()
	if s != nil {
		m.notif.Clear(noticeKey(s.file, chatID))
	}
}

// PinChat pins a chat to the top of the list (at most 5), on all devices.
func (m *Messenger) PinChat(chatID string, pin bool) error {
	c, err := m.loggedIn()
	if err != nil {
		return err
	}
	if err := c.PinChat(context.Background(), chatID, pin); errors.Is(err, core.ErrTooManyPinned) {
		return errors.New("Можно закрепить не больше 5 чатов")
	} else if err != nil {
		return friendly(err)
	}
	return nil
}

// MuteChat turns notifications of a chat off for minutes (-1: forever,
// 0: back on).
func (m *Messenger) MuteChat(chatID string, minutes int) error {
	c, err := m.loggedIn()
	if err != nil {
		return err
	}
	var until int64
	switch {
	case minutes < 0:
		until = core.MuteForever
	case minutes > 0:
		until = time.Now().Add(time.Duration(minutes) * time.Minute).UnixMilli()
	}
	return friendly(c.MuteChat(context.Background(), chatID, until))
}
