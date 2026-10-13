package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/kantracity/kantrae2e/client/transport"
)

// migrationTest: one HTTP/3 connection, then the client's address changes;
// does the next request still use the same connection?
func migrationTest(base string) {
	pem, _ := os.ReadFile(*caFile)
	pool, _ := x509.SystemCertPool()
	pool.AppendCertsFromPEM(pem)
	tr := transport.New(&tls.Config{RootCAs: pool}, transport.OnlyQUIC)
	hc := tr.Client(30 * time.Second)
	get := func(label string) {
		t := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		req, _ := http.NewRequestWithContext(ctx, "GET", base+"/", nil)
		resp, err := hc.Do(req)
		if err != nil {
			fmt.Printf("  %-22s error after %s: %v\n", label, r(time.Since(t)), err)
			return
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		fmt.Printf("  %-22s %s %s\n", label, resp.Proto, r(time.Since(t)))
	}
	get("first (handshake)")
	get("same connection")
	if *idle > 0 {
		time.Sleep(*idle)
		get(fmt.Sprintf("after %s idle", *idle))
	}
	for i := 0; i < *rounds; i++ {
		// A request in flight while the address changes.
		done := make(chan struct{})
		go func() { get("in flight"); close(done) }()
		time.Sleep(time.Duration(sim.delay.Load()) / 2)
		sim.cut()
		if *nudge {
			tr.NetworkChanged()
		}
		<-done
		get("after address change")
		get("next")
	}
}
