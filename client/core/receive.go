package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"
	"google.golang.org/protobuf/proto"

	"github.com/kantracity/kantrae2e/client/mls"
	"github.com/kantracity/kantrae2e/client/store"
	deliveryv1 "github.com/kantracity/kantrae2e/gen/kantra/delivery/v1"
)

// Sync pulls and processes all pending envelopes (no WebSocket needed).
func (c *Client) Sync(ctx context.Context) error {
	if err := c.refreshToken(ctx); err != nil {
		return err
	}
	c.mu.Lock()
	if err := c.requireLogin(); err != nil {
		c.mu.Unlock()
		return err
	}
	err := c.syncLocked(ctx)
	c.mu.Unlock()
	if err != nil {
		return err
	}
	c.maintain(ctx)
	return nil
}

// maintain brings this device's memberships up to date: an approved device
// joins the self-group and every group of its account it is not in yet; a
// pending one repeats its request for approval now and then.
func (c *Client) maintain(ctx context.Context) {
	ok, err := c.IsApproved(ctx)
	if err != nil {
		c.logf("device status: %v", err)
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.requireLogin() != nil {
		return
	}
	if !ok {
		if err := c.requestLinkLocked(ctx, false); err != nil {
			c.logf("link request: %v", err)
		}
		return
	}
	if err := c.ensureSelfGroupLocked(ctx); err != nil {
		c.logf("self group: %v", err)
	}
	if _, _, err := c.joinMyGroupsLocked(ctx); err != nil {
		c.logf("join groups: %v", err)
	}
	c.flushDeferred()
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
		c.flushDeferred()
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
	var histEv *Event   // counted inside the transaction, emitted after it
	var observed string // group whose member keys must be checked

	switch e.Type {
	case deliveryv1.MessageType_MESSAGE_TYPE_WELCOME:
		gid, err := c.m.JoinGroup(e.Payload)
		if err != nil {
			c.logf("welcome for group %s not usable: %v", e.GroupId, err)
			break
		}
		epoch, members, _ := c.m.GroupInfo(gid)
		seen := c.seenWrites(string(gid), members, e.CreatedAt*1000, "")
		peer := ""
		if GroupKind(string(gid)) == KindDirect {
			peer = c.directPeer(members)
		}
		// Added to the self-group = approved by an own device after the user
		// compared codes: the account's devices in it are verified.
		var trusted []store.DeviceKey
		if GroupKind(string(gid)) == KindSelf {
			trusted = c.ownDeviceKeysLocked(ctx, gid)
		}
		// Added back to a group this account had left (by someone else, not
		// as one more device of ours): it is a member again.
		rejoined := GroupKind(string(gid)) == KindGroup && !c.otherDevicesOf(members, c.acct.UserID, c.acct.DeviceID)
		writes = func(tx *store.Tx) error {
			if err := seen(tx); err != nil {
				return err
			}
			if rejoined {
				if err := tx.ResetChatFlags(string(gid), false, true); err != nil {
					return err
				}
			}
			for _, k := range trusted {
				if err := tx.PutDeviceKey(k); err != nil {
					return err
				}
			}
			return tx.UpsertGroup(store.Group{ID: string(gid), Peer: peer, Epoch: epoch, Active: true, CreatedAt: time.Now().Unix()})
		}
		events = append(events, Event{Type: EventGroupJoined, GroupID: string(gid)})
		if trusted == nil {
			observed = string(gid)
		}
		// Joined as an additional device of this account: fetch what we missed.
		if GroupKind(string(gid)) != KindSelf && c.otherDevicesOf(members, c.acct.UserID, c.acct.DeviceID) {
			c.deferHistoryRequestLocked(string(gid))
		}
		// A KeyPackage was consumed; top up in the background.
		go func() {
			if err := c.EnsureKeyPackages(context.Background()); err != nil {
				c.logf("key package refill: %v", err)
			}
		}()

	case deliveryv1.MessageType_MESSAGE_TYPE_JOIN_REQUEST:
		c.handleJoinRequestLocked(e)

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
				_, members, _ := c.m.GroupInfo([]byte(e.GroupId))
				skip := ""
				if p.External && !c.checkExternalJoinLocked(e.GroupId, p.Sender, members) {
					skip, _ = splitIdentity(p.Sender)
					events = append(events, Event{Type: EventSecurity, GroupID: e.GroupId,
						Err: errors.New("a device of a user that was not in the group joined by itself; removing it")})
				}
				if GroupKind(e.GroupId) == KindSelf {
					c.dropResolvedLinksLocked(members)
				}
				seen := c.seenWrites(e.GroupId, members, e.CreatedAt*1000, skip)
				writes = func(tx *store.Tx) error {
					if err := seen(tx); err != nil {
						return err
					}
					return tx.UpsertGroup(store.Group{ID: e.GroupId, Epoch: p.Epoch, Active: true})
				}
				events = append(events, Event{Type: EventGroupUpdated, GroupID: e.GroupId})
				observed = e.GroupId
			}
		case mls.KindApplication:
			var pl payload
			if err := json.Unmarshal(p.Plaintext, &pl); err != nil || pl.ID == "" {
				c.logf("malformed application payload in %s", e.GroupId)
				break
			}
			uid, did := splitIdentity(p.Sender)
			if pl.Type == "history_request" || pl.Type == "history_share" {
				if pl.HistReq != nil && pl.Type == "history_request" {
					c.handleHistoryRequestLocked(e.GroupId, uid, did, pl.HistReq)
				}
				if pl.HistShare != nil && pl.Type == "history_share" {
					writes, histEv = c.handleHistoryShareLocked(ctx, e.GroupId, uid, pl.HistShare)
				}
				break
			}
			if pl.Type == "account_key" {
				if pl.AccountKey != nil {
					writes = c.handleAccountKeyLocked(ctx, e.GroupId, uid, pl.AccountKey)
				}
				break
			}
			if GroupKind(e.GroupId) == KindSelf {
				// Saved GIFs (gifs.go) are the only messages there, and only
				// from the account's own devices.
				if uid != c.acct.UserID || (pl.Type != "media" && pl.Type != "delete") {
					break
				}
			}
			if w, ev := c.applyChatControlLocked(e.GroupId, uid, &pl); ev != nil {
				writes = w
				events = append(events, *ev)
				c.scheduleBackup()
				break
			}
			if g, err := c.st.Group(ctx, e.GroupId); err == nil && g.LeftAt > 0 {
				break // left this group: its messages are not ours any more
			}
			if w, ev := c.applyControlLocked(e.GroupId, uid, &pl); ev != nil {
				writes = w
				events = append(events, *ev)
				if pl.Type != "read" {
					c.scheduleBackup()
				}
				break
			}
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
				Kind: pl.Type, Body: pl.Body, SentAt: pl.TS, ReplyTo: pl.ReplyTo, FwdFrom: pl.Fwd,
				// Sent from another device of this account: ours.
				Outgoing: uid == c.acct.UserID}
			if pl.Media != nil {
				b, _ := json.Marshal(pl.Media)
				msg.Body = string(b)
			}
			writes = func(tx *store.Tx) error { return tx.InsertMessage(msg) }
			events = append(events, Event{Type: EventMessage, GroupID: e.GroupId, Message: &msg})
			c.scheduleBackup()
		}
	}

	var observe func(*store.Tx) error
	if observed != "" {
		var evs []Event
		observe, evs = c.observeGroupLocked(ctx, observed)
		events = append(events, evs...)
	}
	err = c.persistLocked(ctx, func(tx *store.Tx) error {
		for _, w := range []func(*store.Tx) error{writes, observe} {
			if w != nil {
				if err := w(tx); err != nil {
					return err
				}
			}
		}
		return tx.MarkProcessed(e.Id)
	})
	if err != nil {
		return err
	}
	if histEv != nil {
		events = append(events, *histEv)
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
	u += "/v1/ws"
	if ka := time.Duration(c.keepAlive.Load()); ka > 0 {
		u += fmt.Sprintf("?keepalive=%d", int(ka/time.Second))
	}
	return u
}

// minKeepAlive is the floor of the adaptive keep-alive.
const minKeepAlive = time.Minute

// Reconnect drops the realtime connection (if any) and connects again right
// away, skipping the backoff: call it when the network comes back.
func (c *Client) Reconnect() {
	select {
	case c.kick <- struct{}{}:
	default:
	}
}

// Listen keeps a WebSocket to the delivery service open and processes
// messages as they arrive, reconnecting with backoff until ctx is done.
func (c *Client) Listen(ctx context.Context) error {
	backoff := time.Second
	for {
		connCtx, cancel := context.WithCancel(ctx)
		kicked := make(chan struct{})
		go func() {
			select {
			case <-c.kick:
				close(kicked)
				cancel()
			case <-connCtx.Done():
			}
		}()
		start := time.Now()
		err := c.listenOnce(connCtx)
		cancel()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		c.emit(Event{Type: EventDisconnected, Err: err})
		select {
		case <-kicked:
			backoff = time.Second
			continue // reconnect immediately
		default:
		}
		if time.Since(start) > time.Minute {
			backoff = time.Second // it worked for a while: not a persistent failure
		}
		c.logf("realtime connection lost: %v (retry in %s)", err, backoff)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-c.kick:
			backoff = time.Second
			continue
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

var errSilent = errors.New("realtime connection silent for too long")

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
		c.maintain(ctx)
	}()
	for {
		readCtx, cancel := ctx, context.CancelFunc(func() {})
		ka := time.Duration(c.keepAlive.Load())
		if ka > 0 {
			readCtx, cancel = context.WithTimeout(ctx, 2*ka+30*time.Second)
		}
		_, data, err := conn.Read(readCtx)
		silent := ka > 0 && readCtx.Err() == context.DeadlineExceeded && ctx.Err() == nil
		cancel()
		if silent {
			// The network dropped the idle connection: ask for heartbeats
			// more often from now on.
			if next := max(ka/2, minKeepAlive); next < ka {
				c.keepAlive.Store(int64(next))
				c.logf("keep-alive lowered to %s", next)
			}
			return errSilent
		}
		if err != nil {
			return err
		}
		var e deliveryv1.Envelope
		if err := proto.Unmarshal(data, &e); err != nil {
			continue
		}
		if e.Ephemeral {
			if e.Type == deliveryv1.MessageType_MESSAGE_TYPE_TYPING && e.SenderUserId != "" && e.SenderUserId != c.userID() {
				c.emit(Event{Type: EventTyping, GroupID: e.GroupId, UserID: e.SenderUserId})
			}
			continue // not queued: nothing to ack
		}
		if e.Id == 0 {
			continue // heartbeat
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
		c.flushDeferred()
	}
}
