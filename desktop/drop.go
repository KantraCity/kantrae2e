package main

import (
	"crypto/rand"
	"encoding/hex"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Files dragged onto the window from the OS. On macOS and Linux the native
// window takes the drop and hands Go the paths (the page never sees the
// files), so the UI gets one-time URLs to fetch them from: GET /dropped/<token>.

const droppedPrefix = "/dropped/"

// DroppedFile is one file the user dropped onto the window.
type DroppedFile struct {
	URL  string `json:"url"`
	Name string `json:"name"`
	Mime string `json:"mime"`
	Size int64  `json:"size"`
}

type droppedFiles struct {
	mu    sync.Mutex
	paths map[string]droppedEntry
}

type droppedEntry struct {
	path string
	at   time.Time
}

var dropped = droppedFiles{paths: map[string]droppedEntry{}}

// filesDropped registers the paths and tells the UI about them.
func (m *Messenger) filesDropped(paths []string) {
	if files := registerDropped(paths); len(files) > 0 {
		m.emit(UIEvent{Type: "files_dropped", Files: files})
	}
}

// registerDropped gives each regular file a one-time URL.
func registerDropped(paths []string) []DroppedFile {
	var files []DroppedFile
	dropped.mu.Lock()
	for tok, e := range dropped.paths { // forget stale, never fetched drops
		if time.Since(e.at) > 10*time.Minute {
			delete(dropped.paths, tok)
		}
	}
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil || !st.Mode().IsRegular() {
			continue
		}
		b := make([]byte, 16)
		_, _ = rand.Read(b)
		tok := hex.EncodeToString(b)
		dropped.paths[tok] = droppedEntry{path: p, at: time.Now()}
		files = append(files, DroppedFile{
			URL:  droppedPrefix + tok,
			Name: filepath.Base(p),
			Mime: mime.TypeByExtension(strings.ToLower(filepath.Ext(p))),
			Size: st.Size(),
		})
	}
	dropped.mu.Unlock()
	return files
}

// serveDropped serves a dropped file once.
func serveDropped(w http.ResponseWriter, r *http.Request) {
	tok := strings.TrimPrefix(r.URL.Path, droppedPrefix)
	dropped.mu.Lock()
	e, ok := dropped.paths[tok]
	delete(dropped.paths, tok)
	dropped.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(e.path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	if ct := mime.TypeByExtension(strings.ToLower(filepath.Ext(e.path))); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, filepath.Base(e.path), time.Time{}, f)
}
