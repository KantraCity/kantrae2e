package main

// Updates from GitHub Releases. CI publishes every build as a release with
// the apps and latest.json — versions, file names and SHA-256 sums —
// signed with an Ed25519 key kept in the repository's secrets; the app
// knows only the public key. An update is used only if the signature and
// the file's hash match, so a tampered download (or release) is ignored.
//
// Desktop: downloaded in the background, installed when the app quits
// (or at the next start). Android: downloaded, installed with the system
// installer on a tap. iPhone: AltStore / SideStore update it (the app only
// says a new version exists).

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// UpdateManifest is latest.json.
type UpdateManifest struct {
	Version string                 `json:"version"`
	Build   int                    `json:"build"`
	Date    string                 `json:"date"`
	Assets  map[string]UpdateAsset `json:"assets"` // by platform key
}

type UpdateAsset struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// UpdateState for the UI.
type UpdateState struct {
	Current string `json:"current"`
	Build   int    `json:"build"`
	// "off" (local build), "none", "checking", "downloading", "ready"
	// (installed on restart / tap to install), "manual" (iPhone: AltStore),
	// "failed".
	State   string `json:"state"`
	Latest  string `json:"latest,omitempty"`
	Error   string `json:"error,omitempty"`
	Page    string `json:"page,omitempty"` // the release page
	Install string `json:"install"`        // "restart", "installer" or "altstore"
}

type updater struct {
	mu     sync.Mutex
	st     UpdateState
	staged string // the downloaded, verified file
	kick   chan struct{}
}

func (m *Messenger) upd() *updater {
	m.updOnce.Do(func() {
		install := "restart"
		switch platformName {
		case "android":
			install = "installer"
		case "ios":
			install = "altstore"
		}
		m.updater = &updater{kick: make(chan struct{}, 1),
			st: UpdateState{Current: appVersion, Build: appBuild, State: "none", Install: install}}
		if appBuild == 0 || updatePublicKey == "" || m.web {
			m.updater.st.State = "off"
		}
	})
	return m.updater
}

func updatePlatform() string {
	switch platformName {
	case "android":
		return "android-" + runtime.GOARCH
	case "ios":
		return "ios"
	}
	return runtime.GOOS + "-" + runtime.GOARCH
}

func releaseBase() string {
	if u := os.Getenv("KANTRA_UPDATE_URL"); u != "" { // tests, mirrors
		return strings.TrimRight(u, "/") + "/"
	}
	return "https://github.com/" + updateRepository + "/releases/latest/download/"
}

// updateLoop checks shortly after start and then every 6 hours.
func (m *Messenger) updateLoop() {
	u := m.upd()
	if u.st.State == "off" {
		return
	}
	select {
	case <-time.After(20 * time.Second):
	case <-u.kick:
	}
	for {
		if err := m.checkUpdate(context.Background()); err != nil {
			log.Printf("update check: %v", err)
		}
		select {
		case <-time.After(6 * time.Hour):
		case <-u.kick:
		}
	}
}

func (u *updater) set(f func(*UpdateState)) {
	u.mu.Lock()
	f(&u.st)
	u.mu.Unlock()
}

// GetUpdate reports the version and the update state.
func (m *Messenger) GetUpdate() UpdateState {
	u := m.upd()
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.st
}

// CheckForUpdate checks now (Settings).
func (m *Messenger) CheckForUpdate() {
	select {
	case m.upd().kick <- struct{}{}:
	default:
	}
}

func (m *Messenger) checkUpdate(ctx context.Context) error {
	u := m.upd()
	u.mu.Lock()
	if u.st.State == "off" || u.st.State == "downloading" || u.st.State == "ready" {
		u.mu.Unlock()
		return nil
	}
	u.st.State, u.st.Error = "checking", ""
	u.mu.Unlock()
	m.emit(UIEvent{Type: "update"})
	man, err := m.fetchManifest(ctx)
	if err != nil {
		u.set(func(s *UpdateState) { s.State, s.Error = "failed", err.Error() })
		m.emit(UIEvent{Type: "update"})
		return err
	}
	asset, ok := man.Assets[updatePlatform()]
	if man.Build <= appBuild || !ok {
		u.set(func(s *UpdateState) { s.State = "none" })
		m.emit(UIEvent{Type: "update"})
		return nil
	}
	page := "https://github.com/" + updateRepository + "/releases/tag/v" + man.Version
	if platformName == "ios" {
		u.set(func(s *UpdateState) { s.State, s.Latest, s.Page = "manual", man.Version, page })
		m.emit(UIEvent{Type: "update"})
		return nil
	}
	u.set(func(s *UpdateState) { s.State, s.Latest, s.Page = "downloading", man.Version, page })
	m.emit(UIEvent{Type: "update"})
	path, err := m.downloadAsset(ctx, asset)
	if err == nil {
		err = platformStageUpdate(path, man.Version)
	}
	if err != nil {
		u.set(func(s *UpdateState) { s.State, s.Error = "failed", err.Error() })
		m.emit(UIEvent{Type: "update"})
		return err
	}
	u.mu.Lock()
	u.staged = path
	u.st.State = "ready"
	u.mu.Unlock()
	m.emit(UIEvent{Type: "update"})
	return nil
}

func (m *Messenger) fetchManifest(ctx context.Context) (*UpdateManifest, error) {
	body, err := m.get(ctx, releaseBase()+"latest.json", 1<<20)
	if err != nil {
		return nil, err
	}
	sig, err := m.get(ctx, releaseBase()+"latest.json.sig", 1024)
	if err != nil {
		return nil, err
	}
	if err := verifyManifest(body, sig, updatePublicKey); err != nil {
		return nil, err
	}
	var man UpdateManifest
	if err := json.Unmarshal(body, &man); err != nil {
		return nil, err
	}
	return &man, nil
}

var errBadSignature = errors.New("update: bad signature")

func verifyManifest(body, sig []byte, pub string) error {
	key, err := base64.StdEncoding.DecodeString(pub)
	if err != nil || len(key) != ed25519.PublicKeySize {
		return errors.New("update: no valid public key")
	}
	s, err := base64.StdEncoding.DecodeString(string(trimSpace(sig)))
	if err != nil || !ed25519.Verify(key, body, s) {
		return errBadSignature
	}
	return nil
}

func trimSpace(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r' || b[len(b)-1] == ' ') {
		b = b[:len(b)-1]
	}
	return b
}

func (m *Messenger) get(ctx context.Context, url string, limit int64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := m.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

// downloadAsset fetches an asset to the update directory and checks it.
func (m *Messenger) downloadAsset(ctx context.Context, a UpdateAsset) (string, error) {
	dir := filepath.Join(m.dir, "update")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, filepath.Base(a.Name))
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releaseBase()+a.Name, nil)
	if err != nil {
		return "", err
	}
	// Big files: no overall client timeout (the context limits it).
	hc := *m.hc
	hc.Timeout = 0
	resp, err := hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: %s", a.Name, resp.Status)
	}
	f, err := os.Create(path + ".part")
	if err != nil {
		return "", err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, a.Size+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && (n != a.Size || hex.EncodeToString(h.Sum(nil)) != a.SHA256) {
		err = errors.New("update: the download does not match the signed hash")
	}
	if err != nil {
		os.Remove(path + ".part")
		return "", err
	}
	return path, os.Rename(path+".part", path)
}

// ApplyUpdate: desktop — restart now with the new version; Android — open
// the installer; iPhone — the release page (AltStore / SideStore).
func (m *Messenger) ApplyUpdate() error {
	u := m.upd()
	u.mu.Lock()
	st, staged := u.st, u.staged
	u.mu.Unlock()
	switch {
	case st.State == "manual":
		return nil // the UI opens st.Page
	case st.State != "ready" || staged == "":
		return errors.New("Обновление ещё не загружено")
	}
	return platformApplyUpdate(staged)
}
