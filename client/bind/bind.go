// Package bind is a frontend-neutral facade over client/core for UI
// toolkits with restricted type systems. It only uses string, int, []byte,
// error and a callback interface, which is what gomobile (`gomobile bind`)
// supports; Wails can bind the same struct directly. Structured results are
// returned as JSON strings.
package bind

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/kantracity/kantrae2e/client/core"
	"github.com/kantracity/kantrae2e/client/store"
)

// EventHandler receives events as JSON:
// {"type":"message","group_id":"...","message":{...}}.
type EventHandler interface {
	OnEvent(eventJSON string)
}

type Messenger struct {
	c      *core.Client
	mu     sync.Mutex
	cancel context.CancelFunc
}

type jsonMessage struct {
	Seq        int64  `json:"seq"`
	ID         string `json:"id"`
	GroupID    string `json:"group_id"`
	SenderUser string `json:"sender_user_id"`
	Sender     string `json:"sender"`
	Kind       string `json:"kind"`
	Body       string `json:"body"`
	SentAt     int64  `json:"sent_at_ms"`
	Outgoing   bool   `json:"outgoing"`
	Origin     string `json:"origin"` // "", "backup" or "shared"
}

func (m *Messenger) toJSON(ctx context.Context, s *store.Message) jsonMessage {
	return jsonMessage{Seq: s.Seq, ID: s.UID, GroupID: s.GroupID, SenderUser: s.SenderUser,
		Sender: m.c.Username(ctx, s.SenderUser), Kind: s.Kind, Body: s.Body, SentAt: s.SentAt, Outgoing: s.Outgoing, Origin: s.Origin}
}

func marshal(v any) (string, error) {
	b, err := json.Marshal(v)
	return string(b), err
}

// Open opens the local database. handler may be nil.
func Open(serverURL, dbPath string, handler EventHandler) (*Messenger, error) {
	m := &Messenger{}
	opts := core.Options{ServerURL: serverURL, DBPath: dbPath}
	if handler != nil {
		opts.OnEvent = func(e core.Event) {
			out := map[string]any{"type": e.Type, "group_id": e.GroupID}
			if e.Message != nil {
				out["message"] = m.toJSON(context.Background(), e.Message)
			}
			if e.Count > 0 {
				out["count"] = e.Count
			}
			if e.Err != nil {
				out["error"] = e.Err.Error()
			}
			s, _ := marshal(out)
			handler.OnEvent(s)
		}
	}
	c, err := core.Open(context.Background(), opts)
	if err != nil {
		return nil, err
	}
	m.c = c
	return m, nil
}

func (m *Messenger) Close() error {
	m.Stop()
	return m.c.Close()
}

// Register returns the seed phrase.
func (m *Messenger) Register(username, password, deviceName string) (string, error) {
	return m.c.Register(context.Background(), username, password, deviceName)
}

// Login adds this device to an existing account, restores history and joins
// the account's groups. Returns {"Restored":n,"Joined":n,"Requested":n}.
func (m *Messenger) Login(username, password, deviceName, seedPhrase string) (string, error) {
	r, err := m.c.Login(context.Background(), username, password, deviceName, seedPhrase)
	if err != nil {
		return "", err
	}
	return marshal(r)
}

// DevicesJSON lists the account's devices.
func (m *Messenger) DevicesJSON() (string, error) {
	d, err := m.c.Devices(context.Background())
	if err != nil {
		return "", err
	}
	return marshal(d)
}

// RevokeDevice revokes another device and removes it from all groups.
func (m *Messenger) RevokeDevice(deviceID string) (int, error) {
	return m.c.RevokeDevice(context.Background(), deviceID)
}

// AccountJSON returns {"UserID","DeviceID","Username"} or "null".
func (m *Messenger) AccountJSON() (string, error) { return marshal(m.c.Account()) }

// Start keeps the realtime connection open in the background.
func (m *Messenger) Start() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	go func() { _ = m.c.Listen(ctx) }()
}

func (m *Messenger) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
}

func (m *Messenger) Sync() error { return m.c.Sync(context.Background()) }

func (m *Messenger) CreateGroup(name string) (string, error) {
	return m.c.CreateGroup(context.Background(), name)
}

// Invite takes a JSON array of usernames.
func (m *Messenger) Invite(groupID, usernamesJSON string) error {
	var users []string
	if err := json.Unmarshal([]byte(usernamesJSON), &users); err != nil {
		return err
	}
	return m.c.Invite(context.Background(), groupID, users...)
}

func (m *Messenger) Remove(groupID, username string) error {
	return m.c.Remove(context.Background(), groupID, username)
}

func (m *Messenger) GroupsJSON() (string, error) {
	gs, err := m.c.Groups(context.Background())
	if err != nil {
		return "", err
	}
	return marshal(gs)
}

func (m *Messenger) MembersJSON(groupID string) (string, error) {
	ms, err := m.c.Members(context.Background(), groupID)
	if err != nil {
		return "", err
	}
	return marshal(ms)
}

func (m *Messenger) MessagesJSON(groupID string, limit int) (string, error) {
	ctx := context.Background()
	ms, err := m.c.Messages(ctx, groupID, limit)
	if err != nil {
		return "", err
	}
	out := make([]jsonMessage, len(ms))
	for i := range ms {
		out[i] = m.toJSON(ctx, &ms[i])
	}
	return marshal(out)
}

func (m *Messenger) SendText(groupID, text string) error {
	_, err := m.c.SendText(context.Background(), groupID, text)
	return err
}

func (m *Messenger) SendFile(groupID, name string, data []byte) error {
	_, err := m.c.SendFile(context.Background(), groupID, name, data)
	return err
}

// DownloadMedia returns the decrypted file of a media message.
func (m *Messenger) DownloadMedia(groupID string, seq int) ([]byte, error) {
	ctx := context.Background()
	ms, err := m.c.Messages(ctx, groupID, 1_000_000)
	if err != nil {
		return nil, err
	}
	for i := range ms {
		if ms[i].Seq == int64(seq) {
			_, data, err := m.c.DownloadMedia(ctx, &ms[i])
			return data, err
		}
	}
	return nil, store.ErrNotFound
}

func (m *Messenger) BackupHistory() (int, error)  { return m.c.BackupHistory(context.Background()) }
func (m *Messenger) RestoreHistory() (int, error) { return m.c.RestoreHistory(context.Background()) }
