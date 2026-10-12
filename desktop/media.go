package main

import (
	"bytes"
	"container/list"
	"context"
	"mime"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/kantracity/kantrae2e/client/core"
)

// Media is served to the UI decrypted, by URL: GET /media/<chat>/<uid>.<ext>
// (the extension only tells the WebView the type; Android's asset loader
// drops query strings). <img> and <video> then work natively, with Range
// requests for seeking. The handler lives in this process — the desktop
// webview, the local web server of web mode, or the Android app itself.

const mediaPrefix = "/media/"

// mediaCache keeps recently viewed decrypted files in memory (LRU by size).
type mediaCache struct {
	mu    sync.Mutex
	max   int
	size  int
	ll    *list.List
	items map[string]*list.Element
}

type mediaEntry struct {
	key  string
	name string
	data []byte
}

func newMediaCache(max int) *mediaCache {
	return &mediaCache{max: max, ll: list.New(), items: map[string]*list.Element{}}
}

func (c *mediaCache) get(key string) (*mediaEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		c.ll.MoveToFront(el)
		return el.Value.(*mediaEntry), true
	}
	return nil, false
}

func (c *mediaCache) put(e *mediaEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.items[e.key]; ok || len(e.data) > c.max/2 {
		return
	}
	c.items[e.key] = c.ll.PushFront(e)
	c.size += len(e.data)
	for c.size > c.max {
		el := c.ll.Back()
		old := el.Value.(*mediaEntry)
		c.ll.Remove(el)
		delete(c.items, old.key)
		c.size -= len(old.data)
	}
}

var decrypted = newMediaCache(192 << 20)

// mediaExt picks the URL extension from the media's type or name.
func mediaExt(ref *core.MediaRef) string {
	if ext := path.Ext(ref.Name); ext != "" && len(ext) <= 6 {
		return strings.ToLower(ext)
	}
	if exts, _ := mime.ExtensionsByType(ref.Mime); len(exts) > 0 {
		return exts[0]
	}
	return ".bin"
}

// mediaURL is where the UI loads a media message from.
func mediaURL(chatID, uid string, ref *core.MediaRef) string {
	return mediaPrefix + chatID + "/" + uid + mediaExt(ref)
}

func (m *Messenger) serveMedia(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, mediaPrefix)
	chatID, file, ok := strings.Cut(rest, "/")
	if !ok || chatID == "" || file == "" {
		http.NotFound(w, r)
		return
	}
	uid := strings.TrimSuffix(file, path.Ext(file))
	c, err := m.loggedIn()
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	msg, err := c.Message(ctx, chatID, uid)
	if err != nil || msg.Deleted {
		http.NotFound(w, r)
		return
	}
	ref, err := core.ParseMedia(msg)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	e, ok := decrypted.get(ref.Hash)
	if !ok {
		name, data, err := c.DownloadMedia(ctx, msg)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		e = &mediaEntry{key: ref.Hash, name: name, data: data}
		decrypted.put(e)
	}
	ct := ref.Mime
	if ct == "" {
		ct = mime.TypeByExtension(mediaExt(ref))
	}
	if ct == "" {
		ct = http.DetectContentType(e.data)
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "private, max-age=86400")
	if r.URL.Query().Get("download") != "" {
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": e.name}))
	}
	// Serves Range requests (video seeking).
	http.ServeContent(w, r, e.name, time.Time{}, bytes.NewReader(e.data))
}

// mediaMiddleware routes /media/ to the Messenger, everything else to the
// embedded frontend.
func mediaMiddleware(m *Messenger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, mediaPrefix) {
				m.serveMedia(w, r)
				return
			}
			if strings.HasPrefix(r.URL.Path, droppedPrefix) {
				serveDropped(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
