package core

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"time"

	"connectrpc.com/connect"

	"github.com/kantracity/kantrae2e/client/store"
)

// Outbox: messages written without a connection.
//
// When sending a text, media or poll message fails because the server
// cannot be reached, the message is stored as pending (with its plaintext
// payload in the outbox table) instead of failing: it is on screen with a
// clock and leaves by itself once the connection is back, also after a
// restart. It keeps its id, so a copy that did reach the server before
// the error is not shown twice. If the server refuses it for good (no
// longer a member, a key conflict), it is marked failed: the person may
// retry or delete it.

// sendTimeout bounds one attempt to hand a message to the server; a send
// that hangs (a dead network) goes to the outbox instead of blocking.
const sendTimeout = 15 * time.Second

// queueable: kinds of messages written by a person (kept when offline).
func queueable(t string) bool { return t == "text" || t == "media" || t == "poll" }

// transient reports whether err means "the server could not be reached"
// (worth retrying later) rather than a refusal.
func transient(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var ne net.Error
	if errors.As(err, &ne) {
		return true
	}
	switch connect.CodeOf(err) {
	case connect.CodeUnavailable, connect.CodeDeadlineExceeded, connect.CodeResourceExhausted, connect.CodeAborted:
		return true
	}
	return false
}

// refreshForSend refreshes the token when due; being offline is not an
// error here (the message goes to the outbox).
func (c *Client) refreshForSend(ctx context.Context) error {
	if err := c.refreshToken(ctx); err != nil && !transient(err) {
		return err
	}
	return nil
}

// queueLocked stores p as a pending message and wakes the outbox.
func (c *Client) queueLocked(ctx context.Context, groupID string, p payload, plain []byte) (*store.Message, error) {
	msg := c.localMessage(groupID, p)
	if err := c.st.Update(ctx, func(tx *store.Tx) error { return tx.QueueMessage(*msg, string(plain)) }); err != nil {
		return nil, err
	}
	msg.Pending = store.PendingWaiting
	c.logf("no connection: message queued in %s", groupID)
	c.KickOutbox()
	return msg, nil
}

// KickOutbox makes RunOutbox try to send the queued messages now.
func (c *Client) KickOutbox() {
	select {
	case c.outboxKick <- struct{}{}:
	default:
	}
}

// RetryMessage puts a failed message back in the outbox.
func (c *Client) RetryMessage(ctx context.Context, groupID, uid string) error {
	ok := false
	if err := c.st.Update(ctx, func(tx *store.Tx) (err error) {
		ok, err = tx.OutboxRetry(groupID, uid)
		return err
	}); err != nil {
		return err
	}
	if !ok {
		return errors.New("message is not waiting to be sent")
	}
	c.emit(Event{Type: EventMessageUpdated, GroupID: groupID, Detail: uid})
	c.KickOutbox()
	return nil
}

// cancelUnsent deletes a message that never left this device; false if it
// was sent (or is not ours).
func (c *Client) cancelUnsent(ctx context.Context, groupID, uid string) (bool, error) {
	ok := false
	err := c.st.Update(ctx, func(tx *store.Tx) (err error) {
		ok, err = tx.OutboxCancel(groupID, uid)
		return err
	})
	return ok, err
}

// RunOutbox sends queued messages until ctx ends: when kicked (a new
// queued message, the realtime connection is back) and, while messages
// wait, with a growing pause between attempts. Call it once per account.
func (c *Client) RunOutbox(ctx context.Context) {
	const minWait, maxWait = 3 * time.Second, time.Minute
	wait := minWait
	for {
		left := c.flushOutbox(ctx)
		var timer <-chan time.Time
		if left {
			timer = time.After(wait)
			wait = min(wait*2, maxWait)
		} else {
			wait = minWait
		}
		select {
		case <-ctx.Done():
			return
		case <-c.outboxKick:
			wait = minWait
		case <-timer:
		}
	}
}

// flushOutbox sends the queued messages in order; true if some are still
// waiting (no connection).
func (c *Client) flushOutbox(ctx context.Context) bool {
	if c.Account() == nil {
		return false
	}
	items, err := c.st.Outbox(ctx)
	if err != nil || len(items) == 0 {
		return false
	}
	if err := c.refreshToken(ctx); err != nil {
		return transient(err)
	}
	for _, it := range items {
		if ctx.Err() != nil {
			return true
		}
		var p payload
		if err := json.Unmarshal([]byte(it.Payload), &p); err != nil {
			_ = c.st.Update(ctx, func(tx *store.Tx) error { return tx.OutboxFailed(it.GroupID, it.UID, "broken payload") })
			continue
		}
		c.mu.Lock()
		err := c.requireLogin()
		if err == nil {
			err = c.deliverLocked(ctx, it.GroupID, []byte(it.Payload))
		}
		c.mu.Unlock()
		switch {
		case transient(err):
			return true
		case err != nil:
			c.logf("queued message in %s refused: %v", it.GroupID, err)
			_ = c.st.Update(ctx, func(tx *store.Tx) error { return tx.OutboxFailed(it.GroupID, it.UID, err.Error()) })
		default:
			_ = c.st.Update(ctx, func(tx *store.Tx) error { return tx.OutboxSent(it.GroupID, it.UID) })
			c.scheduleBackup()
		}
		c.emit(Event{Type: EventMessageUpdated, GroupID: it.GroupID, Detail: it.UID})
	}
	return false
}
