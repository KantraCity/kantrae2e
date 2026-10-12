package main

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/kantracity/kantrae2e/client/core"
)

// ---- presence, block list, invite links, group info (core/presence.go,
// core/groupinfo.go) ----

// PresenceView: Online, or LastSeen (unix ms); Hidden = "last seen recently".
type PresenceView struct {
	UserID   string `json:"userId"`
	Online   bool   `json:"online"`
	LastSeen int64  `json:"lastSeen"`
	Hidden   bool   `json:"hidden"`
}

func (m *Messenger) Presence(userIDs []string) ([]PresenceView, error) {
	c, err := m.loggedIn()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ps, err := c.Presence(ctx, userIDs...)
	if err != nil {
		return []PresenceView{}, nil // an older server: no presence
	}
	out := make([]PresenceView, 0, len(ps))
	for id, p := range ps {
		v := PresenceView{UserID: id, Online: p.Online, Hidden: p.Hidden}
		if !p.LastSeen.IsZero() {
			v.LastSeen = p.LastSeen.UnixMilli()
		}
		out = append(out, v)
	}
	return out, nil
}

// LastSeenPrivacy: "everybody", "chats" or "nobody".
func (m *Messenger) LastSeenPrivacy() (string, error) {
	c, err := m.loggedIn()
	if err != nil {
		return "", err
	}
	return c.LastSeenPrivacy(context.Background())
}

func (m *Messenger) SetLastSeenPrivacy(who string) error {
	c, err := m.loggedIn()
	if err != nil {
		return err
	}
	return friendly(c.SetLastSeenPrivacy(context.Background(), who))
}

// Block / Unblock a user; Blocked lists them (with profiles).
func (m *Messenger) Block(userID string) error {
	c, err := m.loggedIn()
	if err != nil {
		return err
	}
	return friendly(c.Block(context.Background(), userID))
}

func (m *Messenger) Unblock(userID string) error {
	c, err := m.loggedIn()
	if err != nil {
		return err
	}
	return friendly(c.Unblock(context.Background(), userID))
}

func (m *Messenger) Blocked() ([]UserHit, error) {
	c, err := m.loggedIn()
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	ids, err := c.Blocked(ctx)
	if err != nil {
		return nil, friendly(err)
	}
	_ = c.RefreshProfiles(ctx, ids...)
	out := make([]UserHit, 0, len(ids))
	for _, id := range ids {
		p := profileOf(c, id)
		out = append(out, UserHit{UserID: id, Username: p.Username, Name: core.DisplayName(p), AvatarID: p.AvatarID})
	}
	return out, nil
}

// InviteLink returns the group's invite link (reset: a new one, the old one
// stops working).
func (m *Messenger) InviteLink(chatID string, reset bool) (string, error) {
	c, err := m.loggedIn()
	if err != nil {
		return "", err
	}
	l, err := c.InviteLink(context.Background(), chatID, reset)
	return l, friendly(err)
}

func (m *Messenger) RevokeInviteLink(chatID string) error {
	c, err := m.loggedIn()
	if err != nil {
		return err
	}
	return friendly(c.RevokeInviteLink(context.Background(), chatID))
}

// IsInviteLink reports whether text is a Kantra invite link.
func (m *Messenger) IsInviteLink(text string) bool {
	_, _, err := core.ParseInviteLink(text)
	return err == nil
}

// JoinByLink asks to join a group by its invite link. The group appears
// when a member adds this device (usually within seconds).
func (m *Messenger) JoinByLink(link string) (string, error) {
	c, err := m.loggedIn()
	if err != nil {
		return "", err
	}
	id, err := c.JoinByLink(context.Background(), link)
	if errors.Is(err, core.ErrBadInvite) {
		return "", errors.New("это не ссылка-приглашение")
	}
	if err != nil && strings.Contains(err.Error(), "not valid") {
		return "", errors.New("ссылка недействительна — попросите новую")
	}
	return id, friendly(err)
}

// SetGroupInfo sets a group's name and description.
func (m *Messenger) SetGroupInfo(chatID, name, about string) error {
	c, err := m.loggedIn()
	if err != nil {
		return err
	}
	return friendly(c.SetGroupInfo(context.Background(), chatID, &name, &about))
}

// SetGroupPhoto sets the group photo from an image (base64); "" removes it.
func (m *Messenger) SetGroupPhoto(chatID, imageBase64 string) error {
	c, err := m.loggedIn()
	if err != nil {
		return err
	}
	if imageBase64 == "" {
		return friendly(c.SetGroupPhoto(context.Background(), chatID, nil))
	}
	data, err := base64.StdEncoding.DecodeString(imageBase64)
	if err != nil {
		return err
	}
	return friendly(c.SetGroupPhoto(context.Background(), chatID, &core.MediaInput{Name: "group.jpg", Data: data, Mime: "image/jpeg", Kind: "photo"}))
}

// ---- "online" while the app is in the foreground ----

type activity struct {
	mu   sync.Mutex
	stop chan struct{}
}

var appActivity activity

// setActive starts/stops telling the server the app is open (every minute).
func (m *Messenger) setActive(on bool) {
	appActivity.mu.Lock()
	defer appActivity.mu.Unlock()
	if on == (appActivity.stop != nil) {
		return
	}
	ping := func(v bool) bool {
		c, err := m.client()
		if err != nil || c.Account() == nil {
			return false
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return c.SetActive(ctx, v) == nil
	}
	if !on {
		close(appActivity.stop)
		appActivity.stop = nil
		go ping(false)
		return
	}
	stop := make(chan struct{})
	appActivity.stop = stop
	go func() {
		// Every minute; until the first success (not signed in yet, offline)
		// every 5 seconds.
		wait := time.Duration(0)
		for {
			select {
			case <-stop:
				return
			case <-time.After(wait):
			}
			if ping(true) {
				wait = 60 * time.Second
			} else {
				wait = 5 * time.Second
			}
		}
	}()
}
