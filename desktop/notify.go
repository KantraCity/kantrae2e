package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

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

// Notice is one chat's notification (a newer message replaces it). Key
// identifies it across accounts ("<account>/<chat>").
type Notice struct {
	Key    string
	ChatID string
	Title  string
	Body   string
	Count  int
	// Image: a picture shown in the notification (photo, GIF frame, video
	// thumbnail): a file path for native notifications, ImageURL for the
	// browser.
	Image    string
	ImageURL string
}

func noticeKey(account, chatID string) string { return account + "/" + chatID }

type notifier interface {
	Show(n Notice)
	Clear(key string)
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
	return clientPref(c, key)
}

func clientPref(c *core.Client, key string) bool { return c.Pref(context.Background(), key) != "0" }

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
	key := ""
	if chatID != "" && m.active != nil {
		delete(m.active.unread, chatID)
		key = noticeKey(m.active.file, chatID)
	}
	m.mu.Unlock()
	if key != "" {
		m.notif.Clear(key)
	}
}

// SetForeground tells whether the user is looking at the app (window
// focused / activity resumed / browser tab visible).
func (m *Messenger) SetForeground(fg bool) {
	m.setActive(fg)
	m.mu.Lock()
	m.foreground = fg
	key := ""
	if fg && m.activeChat != "" && m.active != nil {
		delete(m.active.unread, m.activeChat)
		key = noticeKey(m.active.file, m.activeChat)
	}
	m.mu.Unlock()
	if key != "" {
		m.notif.Clear(key)
	}
}

// OpenNotification handles a clicked browser notification (web mode).
func (m *Messenger) OpenNotification(key string) { m.showChat(key) }

// TakeOpenChat returns (once) the chat whose notification was tapped.
func (m *Messenger) TakeOpenChat() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := m.openChat
	m.openChat = ""
	return id
}

// showChat brings a chat to the screen after its notification (key
// "<account>/<chat>") was clicked, switching account if needed.
func (m *Messenger) showChat(key string) {
	account, chatID, ok := strings.Cut(key, "/")
	if !ok {
		account, chatID = "", key
	}
	m.mu.Lock()
	switchTo := account != "" && m.active != nil && m.active.file != account
	m.openChat = chatID
	m.mu.Unlock()
	if switchTo {
		if err := m.SwitchAccount(account); err != nil {
			return
		}
	}
	m.emit(UIEvent{Type: "open_chat", ChatID: chatID})
}

func (m *Messenger) maybeNotify(s *slot, c *core.Client, msg *Message) {
	if msg.Outgoing || msg.Origin != "" { // own or restored/shared history
		return
	}
	// Sent "without sound": counted as unread, but no notification —
	// unless it mentions this account (like Telegram).
	silent := msg.Silent && !mentions(msg.Text, s, c)
	m.mu.Lock()
	if s == m.active && m.foreground && m.activeChat == msg.ChatID {
		m.mu.Unlock()
		return
	}
	s.unread[msg.ChatID]++
	count := s.unread[msg.ChatID]
	multi := len(m.slots) > 1
	m.mu.Unlock()
	if !clientPref(c, prefNotify) || silent {
		return
	}
	preview := clientPref(c, prefPreview)
	n := Notice{Key: noticeKey(s.file, msg.ChatID), ChatID: msg.ChatID, Title: "Kantra", Body: "Новое сообщение", Count: count}
	if g, err := c.ResolveGroup(context.Background(), msg.ChatID); err == nil {
		ch := m.toChat(c, *g)
		n.Title = ch.Name
		if preview {
			body := plainText(msg.Text)
			if msg.Kind == "file" && msg.File != nil {
				body = fileLabel(msg.File)
			} else if msg.Kind == "poll" && msg.Poll != nil {
				body = "📊 " + msg.Poll.Question
			}
			if ch.Kind == core.KindGroup {
				body = msg.Sender + ": " + body
			}
			n.Body = body
		}
	}
	if count > 1 && !preview {
		n.Body = fmt.Sprintf("Новых сообщений: %d", count)
	}
	if multi {
		// Several accounts: say which one, like Telegram.
		if uid := s.userID; uid != "" {
			n.Title += " → " + core.DisplayName(c.CachedProfile(context.Background(), uid))
		}
	}
	n.Body = strings.TrimSpace(n.Body)
	if preview && msg.File != nil && notifyPicture[msg.File.Kind] {
		// Fetching the picture needs the client, which is busy delivering
		// this very message: show the notification once it is ready.
		go func() {
			n.Image, n.ImageURL = m.noticeImage(c, msg)
			m.notif.Show(n)
		}()
		return
	}
	m.notif.Show(n)
}

// Kinds of media whose picture goes into the notification.
var notifyPicture = map[string]bool{"photo": true, "gif": true, "sticker": true, "video": true, "round": true}

// fileLabel: how a media message reads in a notification / the chat list.
func fileLabel(f *FileInfo) string {
	label := map[string]string{"photo": "🖼 Фото", "video": "📹 Видео", "voice": "🎤 Голосовое сообщение",
		"round": "⏺ Видеосообщение", "gif": "GIF", "sticker": "Стикер"}[f.Kind]
	if label == "" {
		label = "📎 " + f.Name
	}
	if f.Caption != "" {
		label += ", " + f.Caption
	}
	return label
}

// noticeImage writes the notification picture of a media message to the
// notification cache: the photo / GIF itself (up to a few MB) or, for
// videos, the preview frame.
func (m *Messenger) noticeImage(c *core.Client, msg *Message) (path, url string) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	sm, err := c.Message(ctx, msg.ChatID, msg.ID)
	if err != nil {
		return "", ""
	}
	ref, err := core.ParseMedia(sm)
	if err != nil {
		return "", ""
	}
	var data []byte
	ext := ".jpg"
	if (ref.Kind == "photo" || ref.Kind == "gif" || ref.Kind == "sticker") && ref.Size <= 8<<20 {
		if e, ok := decrypted.get(ref.Hash); ok {
			data = e.data
		} else if _, d, err := c.DownloadMedia(ctx, sm); err == nil {
			data = d
		}
		switch http.DetectContentType(data) {
		case "image/gif":
			ext = ".gif"
		case "image/png":
			ext = ".png"
		case "image/webp":
			ext = ".webp"
		}
	}
	if len(data) == 0 {
		data, ext = ref.Thumb, ".jpg"
	}
	if msg.File != nil {
		url = msg.File.URL
	}
	if len(data) == 0 {
		return "", url
	}
	m.mu.Lock()
	dir := filepath.Join(m.dir, "notify")
	m.mu.Unlock()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", url
	}
	cleanNoticeImages(dir)
	p := filepath.Join(dir, ref.Hash[:min(16, len(ref.Hash))]+ext)
	if err := os.WriteFile(p, data, 0o600); err != nil {
		return "", url
	}
	return p, url
}

// cleanNoticeImages removes pictures of old notifications (a day old).
func cleanNoticeImages(dir string) {
	es, _ := os.ReadDir(dir)
	for _, e := range es {
		if fi, err := e.Info(); err == nil && time.Since(fi.ModTime()) > 24*time.Hour {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// eventNotifier shows notifications through the frontend (web mode).
type eventNotifier struct{ m *Messenger }

func (e eventNotifier) Show(n Notice) {
	e.m.emit(UIEvent{Type: "notify", ChatID: n.Key, Title: n.Title, Body: n.Body, Count: n.Count, Image: n.ImageURL})
}

func (e eventNotifier) Clear(string) {}

// reconnect reopens the realtime connection now (network came back).
func (m *Messenger) reconnect() {
	if c, err := m.client(); err == nil {
		c.Reconnect()
	}
}

// mentions reports whether a text mentions the slot's account (@username).
var (
	mdLink    = regexp.MustCompile(`\[([^\]\n]{1,200})\]\((https?://[^\s)]+)\)`)
	mdSpoiler = regexp.MustCompile(`\|\|([^|]+?)\|\|`)
	mdMarks   = strings.NewReplacer("```", "", "**", "", "__", "", "~~", "", "`", "")
)

// plainText drops the formatting markup of a message (frontend lib/format.ts)
// for notifications; spoilers stay hidden.
func plainText(s string) string {
	s = mdLink.ReplaceAllString(s, "$1")
	s = mdSpoiler.ReplaceAllString(s, "▒▒▒")
	return mdMarks.Replace(s)
}

func mentions(text string, s *slot, c *core.Client) bool {
	if s.userID == "" || !strings.Contains(text, "@") {
		return false
	}
	u := c.CachedProfile(context.Background(), s.userID).Username
	if u == "" {
		return false
	}
	low, at := strings.ToLower(text), "@"+strings.ToLower(u)
	for i := strings.Index(low, at); i >= 0; {
		end := i + len(at)
		if end == len(low) || !isNameChar(low[end]) {
			return true
		}
		j := strings.Index(low[end:], at)
		if j < 0 {
			break
		}
		i = end + j
	}
	return false
}

func isNameChar(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b == '_' || b == '.' || b == '-'
}
