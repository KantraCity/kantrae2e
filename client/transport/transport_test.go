package transport

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/quic-go/quic-go/http3"
)

func get(t *testing.T, c *http.Client, url string) string {
	t.Helper()
	resp, err := c.Post(url, "text/plain", strings.NewReader("ping"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.Proto + " " + string(b)
}

func echo(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	_, _ = w.Write(b)
}

func clientCfg(srv *httptest.Server) *tls.Config {
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	return &tls.Config{RootCAs: pool}
}

// No UDP at the server: requests go over TCP and never wait for QUIC.
func TestFallsBackToTCP(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(echo))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()
	tr := New(clientCfg(srv), Auto)
	c := tr.Client(10 * time.Second)
	for i := 0; i < 5; i++ {
		t0 := time.Now()
		if got := get(t, c, srv.URL); got != "HTTP/2.0 ping" {
			t.Fatalf("got %q", got)
		}
		if d := time.Since(t0); d > time.Second {
			t.Fatalf("request took %s", d)
		}
	}
	time.Sleep(200 * time.Millisecond)
	if tr.UsesQUIC(strings.TrimPrefix(srv.URL, "https://")) {
		t.Fatal("QUIC marked usable without a QUIC server")
	}
}

// QUIC on the same port: after the probe, requests use HTTP/3.
func TestSwitchesToQUIC(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(echo))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()
	port := srv.Listener.Addr().(*net.TCPAddr).Port
	pc, err := net.ListenPacket("udp", net.JoinHostPort("127.0.0.1", itoa(port)))
	if err != nil {
		t.Skip("UDP port busy:", err)
	}
	h3 := &http3.Server{Handler: http.HandlerFunc(echo), TLSConfig: http3.ConfigureTLSConfig(srv.TLS.Clone())}
	go func() { _ = h3.Serve(pc) }()
	defer h3.Close()

	tr := New(clientCfg(srv), Auto)
	c := tr.Client(10 * time.Second)
	if got := get(t, c, srv.URL); got != "HTTP/2.0 ping" {
		t.Fatalf("first request: %q (QUIC must not be used before the probe)", got)
	}
	deadline := time.Now().Add(5 * time.Second)
	for !tr.UsesQUIC(strings.TrimPrefix(srv.URL, "https://")) {
		if time.Now().After(deadline) {
			t.Fatal("probe did not succeed")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := get(t, c, srv.URL); got != "HTTP/3.0 ping" {
		t.Fatalf("after the probe: %q", got)
	}
	// The QUIC server goes away: the next request still succeeds over TCP.
	h3.Close()
	pc.Close()
	if got := get(t, c, srv.URL); got != "HTTP/2.0 ping" {
		t.Fatalf("after QUIC failed: %q", got)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }
