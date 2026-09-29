package core

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"
	"google.golang.org/protobuf/proto"

	deliveryv1 "github.com/kantracity/kantrae2e/gen/kantra/delivery/v1"
	"github.com/kantracity/kantrae2e/client/mls"
	"github.com/kantracity/kantrae2e/client/store"
)

// Sync pulls and processes all pending envelopes (no WebSocket needed).
func (c *Client) Sync(ctx context.Context) error {
	if err := c.refreshToken(ctx); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.requireLogin(); err != nil {
		return err
	}
	return c.syncLocked(ctx)
}

func (c *Client) syncLocked(ctx context.Context) error {
	for {
		r, err := c.del.FetchPending(ctx, &deliveryv1.FetchPendingRequest{Limit: 100})
		if err != nil {
			return err
		}
		if len(r.Envelopes) == 0 {
			return nil
		}
		var ids []int64
		for _, e := range r.Envelopes {
			if err := c.handleLocked(ctx, e); err != nil {
				return err
			}
			ids = append(ids, e.Id)
		}
		if _, err := c.del.Ack(ctx, &deliveryv1.AckRequest{Ids: ids}); err != nil {
			return err
		}
	}
}

// handleLocked processes one envelope and persists the result atomically.
// It only returns an error for local failures (storage); undecryptable or
// irrelevant messages are logged and marked processed.
func (c *Client) handleLocked(ctx context.Context, e *deliveryv1.Envelope) error {
	done, err := c.st.Processed(ctx, e.Id)
	if err != nil || done {
		return err
	}
	var events []Event
	var writes func(*store.Tx) error

	switch e.Type {
	case deliveryv1.MessageType_MESSAGE_TYPE_WELCOME:
		gid, err := c.m.JoinGroup(e.Payload)
		if err != nil {
			c.logf("welcome for group %s not usable: %v", e.GroupId, err)
			break
		}
		epoch, _, _ := c.m.GroupInfo(gid)
		writes = func(tx *store.Tx) error {
			return tx.UpsertGroup(store.Group{ID: string(gid), Epoch: epoch, Active: true, CreatedAt: time.Now().Unix()})
		}
		events = append(events, Event{Type: EventGroupJoined, GroupID: string(gid)})
		// A KeyPackage was consumed; top up in the background.
		go func() {
			if err := c.EnsureKeyPackages(context.Background()); err != nil {
				c.logf("key package refill: %v", err)
			}
		}()

	case deliveryv1.MessageType_MESSAGE_TYPE_COMMIT, deliveryv1.MessageType_MESSAGE_TYPE_APPLICATION:
		p, err := c.m.Process([]byte(e.GroupId), e.Payload)
		if errors.Is(err, mls.ErrNotFound) {
			c.logf("message for unknown group %s dropped", e.GroupId)
			break
		}
		if err != nil {
			c.logf("cannot process message in %s (epoch %d): %v", e.GroupId, e.Epoch, err)
			break
		}
		switch p.Kind {
		case mls.KindCommit:
			if p.Removed {
				gid := []byte(e.GroupId)
				_ = c.m.ForgetGroup(gid)
				writes = func(tx *store.Tx) error {
					if err := tx.DeleteMLSGroup(gid); err != nil {
						return err
					}
					return tx.UpsertGroup(store.Group{ID: e.GroupId, Epoch: p.Epoch, Active: false})
				}
				events = append(events, Event{Type: EventRemoved, GroupID: e.GroupId})
			} else {
				writes = func(tx *store.Tx) error {
					return tx.UpsertGroup(store.Group{ID: e.GroupId, Epoch: p.Epoch, Active: true})
				}
				events = append(events, Event{Type: EventGroupUpdated, GroupID: e.GroupId})
			}
		case mls.KindApplication:
			var pl payload
			if err := json.Unmarshal(p.Plaintext, &pl); err != nil || pl.ID == "" {
				c.logf("malformed application payload in %s", e.GroupId)
				break
			}
			uid, did := splitIdentity(p.Sender)
			if pl.Type == "meta" {
				if pl.Meta != nil {
					writes = func(tx *store.Tx) error {
						return tx.UpsertGroup(store.Group{ID: e.GroupId, Name: pl.Meta.Name, Epoch: p.Epoch, Active: true})
					}
					events = append(events, Event{Type: EventGroupUpdated, GroupID: e.GroupId})
				}
				break
			}
			msg := store.Message{UID: pl.ID, GroupID: e.GroupId, SenderUser: uid, SenderDevice: did,
				Kind: pl.Type, Body: pl.Body, SentAt: pl.TS}
			if pl.Media != nil {
				b, _ := json.Marshal(pl.Media)
				msg.Body = string(b)
			}
			writes = func(tx *store.Tx) error { return tx.InsertMessage(msg) }
			events = append(events, Event{Type: EventMessage, GroupID: e.GroupId, Message: &msg})
		}
	}

	err = c.persistLocked(ctx, func(tx *store.Tx) error {
		if writes != nil {
			if err := writes(tx); err != nil {
				return err
			}
		}
		return tx.MarkProcessed(e.Id)
	})
	if err != nil {
		return err
	}
	for _, ev := range events {
		c.emit(ev)
	}
	return nil
}

func (c *Client) wsURL() string {
	u := c.opts.ServerURL
	switch {
	case strings.HasPrefix(u, "https://"):
		u = "wss://" + strings.TrimPrefix(u, "https://")
	case strings.HasPrefix(u, "http://"):
		u = "ws://" + strings.TrimPrefix(u, "http://")
	}
	return u + "/v1/ws"
}

// Listen keeps a WebSocket to the delivery service open and processes
// messages as they arrive, reconnecting with backoff until ctx is done.
func (c *Client) Listen(ctx context.Context) error {
	backoff := time.Second
	for {
		err := c.listenOnce(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		c.emit(Event{Type: EventDisconnected, Err: err})
		c.logf("realtime connection lost: %v (retry in %s)", err, backoff)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func (c *Client) listenOnce(ctx context.Context) error {
	if err := c.refreshToken(ctx); err != nil {
		return err
	}
	conn, _, err := websocket.Dial(ctx, c.wsURL(), &websocket.DialOptions{
		HTTPClient: c.hc,
		HTTPHeader: http.Header{"Authorization": {"Bearer " + c.currentToken()}},
	})
	if err != nil {
		return err
	}
	defer conn.CloseNow()
	conn.SetReadLimit(4 << 20)
	c.emit(Event{Type: EventConnected})
	go func() {
		if err := c.EnsureKeyPackages(ctx); err != nil {
			c.logf("key package refill: %v", err)
		}
	}()
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return err
		}
		var e deliveryv1.Envelope
		if err := proto.Unmarshal(data, &e); err != nil {
			continue
		}
		c.mu.Lock()
		err = c.handleLocked(ctx, &e)
		c.mu.Unlock()
		if err != nil {
			return err
		}
		ack, _ := proto.Marshal(&deliveryv1.ClientFrame{Frame: &deliveryv1.ClientFrame_Ack{Ack: &deliveryv1.Ack{Ids: []int64{e.Id}}}})
		if err := conn.Write(ctx, websocket.MessageBinary, ack); err != nil {
			return err
		}
	}
}
