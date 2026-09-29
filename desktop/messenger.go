package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/kantracity/kantrae2e/client/core"
	"github.com/kantracity/kantrae2e/client/store"
)

// EventName is the single event channel from Go to the frontend.
const EventName = "kantra"

// UIEvent is pushed to the frontend for everything that happens in the
// background (new messages, joins, security warnings, connection state).
type UIEvent struct {
	Type    string   `json:"type"`
	ChatID  string   `json:"chatId,omitempty"`
	Message *Message `json:"message,omitempty"`
	Detail  string   `json:"detail,omitempty"`
	Count   int      `json:"count,omitempty"`
}

// Account is the logged-in identity of this device.
type Account struct {
	Username string `json:"username"`
	UserID   string `json:"userId"`
	DeviceID string `json:"deviceId"`
}

// State describes what the UI should show on start.
type State struct {
	ServerURL string   `json:"serverUrl"`
	Account   *Account `json:"account"`
	Online    bool     `json:"online"`
	WebMode   bool     `json:"webMode"`
}

type FileInfo struct {
	Name string `json:"name"`
	Size int    `json:"size"`
}

type Message struct {
	Seq      int64     `json:"seq"`
	ID       string    `json:"id"`
	ChatID   string    `json:"chatId"`
	SenderID string    `json:"senderId"`
	Sender   string    `json:"sender"`
	Kind     string    `json:"kind"` // "text" or "file"
	Text     string    `json:"text"`
	File     *FileInfo `json:"file,omitempty"`
	SentAt   int64     `json:"sentAt"` // unix ms
	Outgoing bool      `json:"outgoing"`
	// Origin: "" direct, "backup" restored, "shared" re-sent by another member.
	Origin string `json:"origin"`
}

type Chat struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Active    bool     `json:"active"`
	Last      *Message `json:"last,omitempty"`
	UpdatedAt int64    `json:"updatedAt"` // unix ms
}

type Member struct {
	Username string `json:"username"`
	UserID   string `json:"userId"`
	DeviceID string `json:"deviceId"`
	// "verified", "seen", "conflict" or "self"
	Status string `json:"status"`
}

type DeviceKey struct {
	DeviceID            string `json:"deviceId"`
	Fingerprint         string `json:"fingerprint"`
	Status              string `json:"status"`
	ConflictFingerprint string `json:"conflictFingerprint,omitempty"`
	ThisDevice          bool   `json:"thisDevice"`
}

type Device struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	CreatedAt  int64  `json:"createdAt"`
	Revoked    bool   `json:"revoked"`
	ThisDevice bool   `json:"thisDevice"`
}

type LoginResult struct {
	Restored  int `json:"restored"`
	Joined    int `json:"joined"`
	Requested int `json:"requested"`
}

type FileData struct {
	Name   string `json:"name"`
	Mime   string `json:"mime"`
	Base64 string `json:"base64"`
}

var errNoServer = errors.New("server address is not set")

// Messenger is the service bound to the frontend. The same code runs in the
// desktop app and in server (web) mode.
type Messenger struct {
	app    *application.App
	dbPath string
	hc     *http.Client
	web    bool

	mu     sync.Mutex
	c      *core.Client
	server string
	cancel context.CancelFunc
	online bool
}

func NewMessenger(dbPath, defaultServer string, hc *http.Client, web bool) *Messenger {
	return &Messenger{dbPath: dbPath, server: defaultServer, hc: hc, web: web}
}

func (m *Messenger) ServiceStartup(ctx context.Context, _ application.ServiceOptions) error {
	m.app = application.Get()
	if _, err := os.Stat(m.dbPath); err == nil {
		// Existing profile: the server URL is stored in it.
		if err := m.open(""); err != nil {
			return err
		}
		if m.c.Account() != nil {
			m.startListening()
		}
	}
	return nil
}

func (m *Messenger) ServiceShutdown() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancel != nil {
		m.cancel()
	}
	if m.c != nil {
		return m.c.Close()
	}
	return nil
}

func (m *Messenger) emit(e UIEvent) {
	if m.app != nil {
		m.app.Event.Emit(EventName, e)
	}
}

// open (re)opens the core client. Caller must not hold m.mu.
func (m *Messenger) open(server string) error {
	if err := os.MkdirAll(filepath.Dir(m.dbPath), 0o700); err != nil {
		return err
	}
	c, err := core.Open(context.Background(), core.Options{
		ServerURL:  server,
		DBPath:     m.dbPath,
		HTTPClient: m.hc,
		OnEvent:    m.onCoreEvent,
		Logf:       log.Printf, // diagnostics only, never plaintext
	})
	if err != nil {
		return err
	}
	m.mu.Lock()
	if m.c != nil {
		m.c.Close()
	}
	m.c = c
	if server != "" {
		m.server = server
	}
	m.mu.Unlock()
	return nil
}

func (m *Messenger) client() (*core.Client, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.c == nil {
		return nil, errNoServer
	}
	return m.c, nil
}

func (m *Messenger) loggedIn() (*core.Client, error) {
	c, err := m.client()
	if err != nil {
		return nil, err
	}
	if c.Account() == nil {
		return nil, core.ErrNotLoggedIn
	}
	return c, nil
}

func (m *Messenger) startListening() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancel != nil || m.c == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	c := m.c
	go func() {
		// Catch up first (join groups of the account, pending messages).
		_ = c.Sync(ctx)
		_ = c.Listen(ctx)
	}()
}

func (m *Messenger) onCoreEvent(e core.Event) {
	ev := UIEvent{Type: string(e.Type), ChatID: e.GroupID, Detail: e.Detail, Count: e.Count}
	if e.Err != nil && ev.Detail == "" {
		ev.Detail = e.Err.Error()
	}
	switch e.Type {
	case core.EventConnected:
		m.mu.Lock()
		m.online = true
		m.mu.Unlock()
	case core.EventDisconnected:
		m.mu.Lock()
		m.online = false
		m.mu.Unlock()
	case core.EventMessage:
		if c, err := m.client(); err == nil && e.Message != nil {
			// Re-read to get the local sequence number.
			if ms, err := c.Messages(context.Background(), e.GroupID, 20); err == nil {
				for i := len(ms) - 1; i >= 0; i-- {
					if ms[i].UID == e.Message.UID {
						ev.Message = m.toMessage(c, &ms[i])
						break
					}
				}
			}
			if ev.Message == nil {
				ev.Message = m.toMessage(c, e.Message)
			}
		}
	}
	m.emit(ev)
}

func (m *Messenger) toMessage(c *core.Client, s *store.Message) *Message {
	msg := &Message{Seq: s.Seq, ID: s.UID, ChatID: s.GroupID, SenderID: s.SenderUser,
		Sender: c.Username(context.Background(), s.SenderUser), Kind: "text", Text: s.Body,
		SentAt: s.SentAt, Outgoing: s.Outgoing, Origin: s.Origin}
	if ref, err := core.ParseMedia(s); err == nil {
		msg.Kind, msg.Text = "file", ""
		msg.File = &FileInfo{Name: ref.Name, Size: ref.Size}
	}
	return msg
}

// ---- account ----------------------------------------------------------------

// GetState returns what the UI needs on start.
func (m *Messenger) GetState() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := State{ServerURL: m.server, Online: m.online, WebMode: m.web}
	if m.c != nil {
		if a := m.c.Account(); a != nil {
			st.Account = &Account{Username: a.Username, UserID: a.UserID, DeviceID: a.DeviceID}
		}
	}
	return st
}

// prepare opens the profile for a (new) server address before an account exists.
func (m *Messenger) prepare(server string) (*core.Client, error) {
	server = strings.TrimRight(strings.TrimSpace(server), "/")
	if server == "" {
		return nil, errNoServer
	}
	if c, err := m.client(); err == nil {
		if c.Account() != nil {
			return nil, core.ErrAlreadySetUp
		}
		if m.server == server {
			return c, nil
		}
	}
	if err := m.open(server); err != nil {
		return nil, err
	}
	return m.client()
}

// Register creates an account and returns the seed phrase (shown once).
func (m *Messenger) Register(server, username, password, deviceName string) (string, error) {
	c, err := m.prepare(server)
	if err != nil {
		return "", err
	}
	phrase, err := c.Register(context.Background(), username, password, orDefault(deviceName))
	if err != nil {
		return "", err
	}
	m.startListening()
	return phrase, nil
}

// Login adds this app as a new device of an existing account.
func (m *Messenger) Login(server, username, password, deviceName, seedPhrase string) (*LoginResult, error) {
	c, err := m.prepare(server)
	if err != nil {
		return nil, err
	}
	r, err := c.Login(context.Background(), username, password, orDefault(deviceName), seedPhrase)
	if r == nil {
		return nil, err
	}
	m.startListening()
	return &LoginResult{Restored: r.Restored, Joined: r.Joined, Requested: r.Requested}, err
}

// Relogin renews the session of this device after it expired.
func (m *Messenger) Relogin(password string) error {
	c, err := m.loggedIn()
	if err != nil {
		return err
	}
	return c.Relogin(context.Background(), password)
}

func orDefault(device string) string {
	if strings.TrimSpace(device) != "" {
		return device
	}
	if h, err := os.Hostname(); err == nil {
		return "desktop@" + h
	}
	return "desktop"
}

// ---- chats --------------------------------------------------------------------

func (m *Messenger) Chats() ([]Chat, error) {
	c, err := m.loggedIn()
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	gs, err := c.Groups(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Chat, 0, len(gs))
	for _, g := range gs {
		ch := Chat{ID: g.ID, Name: g.Name, Active: g.Active, UpdatedAt: g.CreatedAt * 1000}
		if ch.Name == "" {
			ch.Name = "Группа " + g.ID[:4]
		}
		if ms, err := c.Messages(ctx, g.ID, 1); err == nil && len(ms) == 1 {
			ch.Last = m.toMessage(c, &ms[0])
			ch.UpdatedAt = ms[0].SentAt
		}
		out = append(out, ch)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].UpdatedAt > out[j].UpdatedAt })
	return out, nil
}

func (m *Messenger) Messages(chatID string, limit int) ([]Message, error) {
	c, err := m.loggedIn()
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 5000 {
		limit = 200
	}
	ms, err := c.Messages(context.Background(), chatID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]Message, 0, len(ms))
	for i := range ms {
		out = append(out, *m.toMessage(c, &ms[i]))
	}
	return out, nil
}

func (m *Messenger) CreateChat(name string, usernames []string) (string, error) {
	c, err := m.loggedIn()
	if err != nil {
		return "", err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("введите название")
	}
	ctx := context.Background()
	id, err := c.CreateGroup(ctx, name)
	if err != nil {
		return "", err
	}
	if users := clean(usernames); len(users) > 0 {
		if err := c.Invite(ctx, id, users...); err != nil {
			return id, err
		}
	}
	return id, nil
}

func (m *Messenger) Invite(chatID string, usernames []string) error {
	c, err := m.loggedIn()
	if err != nil {
		return err
	}
	return c.Invite(context.Background(), chatID, clean(usernames)...)
}

func (m *Messenger) Remove(chatID, username string) error {
	c, err := m.loggedIn()
	if err != nil {
		return err
	}
	return c.Remove(context.Background(), chatID, username)
}

func clean(in []string) []string {
	var out []string
	for _, s := range in {
		s = strings.TrimPrefix(strings.TrimSpace(s), "@")
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func (m *Messenger) Members(chatID string) ([]Member, error) {
	c, err := m.loggedIn()
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	ms, err := c.Members(ctx, chatID)
	if err != nil {
		return nil, err
	}
	me := c.Account()
	out := make([]Member, 0, len(ms))
	for _, mm := range ms {
		st := "seen"
		if mm.UserID == me.UserID && mm.DeviceID == me.DeviceID {
			st = "self"
		} else if keys, err := c.Keys(ctx, mm.Username); err == nil {
			for _, k := range keys {
				if k.DeviceID == mm.DeviceID {
					st = k.Status
				}
			}
		}
		out = append(out, Member{Username: mm.Username, UserID: mm.UserID, DeviceID: mm.DeviceID, Status: st})
	}
	return out, nil
}

// ---- messages -----------------------------------------------------------------

func (m *Messenger) Send(chatID, text string) (*Message, error) {
	c, err := m.loggedIn()
	if err != nil {
		return nil, err
	}
	s, err := c.SendText(context.Background(), chatID, text)
	if err != nil {
		return nil, friendly(err)
	}
	return m.lookup(c, chatID, s.UID, s), nil
}

// SendFile sends a file given as base64.
func (m *Messenger) SendFile(chatID, name, dataBase64 string) (*Message, error) {
	c, err := m.loggedIn()
	if err != nil {
		return nil, err
	}
	data, err := base64.StdEncoding.DecodeString(dataBase64)
	if err != nil {
		return nil, err
	}
	s, err := c.SendFile(context.Background(), chatID, filepath.Base(name), data)
	if err != nil {
		return nil, friendly(err)
	}
	return m.lookup(c, chatID, s.UID, s), nil
}

func (m *Messenger) lookup(c *core.Client, chatID, uid string, fallback *store.Message) *Message {
	if ms, err := c.Messages(context.Background(), chatID, 20); err == nil {
		for i := len(ms) - 1; i >= 0; i-- {
			if ms[i].UID == uid {
				return m.toMessage(c, &ms[i])
			}
		}
	}
	return m.toMessage(c, fallback)
}

// DownloadFile returns a received file (decrypted) as base64.
func (m *Messenger) DownloadFile(chatID string, seq int64) (*FileData, error) {
	c, err := m.loggedIn()
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	ms, err := c.Messages(ctx, chatID, 100000)
	if err != nil {
		return nil, err
	}
	for i := range ms {
		if ms[i].Seq != seq {
			continue
		}
		name, data, err := c.DownloadMedia(ctx, &ms[i])
		if err != nil {
			return nil, err
		}
		mt := mime.TypeByExtension(filepath.Ext(name))
		if mt == "" {
			mt = "application/octet-stream"
		}
		return &FileData{Name: name, Mime: mt, Base64: base64.StdEncoding.EncodeToString(data)}, nil
	}
	return nil, fmt.Errorf("message %d not found", seq)
}

func friendly(err error) error {
	switch {
	case errors.Is(err, core.ErrKeyConflict):
		return errors.New("отправка заблокирована: у участника сменился ключ — сверьте отпечатки в информации о группе")
	case errors.Is(err, core.ErrNotMember):
		return errors.New("вы больше не участник этой группы")
	}
	return err
}

// ---- security -------------------------------------------------------------------

func (m *Messenger) MyFingerprint() (string, error) {
	c, err := m.loggedIn()
	if err != nil {
		return "", err
	}
	return c.MyFingerprint(context.Background())
}

func (m *Messenger) Keys(username string) ([]DeviceKey, error) {
	c, err := m.loggedIn()
	if err != nil {
		return nil, err
	}
	ks, err := c.Keys(context.Background(), username)
	if err != nil {
		return nil, err
	}
	out := make([]DeviceKey, 0, len(ks))
	for _, k := range ks {
		out = append(out, DeviceKey{DeviceID: k.DeviceID, Fingerprint: k.Fingerprint, Status: k.Status,
			ConflictFingerprint: k.ConflictFingerprint, ThisDevice: k.ThisDevice})
	}
	return out, nil
}

func (m *Messenger) Trust(username, deviceID string) (int, error) {
	c, err := m.loggedIn()
	if err != nil {
		return 0, err
	}
	return c.Trust(context.Background(), username, deviceID)
}

func (m *Messenger) Devices() ([]Device, error) {
	c, err := m.loggedIn()
	if err != nil {
		return nil, err
	}
	ds, err := c.Devices(context.Background())
	if err != nil {
		return nil, err
	}
	me := c.Account()
	out := make([]Device, 0, len(ds))
	for _, d := range ds {
		out = append(out, Device{ID: d.Id, Name: d.DeviceName, CreatedAt: d.CreatedAt * 1000,
			Revoked: d.RevokedAt != 0, ThisDevice: d.Id == me.DeviceID})
	}
	return out, nil
}

func (m *Messenger) RevokeDevice(deviceID string) (int, error) {
	c, err := m.loggedIn()
	if err != nil {
		return 0, err
	}
	return c.RevokeDevice(context.Background(), deviceID)
}

// Backup uploads messages not yet in the history backup now.
func (m *Messenger) Backup() (int, error) {
	c, err := m.loggedIn()
	if err != nil {
		return 0, err
	}
	return c.BackupHistory(context.Background())
}

// Sync fetches pending messages and joins the account's groups.
func (m *Messenger) Sync() error {
	c, err := m.loggedIn()
	if err != nil {
		return err
	}
	return c.Sync(context.Background())
}

// ---- configuration ----------------------------------------------------------------

// httpClientFromEnv honours KANTRA_CA (extra root CA, e.g. Caddy's dev CA)
// and KANTRA_INSECURE=1 (development only).
func httpClientFromEnv() (*http.Client, error) {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.ForceAttemptHTTP2 = true
	cfg := &tls.Config{InsecureSkipVerify: os.Getenv("KANTRA_INSECURE") == "1"} //nolint:gosec // explicit dev switch
	if ca := os.Getenv("KANTRA_CA"); ca != "" {
		pem, err := os.ReadFile(ca)
		if err != nil {
			return nil, err
		}
		pool, err := x509.SystemCertPool()
		if err != nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("no certificates in %s", ca)
		}
		cfg.RootCAs = pool
	}
	tr.TLSClientConfig = cfg
	return &http.Client{Transport: tr, Timeout: 2 * time.Minute}, nil
}

func defaultDBPath() string {
	if p := os.Getenv("KANTRA_DB"); p != "" {
		return p
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, "kantra", "kantra.db")
}
