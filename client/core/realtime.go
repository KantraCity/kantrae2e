package core

// The realtime connection: WebTransport over QUIC when the network lets
// UDP through, the WebSocket over TCP otherwise. Both carry the same
// frames (see services/delivery/ws.go and wt.go).
//
// QUIC connects faster and keeps the connection when the phone changes
// networks, so a network change only needs a ping to check the path
// instead of a new connection.

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/quic-go/quic-go/http3"
	"github.com/quic-go/webtransport-go"
	"google.golang.org/protobuf/proto"

	"github.com/kantracity/kantrae2e/client/store"
	"github.com/kantracity/kantrae2e/client/transport"
	deliveryv1 "github.com/kantracity/kantrae2e/gen/kantra/delivery/v1"
)

// rtConn is one realtime connection.
type rtConn interface {
	Read(ctx context.Context) ([]byte, error)
	Write(ctx context.Context, b []byte) error
	Close()
	Via() string // "webtransport" or "websocket"
}

type wsRT struct{ c *websocket.Conn }

func (w wsRT) Read(ctx context.Context) ([]byte, error) {
	_, b, err := w.c.Read(ctx)
	return b, err
}
func (w wsRT) Write(ctx context.Context, b []byte) error {
	return w.c.Write(ctx, websocket.MessageBinary, b)
}
func (w wsRT) Close()      { w.c.CloseNow() }
func (w wsRT) Via() string { return "websocket" }

type wtRT struct {
	tr   *webtransport.Transport
	sess *webtransport.Session
	str  *webtransport.Stream
	r    *bufio.Reader
	wmu  sync.Mutex
}

func (w *wtRT) Read(ctx context.Context) ([]byte, error) {
	type res struct {
		b   []byte
		err error
	}
	ch := make(chan res, 1)
	go func() {
		n, err := binary.ReadUvarint(w.r)
		if err != nil {
			ch <- res{nil, err}
			return
		}
		if n > 4<<20 {
			ch <- res{nil, errors.New("frame too large")}
			return
		}
		b := make([]byte, n)
		_, err = io.ReadFull(w.r, b)
		ch <- res{b, err}
	}()
	select {
	case r := <-ch:
		return r.b, r.err
	case <-ctx.Done():
		// The pending read cannot be resumed: this connection is done.
		w.Close()
		return nil, ctx.Err()
	}
}

func (w *wtRT) Write(ctx context.Context, b []byte) error {
	w.wmu.Lock()
	defer w.wmu.Unlock()
	if d, ok := ctx.Deadline(); ok {
		_ = w.str.SetWriteDeadline(d)
	} else {
		_ = w.str.SetWriteDeadline(time.Now().Add(10 * time.Second))
	}
	buf := binary.AppendUvarint(make([]byte, 0, len(b)+binary.MaxVarintLen64), uint64(len(b)))
	_, err := w.str.Write(append(buf, b...))
	return err
}

func (w *wtRT) Close() {
	w.str.CancelRead(0)
	_ = w.sess.CloseWithError(0, "")
	_ = w.tr.Close()
}
func (w *wtRT) Via() string { return "webtransport" }

// wtHeadStart: WebTransport gets this long before the WebSocket is tried
// in parallel (a blocked UDP path must not delay the connection much).
const wtHeadStart = 500 * time.Millisecond

// wtBrokenFor: after WebTransport failed, use the WebSocket this long.
const wtBrokenFor = 5 * time.Minute

// wtEndpoint: where the server's WebTransport endpoint is, and the
// SHA-256 of its (self-signed) certificate, learned over HTTPS.
type wtEndpoint struct {
	url     string
	hashes  [][]byte
	fetched time.Time
}

// wtInfoTTL: how long the endpoint info is reused.
const wtInfoTTL = 10 * time.Minute

var errNoWT = errors.New("the server offers no webtransport")

// wtEndpoint returns the endpoint: from memory, from the database (an app
// start connects at once; a stale copy is refreshed in the background) or
// from the server.
func (c *Client) wtEndpoint(ctx context.Context, fresh bool) (*wtEndpoint, error) {
	c.rtMu.Lock()
	e := c.wtInfo
	c.rtMu.Unlock()
	if e == nil && !fresh {
		if b, err := c.st.Get(ctx, kvWTInfo); err == nil {
			var saved savedWT
			if json.Unmarshal(b, &saved) == nil && saved.URL != "" {
				e = &wtEndpoint{url: saved.URL, fetched: time.UnixMilli(saved.At)}
				for _, h := range saved.Hashes {
					e.hashes = append(e.hashes, h)
				}
				c.rtMu.Lock()
				c.wtInfo = e
				c.rtMu.Unlock()
			}
		}
	}
	if e != nil && !fresh {
		if time.Since(e.fetched) > wtInfoTTL {
			go func() { _, _ = c.fetchWTEndpoint(context.Background()) }()
		}
		return e, nil
	}
	return c.fetchWTEndpoint(ctx)
}

const kvWTInfo = "wt_info"

type savedWT struct {
	URL    string   `json:"url"`
	Hashes [][]byte `json:"hashes"`
	At     int64    `json:"at"`
}

func (c *Client) fetchWTEndpoint(ctx context.Context) (*wtEndpoint, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.opts.ServerURL, "/")+"/v1/realtime-info", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errNoWT
	}
	var info struct {
		Port   int      `json:"wtPort"`
		Hashes []string `json:"wtCertSha256"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&info); err != nil || info.Port == 0 || len(info.Hashes) == 0 {
		return nil, errNoWT
	}
	u, err := url.Parse(c.opts.ServerURL)
	if err != nil {
		return nil, err
	}
	addr := net.JoinHostPort(u.Hostname(), strconv.Itoa(info.Port))
	if c.opts.WebTransportAddr != "" {
		addr = c.opts.WebTransportAddr
	}
	e := &wtEndpoint{url: "https://" + addr + "/v1/wt", fetched: time.Now()}
	for _, h := range info.Hashes {
		if b, err := base64.StdEncoding.DecodeString(h); err == nil && len(b) == sha256.Size {
			e.hashes = append(e.hashes, b)
		}
	}
	if len(e.hashes) == 0 {
		return nil, errNoWT
	}
	c.rtMu.Lock()
	c.wtInfo = e
	c.rtMu.Unlock()
	if b, err := json.Marshal(savedWT{URL: e.url, Hashes: e.hashes, At: e.fetched.UnixMilli()}); err == nil {
		_ = c.st.Update(context.Background(), func(tx *store.Tx) error { return tx.Set(kvWTInfo, b) })
	}
	return e, nil
}

// pinned accepts exactly the certificates whose hash the server published.
func (e *wtEndpoint) pinned(rawCerts [][]byte, _ [][]*x509.Certificate) error {
	if len(rawCerts) == 0 {
		return errors.New("no certificate")
	}
	h := sha256.Sum256(rawCerts[0])
	for _, want := range e.hashes {
		if subtle.ConstantTimeCompare(h[:], want) == 1 {
			return nil
		}
	}
	return errPinMismatch
}

func (c *Client) wtURL(base string) string {
	if ka := time.Duration(c.keepAlive.Load()); ka > 0 {
		return base + fmt.Sprintf("?keepalive=%d", int(ka/time.Second))
	}
	return base
}

func (c *Client) dialWS(ctx context.Context) (rtConn, error) {
	conn, _, err := websocket.Dial(ctx, c.wsURL(), &websocket.DialOptions{
		HTTPClient: c.hc,
		HTTPHeader: http.Header{"Authorization": {"Bearer " + c.currentToken()}},
	})
	if err != nil {
		return nil, err
	}
	conn.SetReadLimit(4 << 20)
	return wsRT{conn}, nil
}

func (c *Client) dialWT(ctx context.Context) (rtConn, error) {
	conn, err := c.dialWTOnce(ctx, false)
	if err != nil && errors.Is(err, errPinMismatch) {
		// The server's certificate changed (restart, weekly renewal):
		// learn the new hash and try again.
		conn, err = c.dialWTOnce(ctx, true)
	}
	return conn, err
}

var errPinMismatch = errors.New("webtransport certificate does not match the published hash")

func (c *Client) dialWTOnce(ctx context.Context, fresh bool) (rtConn, error) {
	ep, err := c.wtEndpoint(ctx, fresh)
	if err != nil {
		return nil, err
	}
	cfg := &tls.Config{
		// The certificate is self-signed and checked against the hash the
		// server published over HTTPS (wtEndpoint.pinned).
		InsecureSkipVerify:    true, //nolint:gosec // pinned below
		VerifyPeerCertificate: ep.pinned,
		NextProtos:            []string{http3.NextProtoH3},
		MinVersion:            tls.VersionTLS13,
		// Resumption: the next connection sends its request in 0-RTT.
		ClientSessionCache: c.wtSessions,
	}
	qc := transport.QUICConfig()
	qc.EnableStreamResetPartialDelivery = true
	d := &webtransport.Transport{TLSClientConfig: cfg, QUICConfig: qc}
	_, sess, err := d.Dial(ctx, c.wtURL(ep.url), http.Header{"Authorization": {"Bearer " + c.currentToken()}})
	if err != nil {
		_ = d.Close()
		if strings.Contains(err.Error(), errPinMismatch.Error()) {
			return nil, fmt.Errorf("%w (%v)", errPinMismatch, err)
		}
		return nil, err
	}
	str, err := sess.OpenStreamSync(ctx)
	if err != nil {
		_ = sess.CloseWithError(0, "")
		_ = d.Close()
		return nil, err
	}
	// The server accepts the stream when it sees data: say hello with an
	// empty ack frame.
	w := &wtRT{sess: sess, str: str, r: bufio.NewReader(str), tr: d}
	if err := w.Write(ctx, nil); err != nil {
		w.Close()
		return nil, err
	}
	return w, nil
}

// dialRealtime connects over WebTransport and/or the WebSocket, whichever
// is up first (WebTransport gets a head start).
func (c *Client) dialRealtime(ctx context.Context) (rtConn, error) {
	c.rtMu.Lock()
	// Plain http:// (Android over USB, adb reverse) forwards TCP only.
	useWT := c.opts.WebTransport && strings.HasPrefix(c.opts.ServerURL, "https://") && time.Now().After(c.wtBrokenUntil)
	c.rtMu.Unlock()
	if !useWT {
		return c.dialWS(ctx)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type result struct {
		conn rtConn
		err  error
		wt   bool
	}
	results := make(chan result, 2)
	launched, received := 1, 0
	go func() {
		conn, err := c.dialWT(ctx)
		results <- result{conn, err, true}
	}()
	wsLaunched := false
	launchWS := func() {
		if wsLaunched {
			return
		}
		wsLaunched = true
		launched++
		go func() {
			conn, err := c.dialWS(ctx)
			results <- result{conn, err, false}
		}()
	}
	startWS := time.NewTimer(wtHeadStart)
	defer startWS.Stop()
	var firstErr error
	for {
		select {
		case <-startWS.C:
			launchWS()
		case r := <-results:
			received++
			if r.err == nil {
				// The slower one, if it still connects, is closed.
				if left := launched - received; left > 0 {
					go func() {
						for ; left > 0; left-- {
							if o := <-results; o.conn != nil {
								o.conn.Close()
							}
						}
					}()
				}
				return r.conn, nil
			}
			if r.wt && ctx.Err() == nil {
				c.rtMu.Lock()
				c.wtBrokenUntil = time.Now().Add(wtBrokenFor)
				c.wtInfo = nil // the certificate may have changed
				c.rtMu.Unlock()
				c.logf("webtransport unavailable, using the websocket: %v", r.err)
				launchWS()
			}
			if firstErr == nil {
				firstErr = r.err
			}
			if received == launched {
				return nil, firstErr
			}
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// pingTimeout: how long a ping may take after a network change.
const pingTimeout = 2500 * time.Millisecond

// pingAlive reports whether the current WebTransport connection still
// answers (a ping is answered with a heartbeat). The WebSocket is not
// checked: a TCP connection does not survive a network change.
func (c *Client) pingAlive(ctx context.Context) bool {
	c.rtMu.Lock()
	conn := c.rt
	c.rtMu.Unlock()
	if conn == nil || conn.Via() != "webtransport" {
		return false
	}
	select {
	case <-c.rtRecv: // stale signal
	default:
	}
	ping, _ := proto.Marshal(&deliveryv1.ClientFrame{Frame: &deliveryv1.ClientFrame_Ping{Ping: &deliveryv1.Ping{}}})
	wctx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()
	if err := conn.Write(wctx, ping); err != nil {
		return false
	}
	select {
	case <-c.rtRecv:
		return true
	case <-wctx.Done():
		c.logf("realtime connection did not answer a ping: reconnecting")
		return false
	}
}
