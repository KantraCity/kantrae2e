package main

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kantracity/kantrae2e/client/core"
	"github.com/kantracity/kantrae2e/client/store"
)

// chatView holds what is needed to render a chat's messages once per call.
type chatView struct {
	me        string
	reactions map[string]map[string]string // uid -> user -> emoji
	readUpTo  int64                        // newest time someone else has read up to
	// Per-call caches: a chat of hundreds of messages has a handful of
	// senders, and replies usually quote messages of the same page.
	profiles map[string]*core.Profile
	byUID    map[string]*store.Message
}

func (v *chatView) profile(c *core.Client, userID string) *core.Profile {
	if v == nil {
		return profileOf(c, userID)
	}
	if p, ok := v.profiles[userID]; ok {
		return p
	}
	p := profileOf(c, userID)
	v.profiles[userID] = p
	return p
}

func (m *Messenger) viewOf(c *core.Client, chatID string) *chatView {
	ctx := context.Background()
	v := &chatView{profiles: map[string]*core.Profile{}, byUID: map[string]*store.Message{}}
	if a := c.Account(); a != nil {
		v.me = a.UserID
	}
	v.reactions, _ = c.Reactions(ctx, chatID)
	marks, _ := c.ReadMarks(ctx, chatID)
	for u, t := range marks {
		if u != v.me && t > v.readUpTo {
			v.readUpTo = t
		}
	}
	return v
}

// toMessage converts a stored message; v may be nil (no reactions / read state).
func (m *Messenger) toMessage(c *core.Client, s *store.Message) *Message {
	return m.toMessageView(c, s, nil)
}

func (m *Messenger) toMessageView(c *core.Client, s *store.Message, v *chatView) *Message {
	p := v.profile(c, s.SenderUser)
	msg := &Message{Seq: s.Seq, ID: s.UID, ChatID: s.GroupID, SenderID: s.SenderUser,
		Sender: core.DisplayName(p), SenderAvatar: p.AvatarID, Kind: "text", Text: s.Body,
		SentAt: s.SentAt, Outgoing: s.Outgoing, Origin: s.Origin, Edited: s.EditedAt > 0, Forwarded: s.FwdFrom}
	if ref, err := core.ParseMedia(s); err == nil {
		msg.Kind, msg.Text = "file", ""
		msg.File = fileInfo(ref)
		msg.File.URL = mediaURL(s.GroupID, s.UID, ref)
	}
	if s.ReplyTo != "" {
		msg.Reply = &ReplyInfo{ID: s.ReplyTo, Text: "Сообщение удалено"}
		var r *store.Message
		if v != nil {
			r = v.byUID[s.ReplyTo]
		}
		if r == nil {
			r, _ = c.Message(context.Background(), s.GroupID, s.ReplyTo)
		}
		if r != nil && !r.Deleted {
			msg.Reply.Sender = core.DisplayName(v.profile(c, r.SenderUser))
			msg.Reply.Text = r.Body
			if ref, err := core.ParseMedia(r); err == nil {
				msg.Reply.Text = mediaLabel(ref)
			}
		}
	}
	if v != nil {
		counts := map[string]*ReactionInfo{}
		for user, emoji := range v.reactions[s.UID] {
			ri := counts[emoji]
			if ri == nil {
				ri = &ReactionInfo{Emoji: emoji}
				counts[emoji] = ri
			}
			ri.Count++
			ri.Mine = ri.Mine || user == v.me
		}
		for _, ri := range counts {
			msg.Reactions = append(msg.Reactions, *ri)
		}
		sort.Slice(msg.Reactions, func(i, j int) bool {
			a, b := msg.Reactions[i], msg.Reactions[j]
			return a.Count > b.Count || (a.Count == b.Count && a.Emoji < b.Emoji)
		})
		msg.Read = s.Outgoing && v.readUpTo >= s.SentAt
	}
	return msg
}

func fileInfo(ref *core.MediaRef) *FileInfo {
	f := &FileInfo{Name: ref.Name, Size: ref.Size, Kind: ref.Kind, Mime: ref.Mime, Width: ref.Width,
		Height: ref.Height, Duration: ref.Duration, Caption: ref.Caption}
	if f.Kind == "" {
		f.Kind = "file"
	}
	if len(ref.Thumb) > 0 {
		f.Thumb = "data:" + http.DetectContentType(ref.Thumb) + ";base64," + base64.StdEncoding.EncodeToString(ref.Thumb)
	}
	for _, b := range ref.Waveform {
		f.Waveform = append(f.Waveform, int(b))
	}
	return f
}

// mediaLabel: how a media message is quoted / previewed, like Telegram.
func mediaLabel(ref *core.MediaRef) string {
	label := map[string]string{"photo": "🖼 Фото", "video": "📹 Видео", "voice": "🎤 Голосовое сообщение",
		"round": "⏺ Видеосообщение"}[ref.Kind]
	if label == "" {
		label = "📎 " + ref.Name
	}
	if ref.Caption != "" {
		label += ", " + ref.Caption
	}
	return label
}

func (m *Messenger) Messages(chatID string, limit int) ([]Message, error) {
	c, err := m.loggedIn()
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 5000 {
		limit = 200
	}
	ms, err := c.Messages(context.Background(), chatID, limit)
	if err != nil {
		return nil, err
	}
	v := m.viewOf(c, chatID)
	for i := range ms {
		v.byUID[ms[i].UID] = &ms[i]
	}
	out := make([]Message, 0, len(ms))
	for i := range ms {
		out = append(out, *m.toMessageView(c, &ms[i], v))
	}
	return out, nil
}

// GetMessage returns one message (nil if it was deleted).
func (m *Messenger) GetMessage(chatID, id string) (*Message, error) {
	c, err := m.loggedIn()
	if err != nil {
		return nil, err
	}
	s, err := c.Message(context.Background(), chatID, id)
	if errors.Is(err, store.ErrNotFound) || (err == nil && s.Deleted) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return m.toMessageView(c, s, m.viewOf(c, chatID)), nil
}

// ReadUpTo: outgoing messages sent up to this time (unix ms) are read.
func (m *Messenger) ReadUpTo(chatID string) (int64, error) {
	c, err := m.loggedIn()
	if err != nil {
		return 0, err
	}
	return m.viewOf(c, chatID).readUpTo, nil
}

// Send sends a text message (replyTo: uid of the answered message or "").
func (m *Messenger) Send(chatID, text, replyTo string) (*Message, error) {
	c, err := m.loggedIn()
	if err != nil {
		return nil, err
	}
	s, err := c.SendMessage(context.Background(), chatID, text, core.SendOptions{ReplyTo: replyTo})
	if err != nil {
		return nil, friendly(err)
	}
	return m.lookup(c, chatID, s.UID, s), nil
}

// MediaUpload is a file chosen or recorded in the UI.
type MediaUpload struct {
	Name     string  `json:"name"`
	Base64   string  `json:"base64"`
	Mime     string  `json:"mime"`
	Kind     string  `json:"kind"` // photo, video, voice, round, file
	Width    int     `json:"w"`
	Height   int     `json:"h"`
	Duration float64 `json:"dur"`
	// ThumbBase64: small JPEG preview made by the UI.
	ThumbBase64 string `json:"thumb"`
	Waveform    []int  `json:"wave"`
	Caption     string `json:"caption"`
}

// SendMedia sends a photo, video, voice or video message, or a file.
func (m *Messenger) SendMedia(chatID string, up MediaUpload, replyTo string) (*Message, error) {
	c, err := m.loggedIn()
	if err != nil {
		return nil, err
	}
	data, err := base64.StdEncoding.DecodeString(up.Base64)
	if err != nil {
		return nil, err
	}
	in := core.MediaInput{Name: filepath.Base(up.Name), Data: data, Mime: up.Mime, Kind: up.Kind, Width: up.Width,
		Height: up.Height, Duration: up.Duration, Caption: strings.TrimSpace(up.Caption)}
	if up.ThumbBase64 != "" {
		in.Thumb, _ = base64.StdEncoding.DecodeString(up.ThumbBase64)
	}
	for _, w := range up.Waveform {
		in.Waveform = append(in.Waveform, byte(max(0, min(255, w))))
	}
	s, err := c.SendMedia(context.Background(), chatID, in, core.SendOptions{ReplyTo: replyTo})
	if err != nil {
		return nil, friendly(err)
	}
	return m.lookup(c, chatID, s.UID, s), nil
}

// SendFile sends a file given as base64 (kept for simple callers).
func (m *Messenger) SendFile(chatID, name, dataBase64 string) (*Message, error) {
	return m.SendMedia(chatID, MediaUpload{Name: name, Base64: dataBase64, Kind: "file"}, "")
}

func (m *Messenger) EditMessage(chatID, id, text string) error {
	c, err := m.loggedIn()
	if err != nil {
		return err
	}
	return friendly(c.EditMessage(context.Background(), chatID, id, text))
}

// DeleteMessages deletes messages on this device or for everyone.
func (m *Messenger) DeleteMessages(chatID string, ids []string, forAll bool) error {
	c, err := m.loggedIn()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := c.DeleteMessage(context.Background(), chatID, id, forAll); err != nil {
			return friendly(err)
		}
	}
	return nil
}

// React sets (or with "" removes) this user's reaction.
func (m *Messenger) React(chatID, id, emoji string) error {
	c, err := m.loggedIn()
	if err != nil {
		return err
	}
	return friendly(c.React(context.Background(), chatID, id, emoji))
}

// Forward copies messages to other chats.
func (m *Messenger) Forward(chatID string, ids []string, toChats []string) error {
	c, err := m.loggedIn()
	if err != nil {
		return err
	}
	for _, to := range toChats {
		for _, id := range ids {
			if _, err := c.Forward(context.Background(), chatID, id, to); err != nil {
				return friendly(err)
			}
		}
	}
	return nil
}

// MarkRead sends a read receipt for the chat if there is something new.
func (m *Messenger) MarkRead(chatID string) error {
	c, err := m.loggedIn()
	if err != nil {
		return err
	}
	return c.MarkRead(context.Background(), chatID)
}

// Typing signals "typing…" (the UI calls it at most every few seconds).
func (m *Messenger) Typing(chatID string) error {
	c, err := m.loggedIn()
	if err != nil {
		return err
	}
	return c.Typing(context.Background(), chatID)
}
