package core

// Presence ("online" / "last seen") and the block list: thin wrappers over
// the delivery and auth services (see the server for the rules).

import (
	"context"
	"time"

	authv1 "github.com/kantracity/kantrae2e/gen/kantra/auth/v1"
	deliveryv1 "github.com/kantracity/kantrae2e/gen/kantra/delivery/v1"
)

// Presence of a user.
type Presence struct {
	Online   bool
	LastSeen time.Time // zero: unknown or hidden
	Hidden   bool      // "last seen recently"
}

// Visibility of one's last seen time.
const (
	SeenEverybody = "everybody"
	SeenChats     = "chats"
	SeenNobody    = "nobody"
)

// SetActive tells the server the app is open (call every minute) or closed.
func (c *Client) SetActive(ctx context.Context, active bool) error {
	if err := c.refreshToken(ctx); err != nil {
		return err
	}
	_, err := c.del.SetActive(ctx, &deliveryv1.SetActiveRequest{Active: active})
	return err
}

// Presence returns the presence of users.
func (c *Client) Presence(ctx context.Context, userIDs ...string) (map[string]Presence, error) {
	if err := c.refreshToken(ctx); err != nil {
		return nil, err
	}
	r, err := c.del.GetPresence(ctx, &deliveryv1.GetPresenceRequest{UserIds: userIDs})
	if err != nil {
		return nil, err
	}
	out := map[string]Presence{}
	for _, p := range r.Presence {
		pr := Presence{Online: p.Online, Hidden: p.Hidden}
		if p.LastSeen > 0 {
			pr.LastSeen = time.Unix(p.LastSeen, 0)
		}
		out[p.UserId] = pr
	}
	return out, nil
}

var visNames = map[deliveryv1.PresenceVisibility]string{
	deliveryv1.PresenceVisibility_PRESENCE_VISIBILITY_UNSPECIFIED: SeenEverybody,
	deliveryv1.PresenceVisibility_PRESENCE_VISIBILITY_EVERYBODY:   SeenEverybody,
	deliveryv1.PresenceVisibility_PRESENCE_VISIBILITY_CHATS:       SeenChats,
	deliveryv1.PresenceVisibility_PRESENCE_VISIBILITY_NOBODY:      SeenNobody,
}

// LastSeenPrivacy returns who sees this account's last seen time.
func (c *Client) LastSeenPrivacy(ctx context.Context) (string, error) {
	if err := c.refreshToken(ctx); err != nil {
		return "", err
	}
	r, err := c.del.GetPresencePrivacy(ctx, &deliveryv1.GetPresencePrivacyRequest{})
	if err != nil {
		return "", err
	}
	return visNames[r.Visibility], nil
}

// SetLastSeenPrivacy: SeenEverybody, SeenChats or SeenNobody.
func (c *Client) SetLastSeenPrivacy(ctx context.Context, who string) error {
	v := deliveryv1.PresenceVisibility_PRESENCE_VISIBILITY_EVERYBODY
	switch who {
	case SeenChats:
		v = deliveryv1.PresenceVisibility_PRESENCE_VISIBILITY_CHATS
	case SeenNobody:
		v = deliveryv1.PresenceVisibility_PRESENCE_VISIBILITY_NOBODY
	}
	if err := c.refreshToken(ctx); err != nil {
		return err
	}
	_, err := c.del.SetPresencePrivacy(ctx, &deliveryv1.SetPresencePrivacyRequest{Visibility: v})
	return err
}

// Block / Unblock a user; Blocked lists the blocked user ids.
func (c *Client) Block(ctx context.Context, userID string) error {
	if err := c.refreshToken(ctx); err != nil {
		return err
	}
	_, err := c.auth.BlockUser(ctx, &authv1.BlockUserRequest{UserId: userID})
	return err
}

func (c *Client) Unblock(ctx context.Context, userID string) error {
	if err := c.refreshToken(ctx); err != nil {
		return err
	}
	_, err := c.auth.UnblockUser(ctx, &authv1.UnblockUserRequest{UserId: userID})
	return err
}

func (c *Client) Blocked(ctx context.Context) ([]string, error) {
	if err := c.refreshToken(ctx); err != nil {
		return nil, err
	}
	r, err := c.auth.ListBlocked(ctx, &authv1.ListBlockedRequest{})
	if err != nil {
		return nil, err
	}
	return r.UserIds, nil
}
