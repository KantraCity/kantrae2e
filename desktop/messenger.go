package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/kantracity/kantrae2e/client/core"
	"github.com/kantracity/kantrae2e/client/crypto"
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
	// DeviceID for link_request / link_resolved.
	DeviceID string `json:"deviceId,omitempty"`
	// Title/Body of a "notify" event (web mode shows it as a browser notification).
	Title string `json:"title,omitempty"`
	Body  string `json:"body,omitempty"`
}

// Account is the logged-in identity of this device.
type Account struct {
	// Name is the display name; Username the unique @username.
	Name     string `json:"name"`
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
	// Pending: this device waits for approval; show LinkCode.
	Pending  bool   `json:"pending"`
	LinkCode string `json:"linkCode,omitempty"`
	// Accounts on this device (including the active one, set up or not).
	Accounts int `json:"accounts"`
}

// LinkRequest is a new device of the account waiting for approval.
type LinkRequest struct {
	DeviceID string `json:"deviceId"`
	Name     string `json:"name"`
	Code     string `json:"code"`
	At       int64  `json:"at"`
}

// UserHit is a global search result.
type UserHit struct {
	UserID   string `json:"userId"`
	Username string `json:"username"`
	Name     string `json:"name"`
	AvatarID string `json:"avatarId,omitempty"`
}

type FileInfo struct {
	Name string `json:"name"`
	Size int    `json:"size"`
	// Kind: "photo", "video", "voice", "round" or "file".
	Kind     string  `json:"kind"`
	Mime     string  `json:"mime,omitempty"`
	Width    int     `json:"w,omitempty"`
	Height   int     `json:"h,omitempty"`
	Duration float64 `json:"dur,omitempty"`
	// Thumb: tiny preview as a data: URL; Waveform: voice bars 0..255.
	Thumb    string `json:"thumb,omitempty"`
	Waveform []int  `json:"wave,omitempty"`
	Caption  string `json:"caption,omitempty"`
	// URL serves the decrypted file (media.go); "?download=1" saves it.
	URL string `json:"url"`
}

// ReplyInfo is the quoted message above a reply.
type ReplyInfo struct {
	ID     string `json:"id"`
	Sender string `json:"sender"`
	Text   string `json:"text"`
}

// ReactionInfo is one emoji under a message.
type ReactionInfo struct {
	Emoji string `json:"emoji"`
	Count int    `json:"count"`
	Mine  bool   `json:"mine"`
}

type Message struct {
	Seq      int64  `json:"seq"`
	ID       string `json:"id"`
	ChatID   string `json:"chatId"`
	SenderID string `json:"senderId"`
	// Sender is the display name; SenderAvatar its photo id (or "").
	Sender       string    `json:"sender"`
	SenderAvatar string    `json:"senderAvatar,omitempty"`
	Kind         string    `json:"kind"` // "text" or "file"
	Text         string    `json:"text"`
	File         *FileInfo `json:"file,omitempty"`
	SentAt       int64     `json:"sentAt"` // unix ms
	Outgoing     bool      `json:"outgoing"`
	// Origin: "" direct, "backup" restored, "shared" re-sent by another member.
	Origin string `json:"origin"`

	Reply     *ReplyInfo     `json:"reply,omitempty"`
	Edited    bool           `json:"edited,omitempty"`
	Forwarded string         `json:"forwarded,omitempty"` // "Переслано от …"
	Reactions []ReactionInfo `json:"reactions,omitempty"`
	// Read: an outgoing message someone else has read (✓✓).
	Read bool `json:"read,omitempty"`
}

type Chat struct {
	ID string `json:"id"`
	// Kind: "direct" (one person, found by username) or "group".
	Kind string `json:"kind"`
	// Peer of a direct chat (@username / user id / photo id); Name is
	// its display name.
	Peer       string   `json:"peer,omitempty"`
	PeerID     string   `json:"peerId,omitempty"`
	PeerAvatar string   `json:"peerAvatar,omitempty"`
	Name       string   `json:"name"`
	Active     bool     `json:"active"`
	Last       *Message `json:"last,omitempty"`
	UpdatedAt  int64    `json:"updatedAt"` // unix ms
}

type Member struct {
	Name     string `json:"name"`
	AvatarID string `json:"avatarId,omitempty"`
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
	Pending   bool   `json:"pending"`
	Code      string `json:"code,omitempty"`
	Restored  int    `json:"restored"`
	Joined    int    `json:"joined"`
	Requested int    `json:"requested"`
}

type FileData struct {
	Name   string `json:"name"`
	Mime   string `json:"mime"`
	Base64 string `json:"base64"`
}

var errNoServer = errors.New("server address is not set")

// Messenger is the service bound to the frontend. The same code runs in the
// desktop app and in server (web) mode. It holds every account added on this
// device (accounts.go); the UI shows the active one, all of them stay
// connected for notifications.
type Messenger struct {
	app    *application.App
	dbFunc func() string
	dir    string
	hc     *http.Client
	web    bool

	mu            sync.Mutex
	slots         []*slot
	active        *slot
	defaultServer string

	// startOnce: the accounts are opened once per process, by the UI or (on
	// Android) by the background service, whichever comes first.
	startOnce sync.Once
	startErr  error

	// Notifications (notify.go), guarded by mu.
	notif      notifier
	foreground bool
	activeChat string
	openChat   string // chat to open once the UI asks (tapped notification)
}

// NewMessenger: dbPath is resolved at startup (on Android the storage
// directory is only known once the app runs).
func NewMessenger(dbPath func() string, defaultServer string, hc *http.Client, web bool) *Messenger {
	m := &Messenger{dbFunc: dbPath, defaultServer: defaultServer, hc: hc, web: web, foreground: true}
	m.notif = newNotifier(m)
	return m
}

func (m *Messenger) ServiceStartup(ctx context.Context, _ application.ServiceOptions) error {
	m.mu.Lock()
	m.app = application.Get()
	m.mu.Unlock()
	return m.start()
}

func (m *Messenger) ServiceShutdown() error {
	m.mu.Lock()
	slots := append([]*slot(nil), m.slots...)
	m.mu.Unlock()
	for _, s := range slots {
		s.close()
	}
	return nil
}

func (m *Messenger) emit(e UIEvent) {
	m.mu.Lock()
	app := m.app
	m.mu.Unlock()
	if app != nil {
		app.Event.Emit(EventName, e)
	}
}

// client returns the active account's client.
func (m *Messenger) client() (*core.Client, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active == nil || m.active.c == nil {
		return nil, errNoServer
	}
	return m.active.c, nil
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

func (m *Messenger) startListening(s *slot) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s.cancel != nil || s.c == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	c := s.c
	m.mu.Unlock()
	a := c.Account()
	m.mu.Lock()
	if a != nil {
		s.userID = a.UserID
	}
	go func() {
		// Catch up first (join groups of the account, pending messages).
		_ = c.Sync(ctx)
		_ = c.Listen(ctx)
	}()
	// A pending device keeps asking for approval and notices it even when
	// the approving device could not send the history key.
	go func() {
		for {
			if ok, err := c.IsApproved(ctx); err == nil && ok {
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(15 * time.Second):
			}
			_ = c.Sync(ctx)
		}
	}()
}

func (m *Messenger) onCoreEvent(s *slot, e core.Event) {
	m.mu.Lock()
	isActive := s == m.active
	switch e.Type {
	case core.EventConnected:
		s.online = true
	case core.EventDisconnected:
		s.online = false
	}
	m.mu.Unlock()
	if !isActive {
		// Background account: only notifications and its unread badge.
		if e.Type == core.EventMessage && e.Message != nil && s.c != nil {
			m.maybeNotify(s, s.c, m.toMessage(s.c, e.Message))
			m.emit(UIEvent{Type: "accounts"})
		}
		return
	}
	ev := UIEvent{Type: string(e.Type), ChatID: e.GroupID, Detail: e.Detail, Count: e.Count, DeviceID: e.DeviceID}
	if e.Err != nil && ev.Detail == "" {
		ev.Detail = e.Err.Error()
	}
	if e.Type == core.EventTyping && s.c != nil {
		ev.Detail = core.DisplayName(s.c.CachedProfile(context.Background(), e.UserID))
	}
	if e.Type == core.EventMessage && e.Message != nil && s.c != nil {
		c := s.c
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
		m.maybeNotify(s, c, ev.Message)
	}
	m.emit(ev)
}

// ---- account ----------------------------------------------------------------

// GetState returns what the UI needs on start.
func (m *Messenger) GetState() State {
	m.mu.Lock()
	st := State{WebMode: m.web, ServerURL: m.defaultServer}
	var c *core.Client
	if s := m.active; s != nil {
		st.Online = s.online
		if s.server != "" {
			st.ServerURL = s.server
		}
		c = s.c
	}
	st.Accounts = len(m.slots)
	m.mu.Unlock()
	if c == nil {
		return st
	}
	if a := c.Account(); a != nil {
		st.Account = &Account{Username: a.Username, UserID: a.UserID, DeviceID: a.DeviceID,
			Name: core.DisplayName(c.CachedProfile(context.Background(), a.UserID))}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if ok, err := c.IsApproved(ctx); err == nil && !ok {
			st.Pending = true
			st.LinkCode, _ = c.MyLinkCode(ctx)
		}
	}
	return st
}

// prepare opens the active (not yet set up) account slot for a server.
func (m *Messenger) prepare(server string) (*slot, error) {
	server = strings.TrimRight(strings.TrimSpace(server), "/")
	if server == "" {
		return nil, errNoServer
	}
	m.mu.Lock()
	s := m.active
	m.mu.Unlock()
	if s == nil {
		return nil, errNoServer
	}
	if s.c != nil {
		if s.c.Account() != nil {
			return nil, core.ErrAlreadySetUp
		}
		if s.server == server {
			return s, nil
		}
	}
	if err := m.openSlot(s, server); err != nil {
		return nil, err
	}
	return s, nil
}

// Register creates an account with this device (added as another account
// if there already are some).
func (m *Messenger) Register(server, username, password, displayName, deviceName string) error {
	s, err := m.prepare(server)
	if err != nil {
		return err
	}
	if err := m.checkNotAdded(username); err != nil {
		return err
	}
	if err := s.c.Register(context.Background(), username, password, strings.TrimSpace(displayName), orDefault(deviceName)); err != nil {
		return err
	}
	m.saveAccounts()
	m.startListening(s)
	return nil
}

// Login adds this app as a new device of an existing account. Usually the
// result is Pending: approve it on another device (same Code) or Recover.
func (m *Messenger) Login(server, username, password, deviceName string) (*LoginResult, error) {
	s, err := m.prepare(server)
	if err != nil {
		return nil, err
	}
	if err := m.checkNotAdded(username); err != nil {
		return nil, err
	}
	r, err := s.c.Login(context.Background(), username, password, orDefault(deviceName))
	if r == nil {
		return nil, err
	}
	m.saveAccounts()
	m.startListening(s)
	return toLoginResult(r), err
}

func toLoginResult(r *core.LoginResult) *LoginResult {
	return &LoginResult{Pending: r.Pending, Code: r.Code, Restored: r.Restored, Joined: r.Joined, Requested: r.Requested}
}

// Recover approves this new device with the recovery phrase.
func (m *Messenger) Recover(phrase string) (*LoginResult, error) {
	c, err := m.loggedIn()
	if err != nil {
		return nil, err
	}
	r, err := c.Recover(context.Background(), phrase)
	if errors.Is(err, crypto.ErrBadPhrase) {
		return nil, errors.New("неверная фраза: проверьте все 12 слов")
	}
	if errors.Is(err, core.ErrNoRecovery) {
		return nil, errors.New("для аккаунта не создан ключ восстановления — подтвердите вход с другого устройства")
	}
	if connect.CodeOf(err) == connect.CodePermissionDenied {
		return nil, errors.New("фраза не подходит к этому аккаунту")
	}
	if r == nil {
		return nil, err
	}
	m.emit(UIEvent{Type: string(core.EventApproved), Count: r.Restored})
	return toLoginResult(r), err
}

// RequestLink asks the other devices again to approve this one.
func (m *Messenger) RequestLink() error {
	c, err := m.loggedIn()
	if err != nil {
		return err
	}
	return c.RequestLink(context.Background())
}

// CancelPending forgets this device's local profile (only while it is still
// waiting for approval: nothing is lost).
func (m *Messenger) CancelPending() error {
	c, err := m.loggedIn()
	if err != nil {
		return err
	}
	if ok, err := c.IsApproved(context.Background()); err != nil || ok {
		return errors.New("устройство уже подтверждено")
	}
	m.mu.Lock()
	s := m.active
	m.mu.Unlock()
	m.removeSlot(s)
	return nil
}

// ---- linked devices & recovery ------------------------------------------------

func (m *Messenger) PendingLinks() ([]LinkRequest, error) {
	c, err := m.loggedIn()
	if err != nil {
		return nil, err
	}
	ls := c.PendingLinks(context.Background())
	out := make([]LinkRequest, 0, len(ls))
	for _, l := range ls {
		out = append(out, LinkRequest{DeviceID: l.DeviceID, Name: l.DeviceName, Code: l.Code, At: l.At.UnixMilli()})
	}
	return out, nil
}

func (m *Messenger) ApproveLink(deviceID string) error {
	c, err := m.loggedIn()
	if err != nil {
		return err
	}
	return c.ApproveLink(context.Background(), deviceID)
}

func (m *Messenger) RejectLink(deviceID string) error {
	c, err := m.loggedIn()
	if err != nil {
		return err
	}
	return c.RejectLink(context.Background(), deviceID)
}

// HasRecovery reports whether a recovery phrase exists for the account.
func (m *Messenger) HasRecovery() (bool, error) {
	c, err := m.loggedIn()
	if err != nil {
		return false, err
	}
	return c.HasRecovery(context.Background())
}

// SetupRecovery creates a new recovery phrase (replacing an older one).
func (m *Messenger) SetupRecovery() (string, error) {
	c, err := m.loggedIn()
	if err != nil {
		return "", err
	}
	return c.SetupRecovery(context.Background())
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
	if deviceKind == "Android" {
		return deviceKind
	}
	if h, err := os.Hostname(); err == nil {
		return deviceKind + "@" + h
	}
	return deviceKind
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
	// Refresh stale profiles of the people in the list in one request.
	var stale []string
	for _, g := range gs {
		if g.Peer != "" && time.Since(time.UnixMilli(c.CachedProfile(ctx, g.Peer).FetchedAt)) > 10*time.Minute {
			stale = append(stale, g.Peer)
		}
	}
	_ = c.RefreshProfiles(ctx, stale...)
	out := make([]Chat, 0, len(gs))
	for _, g := range gs {
		ch := m.toChat(c, g)
		if ms, err := c.Messages(ctx, g.ID, 1); err == nil && len(ms) == 1 {
			ch.Last = m.toMessage(c, &ms[0])
			ch.UpdatedAt = ms[0].SentAt
		}
		out = append(out, ch)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].UpdatedAt > out[j].UpdatedAt })
	return out, nil
}

func (m *Messenger) toChat(c *core.Client, g store.Group) Chat {
	ch := Chat{ID: g.ID, Kind: core.GroupKind(g.ID), Name: g.Name, Active: g.Active, UpdatedAt: g.CreatedAt * 1000}
	if ch.Kind == core.KindDirect && g.Peer != "" {
		p := profileOf(c, g.Peer)
		ch.PeerID, ch.Peer, ch.PeerAvatar, ch.Name = g.Peer, p.Username, p.AvatarID, core.DisplayName(p)
	}
	if ch.Name == "" {
		ch.Name = "Группа " + g.ID[:4]
	}
	return ch
}

// Chat returns one chat.
func (m *Messenger) Chat(chatID string) (*Chat, error) {
	c, err := m.loggedIn()
	if err != nil {
		return nil, err
	}
	g, err := c.ResolveGroup(context.Background(), chatID)
	if err != nil {
		return nil, err
	}
	ch := m.toChat(c, *g)
	return &ch, nil
}

// SearchUsers finds people by username (global search, like in Telegram).
func (m *Messenger) SearchUsers(query string) ([]UserHit, error) {
	c, err := m.loggedIn()
	if err != nil {
		return nil, err
	}
	query = strings.TrimPrefix(strings.TrimSpace(query), "@")
	if len([]rune(query)) < 2 {
		return []UserHit{}, nil
	}
	us, err := c.SearchUsers(context.Background(), query)
	if err != nil {
		return nil, err
	}
	me := c.Account()
	out := make([]UserHit, 0, len(us))
	for _, u := range us {
		if u.UserID != me.UserID {
			out = append(out, UserHit{UserID: u.UserID, Username: u.Username, Name: u.DisplayName, AvatarID: u.AvatarID})
		}
	}
	return out, nil
}

// OpenDirect opens (or starts) the direct chat with a user.
func (m *Messenger) OpenDirect(username string) (*Chat, error) {
	c, err := m.loggedIn()
	if err != nil {
		return nil, err
	}
	id, err := c.OpenDirect(context.Background(), username)
	if errors.Is(err, core.ErrNoKeyPackages) {
		return nil, errors.New("пользователь ещё ни разу не был в сети — написать ему пока нельзя")
	}
	if err != nil && id == "" {
		return nil, friendly(err)
	}
	return m.Chat(id)
}

// CreateGroup creates a group chat with the given members.
func (m *Messenger) CreateGroup(name string, usernames []string) (string, error) {
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
		p := profileOf(c, mm.UserID)
		out = append(out, Member{Name: core.DisplayName(p), AvatarID: p.AvatarID, Username: mm.Username,
			UserID: mm.UserID, DeviceID: mm.DeviceID, Status: st})
	}
	return out, nil
}

// ---- messages -----------------------------------------------------------------

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
	case errors.Is(err, core.ErrNotYours):
		return errors.New("это можно сделать только со своим сообщением")
	case errors.Is(err, core.ErrNotEditable):
		return errors.New("редактировать можно только текстовые сообщения")
	case errors.Is(err, core.ErrPendingApproval):
		return errors.New("устройство ещё не подтверждено")
	case errors.Is(err, core.ErrDirectInvite):
		return errors.New("в личный чат нельзя добавлять участников — создайте группу")
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
