package main

import (
	"context"
	"encoding/base64"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/kantracity/kantrae2e/client/core"
	"github.com/kantracity/kantrae2e/client/store"
)

// ---- message extras: link previews, polls, pins, silent and scheduled
// messages, search (core/extras.go, core/scheduled.go) ----

// LinkView is a link preview card; Image is a data: URL.
type LinkView struct {
	URL   string `json:"url"`
	Site  string `json:"site,omitempty"`
	Title string `json:"title,omitempty"`
	Desc  string `json:"desc,omitempty"`
	Image string `json:"image,omitempty"`
}

// PollView: a poll with its results.
type PollView struct {
	Question  string       `json:"question"`
	Options   []PollOption `json:"options"`
	Multiple  bool         `json:"multiple,omitempty"`
	Anonymous bool         `json:"anonymous,omitempty"`
	Total     int          `json:"total"` // people who voted
	Voted     bool         `json:"voted"` // this account voted
}

type PollOption struct {
	Text   string   `json:"text"`
	Votes  int      `json:"votes"`
	Mine   bool     `json:"mine,omitempty"`
	Voters []string `json:"voters,omitempty"` // names (not for anonymous polls)
}

func linkView(lp *core.LinkPreview) *LinkView {
	if lp == nil {
		return nil
	}
	v := &LinkView{URL: lp.URL, Site: lp.Site, Title: lp.Title, Desc: lp.Desc}
	if len(lp.Image) > 0 {
		v.Image = "data:" + http.DetectContentType(lp.Image) + ";base64," + base64.StdEncoding.EncodeToString(lp.Image)
	}
	return v
}

func (m *Messenger) addExtras(c *core.Client, s *store.Message, msg *Message, v *chatView) {
	msg.Pinned = s.PinnedAt > 0
	x := core.ParseExtra(s)
	if x == nil {
		return
	}
	msg.Silent = x.Silent
	msg.Link = linkView(x.Link)
	if s.Kind == "poll" && x.Poll != nil {
		msg.Kind = "poll"
		pv := &PollView{Question: x.Poll.Question, Multiple: x.Poll.Multiple, Anonymous: x.Poll.Anonymous}
		pv.Options = make([]PollOption, len(x.Poll.Options))
		for i, o := range x.Poll.Options {
			pv.Options[i].Text = o
		}
		votes, _ := c.Votes(context.Background(), s.GroupID, s.UID)
		me := ""
		if v != nil {
			me = v.me
		}
		for user, opts := range votes {
			pv.Total++
			for _, o := range opts {
				if o < 0 || o >= len(pv.Options) {
					continue
				}
				pv.Options[o].Votes++
				if user == me {
					pv.Options[o].Mine, pv.Voted = true, true
				}
				if !pv.Anonymous {
					pv.Options[o].Voters = append(pv.Options[o].Voters, core.DisplayName(v.profile(c, user)))
				}
			}
		}
		msg.Poll = pv
	}
}

// SendOpts for SendWith: reply, silent (no notification), link preview.
type SendOpts struct {
	ReplyTo string    `json:"replyTo"`
	Silent  bool      `json:"silent"`
	Link    *LinkView `json:"link,omitempty"`
}

func (o SendOpts) core() core.SendOptions {
	so := core.SendOptions{ReplyTo: o.ReplyTo, Silent: o.Silent}
	if o.Link != nil {
		lp := &core.LinkPreview{URL: o.Link.URL, Site: o.Link.Site, Title: o.Link.Title, Desc: o.Link.Desc}
		if _, b64, ok := strings.Cut(o.Link.Image, ";base64,"); ok {
			if img, err := base64.StdEncoding.DecodeString(b64); err == nil && len(img) <= core.MaxThumb {
				lp.Image = img
			}
		}
		so.Link = lp
	}
	return so
}

// SendWith sends a text message with options.
func (m *Messenger) SendWith(chatID, text string, opts SendOpts) (*Message, error) {
	c, err := m.loggedIn()
	if err != nil {
		return nil, err
	}
	s, err := c.SendMessage(context.Background(), chatID, text, opts.core())
	if err != nil {
		return nil, friendly(err)
	}
	return m.lookup(c, chatID, s.UID, s), nil
}

// LinkPreview fetches the preview card of a web link (on this device).
func (m *Messenger) LinkPreview(url string) (*LinkView, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	lp, err := core.FetchLinkPreview(ctx, url)
	if err != nil {
		return nil, nil // no preview: not an error for the UI
	}
	return linkView(lp), nil
}

// SendPoll sends a poll.
func (m *Messenger) SendPoll(chatID, question string, options []string, multiple, anonymous bool) (*Message, error) {
	c, err := m.loggedIn()
	if err != nil {
		return nil, err
	}
	s, err := c.SendPoll(context.Background(), chatID, core.Poll{Question: question, Options: options, Multiple: multiple, Anonymous: anonymous})
	if err != nil {
		return nil, friendly(err)
	}
	return m.lookup(c, chatID, s.UID, s), nil
}

// Vote chooses options of a poll (none retracts the vote).
func (m *Messenger) Vote(chatID, id string, options []int) error {
	c, err := m.loggedIn()
	if err != nil {
		return err
	}
	return friendly(c.Vote(context.Background(), chatID, id, options))
}

// Pin pins / unpins a message, for everybody or only for me.
func (m *Messenger) Pin(chatID, id string, pin, forAll bool) error {
	c, err := m.loggedIn()
	if err != nil {
		return err
	}
	return friendly(c.Pin(context.Background(), chatID, id, pin, forAll))
}

// Pinned messages of a chat, newest pin first.
func (m *Messenger) Pinned(chatID string) ([]Message, error) {
	c, err := m.loggedIn()
	if err != nil {
		return nil, err
	}
	ms, err := c.Pinned(context.Background(), chatID)
	if err != nil {
		return nil, err
	}
	v := m.viewOf(c, chatID)
	out := make([]Message, 0, len(ms))
	for i := range ms {
		out = append(out, *m.toMessageView(c, &ms[i], v))
	}
	return out, nil
}

// Search finds messages of a chat (newest first).
func (m *Messenger) Search(chatID, query string) ([]Message, error) {
	c, err := m.loggedIn()
	if err != nil {
		return nil, err
	}
	ms, err := c.Search(context.Background(), chatID, query, 200)
	if err != nil {
		return nil, err
	}
	v := m.viewOf(c, chatID)
	out := make([]Message, 0, len(ms))
	for i := range ms {
		out = append(out, *m.toMessageView(c, &ms[i], v))
	}
	return out, nil
}

// ScheduledView: a message waiting to be sent.
type ScheduledView struct {
	ID     string `json:"id"`
	ChatID string `json:"chatId"`
	Text   string `json:"text"`
	At     int64  `json:"at"` // unix ms
	Silent bool   `json:"silent,omitempty"`
}

// Schedule sends a text message later (at: unix ms).
func (m *Messenger) Schedule(chatID, text string, at int64, opts SendOpts) error {
	c, err := m.loggedIn()
	if err != nil {
		return err
	}
	_, err = c.Schedule(context.Background(), chatID, text, time.UnixMilli(at), opts.core())
	return friendly(err)
}

func (m *Messenger) Scheduled(chatID string) ([]ScheduledView, error) {
	c, err := m.loggedIn()
	if err != nil {
		return nil, err
	}
	list, err := c.ScheduledIn(context.Background(), chatID)
	if err != nil {
		return nil, err
	}
	out := make([]ScheduledView, 0, len(list))
	for _, s := range list {
		out = append(out, ScheduledView{ID: s.ID, ChatID: s.GroupID, Text: s.Text, At: s.At, Silent: s.Silent})
	}
	return out, nil
}

func (m *Messenger) CancelScheduled(id string) error {
	c, err := m.loggedIn()
	if err != nil {
		return err
	}
	return friendly(c.CancelScheduled(context.Background(), id))
}

func (m *Messenger) SendScheduledNow(id string) error {
	c, err := m.loggedIn()
	if err != nil {
		return err
	}
	_, err = c.SendScheduledNow(context.Background(), id)
	return friendly(err)
}

// sysText: a group event as Telegram shows it ("Анна добавила Бориса").
func (m *Messenger) sysText(c *core.Client, v *chatView, s *store.Message, e *core.SysEvent) string {
	me := ""
	if v != nil {
		me = v.me
	} else {
		// Called from core event callbacks too: never c.Account() here.
		me = m.userIDOf(c)
	}
	actor := core.DisplayName(v.profile(c, s.SenderUser))
	self := s.SenderUser == me
	if self {
		actor = "Вы"
	}
	verb := func(one, you string) string {
		if self {
			return you
		}
		return one
	}
	names := func(ids []string, you string) string {
		out := make([]string, 0, len(ids))
		for _, id := range ids {
			if id == me {
				out = append(out, you)
			} else {
				out = append(out, core.DisplayName(v.profile(c, id)))
			}
		}
		return strings.Join(out, ", ")
	}
	switch e.Action {
	case "create":
		return actor + " " + verb("создал(а)", "создали") + " группу «" + e.Name + "»"
	case "add":
		return actor + " " + verb("добавил(а)", "добавили") + " " + names(e.Users, "вас")
	case "join":
		who := names(e.Users, "Вы")
		if len(e.Users) == 1 && e.Users[0] == me {
			return "Вы присоединились к группе по ссылке"
		}
		return who + " присоединился(-лась) к группе по ссылке"
	case "leave":
		return actor + " " + verb("покинул(а)", "покинули") + " группу"
	case "remove":
		return actor + " " + verb("удалил(а)", "удалили") + " " + names(e.Users, "вас")
	case "rename":
		return actor + " " + verb("изменил(а)", "изменили") + " название группы на «" + e.Name + "»"
	case "photo":
		return actor + " " + verb("изменил(а)", "изменили") + " фото группы"
	case "nophoto":
		return actor + " " + verb("удалил(а)", "удалили") + " фото группы"
	}
	return actor + ": событие в группе"
}

// userIDOf: the account of a client, without calling into the client.
func (m *Messenger) userIDOf(c *core.Client) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.slots {
		if s.c == c {
			return s.userID
		}
	}
	return ""
}

// SharedMedia lists a chat's media for the profile tabs (newest first):
// kind "media" (photos, videos, GIFs), "files", "voice" (voice and video
// messages) or "links".
func (m *Messenger) SharedMedia(chatID, kind string) ([]Message, error) {
	c, err := m.loggedIn()
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	storeKind := "media"
	if kind == "links" {
		storeKind = "text"
	}
	ms, err := c.MessagesOfKind(ctx, chatID, storeKind, 3000)
	if err != nil {
		return nil, err
	}
	v := m.viewOf(c, chatID)
	out := []Message{}
	for i := len(ms) - 1; i >= 0 && len(out) < 500; i-- {
		s := &ms[i]
		if kind == "links" {
			if !linkRE.MatchString(s.Body) {
				continue
			}
		} else {
			ref, err := core.ParseMedia(s)
			if err != nil || sharedTab(ref.Kind) != kind {
				continue
			}
		}
		out = append(out, *m.toMessageView(c, s, v))
	}
	return out, nil
}

var linkRE = regexp.MustCompile(`(?i)\bhttps?://\S+`)

func sharedTab(mediaKind string) string {
	switch mediaKind {
	case "photo", "video", "gif":
		return "media"
	case "voice", "round":
		return "voice"
	case "sticker":
		return ""
	}
	return "files"
}
