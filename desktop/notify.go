package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/kantracity/kantrae2e/client/core"
)

// Notifications for new messages. They are produced by the Go client itself
// from the realtime connection it already holds — no push provider (FCM/APNs)
// sees anything. The platform shows them:
//   - desktop: native notifications (Wails notifications service), the app
//     keeps running in the tray when the window is closed;
//   - Android: the process stays alive in a foreground service
//     (KantraPushService) and posts notifications through JNI;
//   - web mode: the browser's Notification API (UIEvent "notify").

// Notice is one chat's notification (a newer message replaces it).
type Notice struct {
	ChatID string
	Title  string
	Body   string
	Count  int
}

type notifier interface {
	Show(n Notice)
	Clear(chatID string)
}

// Settings are this device's notification preferences.
type Settings struct {
	Notifications bool `json:"notifications"`
	// Preview shows the message text (otherwise only "new message").
	Preview bool `json:"preview"`
	// Background: Android keeps the connection while the app is closed;
	// desktop closes to the tray instead of quitting.
	Background bool `json:"background"`
	// Platform: "android", "desktop" or "web".
	Platform string `json:"platform"`
	// Android only: system state the user may need to change.
	NotificationsAllowed bool `json:"notificationsAllowed"`
	BatteryUnrestricted  bool `json:"batteryUnrestricted"`
}

const (
	prefNotify     = "notify"
	prefPreview    = "preview"
	prefBackground = "background"
)

// pref reads a boolean preference (default on). It must not take the core
// client's lock: it runs inside core event callbacks.
func (m *Messenger) pref(key string) bool {
	c, err := m.client()
	if err != nil {
		return true
	}
	return c.Pref(context.Background(), key) != "0"
}

func boolPref(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// GetSettings returns the notification settings of this device.
func (m *Messenger) GetSettings() Settings {
	s := Settings{Notifications: m.pref(prefNotify), Preview: m.pref(prefPreview),
		Background: m.pref(prefBackground), Platform: platformName}
	if m.web {
		s.Platform = "web"
	}
	platformStatus(&s)
	return s
}

// SetSettings stores the notification settings.
func (m *Messenger) SetSettings(s Settings) error {
	c, err := m.loggedIn()
	if err != nil {
		return err
	}
	ctx := context.Background()
	for k, v := range map[string]bool{prefNotify: s.Notifications, prefPreview: s.Preview, prefBackground: s.Background} {
		if err := c.SetPref(ctx, k, boolPref(v)); err != nil {
			return err
		}
	}
	platformApplyBackground(s.Background)
	return nil
}

// OpenSystemSettings opens the system screen for "notifications" or
// "battery" (Android).
func (m *Messenger) OpenSystemSettings(what string) { platformOpenSettings(what) }

// SetActiveChat tells which chat is on screen ("" for none): its messages
// are not notified, and its notification is removed.
func (m *Messenger) SetActiveChat(chatID string) {
	m.mu.Lock()
	m.activeChat = chatID
	if chatID != "" {
		delete(m.unread, chatID)
	}
	m.mu.Unlock()
	if chatID != "" {
		m.notif.Clear(chatID)
	}
}

// SetForeground tells whether the user is looking at the app (window
// focused / activity resumed / browser tab visible).
func (m *Messenger) SetForeground(fg bool) {
	m.mu.Lock()
	m.foreground = fg
	chat := m.activeChat
	if fg && chat != "" {
		delete(m.unread, chat)
	}
	m.mu.Unlock()
	if fg && chat != "" {
		m.notif.Clear(chat)
	}
}

// TakeOpenChat returns (once) the chat whose notification was tapped.
func (m *Messenger) TakeOpenChat() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := m.openChat
	m.openChat = ""
	return id
}

// showChat brings a chat to the screen after its notification was clicked.
func (m *Messenger) showChat(chatID string) {
	m.mu.Lock()
	m.openChat = chatID
	m.mu.Unlock()
	m.emit(UIEvent{Type: "open_chat", ChatID: chatID})
}

func (m *Messenger) maybeNotify(c *core.Client, msg *Message) {
	if msg.Outgoing || msg.Origin != "" { // own or restored/shared history
		return
	}
	m.mu.Lock()
	visible := m.foreground && m.activeChat == msg.ChatID
	if visible {
		m.mu.Unlock()
		return
	}
	m.unread[msg.ChatID]++
	count := m.unread[msg.ChatID]
	m.mu.Unlock()
	if !m.pref(prefNotify) {
		return
	}
	n := Notice{ChatID: msg.ChatID, Title: "Kantra", Body: "Новое сообщение", Count: count}
	if g, err := c.ResolveGroup(context.Background(), msg.ChatID); err == nil {
		ch := m.toChat(c, *g)
		n.Title = ch.Name
		if m.pref(prefPreview) {
			body := msg.Text
			if msg.Kind == "file" && msg.File != nil {
				body = "📎 " + msg.File.Name
			}
			if ch.Kind == core.KindGroup {
				body = msg.Sender + ": " + body
			}
			n.Body = body
		}
	}
	if count > 1 && !m.pref(prefPreview) {
		n.Body = fmt.Sprintf("Новых сообщений: %d", count)
	}
	n.Body = strings.TrimSpace(n.Body)
	m.notif.Show(n)
}

// eventNotifier shows notifications through the frontend (web mode).
type eventNotifier struct{ m *Messenger }

func (e eventNotifier) Show(n Notice) {
	e.m.emit(UIEvent{Type: "notify", ChatID: n.ChatID, Title: n.Title, Body: n.Body, Count: n.Count})
}

func (e eventNotifier) Clear(string) {}

// reconnect reopens the realtime connection now (network came back).
func (m *Messenger) reconnect() {
	if c, err := m.client(); err == nil {
		c.Reconnect()
	}
}
