package main

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"time"
)

// Diagnostics log: what went wrong on this device, kept on the device and
// sent only by the person ("Отчёт об ошибках" in Settings). It holds the
// app's own log lines (errors, reconnects; never message text, the client
// core logs no plaintext), Go crash traces and UI script errors.
//
// logs/kantra.log rotates to kantra.log.1 at logMaxSize; logs/crash.log
// receives the trace of a crash (debug.SetCrashOutput), shown in the next
// report.

const (
	logMaxSize   = 1 << 20
	reportTail   = 256 << 10
	earlyBufSize = 64 << 10
)

type diagLog struct {
	mu    sync.Mutex
	dir   string
	f     *os.File
	size  int64
	early bytes.Buffer // lines before the data directory is known
}

var diag = &diagLog{}

func init() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	log.SetOutput(io.MultiWriter(os.Stderr, diag))
}

func (d *diagLog) Write(p []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.f == nil {
		if d.early.Len()+len(p) <= earlyBufSize {
			d.early.Write(p)
		}
		return len(p), nil
	}
	if d.size+int64(len(p)) > logMaxSize {
		d.rotateLocked()
	}
	n, err := d.f.Write(p)
	d.size += int64(n)
	return len(p), err
}

func (d *diagLog) rotateLocked() {
	d.f.Close()
	path := filepath.Join(d.dir, "kantra.log")
	_ = os.Rename(path, path+".1")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		d.f = nil
		return
	}
	d.f, d.size = f, 0
}

// open starts writing to <dataDir>/logs once the data directory is known
// (on Android only after the Java side attached).
func (d *diagLog) open(dataDir string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.f != nil {
		return
	}
	dir := filepath.Join(dataDir, "logs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, "kantra.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	st, _ := f.Stat()
	d.dir, d.f = dir, f
	if st != nil {
		d.size = st.Size()
	}
	fmt.Fprintf(f, "\n=== %s start %s (%d) %s/%s ===\n", time.Now().Format(time.RFC3339), appVersion, appBuild, platformName, runtime.GOARCH)
	d.size += int64(d.early.Len())
	_, _ = d.early.WriteTo(f)

	// A crash of the previous run is kept for the report; this run's crash
	// (if any) replaces it.
	crash := filepath.Join(dir, "crash.log")
	if st, err := os.Stat(crash); err == nil && st.Size() > 0 {
		_ = os.Rename(crash, filepath.Join(dir, "crash-prev.log"))
	}
	if cf, err := os.OpenFile(crash, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600); err == nil {
		_ = debug.SetCrashOutput(cf, debug.CrashOptions{})
		cf.Close()
	}
}

// tail returns up to n last bytes of the log (both rotated parts).
func (d *diagLog) tail(n int) string {
	d.mu.Lock()
	dir := d.dir
	early := d.early.String()
	d.mu.Unlock()
	if dir == "" {
		return early
	}
	var b []byte
	for _, name := range []string{"kantra.log.1", "kantra.log"} {
		if x, err := os.ReadFile(filepath.Join(dir, name)); err == nil {
			b = append(b, x...)
		}
	}
	if len(b) > n {
		b = b[len(b)-n:]
		if i := bytes.IndexByte(b, '\n'); i >= 0 {
			b = b[i+1:]
		}
	}
	return string(b)
}

func (d *diagLog) crash() string {
	d.mu.Lock()
	dir := d.dir
	d.mu.Unlock()
	if dir == "" {
		return ""
	}
	var out []string
	for _, name := range []string{"crash-prev.log", "crash.log"} {
		if x, err := os.ReadFile(filepath.Join(dir, name)); err == nil && len(bytes.TrimSpace(x)) > 0 {
			if len(x) > 64<<10 {
				x = x[:64<<10]
			}
			out = append(out, string(x))
		}
	}
	return strings.Join(out, "\n---\n")
}

// LogClient records an error of the UI (uncaught script error, failed
// call). Only the error text and where it happened, never message content.
func (m *Messenger) LogClient(text string) {
	text = strings.ReplaceAll(strings.TrimSpace(text), "\n", " ⏎ ")
	if len(text) > 2000 {
		text = text[:2000] + "…"
	}
	log.Printf("ui: %s", text)
}

// DiagnosticReport: the text the person can copy or save and send to the
// developers. It holds the app version, platform, connection state, the
// recent log and crash traces; no messages, contacts or keys.
func (m *Messenger) DiagnosticReport() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Kantra %s (сборка %d)\n", appVersion, appBuild)
	fmt.Fprintf(&b, "Платформа: %s %s/%s, %s\n", platformName, runtime.GOOS, runtime.GOARCH, runtime.Version())
	fmt.Fprintf(&b, "Время: %s\n", time.Now().Format(time.RFC3339))
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	fmt.Fprintf(&b, "Память Go: %d МБ, горутин: %d\n", ms.Sys>>20, runtime.NumGoroutine())
	m.mu.Lock()
	online := 0
	for _, s := range m.slots {
		if s.online {
			online++
		}
	}
	fmt.Fprintf(&b, "Аккаунтов: %d, на связи: %d\n", len(m.slots), online)
	m.mu.Unlock()
	if cr := diag.crash(); cr != "" {
		b.WriteString("\n===== Падение =====\n")
		b.WriteString(cr)
		b.WriteString("\n")
	}
	b.WriteString("\n===== Журнал =====\n")
	b.WriteString(diag.tail(reportTail))
	return b.String()
}
