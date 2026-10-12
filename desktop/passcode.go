package main

// Local passcode, like Telegram's: a 4-digit code that locks the app on this
// device (all accounts). It is checked here, never sent anywhere; the file
// keeps only a salted PBKDF2 hash. While the app is locked (or would be, when
// it comes back after the auto-lock time) notifications show no text.

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

type passcodeFile struct {
	Salt     []byte `json:"salt"`
	Hash     []byte `json:"hash"`
	AutoLock int    `json:"autolock"` // minutes in the background before locking; 0 = only on start
}

// PasscodeState for the UI.
type PasscodeState struct {
	Enabled  bool `json:"enabled"`
	Locked   bool `json:"locked"`
	AutoLock int  `json:"autoLock"`
	// Wrong attempts: wait until (unix ms) before the next one.
	RetryAt int64 `json:"retryAt,omitempty"`
}

var passcodeRE = regexp.MustCompile(`^[0-9]{4}$`)

func (m *Messenger) passcodePath() string { return filepath.Join(m.dir, "passcode.json") }

func hashPasscode(code string, salt []byte) []byte {
	h, _ := pbkdf2.Key(sha256.New, code, salt, 200_000, 32)
	return h
}

// loadPasscode reads the file (called once at start: the app starts locked).
func (m *Messenger) loadPasscode() {
	b, err := os.ReadFile(m.passcodePath())
	if err != nil {
		return
	}
	var f passcodeFile
	if json.Unmarshal(b, &f) != nil || len(f.Hash) == 0 {
		return
	}
	m.mu.Lock()
	m.passcode = &f
	m.locked = true
	m.mu.Unlock()
}

func (m *Messenger) savePasscode(f *passcodeFile) error {
	if f == nil {
		if err := os.Remove(m.passcodePath()); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	b, _ := json.Marshal(f)
	return os.WriteFile(m.passcodePath(), b, 0o600)
}

// lockedLocked: locked now, or the app is in the background longer than
// the auto-lock time (it will ask for the code when it comes back).
// Caller holds m.mu.
func (m *Messenger) lockedLocked() bool {
	if m.passcode == nil {
		return false
	}
	if m.locked {
		return true
	}
	return !m.foreground && m.passcode.AutoLock > 0 && !m.bgSince.IsZero() &&
		time.Since(m.bgSince) >= time.Duration(m.passcode.AutoLock)*time.Minute
}

// onForeground updates the lock when the app is shown / hidden. Caller holds m.mu.
func (m *Messenger) onForegroundLocked(fg bool) (lockedNow bool) {
	if !fg {
		m.bgSince = time.Now()
		return false
	}
	if m.lockedLocked() && !m.locked {
		m.locked = true
		return true
	}
	return false
}

func (m *Messenger) GetPasscode() PasscodeState {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := PasscodeState{Enabled: m.passcode != nil, Locked: m.lockedLocked()}
	if m.passcode != nil {
		st.AutoLock = m.passcode.AutoLock
	}
	if time.Now().Before(m.retryAt) {
		st.RetryAt = m.retryAt.UnixMilli()
	}
	return st
}

// Unlock checks the code; after 5 wrong attempts the next ones wait.
func (m *Messenger) Unlock(code string) (bool, error) {
	m.mu.Lock()
	f := m.passcode
	if f == nil {
		m.locked = false
		m.mu.Unlock()
		return true, nil
	}
	if wait := time.Until(m.retryAt); wait > 0 {
		m.mu.Unlock()
		return false, errors.New("Слишком много попыток — подождите " + wait.Round(time.Second).String())
	}
	m.mu.Unlock()
	good := subtle.ConstantTimeCompare(hashPasscode(code, f.Salt), f.Hash) == 1
	m.mu.Lock()
	defer m.mu.Unlock()
	if !good {
		m.failures++
		if m.failures >= 5 {
			m.retryAt = time.Now().Add(time.Duration(m.failures-4) * 30 * time.Second)
		}
		return false, nil
	}
	m.failures, m.retryAt = 0, time.Time{}
	m.locked = false
	m.bgSince = time.Time{}
	return true, nil
}

// Lock locks the app now (the lock icon), if a passcode is set.
func (m *Messenger) Lock() {
	m.mu.Lock()
	ok := m.passcode != nil
	if ok {
		m.locked = true
	}
	m.mu.Unlock()
	if ok {
		m.emit(UIEvent{Type: "locked"})
	}
}

// SetPasscode sets or changes the code (4 digits; "" turns it off). The
// current code is needed when one is set.
func (m *Messenger) SetPasscode(current, code string) error {
	m.mu.Lock()
	f := m.passcode
	m.mu.Unlock()
	if f != nil && subtle.ConstantTimeCompare(hashPasscode(current, f.Salt), f.Hash) != 1 {
		return errors.New("Неверный текущий код-пароль")
	}
	if code == "" {
		if err := m.savePasscode(nil); err != nil {
			return err
		}
		m.mu.Lock()
		m.passcode, m.locked = nil, false
		m.mu.Unlock()
		return nil
	}
	if !passcodeRE.MatchString(code) {
		return errors.New("Код-пароль — 4 цифры")
	}
	nf := &passcodeFile{Salt: make([]byte, 16), AutoLock: 60}
	if f != nil {
		nf.AutoLock = f.AutoLock
	}
	_, _ = rand.Read(nf.Salt)
	nf.Hash = hashPasscode(code, nf.Salt)
	if err := m.savePasscode(nf); err != nil {
		return err
	}
	m.mu.Lock()
	m.passcode = nf
	m.mu.Unlock()
	return nil
}

// SetAutoLock: minutes in the background before the app locks (0: only
// when it starts or is locked by hand).
func (m *Messenger) SetAutoLock(minutes int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.passcode == nil {
		return errors.New("Сначала задайте код-пароль")
	}
	nf := *m.passcode
	nf.AutoLock = minutes
	if err := m.savePasscode(&nf); err != nil {
		return err
	}
	m.passcode = &nf
	return nil
}
