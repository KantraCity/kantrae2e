// Package core is the platform-independent messenger client: it drives the
// services over the network, keeps MLS state (via cgo -> mls-rs) and stores
// everything in local SQLite. The CLI is one frontend; Wails / gomobile
// frontends can wrap the same API (see client/bind for a string-only facade).
package core

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/golang-jwt/jwt/v5"

	"github.com/kantracity/kantrae2e/client/crypto"
	"github.com/kantracity/kantrae2e/client/mls"
	"github.com/kantracity/kantrae2e/client/store"
	authv1 "github.com/kantracity/kantrae2e/gen/kantra/auth/v1"
	"github.com/kantracity/kantrae2e/gen/kantra/auth/v1/authv1connect"
	"github.com/kantracity/kantrae2e/gen/kantra/delivery/v1/deliveryv1connect"
	directoryv1 "github.com/kantracity/kantrae2e/gen/kantra/directory/v1"
	"github.com/kantracity/kantrae2e/gen/kantra/directory/v1/directoryv1connect"
	"github.com/kantracity/kantrae2e/gen/kantra/history/v1/historyv1connect"
	"github.com/kantracity/kantrae2e/gen/kantra/media/v1/mediav1connect"
)

var (
	ErrNotLoggedIn   = errors.New("not logged in")
	ErrAlreadySetUp  = errors.New("this client database already has an account")
	ErrNoHistoryKey  = errors.New("no history key: log in with the seed phrase")
	ErrNotMember     = errors.New("not an active member of this group")
	ErrNoKeyPackages = errors.New("user has no devices with available key packages")
)

// KeyPackage stock management.
const (
	KeyPackageBatch    = 30
	KeyPackageLowWater = 10
)

// Options configure a Client.
type Options struct {
	// ServerURL is the gateway base URL, e.g. https://chat.example.com.
	ServerURL string
	// DBPath is the local SQLite file.
	DBPath string
	// HTTPClient is optional (e.g. to trust a dev CA).
	HTTPClient *http.Client
	// OnEvent receives incoming events (called from the client's goroutines).
	OnEvent func(Event)
	// Logf receives diagnostic messages (never plaintext). Optional.
	Logf func(format string, args ...any)
	// DisableAutoBackup turns off the continuous history backup (it is on
	// whenever the device knows the history key).
	DisableAutoBackup bool
	// BackupDelay debounces the automatic backup (default 2s).
	BackupDelay time.Duration
}

// EventType enumerates Event kinds.
type EventType string

const (
	EventMessage      EventType = "message"
	EventGroupJoined  EventType = "group_joined"
	EventGroupUpdated EventType = "group_updated"
	EventRemoved      EventType = "removed"
	EventConnected    EventType = "connected"
	EventDisconnected EventType = "disconnected"
	// EventHistory: another member answered a history request (Count new
	// messages, possibly 0).
	EventHistory EventType = "history"
	// EventSecurity: something suspicious was detected and handled.
	EventSecurity EventType = "security"
)

type Event struct {
	Type    EventType
	GroupID string
	Message *store.Message
	Err     error
	// Count of messages for EventHistory.
	Count int
}

// Account is the identity of this device.
type Account struct {
	UserID   string
	DeviceID string
	Username string
}

// Client is safe for concurrent use.
type Client struct {
	opts Options
	st   *store.Store
	hc   *http.Client

	// mu serializes MLS operations with their persistence and the network
	// calls that must happen in between (commit -> server -> apply).
	mu   sync.Mutex
	acct *Account
	m    *mls.Client

	tokMu sync.Mutex
	token string

	// backupMu serializes history backups (auto and manual).
	backupMu sync.Mutex

	// Background work (deferred actions, auto backup). Guarded by bgMu.
	bgMu          sync.Mutex
	bg            sync.WaitGroup
	later         []func(context.Context)
	backupTimer   *time.Timer
	backupPending bool
	closed        bool
	// Multi-device bookkeeping, guarded by c.mu.
	answered      map[string]bool      // history requests already answered by someone
	joinRequested map[string]time.Time // groups we asked to be added to

	auth  authv1connect.AuthServiceClient
	dir   directoryv1connect.DirectoryServiceClient
	del   deliveryv1connect.DeliveryServiceClient
	hist  historyv1connect.HistoryServiceClient
	media mediav1connect.MediaServiceClient
}

const (
	kvServer    = "server_url"
	kvUserID    = "user_id"
	kvDeviceID  = "device_id"
	kvUsername  = "username"
	kvToken     = "token"
	kvSigSecret = "sig_secret"
	kvSigPublic = "sig_public"
	kvHistKey   = "history_key"
)

// Open opens the local database and restores the account and MLS state.
func Open(ctx context.Context, opts Options) (*Client, error) {
	st, err := store.Open(opts.DBPath)
	if err != nil {
		return nil, err
	}
	if opts.ServerURL == "" {
		if v, err := st.Get(ctx, kvServer); err == nil {
			opts.ServerURL = string(v)
		}
	}
	if opts.ServerURL == "" {
		st.Close()
		return nil, errors.New("server URL is required")
	}
	opts.ServerURL = strings.TrimRight(opts.ServerURL, "/")
	if opts.BackupDelay == 0 {
		opts.BackupDelay = 2 * time.Second
	}
	c := &Client{opts: opts, st: st, hc: opts.HTTPClient,
		answered: map[string]bool{}, joinRequested: map[string]time.Time{}}
	if c.hc == nil {
		c.hc = http.DefaultClient
	}
	auth := connect.WithInterceptors(c.authInterceptor())
	c.auth = authv1connect.NewAuthServiceClient(c.hc, opts.ServerURL, auth)
	c.dir = directoryv1connect.NewDirectoryServiceClient(c.hc, opts.ServerURL, auth)
	c.del = deliveryv1connect.NewDeliveryServiceClient(c.hc, opts.ServerURL, auth)
	c.hist = historyv1connect.NewHistoryServiceClient(c.hc, opts.ServerURL, auth)
	c.media = mediav1connect.NewMediaServiceClient(c.hc, opts.ServerURL, auth)

	if err := c.loadAccount(ctx); err != nil {
		st.Close()
		return nil, err
	}
	return c, nil
}

// Close waits for background work, flushes a pending history backup and
// closes the database.
func (c *Client) Close() error {
	c.bgMu.Lock()
	if c.closed {
		c.bgMu.Unlock()
		return nil
	}
	c.closed = true
	flush := c.backupPending
	c.backupPending = false
	if c.backupTimer != nil {
		c.backupTimer.Stop()
	}
	c.bgMu.Unlock()
	c.waitBackground(10 * time.Second)
	if flush {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if _, err := c.BackupHistory(ctx); err != nil && !errors.Is(err, ErrNoHistoryKey) {
			c.logf("final history backup: %v", err)
		}
		cancel()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m != nil {
		c.m.Close()
	}
	return c.st.Close()
}

// Settle waits until background work triggered so far (auto-invites,
// history sharing, backups) has finished. Useful for tests and CLIs.
func (c *Client) Settle(timeout time.Duration) { c.waitBackground(timeout) }

func (c *Client) waitBackground(timeout time.Duration) {
	done := make(chan struct{})
	go func() { c.bg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(timeout):
	}
}

// deferLocked queues fn to run in the background after the current batch of
// incoming messages has been persisted and acknowledged. fn must take c.mu
// itself. Caller holds c.mu.
func (c *Client) deferLocked(fn func(context.Context)) {
	c.bgMu.Lock()
	c.later = append(c.later, fn)
	c.bgMu.Unlock()
}

// flushDeferred starts the queued background actions.
func (c *Client) flushDeferred() {
	c.bgMu.Lock()
	later := c.later
	c.later = nil
	closed := c.closed
	if !closed {
		c.bg.Add(len(later))
	}
	c.bgMu.Unlock()
	if closed {
		return
	}
	for _, fn := range later {
		go func() {
			defer c.bg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			fn(ctx)
		}()
	}
}

// scheduleBackup (debounced) uploads new messages to the history backup.
func (c *Client) scheduleBackup() {
	if c.opts.DisableAutoBackup {
		return
	}
	c.bgMu.Lock()
	defer c.bgMu.Unlock()
	if c.closed {
		return
	}
	c.backupPending = true
	if c.backupTimer != nil {
		c.backupTimer.Stop()
	}
	c.backupTimer = time.AfterFunc(c.opts.BackupDelay, func() {
		c.bgMu.Lock()
		if c.closed || !c.backupPending {
			c.bgMu.Unlock()
			return
		}
		c.backupPending = false
		c.bg.Add(1)
		c.bgMu.Unlock()
		defer c.bg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if _, err := c.BackupHistory(ctx); err != nil && !errors.Is(err, ErrNoHistoryKey) {
			c.logf("auto backup: %v", err)
			c.bgMu.Lock()
			c.backupPending = true // retried on the next message or on Close
			c.bgMu.Unlock()
		}
	})
}

func (c *Client) logf(format string, args ...any) {
	if c.opts.Logf != nil {
		c.opts.Logf(format, args...)
	}
}

func (c *Client) emit(e Event) {
	if c.opts.OnEvent != nil {
		c.opts.OnEvent(e)
	}
}

// Account returns the logged-in account or nil.
func (c *Client) Account() *Account {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.acct == nil {
		return nil
	}
	a := *c.acct
	return &a
}

func identity(userID, deviceID string) []byte { return []byte(userID + ":" + deviceID) }

func splitIdentity(id []byte) (userID, deviceID string) {
	u, d, _ := strings.Cut(string(id), ":")
	return u, d
}

func (c *Client) loadAccount(ctx context.Context) error {
	uid, err := c.st.Get(ctx, kvUserID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	vals := map[string][]byte{}
	for _, k := range []string{kvDeviceID, kvUsername, kvToken, kvSigSecret, kvSigPublic} {
		v, err := c.st.Get(ctx, k)
		if err != nil {
			return fmt.Errorf("corrupt account (%s): %w", k, err)
		}
		vals[k] = v
	}
	acct := &Account{UserID: string(uid), DeviceID: string(vals[kvDeviceID]), Username: string(vals[kvUsername])}
	m, err := mls.NewClient(identity(acct.UserID, acct.DeviceID), vals[kvSigSecret], vals[kvSigPublic])
	if err != nil {
		return err
	}
	if err := c.st.LoadMLS(ctx, m); err != nil {
		m.Close()
		return fmt.Errorf("restore MLS state: %w", err)
	}
	c.acct, c.m, c.token = acct, m, string(vals[kvToken])
	return nil
}

// ---- tokens -------------------------------------------------------------------

func (c *Client) authInterceptor() connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			c.tokMu.Lock()
			tok := c.token
			c.tokMu.Unlock()
			if tok != "" && req.Header().Get("Authorization") == "" {
				req.Header().Set("Authorization", "Bearer "+tok)
			}
			return next(ctx, req)
		}
	}
}

func (c *Client) currentToken() string {
	c.tokMu.Lock()
	defer c.tokMu.Unlock()
	return c.token
}

func (c *Client) setToken(ctx context.Context, tok string) error {
	c.tokMu.Lock()
	c.token = tok
	c.tokMu.Unlock()
	return c.st.Update(ctx, func(tx *store.Tx) error { return tx.Set(kvToken, []byte(tok)) })
}

// refreshToken renews the session token when less than half its lifetime is left.
func (c *Client) refreshToken(ctx context.Context) error {
	tok := c.currentToken()
	if tok == "" {
		return ErrNotLoggedIn
	}
	claims := jwt.RegisteredClaims{}
	if _, _, err := jwt.NewParser().ParseUnverified(tok, &claims); err != nil {
		return err
	}
	if claims.ExpiresAt == nil || claims.IssuedAt == nil {
		return nil
	}
	half := claims.IssuedAt.Add(claims.ExpiresAt.Sub(claims.IssuedAt.Time) / 2)
	if time.Now().Before(half) {
		return nil
	}
	if time.Now().After(claims.ExpiresAt.Time) {
		return errors.New("session expired: log in again")
	}
	r, err := c.auth.RefreshToken(ctx, &authv1.RefreshTokenRequest{})
	if err != nil {
		return err
	}
	return c.setToken(ctx, r.Token)
}

// ---- onboarding -------------------------------------------------------------

// Register creates an account and this device. It returns the seed phrase
// that protects the history backup: show it to the user exactly once.
func (c *Client) Register(ctx context.Context, username, password, deviceName string) (string, error) {
	if c.Account() != nil {
		return "", ErrAlreadySetUp
	}
	r, err := c.auth.Register(ctx, &authv1.RegisterRequest{Username: username, Password: password})
	if err != nil {
		return "", err
	}
	phrase, err := crypto.NewSeedPhrase()
	if err != nil {
		return "", err
	}
	if err := c.setupDevice(ctx, r.UserId, username, r.Token, deviceName, phrase); err != nil {
		return "", err
	}
	return phrase, nil
}

// LoginResult summarizes what a new device picked up on login.
type LoginResult struct {
	Restored  int // messages restored from the history backup
	Joined    int // groups joined by itself (External Commit)
	Requested int // groups where online members were asked to add it
}

// Login adds this client as a new device of an existing account and makes it
// a full participant right away: it restores the history backup (with the
// seed phrase), joins every group of the account by itself and asks the other
// members for messages that are missing from the backup.
func (c *Client) Login(ctx context.Context, username, password, deviceName, seedPhrase string) (*LoginResult, error) {
	if c.Account() != nil {
		return nil, ErrAlreadySetUp
	}
	r, err := c.auth.Login(ctx, &authv1.LoginRequest{Username: username, Password: password})
	if err != nil {
		return nil, err
	}
	if err := c.setupDevice(ctx, r.UserId, username, r.Token, deviceName, seedPhrase); err != nil {
		return nil, err
	}
	res := &LoginResult{}
	if seedPhrase != "" {
		if res.Restored, err = c.RestoreHistory(ctx); err != nil {
			return res, fmt.Errorf("restore history: %w", err)
		}
	}
	res.Joined, res.Requested, err = c.JoinMyGroups(ctx)
	return res, err
}

// Relogin refreshes the session of this existing device with the password.
func (c *Client) Relogin(ctx context.Context, password string) error {
	a := c.Account()
	if a == nil {
		return ErrNotLoggedIn
	}
	r, err := c.auth.Login(ctx, &authv1.LoginRequest{Username: a.Username, Password: password, DeviceId: a.DeviceID})
	if err != nil {
		return err
	}
	return c.setToken(ctx, r.Token)
}

func (c *Client) setupDevice(ctx context.Context, userID, username, accountToken, deviceName, phrase string) error {
	var histKey []byte
	if phrase != "" {
		k, err := crypto.HistoryKey(phrase, userID)
		if err != nil {
			return err
		}
		histKey = k
	}
	c.tokMu.Lock()
	c.token = accountToken // RegisterDevice is called with the account token
	c.tokMu.Unlock()
	dev, err := c.auth.RegisterDevice(ctx, &authv1.RegisterDeviceRequest{DeviceName: deviceName})
	if err != nil {
		return err
	}
	sk, pk, err := mls.GenerateSignatureKeyPair()
	if err != nil {
		return err
	}
	m, err := mls.NewClient(identity(userID, dev.DeviceId), sk, pk)
	if err != nil {
		return err
	}
	err = c.st.Update(ctx, func(tx *store.Tx) error {
		for k, v := range map[string][]byte{
			kvServer: []byte(c.opts.ServerURL), kvUserID: []byte(userID), kvDeviceID: []byte(dev.DeviceId),
			kvUsername: []byte(username), kvToken: []byte(dev.Token), kvSigSecret: sk, kvSigPublic: pk,
		} {
			if err := tx.Set(k, v); err != nil {
				return err
			}
		}
		if histKey != nil {
			if err := tx.Set(kvHistKey, histKey); err != nil {
				return err
			}
		}
		return tx.PutUser(userID, username)
	})
	if err != nil {
		m.Close()
		return err
	}
	c.mu.Lock()
	c.acct = &Account{UserID: userID, DeviceID: dev.DeviceId, Username: username}
	c.m = m
	c.mu.Unlock()
	c.tokMu.Lock()
	c.token = dev.Token
	c.tokMu.Unlock()
	return c.EnsureKeyPackages(ctx)
}

// EnsureKeyPackages tops the directory up to KeyPackageBatch unused
// KeyPackages when fewer than KeyPackageLowWater are left.
func (c *Client) EnsureKeyPackages(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		return ErrNotLoggedIn
	}
	cnt, err := c.dir.CountKeyPackages(ctx, &directoryv1.CountKeyPackagesRequest{})
	if err != nil {
		return err
	}
	if cnt.Available >= KeyPackageLowWater {
		return nil
	}
	var kps [][]byte
	for i := int64(0); i < KeyPackageBatch-cnt.Available; i++ {
		kp, err := c.m.GenerateKeyPackage()
		if err != nil {
			return err
		}
		kps = append(kps, kp)
	}
	// Persist the secrets before publishing: a published KeyPackage whose
	// secrets were lost could never be joined with.
	if err := c.persistLocked(ctx, nil); err != nil {
		return err
	}
	if cnt.Available == 0 {
		_, err = c.dir.PublishKeyPackages(ctx, &directoryv1.PublishKeyPackagesRequest{KeyPackages: kps})
	} else {
		_, err = c.dir.RefillKeyPackages(ctx, &directoryv1.RefillKeyPackagesRequest{KeyPackages: kps})
	}
	return err
}

// persistLocked writes pending MLS changes plus fn's writes atomically.
// Caller holds c.mu.
func (c *Client) persistLocked(ctx context.Context, fn func(*store.Tx) error) error {
	changes, err := c.m.TakeChanges()
	if err != nil {
		return err
	}
	return c.st.Update(ctx, func(tx *store.Tx) error {
		if err := tx.ApplyMLSChanges(changes); err != nil {
			return err
		}
		if fn != nil {
			return fn(tx)
		}
		return nil
	})
}
