package core

// Calls (1:1 and group). The media goes peer to peer over WebRTC (DTLS-SRTP,
// relayed by the server's TURN only when needed). The signaling — offers,
// answers, joins, hang-ups — travels as MLS application messages in the
// chat itself, so the server cannot read or forge it, and the DTLS
// fingerprints inside the SDP are authenticated by MLS: a server in the
// middle cannot substitute its own keys. Signals are not stored, not backed
// up and not shown as messages.

import (
	"context"
	"errors"
	"time"

	authv1 "github.com/kantracity/kantrae2e/gen/kantra/auth/v1"
)

// EventSignal: a call signal from another device (GroupID, UserID,
// DeviceID, Detail = the signal body, Count = sent at, unix seconds).
const EventSignal EventType = "signal"

// MaxSignal bounds a signal's size (an SDP with candidates is a few KB).
const MaxSignal = 64 << 10

// IceServer is a STUN/TURN server for WebRTC.
type IceServer struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username,omitempty"`
	Credential string   `json:"credential,omitempty"`
}

// IceServers returns the call servers of the server this account is on.
func (c *Client) IceServers(ctx context.Context) ([]IceServer, error) {
	if err := c.refreshToken(ctx); err != nil {
		return nil, err
	}
	r, err := c.auth.GetIceServers(ctx, &authv1.GetIceServersRequest{})
	if err != nil {
		return nil, err
	}
	var out []IceServer
	for _, s := range r.Servers {
		out = append(out, IceServer{URLs: s.Urls, Username: s.Username, Credential: s.Credential})
	}
	return out, nil
}

// SendSignal sends a call signal to the other members of a chat (all of
// them decrypt it; the body says whom it is for).
func (c *Client) SendSignal(ctx context.Context, groupID, body string) error {
	if len(body) == 0 || len(body) > MaxSignal {
		return errors.New("bad signal")
	}
	if GroupKind(groupID) == KindSelf {
		return errors.New("no calls in the self-group")
	}
	if err := c.refreshToken(ctx); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.requireLogin(); err != nil {
		return err
	}
	_, err := c.sendLocked(ctx, groupID, payload{Type: "signal", Body: body})
	return err
}

// signalEvent turns a received signal into an event; signals from this very
// device are dropped.
func (c *Client) signalEvent(groupID, uid, did string, pl *payload) *Event {
	if c.acct != nil && uid == c.acct.UserID && did == c.acct.DeviceID {
		return nil
	}
	if len(pl.Body) > MaxSignal {
		return nil
	}
	ts := pl.TS / 1000
	if ts == 0 {
		ts = time.Now().Unix()
	}
	return &Event{Type: EventSignal, GroupID: groupID, UserID: uid, DeviceID: did, Detail: pl.Body, Count: int(ts)}
}
