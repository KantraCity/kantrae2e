package main

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"

	"github.com/kantracity/kantrae2e/client/core"
)

// UserProfile is a public profile as the UI shows it.
type UserProfile struct {
	UserID   string `json:"userId"`
	Username string `json:"username"`
	Name     string `json:"name"`
	Bio      string `json:"bio"`
	AvatarID string `json:"avatarId,omitempty"`
}

func toUserProfile(p *core.Profile) *UserProfile {
	return &UserProfile{UserID: p.UserID, Username: p.Username, Name: core.DisplayName(p), Bio: p.Bio, AvatarID: p.AvatarID}
}

// profileOf: cached profile, fetched once if never seen (no TTL refresh
// here: it is used while rendering lists and inside event callbacks).
func profileOf(c *core.Client, userID string) *core.Profile {
	ctx := context.Background()
	p := c.CachedProfile(ctx, userID)
	if p.FetchedAt == 0 {
		if q, err := c.Profile(ctx, userID); err == nil {
			p = q
		}
	}
	return p
}

// MyProfile returns this account's profile.
func (m *Messenger) MyProfile() (*UserProfile, error) {
	c, err := m.loggedIn()
	if err != nil {
		return nil, err
	}
	p, err := c.MyProfile(context.Background())
	if err != nil {
		return nil, err
	}
	return toUserProfile(p), nil
}

// UserProfile returns someone's profile, refreshed from the server (the
// user opened it; offline the cached one is shown).
func (m *Messenger) UserProfile(userID string) (*UserProfile, error) {
	c, err := m.loggedIn()
	if err != nil {
		return nil, err
	}
	_ = c.RefreshProfiles(context.Background(), userID)
	p, err := c.Profile(context.Background(), userID)
	if err != nil {
		return nil, err
	}
	return toUserProfile(p), nil
}

// UpdateProfile sets the display name and bio.
func (m *Messenger) UpdateProfile(name, bio string) (*UserProfile, error) {
	c, err := m.loggedIn()
	if err != nil {
		return nil, err
	}
	p, err := c.UpdateProfile(context.Background(), name, bio)
	if err != nil {
		return nil, err
	}
	m.emit(UIEvent{Type: "accounts"})
	return toUserProfile(p), nil
}

// SetUsername changes the @username.
func (m *Messenger) SetUsername(username string) (*UserProfile, error) {
	c, err := m.loggedIn()
	if err != nil {
		return nil, err
	}
	p, err := c.SetUsername(context.Background(), username)
	if errors.Is(err, core.ErrUsernameTaken) {
		return nil, errors.New("это имя пользователя уже занято")
	}
	if err != nil {
		return nil, err
	}
	m.emit(UIEvent{Type: "accounts"})
	return toUserProfile(p), nil
}

// SetAvatar sets the profile photo (base64 JPEG/PNG/WebP, the UI crops and
// scales it); empty removes it.
func (m *Messenger) SetAvatar(imageBase64 string) (*UserProfile, error) {
	c, err := m.loggedIn()
	if err != nil {
		return nil, err
	}
	var img []byte
	if imageBase64 != "" {
		if img, err = base64.StdEncoding.DecodeString(imageBase64); err != nil {
			return nil, err
		}
	}
	p, err := c.SetAvatar(context.Background(), img)
	if err != nil {
		return nil, err
	}
	m.emit(UIEvent{Type: "accounts"})
	return toUserProfile(p), nil
}

// Avatar returns a user's photo as a data: URL ("" if none). Cached
// locally by photo id; the UI caches by avatarId too.
func (m *Messenger) Avatar(userID string) (string, error) {
	c, err := m.loggedIn()
	if err != nil {
		return "", err
	}
	img, err := c.Avatar(context.Background(), userID)
	if err != nil || len(img) == 0 {
		return "", err
	}
	return "data:" + http.DetectContentType(img) + ";base64," + base64.StdEncoding.EncodeToString(img), nil
}
