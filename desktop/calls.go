package main

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/kantracity/kantrae2e/client/core"
)

// ---- calls (core/calls.go; the WebRTC side is frontend/src/lib/call.svelte.ts) ----

// IceServers returns the STUN/TURN servers for RTCPeerConnection.
func (m *Messenger) IceServers() ([]core.IceServer, error) {
	c, err := m.loggedIn()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, err := c.IceServers(ctx)
	if err != nil {
		return []core.IceServer{}, nil // older server without calls support: direct connections only
	}
	return s, nil
}

// SendSignal sends a call signal (JSON) to the other members of a chat.
func (m *Messenger) SendSignal(chatID, body string) error {
	c, err := m.loggedIn()
	if err != nil {
		return err
	}
	return friendly(c.SendSignal(context.Background(), chatID, body))
}

// CallSignal is a received call signal, as the UI gets it ("signal" event,
// and PendingCall after a cold start).
type CallSignal struct {
	ChatID   string `json:"chatId"`
	UserID   string `json:"userId"`
	DeviceID string `json:"deviceId"`
	Name     string `json:"name"`
	Body     string `json:"body"`
	At       int64  `json:"at"` // unix seconds (sender's clock)
}

// ringing: the newest call invitation per account, so that a UI started by
// tapping the "incoming call" notification can still answer it.
var ringing sync.Map // slot file -> CallSignal

const ringFor = 45 * time.Second

// PendingCall returns a call that is ringing right now (or nothing).
func (m *Messenger) PendingCall() *CallSignal {
	m.mu.Lock()
	s := m.active
	m.mu.Unlock()
	if s == nil {
		return nil
	}
	v, ok := ringing.Load(s.file)
	if !ok {
		return nil
	}
	sig := v.(CallSignal)
	if time.Since(time.Unix(sig.At, 0)) > ringFor {
		ringing.Delete(s.file)
		return nil
	}
	return &sig
}

// onSignal handles a call signal from the core (called from onCoreEvent).
func (m *Messenger) onSignal(s *slot, isActive bool, e core.Event) {
	if s.c == nil {
		return
	}
	sig := CallSignal{ChatID: e.GroupID, UserID: e.UserID, DeviceID: e.DeviceID, Body: e.Detail, At: int64(e.Count)}
	var head struct {
		T     string `json:"t"`
		Call  string `json:"call"`
		Video bool   `json:"video"`
	}
	_ = json.Unmarshal([]byte(e.Detail), &head)
	fresh := time.Since(time.Unix(sig.At, 0)) < ringFor
	switch head.T {
	case "invite":
		if !fresh || e.UserID == s.userID {
			break
		}
		sig.Name = core.DisplayName(s.c.CachedProfile(context.Background(), e.UserID))
		ringing.Store(s.file, sig)
		m.mu.Lock()
		fg := m.foreground && isActive
		m.mu.Unlock()
		if !fg && clientPref(s.c, prefNotify) {
			title := "Kantra"
			if g, err := s.c.ResolveGroup(context.Background(), e.GroupID); err == nil {
				title = m.toChat(s.c, *g).Name
			}
			body := "📞 Входящий звонок"
			if head.Video {
				body = "📹 Входящий видеозвонок"
			}
			if title != sig.Name && sig.Name != "" {
				body += " · " + sig.Name
			}
			m.notif.Show(Notice{Key: noticeKey(s.file, e.GroupID), ChatID: e.GroupID, Title: title, Body: body})
		}
	case "end", "leave", "decline", "join":
		if v, ok := ringing.Load(s.file); ok {
			var r struct {
				Call string `json:"call"`
			}
			_ = json.Unmarshal([]byte(v.(CallSignal).Body), &r)
			// The caller hung up, or another device of ours answered/declined.
			if r.Call == head.Call && (head.T == "end" || e.UserID == s.userID || (head.T == "leave" && e.UserID == v.(CallSignal).UserID)) {
				ringing.Delete(s.file)
				m.notif.Clear(noticeKey(s.file, e.GroupID))
			}
		}
	}
	if !isActive {
		return
	}
	if sig.Name == "" {
		sig.Name = core.DisplayName(s.c.CachedProfile(context.Background(), e.UserID))
	}
	b, _ := json.Marshal(sig)
	m.emit(UIEvent{Type: "signal", ChatID: e.GroupID, Detail: string(b)})
}
