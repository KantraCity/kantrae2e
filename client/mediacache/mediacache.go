// Package mediacache keeps downloaded media on disk so that photos, videos
// and voice messages open without downloading them again.
//
// It stores the ciphertext exactly as the media service serves it, named
// by its BLAKE3 hash (the object key): the files are as private as the
// server's copy (the keys are in the messages) and their integrity is
// checked on every read. The cache is shared by the accounts of a device
// and trimmed to a size limit, least recently used first.
package mediacache

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kantracity/kantrae2e/pkg/blobstore"
)

// Unlimited as a limit keeps every file.
const Unlimited = 0

type Cache struct {
	dir   string
	max   atomic.Int64
	trimM sync.Mutex
	// trimming coalesces trims after a burst of puts.
	trimming atomic.Bool
}

// Open uses dir (created if missing) with a size limit in bytes
// (Unlimited: no limit).
func Open(dir string, limit int64) (*Cache, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	c := &Cache{dir: dir}
	c.max.Store(limit)
	return c, nil
}

func validHash(h string) bool {
	if len(h) < 16 || len(h) > 128 {
		return false
	}
	for _, r := range h {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

func (c *Cache) path(hash string) string { return filepath.Join(c.dir, hash[:2], hash) }

// Get returns the cached ciphertext of hash, verified.
func (c *Cache) Get(hash string) ([]byte, bool) {
	if c == nil || !validHash(hash) {
		return nil, false
	}
	p := c.path(hash)
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, false
	}
	if blobstore.Hash(data) != hash {
		_ = os.Remove(p)
		return nil, false
	}
	now := time.Now()
	_ = os.Chtimes(p, now, now) // recently used
	return data, true
}

// Put stores the ciphertext of hash (it must match).
func (c *Cache) Put(hash string, data []byte) {
	if c == nil || !validHash(hash) || blobstore.Hash(data) != hash {
		return
	}
	if lim := c.max.Load(); lim != Unlimited && int64(len(data)) > lim/4 {
		return // one file never takes most of the cache
	}
	p := c.path(hash)
	if _, err := os.Stat(p); err == nil {
		now := time.Now()
		_ = os.Chtimes(p, now, now)
		return
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".put-*")
	if err != nil {
		return
	}
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if werr != nil || cerr != nil || os.Rename(tmp.Name(), p) != nil {
		_ = os.Remove(tmp.Name())
		return
	}
	if c.trimming.CompareAndSwap(false, true) {
		go func() {
			time.Sleep(2 * time.Second)
			c.trimming.Store(false)
			c.Trim()
		}()
	}
}

type entry struct {
	path string
	size int64
	used time.Time
}

func (c *Cache) entries() ([]entry, int64) {
	var out []entry
	var total int64
	_ = filepath.WalkDir(c.dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".put-") {
			if time.Since(info.ModTime()) > time.Hour {
				_ = os.Remove(p) // left by a crash
			}
			return nil
		}
		out = append(out, entry{p, info.Size(), info.ModTime()})
		total += info.Size()
		return nil
	})
	return out, total
}

// Trim deletes the least recently used files above the limit (down to 90%
// of it).
func (c *Cache) Trim() {
	if c == nil {
		return
	}
	lim := c.max.Load()
	if lim == Unlimited {
		return
	}
	c.trimM.Lock()
	defer c.trimM.Unlock()
	es, total := c.entries()
	if total <= lim {
		return
	}
	sort.Slice(es, func(i, j int) bool { return es[i].used.Before(es[j].used) })
	target := lim / 10 * 9
	for _, e := range es {
		if total <= target {
			break
		}
		if os.Remove(e.path) == nil {
			total -= e.size
		}
	}
}

// SetLimit changes the size limit and trims to it.
func (c *Cache) SetLimit(limit int64) {
	if c == nil {
		return
	}
	c.max.Store(limit)
	c.Trim()
}

// Limit is the size limit in bytes (Unlimited: none).
func (c *Cache) Limit() int64 {
	if c == nil {
		return 0
	}
	return c.max.Load()
}

// Usage returns the bytes and number of files cached.
func (c *Cache) Usage() (bytes int64, files int) {
	if c == nil {
		return 0, 0
	}
	es, total := c.entries()
	return total, len(es)
}

// Clear deletes every cached file.
func (c *Cache) Clear() error {
	if c == nil {
		return nil
	}
	c.trimM.Lock()
	defer c.trimM.Unlock()
	es, _ := c.entries()
	var errs []error
	for _, e := range es {
		if err := os.Remove(e.path); err != nil && !os.IsNotExist(err) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
