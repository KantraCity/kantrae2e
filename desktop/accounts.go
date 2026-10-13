package main

// Several accounts on one device, as in Telegram: each account is its own
// local database (its own MLS identity, keys and history) and its own
// connection, so all of them receive messages and notifications; the UI
// shows the active one and switches instantly. accounts.json lists them.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/kantracity/kantrae2e/client/core"
)

// MaxAccounts per device (Telegram: 3, 6 with Premium).
const MaxAccounts = 5

const defaultFile = "kantra.db"

// slot is one account on this device (possibly not set up yet).
type slot struct {
	file   string
	c      *core.Client
	server string
	cancel context.CancelFunc
	online bool
	unread map[string]int // per chat, since its notification was cleared
	// userID of a set-up account (read without calling into the client,
	// which may be holding its lock while calling back).
	userID string
}

func (s *slot) close() {
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	if s.c != nil {
		s.c.Close()
		s.c = nil
	}
}

func newSlot(file string) *slot { return &slot{file: file, unread: map[string]int{}} }

// slotView is a slot with its client, taken under m.mu. Client methods
// (Account etc.) take the client's own lock, which is held while it calls
// back into Messenger; so they are only called on a view, never under m.mu.
type slotView struct {
	s *slot
	c *core.Client
}

func (v slotView) account() *core.Account {
	if v.c == nil {
		return nil
	}
	return v.c.Account()
}

func (m *Messenger) views() (all []slotView, active *slot) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.slots {
		all = append(all, slotView{s, s.c})
	}
	return all, m.active
}

type accountsFile struct {
	Accounts []string `json:"accounts"`
	Active   string   `json:"active"`
}

func (m *Messenger) accountsPath() string { return filepath.Join(m.dir, "accounts.json") }

func (m *Messenger) path(s *slot) string { return filepath.Join(m.dir, s.file) }

// start opens all accounts and connects them. Safe to call repeatedly and
// without the UI (Android background service).
func (m *Messenger) start() error {
	m.startOnce.Do(func() {
		m.dir = filepath.Dir(m.dbFunc())
		if err := os.MkdirAll(m.dir, 0o700); err != nil {
			m.startErr = err
			return
		}
		diag.open(m.dir)
		m.openMediaCache()
		m.loadPasscode() // the app starts locked when a passcode is set
		var af accountsFile
		if b, err := os.ReadFile(m.accountsPath()); err == nil {
			_ = json.Unmarshal(b, &af)
		} else if _, err := os.Stat(filepath.Join(m.dir, defaultFile)); err == nil {
			af.Accounts = []string{defaultFile} // single-account profile from before
		}
		var active *slot
		for _, f := range af.Accounts {
			if _, err := os.Stat(filepath.Join(m.dir, f)); err != nil {
				continue
			}
			s := newSlot(f)
			// The server URL is stored in the profile.
			if err := m.openSlot(s, ""); err != nil {
				log.Printf("account %s: %v", f, err)
				continue
			}
			m.mu.Lock()
			m.slots = append(m.slots, s)
			m.mu.Unlock()
			if f == af.Active || active == nil {
				active = s
			}
			if s.c.Account() != nil {
				m.startListening(s)
			}
		}
		if active == nil {
			active = newSlot(defaultFile)
			m.mu.Lock()
			m.slots = append(m.slots, active)
			m.mu.Unlock()
		}
		m.mu.Lock()
		m.active = active
		m.mu.Unlock()
	})
	return m.startErr
}

// openSlot (re)opens an account's client. Caller must not hold m.mu.
func (m *Messenger) openSlot(s *slot, server string) error {
	c, err := core.Open(context.Background(), core.Options{
		ServerURL:  server,
		DBPath:     m.path(s),
		HTTPClient: m.hc,
		OnEvent:    func(e core.Event) { m.onCoreEvent(s, e) },
		Logf:       log.Printf, // diagnostics only, never plaintext
		KeepAlive:  keepAlive,
		MediaCache: m.cache,
		// Realtime over WebTransport (QUIC) when the server offers it;
		// KANTRA_WT=0 keeps the WebSocket.
		WebTransport:     os.Getenv("KANTRA_WT") != "0",
		WebTransportAddr: os.Getenv("KANTRA_WT_ADDR"), // tests: through a proxy
	})
	if err != nil {
		return err
	}
	m.mu.Lock()
	if s.c != nil {
		s.c.Close()
	}
	s.c = c
	if server != "" {
		s.server = server
	}
	m.mu.Unlock()
	return nil
}

// saveAccounts writes the list of set-up accounts and the active one.
func (m *Messenger) saveAccounts() {
	views, active := m.views()
	var af accountsFile
	for _, v := range views {
		if v.account() != nil {
			af.Accounts = append(af.Accounts, v.s.file)
		}
	}
	if active != nil {
		af.Active = active.file
	}
	b, _ := json.Marshal(af)
	if err := os.WriteFile(m.accountsPath(), b, 0o600); err != nil {
		log.Printf("save accounts: %v", err)
	}
}

// checkNotAdded refuses to add the same account twice on this device.
func (m *Messenger) checkNotAdded(username string) error {
	username = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(username), "@"))
	views, _ := m.views()
	for _, v := range views {
		if a := v.account(); a != nil && strings.ToLower(a.Username) == username {
			return errors.New("этот аккаунт уже добавлен на этом устройстве")
		}
	}
	return nil
}

// removeSlot forgets an account on this device (database deleted) and
// switches to another one.
func (m *Messenger) removeSlot(s *slot) {
	var next *slot
	views, _ := m.views()
	for _, v := range views {
		if v.s != s && v.account() != nil {
			next = v.s
			break
		}
	}
	m.mu.Lock()
	s.close()
	for i, x := range m.slots {
		if x == s {
			m.slots = append(m.slots[:i], m.slots[i+1:]...)
			break
		}
	}
	if m.active == s {
		m.active = next
		if m.active == nil {
			m.active = newSlot(m.freeFileLocked())
			m.slots = append(m.slots, m.active)
		}
	}
	m.mu.Unlock()
	for _, suffix := range []string{"", "-wal", "-shm"} {
		_ = os.Remove(m.path(s) + suffix)
	}
	m.saveAccounts()
	m.activeChanged()
}

func (m *Messenger) freeFileLocked() string {
	used := map[string]bool{}
	for _, s := range m.slots {
		used[s.file] = true
	}
	if !used[defaultFile] {
		if _, err := os.Stat(filepath.Join(m.dir, defaultFile)); err != nil {
			return defaultFile
		}
	}
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return "kantra-" + hex.EncodeToString(b) + ".db"
}

func (m *Messenger) activeChanged() {
	m.mu.Lock()
	m.activeChat = ""
	m.mu.Unlock()
	m.emit(UIEvent{Type: "account_switched"})
}

// AccountInfo is one account in the account switcher.
type AccountInfo struct {
	ID       string `json:"id"`
	UserID   string `json:"userId"`
	Name     string `json:"name"`
	Username string `json:"username"`
	Active   bool   `json:"active"`
	Unread   int    `json:"unread"`
}

// Accounts lists the accounts set up on this device.
func (m *Messenger) Accounts() []AccountInfo {
	views, active := m.views()
	out := []AccountInfo{}
	for _, v := range views {
		a := v.account()
		if a == nil {
			continue
		}
		info := AccountInfo{ID: v.s.file, UserID: a.UserID, Username: a.Username, Active: v.s == active,
			Name: core.DisplayName(v.c.CachedProfile(context.Background(), a.UserID))}
		m.mu.Lock()
		for _, n := range v.s.unread {
			info.Unread += n
		}
		m.mu.Unlock()
		out = append(out, info)
	}
	return out
}

// SwitchAccount makes another account the active one.
func (m *Messenger) SwitchAccount(id string) error {
	views, prev := m.views()
	var target *slot
	prevSetUp := false
	for _, v := range views {
		set := v.account() != nil
		if v.s.file == id && set {
			target = v.s
		}
		if v.s == prev {
			prevSetUp = set
		}
	}
	if target == nil {
		return errors.New("нет такого аккаунта")
	}
	m.mu.Lock()
	m.active = target
	m.mu.Unlock()
	// An unfinished "add account" is dropped when switching away.
	if prev != nil && prev != target && !prevSetUp {
		m.dropUnfinished(prev)
	}
	m.saveAccounts()
	m.activeChanged()
	return nil
}

func (m *Messenger) dropUnfinished(s *slot) {
	m.mu.Lock()
	s.close()
	for i, x := range m.slots {
		if x == s {
			m.slots = append(m.slots[:i], m.slots[i+1:]...)
			break
		}
	}
	m.mu.Unlock()
	for _, suffix := range []string{"", "-wal", "-shm"} {
		_ = os.Remove(m.path(s) + suffix)
	}
}

// AddAccount starts adding another account: the UI shows the login /
// registration screen for it (CancelAddAccount returns to the previous one).
func (m *Messenger) AddAccount() error {
	if n := len(m.Accounts()); n >= MaxAccounts {
		return errors.New("на одном устройстве можно добавить не больше 5 аккаунтов")
	}
	if _, err := m.loggedIn(); err != nil {
		return nil // the active slot is already a new, unfinished one
	}
	m.mu.Lock()
	s := newSlot(m.freeFileLocked())
	m.slots = append(m.slots, s)
	m.active = s
	m.mu.Unlock()
	m.activeChanged()
	return nil
}

// CancelAddAccount returns to the previous account.
func (m *Messenger) CancelAddAccount() error {
	accs := m.Accounts()
	if len(accs) == 0 {
		return errors.New("нет других аккаунтов")
	}
	return m.SwitchAccount(accs[len(accs)-1].ID)
}

// LogoutInfo warns before logging out.
type LogoutInfo struct {
	LastDevice  bool `json:"lastDevice"`
	HasRecovery bool `json:"hasRecovery"`
}

func (m *Messenger) GetLogoutInfo() (*LogoutInfo, error) {
	c, err := m.loggedIn()
	if err != nil {
		return nil, err
	}
	i, err := c.LogoutInfo(context.Background())
	if err != nil {
		return nil, err
	}
	return &LogoutInfo{LastDevice: i.LastDevice, HasRecovery: i.HasRecovery}, nil
}

// Logout signs this device out of the active account and deletes its local
// data; the app switches to another account (or the login screen).
func (m *Messenger) Logout() error {
	c, err := m.loggedIn()
	if err != nil {
		return err
	}
	if err := c.Logout(context.Background()); err != nil {
		return err
	}
	m.mu.Lock()
	s := m.active
	m.mu.Unlock()
	m.removeSlot(s)
	return nil
}
