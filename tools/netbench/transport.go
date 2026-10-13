package main

import (
	"crypto/tls"
	"net/http"
	"time"
)

func newTransport(cfg *tls.Config) *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.ForceAttemptHTTP2 = true
	tr.TLSClientConfig = cfg
	return &http.Client{Transport: traceRT{tr}, Timeout: 2 * time.Minute}
}
