// netbench measures what users feel on slow networks: app start (until the
// realtime connection is up), message delivery, recovery after a network
// change and the first message to a new contact. It runs the real client
// core against a server through a delay-adding proxy.
//
//	go run ./tools/netbench -server localhost:443 -ca deploy/caddy-root.crt -rtt 0,50,150
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"flag"
	"fmt"
	"log"
	"math/rand/v2"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kantracity/kantrae2e/client/core"
)

var (
	server    = flag.String("server", "localhost:443", "server host:port (TCP and UDP)")
	caFile    = flag.String("ca", "", "extra root CA (Caddy's local CA)")
	rtts      = flag.String("rtt", "0,50,150", "round-trip times to simulate, ms")
	loss      = flag.Int("loss", 0, "UDP loss, per mille")
	transport = flag.String("transport", "h2", "client transport: h2 (HTTP/2 + WebSocket) or h3 (QUIC)")
	samples   = flag.Int("n", 15, "delivery samples per RTT")
	verbose   = flag.Bool("v", false, "details")
)

type user struct {
	*core.Client
	name   string
	db     string
	mu     sync.Mutex
	events chan core.Event
	cancel context.CancelFunc
}

var sim = &netsim{conns: map[net.Conn]struct{}{}}

func main() {
	flag.Parse()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	pc, err := net.ListenPacket("udp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		log.Fatal(err)
	}
	go sim.serveTCP(ln, *server)
	go sim.serveUDP(pc, *server)
	base := fmt.Sprintf("https://localhost:%d", port)
	dir, _ := os.MkdirTemp("", "netbench")
	defer os.RemoveAll(dir)
	ctx := context.Background()
	tag := strconv.Itoa(rand.IntN(1e6))

	alice := open(ctx, base, dir, "alice"+tag)
	register(ctx, alice, tag)
	bob := open(ctx, base, dir, "bob"+tag)
	register(ctx, bob, tag)
	dm, err := alice.OpenDirect(ctx, bob.name)
	must(err)
	must(bob.Sync(ctx))
	alice.listen()
	bob.listen()
	alice.waitFor(core.EventConnected, 10*time.Second)
	bob.waitFor(core.EventConnected, 10*time.Second)

	fmt.Printf("transport %s, UDP loss %d‰\n\n", *transport, *loss)
	fmt.Printf("%-8s %12s %12s %14s %16s %16s\n", "RTT", "start", "deliver p50", "deliver p90", "after net change", "first contact")
	for _, s := range strings.Split(*rtts, ",") {
		ms, _ := strconv.Atoi(strings.TrimSpace(s))
		sim.delay.Store(int64(time.Duration(ms) * time.Millisecond / 2))
		sim.loss.Store(int64(*loss))
		time.Sleep(200 * time.Millisecond)

		// App start: open the profile, connect.
		bob.close()
		sim.cut()
		t0 := time.Now()
		bob = reopen(ctx, base, bob)
		bob.listen()
		bob.waitFor(core.EventConnected, time.Minute)
		start := time.Since(t0)
		alice.drain()

		// Steady delivery: send → shown on the other device.
		var lat []time.Duration
		for i := 0; i < *samples; i++ {
			lat = append(lat, deliver(ctx, alice, bob, dm, fmt.Sprintf("m%d", i)))
		}
		if *verbose {
			fmt.Printf("  [deliver] %v\n", lat)
		}
		slices.Sort(lat)

		// Network change: every connection dies; the next message must
		// still arrive (both apps reconnect).
		sim.cut()
		alice.Reconnect()
		bob.Reconnect()
		t1 := time.Now()
		_, err := alice.SendText(ctx, dm, "after change")
		tries := 1
		for err != nil && time.Since(t1) < time.Minute {
			tries++
			_, err = alice.SendText(ctx, dm, "after change")
		}
		sent := time.Since(t1)
		bob.waitMessage("after change", time.Minute)
		change := time.Since(t1)
		if *verbose {
			fmt.Printf("  [net change] sent after %s (%d tries), received after %s\n", r(sent), tries, r(change))
		}
		alice.drain()

		// First message to someone new (directory, MLS group, Welcome).
		carol := open(ctx, base, dir, fmt.Sprintf("carol%d_%s", ms, tag))
		sim.delay.Store(0)
		register(ctx, carol, tag)
		sim.delay.Store(int64(time.Duration(ms) * time.Millisecond / 2))
		carol.listen()
		carol.waitFor(core.EventConnected, time.Minute)
		t2 := time.Now()
		tracing.Store(*verbose)
		g, err := alice.OpenDirect(ctx, carol.name)
		must(err)
		tracing.Store(false)
		opened := time.Since(t2)
		_, err = alice.SendText(ctx, g, "hello carol")
		must(err)
		sentAt := time.Since(t2)
		carol.waitMessage("hello carol", time.Minute)
		first := time.Since(t2)
		if *verbose {
			fmt.Printf("  [first contact] chat created %s, message sent %s, received %s\n", r(opened), r(sentAt), r(first))
		}
		carol.close()

		fmt.Printf("%-8s %12s %12s %14s %16s %16s\n", fmt.Sprintf("%d ms", ms), r(start), r(lat[len(lat)/2]), r(lat[len(lat)*9/10]), r(change), r(first))
	}
	alice.close()
	bob.close()
}

func r(d time.Duration) string { return d.Round(time.Millisecond).String() }

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

func deliver(ctx context.Context, from, to *user, gid, text string) time.Duration {
	t := time.Now()
	_, err := from.SendText(ctx, gid, text)
	must(err)
	to.waitMessage(text, time.Minute)
	return time.Since(t)
}

func httpClient() *http.Client {
	cfg := &tls.Config{}
	if *caFile != "" {
		pem, err := os.ReadFile(*caFile)
		must(err)
		pool, _ := x509.SystemCertPool()
		if pool == nil {
			pool = x509.NewCertPool()
		}
		pool.AppendCertsFromPEM(pem)
		cfg.RootCAs = pool
	}
	return newTransport(cfg)
}

func open(ctx context.Context, base, dir, name string) *user {
	u := &user{name: name, db: filepath.Join(dir, name+".db"), events: make(chan core.Event, 1024)}
	c, err := core.Open(ctx, core.Options{ServerURL: base, DBPath: u.db, HTTPClient: httpClient(), DisableAutoBackup: true,
		OnEvent: func(e core.Event) {
			select {
			case u.events <- e:
			default:
			}
		}})
	must(err)
	u.Client = c
	return u
}

func reopen(ctx context.Context, base string, old *user) *user {
	u := open(ctx, base, filepath.Dir(old.db), old.name)
	return u
}

func (u *user) listen() {
	ctx, cancel := context.WithCancel(context.Background())
	u.cancel = cancel
	go func() { _ = u.Listen(ctx) }()
}

func (u *user) close() {
	if u.cancel != nil {
		u.cancel()
	}
	_ = u.Close()
}

func (u *user) drain() {
	for {
		select {
		case <-u.events:
		default:
			return
		}
	}
}

func (u *user) waitFor(t core.EventType, d time.Duration) {
	deadline := time.After(d)
	for {
		select {
		case e := <-u.events:
			if e.Type == t {
				return
			}
		case <-deadline:
			log.Fatalf("%s: no %s within %s", u.name, t, d)
		}
	}
}

func (u *user) waitMessage(text string, d time.Duration) {
	deadline := time.After(d)
	for {
		select {
		case e := <-u.events:
			if e.Type == core.EventMessage && e.Message != nil && e.Message.Body == text {
				return
			}
		case <-deadline:
			log.Fatalf("%s: message %q not received within %s", u.name, text, d)
		}
	}
}

// register waits out the gateway's sign-up rate limit (5 per minute).
func register(ctx context.Context, u *user, tag string) {
	for {
		err := u.Register(ctx, u.name, "password-"+tag, "", "bench")
		if err == nil {
			return
		}
		if !strings.Contains(err.Error(), "429") {
			log.Fatal(err)
		}
		time.Sleep(10 * time.Second)
	}
}
