package main

import (
	"crypto/tls"
	"net/http"
	"time"

	"github.com/kantracity/kantrae2e/client/transport"
)

func newTransport(cfg *tls.Config) *http.Client {
	mode := transport.OnlyTCP
	switch *transportFlag {
	case "h3":
		mode = transport.Auto
	case "quic":
		mode = transport.OnlyQUIC
	}
	return &http.Client{Transport: traceRT{transport.New(cfg, mode)}, Timeout: 2 * time.Minute}
}
