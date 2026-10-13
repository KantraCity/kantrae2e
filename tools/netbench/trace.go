package main

import (
	"fmt"
	"net/http"
	"sync/atomic"
	"time"
)

// tracing prints every request (path, duration) while on.
var tracing atomic.Bool

type traceRT struct{ next http.RoundTripper }

func (t traceRT) RoundTrip(r *http.Request) (*http.Response, error) {
	if !tracing.Load() {
		return t.next.RoundTrip(r)
	}
	t0 := time.Now()
	resp, err := t.next.RoundTrip(r)
	proto := ""
	if resp != nil {
		proto = resp.Proto
	}
	fmt.Printf("    %-60s %-9s %s %v\n", r.URL.Path, proto, time.Since(t0).Round(time.Millisecond), err)
	return resp, err
}

// NetworkChanged passes the notice on to the wrapped transport.
func (t traceRT) NetworkChanged() {
	if nc, ok := t.next.(interface{ NetworkChanged() }); ok {
		nc.NetworkChanged()
	}
}
