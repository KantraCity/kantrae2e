package delivery

// The realtime connection over WebTransport (QUIC): the same frames as the
// WebSocket, length-prefixed on one bidirectional stream the client opens.
// QUIC connects in one round trip, keeps going when the phone changes
// networks (connection migration) and has no head-of-line blocking.
// Clients fall back to the WebSocket when UDP does not get through.

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	"github.com/quic-go/webtransport-go"

	"github.com/kantracity/kantrae2e/pkg/authmiddleware"
	"github.com/kantracity/kantrae2e/pkg/devicestatus"
)

// WTPath is the WebTransport endpoint.
const WTPath = "/v1/wt"

// maxFrame bounds a frame read from the client (acks are tiny).
const maxFrame = 1 << 20

// WebTransportServer serves WTPath on its own UDP address with a
// self-signed certificate that clients pin (see RealtimeInfo).
func (s *Service) WebTransportServer(addr string) *webtransport.Server {
	certs := s.wtCerts
	wt := &webtransport.Server{
		H3: &http3.Server{
			Addr:      addr,
			TLSConfig: http3.ConfigureTLSConfig(&tls.Config{GetCertificate: certs.get, MinVersion: tls.VersionTLS13}),
			QUICConfig: &quic.Config{
				MaxIdleTimeout:  90 * time.Second,
				EnableDatagrams: true,
				// A returning client opens its session in the first flight.
				// Replaying the CONNECT only opens another session.
				Allow0RTT: true,
			},
		},
		// Native clients send no Origin; browsers are not served here.
		CheckOrigin: func(r *http.Request) bool { return r.Header.Get("Origin") == "" },
	}
	webtransport.ConfigureHTTP3Server(wt.H3)
	mux := http.NewServeMux()
	mux.Handle(WTPath, authmiddleware.JWTMiddleware(s.secret)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		did, ok := authmiddleware.DeviceIDFromContext(r.Context())
		if !ok {
			http.Error(w, "device-bound token required", http.StatusForbidden)
			return
		}
		if err := devicestatus.RequireActive(r.Context(), s.devices, did); err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
		sess, err := wt.Upgrade(w, r)
		if err != nil {
			return
		}
		defer sess.CloseWithError(0, "")
		ctx, cancel := context.WithTimeout(sess.Context(), 10*time.Second)
		str, err := sess.AcceptStream(ctx)
		cancel()
		if err != nil {
			return
		}
		defer str.Close()
		s.serveFrames(sess.Context(), did, keepAliveFrom(r), "webtransport", &wtFrames{str: str, r: bufio.NewReader(str)})
	})))
	wt.H3.Handler = mux
	return wt
}

// wtFrames: uvarint length + payload on a WebTransport stream.
type wtFrames struct {
	str *webtransport.Stream
	r   *bufio.Reader
	wmu sync.Mutex
}

func (f *wtFrames) ReadFrame(ctx context.Context) ([]byte, error) {
	n, err := binary.ReadUvarint(f.r)
	if err != nil {
		return nil, err
	}
	if n > maxFrame {
		return nil, errors.New("frame too large")
	}
	b := make([]byte, n)
	_, err = io.ReadFull(f.r, b)
	return b, err
}

func (f *wtFrames) WriteFrame(ctx context.Context, b []byte) error {
	f.wmu.Lock()
	defer f.wmu.Unlock()
	if d, ok := ctx.Deadline(); ok {
		_ = f.str.SetWriteDeadline(d)
	}
	buf := binary.AppendUvarint(make([]byte, 0, len(b)+binary.MaxVarintLen64), uint64(len(b)))
	_, err := f.str.Write(append(buf, b...))
	return err
}

func (f *wtFrames) Fail(string) { f.str.CancelWrite(1) }

// wtCert is the WebTransport endpoint's own certificate: self-signed,
// renewed weekly. Clients learn its SHA-256 over HTTPS (RealtimeInfo,
// served through the gateway with the domain's certificate) and pin it,
// so trust is the same as the domain's — without sharing the gateway's
// key files.
type wtCert struct {
	mu   sync.Mutex
	cur  *tls.Certificate
	prev *tls.Certificate
	made time.Time
}

const wtCertRenew = 7 * 24 * time.Hour

func (c *wtCert) current() (*tls.Certificate, *tls.Certificate) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cur == nil || time.Since(c.made) > wtCertRenew {
		cert, err := selfSigned(2 * wtCertRenew)
		if err == nil {
			c.prev, c.cur, c.made = c.cur, cert, time.Now()
		}
	}
	return c.cur, c.prev
}

func (c *wtCert) get(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	cur, _ := c.current()
	if cur == nil {
		return nil, errors.New("no certificate")
	}
	return cur, nil
}

// hashes: base64 SHA-256 of the current and the previous certificate.
func (c *wtCert) hashes() []string {
	cur, prev := c.current()
	var out []string
	for _, x := range []*tls.Certificate{cur, prev} {
		if x != nil && len(x.Certificate) > 0 {
			h := sha256.Sum256(x.Certificate[0])
			out = append(out, base64.StdEncoding.EncodeToString(h[:]))
		}
	}
	return out
}

func selfSigned(valid time.Duration) (*tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "kantra-webtransport"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(valid),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	return &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, nil
}

// RealtimeInfoPath tells clients where the WebTransport endpoint is.
const RealtimeInfoPath = "/v1/realtime-info"

// RealtimeInfo is served over HTTPS (through the gateway).
type RealtimeInfo struct {
	Port   int      `json:"wtPort"`
	Hashes []string `json:"wtCertSha256"`
}

// RealtimeInfoHandler answers RealtimeInfoPath; publicPort is the UDP port
// clients reach the endpoint at (0: WebTransport off → 404).
func (s *Service) RealtimeInfoHandler(publicPort int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if publicPort == 0 {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(RealtimeInfo{Port: publicPort, Hashes: s.wtCerts.hashes()})
	})
}
