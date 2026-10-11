package core

// Public profiles, as in Telegram: a display name separate from the unique
// @username, a bio and a photo. They live on the server (visible to anyone
// who finds the user, like in Telegram) and are cached locally.

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"

	"github.com/kantracity/kantrae2e/client/store"
	authv1 "github.com/kantracity/kantrae2e/gen/kantra/auth/v1"
)

// Profile is a user's public profile.
type Profile = store.Profile

// profileTTL: cached profiles older than this are refreshed on use.
const profileTTL = 10 * time.Minute

func toProfile(u *authv1.User) Profile {
	return Profile{UserID: u.UserId, Username: u.Username, DisplayName: u.DisplayName, Bio: u.Bio,
		AvatarID: u.AvatarId, FetchedAt: time.Now().UnixMilli()}
}

// putProfiles caches profiles. It must not take c.mu (callers may hold it).
func (c *Client) putProfiles(ctx context.Context, us []*authv1.User) error {
	return c.st.Update(ctx, func(tx *store.Tx) error {
		for _, u := range us {
			if err := tx.PutProfile(toProfile(u)); err != nil {
				return err
			}
		}
		return nil
	})
}

// syncOwnProfile keeps this device's idea of its @username current (it may
// be changed on another device). Caller must not hold c.mu.
func (c *Client) syncOwnProfile(ctx context.Context, u *authv1.User) error {
	if err := c.putProfiles(ctx, []*authv1.User{u}); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.acct == nil || c.acct.UserID != u.UserId || c.acct.Username == u.Username {
		return nil
	}
	c.acct.Username = u.Username
	return c.st.Update(ctx, func(tx *store.Tx) error { return tx.Set(kvUsername, []byte(u.Username)) })
}

// RefreshProfiles fetches the profiles of the given users.
func (c *Client) RefreshProfiles(ctx context.Context, userIDs ...string) error {
	if len(userIDs) == 0 {
		return nil
	}
	if err := c.refreshToken(ctx); err != nil {
		return err
	}
	r, err := c.auth.GetProfiles(ctx, &authv1.GetProfilesRequest{UserIds: userIDs})
	if err != nil {
		return err
	}
	return c.putProfiles(ctx, r.Users)
}

// Profile returns a user's profile, from the cache when fresh enough. If the
// server is unreachable, a stale cached profile is returned.
func (c *Client) Profile(ctx context.Context, userID string) (*Profile, error) {
	p, err := c.st.Profile(ctx, userID)
	if err == nil && time.Since(time.UnixMilli(p.FetchedAt)) < profileTTL {
		return p, nil
	}
	if rerr := c.RefreshProfiles(ctx, userID); rerr != nil {
		if err == nil {
			return p, nil
		}
		return nil, rerr
	}
	return c.st.Profile(ctx, userID)
}

// CachedProfile never touches the network (safe inside event callbacks).
func (c *Client) CachedProfile(ctx context.Context, userID string) *Profile {
	p, err := c.st.Profile(ctx, userID)
	if err != nil {
		return &Profile{UserID: userID, Username: userID}
	}
	return p
}

// DisplayName is the name to show for a user (falls back to the username).
func DisplayName(p *Profile) string {
	if p.DisplayName != "" {
		return p.DisplayName
	}
	return p.Username
}

// Avatar returns a user's photo (nil if none), cached by avatar id.
func (c *Client) Avatar(ctx context.Context, userID string) ([]byte, error) {
	p, err := c.Profile(ctx, userID)
	if err != nil || p.AvatarID == "" {
		return nil, err
	}
	if img, err := c.st.Avatar(ctx, userID, p.AvatarID); err == nil {
		return img, nil
	}
	if err := c.refreshToken(ctx); err != nil {
		return nil, err
	}
	r, err := c.auth.GetAvatar(ctx, &authv1.GetAvatarRequest{UserId: userID})
	if connect.CodeOf(err) == connect.CodeNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	err = c.st.Update(ctx, func(tx *store.Tx) error { return tx.PutAvatar(userID, r.AvatarId, r.Image) })
	return r.Image, err
}

// MyProfile returns this account's profile (fresh from the server).
func (c *Client) MyProfile(ctx context.Context) (*Profile, error) {
	a := c.Account()
	if a == nil {
		return nil, ErrNotLoggedIn
	}
	if err := c.refreshToken(ctx); err != nil {
		return nil, err
	}
	r, err := c.auth.GetProfiles(ctx, &authv1.GetProfilesRequest{UserIds: []string{a.UserID}})
	if err != nil {
		return nil, err
	}
	if len(r.Users) == 1 {
		if err := c.syncOwnProfile(ctx, r.Users[0]); err != nil {
			return nil, err
		}
	}
	return c.st.Profile(ctx, a.UserID)
}

// UpdateProfile changes the display name and bio.
func (c *Client) UpdateProfile(ctx context.Context, displayName, bio string) (*Profile, error) {
	if err := c.refreshToken(ctx); err != nil {
		return nil, err
	}
	r, err := c.auth.UpdateProfile(ctx, &authv1.UpdateProfileRequest{DisplayName: displayName, Bio: bio})
	if err != nil {
		return nil, err
	}
	p := toProfile(r.User)
	return &p, c.syncOwnProfile(ctx, r.User)
}

// ErrUsernameTaken: someone else has this @username.
var ErrUsernameTaken = errors.New("this username is already taken")

// SetUsername changes this account's @username (login uses the new one).
func (c *Client) SetUsername(ctx context.Context, username string) (*Profile, error) {
	if err := c.refreshToken(ctx); err != nil {
		return nil, err
	}
	r, err := c.auth.SetUsername(ctx, &authv1.SetUsernameRequest{Username: username})
	if connect.CodeOf(err) == connect.CodeAlreadyExists {
		return nil, ErrUsernameTaken
	}
	if err != nil {
		return nil, err
	}
	p := toProfile(r.User)
	return &p, c.syncOwnProfile(ctx, r.User)
}

// SetAvatar sets (nil: removes) the profile photo.
func (c *Client) SetAvatar(ctx context.Context, img []byte) (*Profile, error) {
	if err := c.refreshToken(ctx); err != nil {
		return nil, err
	}
	r, err := c.auth.SetAvatar(ctx, &authv1.SetAvatarRequest{Image: img})
	if err != nil {
		return nil, err
	}
	p := toProfile(r.User)
	if err := c.syncOwnProfile(ctx, r.User); err != nil {
		return nil, err
	}
	if len(img) > 0 {
		_ = c.st.Update(ctx, func(tx *store.Tx) error { return tx.PutAvatar(p.UserID, p.AvatarID, img) })
	}
	return &p, nil
}

// LogoutInfo tells the user what logging out of this device means.
type LogoutInfo struct {
	// LastDevice: no other active device; without a recovery phrase the
	// message history cannot be restored on a new device.
	LastDevice  bool
	HasRecovery bool
}

func (c *Client) LogoutInfo(ctx context.Context) (*LogoutInfo, error) {
	devs, err := c.Devices(ctx)
	if err != nil {
		return nil, err
	}
	me := c.Account()
	info := &LogoutInfo{LastDevice: true}
	for _, d := range devs {
		if d.Id != me.DeviceID && d.RevokedAt == 0 && d.Approved {
			info.LastDevice = false
		}
	}
	info.HasRecovery, err = c.HasRecovery(ctx)
	return info, err
}

// Logout signs this device out of the account: pending messages are backed
// up, the device is revoked (removed from all chats by the other devices)
// and must not be used again. The caller deletes the local database.
func (c *Client) Logout(ctx context.Context) error {
	a := c.Account()
	if a == nil {
		return ErrNotLoggedIn
	}
	if _, err := c.BackupHistory(ctx); err != nil && !errors.Is(err, ErrNoHistoryKey) {
		c.logf("backup before logout: %v", err)
	}
	if err := c.refreshToken(ctx); err != nil {
		return err
	}
	_, err := c.auth.RevokeDevice(ctx, &authv1.RevokeDeviceRequest{DeviceId: a.DeviceID})
	if connect.CodeOf(err) == connect.CodeNotFound {
		return nil // already revoked
	}
	return err
}
