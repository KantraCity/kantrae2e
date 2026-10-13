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
	fmt.Printf("    %-60s %s\n", r.URL.Path, time.Since(t0).Round(time.Millisecond))
	return resp, err
}
