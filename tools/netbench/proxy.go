package main

// A tiny network simulator: it forwards TCP and UDP to the server and adds a
// one-way delay in each direction (and optional UDP loss), so round-trip
// costs show up like on a real mobile network. The kernel's netem would be
// better but is not available everywhere.

import (
	"io"
	"log"
	"math/rand/v2"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

type netsim struct {
	delay atomic.Int64 // one-way, ns
	loss  atomic.Int64 // UDP loss per mille
	mu    sync.Mutex
	conns map[net.Conn]struct{}
}

func (n *netsim) oneWay() time.Duration { return time.Duration(n.delay.Load()) }

// cut closes every proxied TCP connection, like a phone switching networks.
func (n *netsim) cut() {
	n.mu.Lock()
	defer n.mu.Unlock()
	for c := range n.conns {
		c.Close()
	}
	n.conns = map[net.Conn]struct{}{}
}

func (n *netsim) track(c net.Conn) {
	n.mu.Lock()
	n.conns[c] = struct{}{}
	n.mu.Unlock()
}

func (n *netsim) serveTCP(ln net.Listener, target string) {
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go func() {
			up, err := net.Dial("tcp", target)
			if err != nil {
				c.Close()
				return
			}
			n.track(c)
			n.track(up)
			go n.pipe(up, c)
			n.pipe(c, up)
		}()
	}
}

// pipe copies src→dst, holding every chunk for the one-way delay while
// keeping the order (a delay line, not a sleep per chunk).
func (n *netsim) pipe(dst, src net.Conn) {
	type chunk struct {
		at   time.Time
		data []byte
	}
	q := make(chan chunk, 4096)
	go func() {
		defer dst.Close()
		for ch := range q {
			if d := time.Until(ch.at); d > 0 {
				time.Sleep(d)
			}
			if _, err := dst.Write(ch.data); err != nil {
				for range q {
				}
				return
			}
		}
	}()
	buf := make([]byte, 64<<10)
	for {
		k, err := src.Read(buf)
		if k > 0 {
			q <- chunk{time.Now().Add(n.oneWay()), append([]byte(nil), buf[:k]...)}
		}
		if err != nil {
			close(q)
			if err != io.EOF {
				src.Close()
			}
			return
		}
	}
}

// serveUDP relays datagrams per client address with the same delay.
func (n *netsim) serveUDP(pc net.PacketConn, target string) {
	raddr, err := net.ResolveUDPAddr("udp", target)
	if err != nil {
		log.Fatal(err)
	}
	var mu sync.Mutex
	peers := map[string]*net.UDPConn{}
	buf := make([]byte, 64<<10)
	for {
		k, from, err := pc.ReadFrom(buf)
		if err != nil {
			return
		}
		mu.Lock()
		up := peers[from.String()]
		if up == nil {
			up, err = net.DialUDP("udp", nil, raddr)
			if err != nil {
				mu.Unlock()
				continue
			}
			peers[from.String()] = up
			go func(from net.Addr, up *net.UDPConn) {
				b := make([]byte, 64<<10)
				for {
					k, err := up.Read(b)
					if err != nil {
						return
					}
					data := append([]byte(nil), b[:k]...)
					if n.drop() {
						continue
					}
					time.AfterFunc(n.oneWay(), func() { _, _ = pc.WriteTo(data, from) })
				}
			}(from, up)
		}
		mu.Unlock()
		if n.drop() {
			continue
		}
		data := append([]byte(nil), buf[:k]...)
		time.AfterFunc(n.oneWay(), func() { _, _ = up.Write(data) })
	}
}

func (n *netsim) drop() bool {
	l := n.loss.Load()
	return l > 0 && rand.Int64N(1000) < l
}
