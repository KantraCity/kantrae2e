// Package transport is the client's HTTP transport: HTTP/3 (QUIC) when the
// network lets UDP through, HTTP/2 over TCP otherwise.
//
// QUIC connects in one round trip instead of two (TCP + TLS), has no
// head-of-line blocking on lossy mobile links and survives NAT rebinding.
// Some networks block or throttle UDP, so QUIC is used only after a probe
// succeeded; until then, and after a QUIC failure, requests go over
// HTTP/2. A failed QUIC request is retried over HTTP/2 when its body can
// be replayed, so a broken UDP path never fails a request.
package transport

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
)

// Mode selects the protocols.
type Mode int

const (
	Auto     Mode = iota // HTTP/3 when it works, HTTP/2 otherwise
	OnlyTCP              // HTTP/2 / HTTP/1.1 only
	OnlyQUIC             // HTTP/3 only (tests)
)

// brokenFor: after a QUIC failure, stay on TCP this long before probing again.
const brokenFor = 5 * time.Minute

// Transport is an http.RoundTripper choosing HTTP/3 or HTTP/2.
type Transport struct {
	mode Mode
	tcp  *http.Transport
	quic *http3.Transport

	mu       sync.Mutex
	conns    map[*quic.Conn]struct{} // live QUIC connections (to nudge)
	ok       map[string]bool         // host → QUIC works
	brokenAt map[string]time.Time    // host → QUIC failed at
	probing  map[string]bool
	// OnChange is called when a host switches protocol ("h3" / "h2").
	OnChange func(host, proto string)
}

// QUICConfig is shared by HTTP/3 and the realtime WebTransport session.
func QUICConfig() *quic.Config {
	return &quic.Config{
		HandshakeIdleTimeout: 3 * time.Second,
		MaxIdleTimeout:       90 * time.Second,
		KeepAlivePeriod:      0, // the realtime session has its own heartbeats
		EnableDatagrams:      true,
	}
}

// New returns a transport; cfg carries extra root CAs (dev CA).
func New(cfg *tls.Config, mode Mode) *Transport {
	if cfg == nil {
		cfg = &tls.Config{}
	}
	cfg = cfg.Clone()
	if cfg.ClientSessionCache == nil {
		// Resumed TLS sessions skip certificate work and allow 0-RTT.
		cfg.ClientSessionCache = tls.NewLRUClientSessionCache(32)
	}
	tcp := http.DefaultTransport.(*http.Transport).Clone()
	tcp.ForceAttemptHTTP2 = true
	tcp.TLSClientConfig = cfg.Clone() // net/http adjusts it (ALPN)
	t := &Transport{
		mode:     mode,
		tcp:      tcp,
		conns:    map[*quic.Conn]struct{}{},
		ok:       map[string]bool{},
		brokenAt: map[string]time.Time{},
		probing:  map[string]bool{},
	}
	t.quic = &http3.Transport{TLSClientConfig: cfg.Clone(), QUICConfig: QUICConfig(), Dial: t.dial}
	return t
}

// dial opens a QUIC connection and remembers it for Nudge.
func (t *Transport) dial(ctx context.Context, addr string, tlsCfg *tls.Config, cfg *quic.Config) (*quic.Conn, error) {
	conn, err := quic.DialAddrEarly(ctx, addr, tlsCfg, cfg)
	if err != nil {
		return nil, err
	}
	t.mu.Lock()
	t.conns[conn] = struct{}{}
	t.mu.Unlock()
	context.AfterFunc(conn.Context(), func() {
		t.mu.Lock()
		delete(t.conns, conn)
		t.mu.Unlock()
	})
	return conn, nil
}

// Nudge makes every QUIC connection send a packet now. After a network
// change the server keeps answering the old address until it hears from
// the new one; a client waiting for a response would otherwise stay
// silent until the idle timeout. The packet opens a reserved (GREASE)
// stream that HTTP/3 servers ignore.
func Nudge(conn *quic.Conn) {
	str, err := conn.OpenUniStream()
	if err != nil {
		return
	}
	_, _ = str.Write([]byte{0x21}) // stream type 0x21: reserved, ignored
	_ = str.Close()
}

// Client wraps the transport in an http.Client.
func (t *Transport) Client(timeout time.Duration) *http.Client {
	return &http.Client{Transport: t, Timeout: timeout}
}

// UsesQUIC reports whether requests to host currently go over HTTP/3.
func (t *Transport) UsesQUIC(host string) bool {
	if t.mode == OnlyQUIC {
		return true
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.mode == Auto && t.ok[host]
}

func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	// WebSocket upgrades and plain http:// (adb over USB) need TCP.
	if t.mode == OnlyTCP || req.URL.Scheme != "https" || strings.EqualFold(req.Header.Get("Upgrade"), "websocket") {
		return t.tcp.RoundTrip(req)
	}
	if t.mode == OnlyQUIC {
		return t.quic.RoundTrip(req)
	}
	host := req.URL.Host
	if !t.UsesQUIC(host) {
		t.maybeProbe(req.URL.Scheme + "://" + host)
		return t.tcp.RoundTrip(req)
	}
	resp, err := t.quicRoundTrip(req)
	if err == nil {
		return resp, nil
	}
	if req.Context().Err() != nil {
		return nil, err
	}
	// A connection that went idle (the server closed it) is not a broken
	// path: retry once on a fresh QUIC connection.
	if r2, ok := replayable(req); ok && isStale(err) {
		t.quic.CloseIdleConnections()
		if resp, err2 := t.quicRoundTrip(r2); err2 == nil {
			return resp, nil
		} else {
			err = err2
		}
		if req.Context().Err() != nil {
			return nil, err
		}
	}
	t.markBroken(host, err)
	// Retry over TCP when the body can be sent again.
	r2, ok := replayable(req)
	if !ok {
		return nil, err
	}
	return t.tcp.RoundTrip(r2)
}

// headerTimeout: a small QUIC request without a response this long is
// taken as a dead path (the server unreachable over UDP, a lost
// migration) and retried over TCP. Uploads are not limited.
const headerTimeout = 3 * time.Second

const smallBody = 256 << 10

var errNoAnswer = errors.New("no answer over QUIC")

func (t *Transport) quicRoundTrip(req *http.Request) (*http.Response, error) {
	if req.ContentLength < 0 || req.ContentLength > smallBody {
		return t.quic.RoundTrip(req)
	}
	ctx, cancel := context.WithCancel(req.Context())
	type result struct {
		resp *http.Response
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		resp, err := t.quic.RoundTrip(req.WithContext(ctx))
		ch <- result{resp, err}
	}()
	timer := time.NewTimer(headerTimeout)
	defer timer.Stop()
	select {
	case r := <-ch:
		if r.err != nil {
			cancel()
			return nil, r.err
		}
		// The body is read later: cancel the context only when it is closed.
		r.resp.Body = &cancelOnClose{ReadCloser: r.resp.Body, cancel: cancel}
		return r.resp, nil
	case <-timer.C:
		cancel()
		return nil, errNoAnswer
	}
}

type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c *cancelOnClose) Close() error {
	err := c.ReadCloser.Close()
	c.cancel()
	return err
}

// replayable returns a copy of req that can be sent again (fresh body).
func replayable(req *http.Request) (*http.Request, bool) {
	if req.Body == nil || req.Body == http.NoBody {
		return req.Clone(req.Context()), true
	}
	if req.GetBody == nil {
		return nil, false
	}
	body, err := req.GetBody()
	if err != nil {
		return nil, false
	}
	r2 := req.Clone(req.Context())
	r2.Body = body
	return r2, true
}

// isStale: the QUIC connection died (idle timeout, closed by the server,
// stateless reset) — a new one will likely work.
func isStale(err error) bool {
	var idle *quic.IdleTimeoutError
	var app *quic.ApplicationError
	var reset *quic.StatelessResetError
	return errors.As(err, &idle) || errors.As(err, &app) || errors.As(err, &reset)
}

func (t *Transport) markBroken(host string, _ error) {
	t.mu.Lock()
	was := t.ok[host]
	t.ok[host] = false
	t.brokenAt[host] = time.Now()
	cb := t.OnChange
	t.mu.Unlock()
	t.quic.CloseIdleConnections()
	if was && cb != nil {
		cb(host, "h2")
	}
}

// maybeProbe checks in the background whether QUIC reaches the host.
func (t *Transport) maybeProbe(base string) {
	host := strings.TrimPrefix(base, "https://")
	t.mu.Lock()
	if t.probing[host] || time.Since(t.brokenAt[host]) < brokenFor {
		t.mu.Unlock()
		return
	}
	t.probing[host] = true
	t.mu.Unlock()
	go func() {
		ok := t.probe(base)
		t.mu.Lock()
		t.probing[host] = false
		if ok {
			t.ok[host] = true
		} else {
			t.brokenAt[host] = time.Now()
		}
		cb := t.OnChange
		t.mu.Unlock()
		if ok && cb != nil {
			cb(host, "h3")
		}
	}()
}

func (t *Transport) probe(base string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/", nil)
	if err != nil {
		return false
	}
	resp, err := t.quic.RoundTrip(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return true // any HTTP answer: the QUIC path works
}

// NetworkChanged forgets what is known about the paths: the new network
// may let UDP through (or not). Open QUIC connections are kept — they
// migrate — after a nudge from the new address; TCP connections are dead.
func (t *Transport) NetworkChanged() {
	t.mu.Lock()
	t.brokenAt = map[string]time.Time{}
	conns := make([]*quic.Conn, 0, len(t.conns))
	for c := range t.conns {
		conns = append(conns, c)
	}
	t.mu.Unlock()
	for _, c := range conns {
		Nudge(c)
	}
	t.tcp.CloseIdleConnections()
}

// CloseIdleConnections closes idle connections of both protocols.
func (t *Transport) CloseIdleConnections() {
	t.tcp.CloseIdleConnections()
	t.quic.CloseIdleConnections()
}
