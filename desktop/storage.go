package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/kantracity/kantrae2e/client/mediacache"
)

// Data and storage (Settings → Данные и память): downloaded media are kept
// on disk (client/mediacache: ciphertext, shared by the accounts of this
// device) up to a limit the person chooses; the cache can be cleared.

type storagePrefs struct {
	CacheLimit *int64 `json:"cacheLimit,omitempty"`
}

// defaultCacheLimit: phones have less room than computers.
func defaultCacheLimit() int64 {
	if platformName == "android" || platformName == "ios" {
		return 1 << 30 // 1 GB
	}
	return 2 << 30 // 2 GB
}

func (m *Messenger) storagePath() string { return filepath.Join(m.dir, "storage.json") }

func (m *Messenger) loadStoragePrefs() storagePrefs {
	var p storagePrefs
	if b, err := os.ReadFile(m.storagePath()); err == nil {
		_ = json.Unmarshal(b, &p)
	}
	return p
}

// openMediaCache is called by start() once the data directory is known.
func (m *Messenger) openMediaCache() {
	limit := defaultCacheLimit()
	if p := m.loadStoragePrefs(); p.CacheLimit != nil {
		limit = *p.CacheLimit
	}
	c, err := mediacache.Open(filepath.Join(m.dir, "media-cache"), limit)
	if err != nil {
		return // works without it, downloading every time
	}
	m.cache = c
	go c.Trim()
}

type StorageInfo struct {
	// Downloaded media on this device; CacheLimit 0 = no limit.
	CacheBytes int64 `json:"cacheBytes"`
	CacheFiles int   `json:"cacheFiles"`
	CacheLimit int64 `json:"cacheLimit"`
	// Messages and keys of the accounts (databases), and the diagnostics log.
	DataBytes int64 `json:"dataBytes"`
	LogBytes  int64 `json:"logBytes"`
}

func dirSize(dir string, keep func(name string) bool) int64 {
	var n int64
	_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && (keep == nil || keep(info.Name())) {
			n += info.Size()
		}
		return nil
	})
	return n
}

// StorageInfo reports how much this device keeps.
func (m *Messenger) StorageInfo() StorageInfo {
	var s StorageInfo
	if m.cache != nil {
		s.CacheBytes, s.CacheFiles = m.cache.Usage()
		s.CacheLimit = m.cache.Limit()
	}
	if entries, err := os.ReadDir(m.dir); err == nil {
		for _, e := range entries {
			if info, err := e.Info(); err == nil && !e.IsDir() && strings.Contains(e.Name(), ".db") {
				s.DataBytes += info.Size()
			}
		}
	}
	s.LogBytes = dirSize(filepath.Join(m.dir, "logs"), nil)
	return s
}

// SetCacheLimit sets the media cache limit in bytes (0: no limit) and
// trims the cache to it.
func (m *Messenger) SetCacheLimit(limit int64) error {
	if limit < 0 {
		limit = 0
	}
	p := m.loadStoragePrefs()
	p.CacheLimit = &limit
	b, _ := json.Marshal(p)
	if err := os.WriteFile(m.storagePath(), b, 0o600); err != nil {
		return err
	}
	if m.cache != nil {
		m.cache.SetLimit(limit)
	}
	return nil
}

// ClearCache deletes the downloaded media (they download again when
// opened) and the decrypted copies in memory.
func (m *Messenger) ClearCache() error {
	decrypted.clear()
	if m.cache == nil {
		return nil
	}
	return m.cache.Clear()
}
