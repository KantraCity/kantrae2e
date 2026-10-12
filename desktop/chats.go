package main

import (
	"context"
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
