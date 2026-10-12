package core

// Scheduled messages ("send later"). Because of end-to-end encryption the
// server cannot send for you: a scheduled message is kept as a "sched" item
// in the account's self-group (so every device of the account has it and
// the history backup carries it) and goes out at its time from whichever
// device of the account is running (Android also in the background). It
// keeps its id, so if two devices send it, recipients store it once.

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/kantracity/kantrae2e/client/store"
)

// Scheduled is one message waiting to be sent.
type Scheduled struct {
	ID      string `json:"-"`
	GroupID string `json:"g"`
	Text    string `json:"t"`
	At      int64  `json:"at"` // unix ms
	ReplyTo string `json:"re,omitempty"`
	Silent  bool   `json:"silent,omitempty"`
}

// Schedule queues a text message to be sent at a time.
func (c *Client) Schedule(ctx context.Context, groupID, text string, at time.Time, opt SendOptions) (*Scheduled, error) {
	if strings.TrimSpace(text) == "" {
		return nil, errors.New("empty message")
	}
	if at.Before(time.Now().Add(-time.Minute)) {
		return nil, errors.New("the time is in the past")
	}
	s := Scheduled{GroupID: groupID, Text: text, At: at.UnixMilli(), ReplyTo: opt.ReplyTo, Silent: opt.Silent}
	b, _ := json.Marshal(s)
	if err := c.refreshToken(ctx); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.requireLogin(); err != nil {
		return nil, err
	}
	m, err := c.sendLocked(ctx, selfGroupID(c.acct.UserID), payload{Type: "sched", Body: string(b)})
	if err != nil {
		return nil, err
	}
	s.ID = m.UID
	return &s, nil
}

// ScheduledIn lists the scheduled messages of a chat ("" = all), soonest first.
func (c *Client) ScheduledIn(ctx context.Context, groupID string) ([]Scheduled, error) {
	self := c.SelfGroupID()
	if self == "" {
		return nil, ErrNotLoggedIn
	}
	ms, err := c.st.Messages(ctx, self, 2000)
	if err != nil {
		return nil, err
	}
	var out []Scheduled
	for _, m := range ms {
		if m.Kind != "sched" {
			continue
		}
		var s Scheduled
		if json.Unmarshal([]byte(m.Body), &s) != nil || (groupID != "" && s.GroupID != groupID) {
			continue
		}
		s.ID = m.UID
		out = append(out, s)
	}
	for i := 1; i < len(out); i++ { // few items: insertion sort by time
		for j := i; j > 0 && out[j].At < out[j-1].At; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out, nil
}

// CancelScheduled deletes a scheduled message (on all devices).
func (c *Client) CancelScheduled(ctx context.Context, id string) error {
	return c.DeleteMessage(ctx, c.SelfGroupID(), id, true)
}

// SendScheduledNow sends a scheduled message right away.
func (c *Client) SendScheduledNow(ctx context.Context, id string) (*store.Message, error) {
	list, err := c.ScheduledIn(ctx, "")
	if err != nil {
		return nil, err
	}
	for _, s := range list {
		if s.ID == id {
			return c.sendScheduled(ctx, s)
		}
	}
	return nil, ErrNoMessage
}

func (c *Client) sendScheduled(ctx context.Context, s Scheduled) (*store.Message, error) {
	// Already sent by another device of ours (same id)?
	if m, err := c.st.Message(ctx, s.GroupID, s.ID); err == nil && m != nil {
		_ = c.CancelScheduled(ctx, s.ID)
		return m, nil
	}
	if err := c.refreshToken(ctx); err != nil {
		return nil, err
	}
	c.mu.Lock()
	m, err := c.sendLocked(ctx, s.GroupID, payload{ID: s.ID, Type: "text", Body: s.Text, ReplyTo: s.ReplyTo,
		Extra: SendOptions{Silent: s.Silent}.extra()})
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	_ = c.CancelScheduled(ctx, s.ID)
	// Sent in the background: show it like a message of another device,
	// and tell the UI the scheduled list changed.
	c.emit(Event{Type: EventMessage, GroupID: s.GroupID, Message: m})
	c.emit(Event{Type: EventMessageUpdated, GroupID: c.SelfGroupID(), Detail: s.ID})
	return m, nil
}

// RunScheduler sends due scheduled messages until ctx ends. Call it once
// per open account (it is cheap: a local query every 15 seconds).
func (c *Client) RunScheduler(ctx context.Context) {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if c.Account() == nil {
			continue
		}
		list, err := c.ScheduledIn(ctx, "")
		if err != nil {
			continue
		}
		now := time.Now().UnixMilli()
		for _, s := range list {
			if s.At > now {
				break
			}
			// A short random delay keeps two running devices from both
			// sending in the same instant (the duplicate would be dropped
			// anyway, by its id).
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Duration(rand.IntN(3000)) * time.Millisecond):
			}
			if _, err := c.sendScheduled(ctx, s); err != nil {
				c.logf("scheduled message %s: %v", s.ID, err)
			}
		}
	}
}
