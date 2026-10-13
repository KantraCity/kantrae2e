package delivery

import (
	"context"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/coder/websocket"
	"google.golang.org/protobuf/proto"

	deliveryv1 "github.com/kantracity/kantrae2e/gen/kantra/delivery/v1"
	"github.com/kantracity/kantrae2e/pkg/authmiddleware"
	"github.com/kantracity/kantrae2e/pkg/devicestatus"
)

// WSPath is the realtime endpoint (roadmap 2.3).
const WSPath = "/v1/ws"

// Keep-alive bounds (seconds) for ?keepalive=N. A heartbeat is an empty
// binary frame (an Envelope with id 0) sent when the connection was silent
// for N seconds. Mobile clients choose a long interval: every packet wakes
// the radio, but NAT/carrier timeouts must not kill the idle connection.
var (
	MinKeepAlive = 15 * time.Second // lowered by tests
	MaxKeepAlive = 15 * time.Minute
)

// keepAliveFrom parses ?keepalive=N; 0 means no heartbeats.
func keepAliveFrom(r *http.Request) time.Duration {
	n, err := strconv.Atoi(r.URL.Query().Get("keepalive"))
	if err != nil || n <= 0 {
		return 0
	}
	return max(MinKeepAlive, min(time.Duration(n)*time.Second, MaxKeepAlive))
}

// pollInterval is a safety net for missed in-process notifications
// (e.g. messages accepted by another instance of the service).
const pollInterval = 15 * time.Second

// Hub wakes up WebSocket connections of devices that have new queue entries
// and carries ephemeral envelopes (typing) to connected devices.
type Hub struct {
	mu    sync.Mutex
	conns map[string]map[*sub]struct{}
}

type sub struct {
	wake chan struct{}
	eph  chan *deliveryv1.Envelope
}

func NewHub() *Hub { return &Hub{conns: map[string]map[*sub]struct{}{}} }

func (h *Hub) subscribe(deviceID string) (*sub, func()) {
	sb := &sub{wake: make(chan struct{}, 1), eph: make(chan *deliveryv1.Envelope, 16)}
	h.mu.Lock()
	if h.conns[deviceID] == nil {
		h.conns[deviceID] = map[*sub]struct{}{}
	}
	h.conns[deviceID][sb] = struct{}{}
	h.mu.Unlock()
	return sb, func() {
		h.mu.Lock()
		delete(h.conns[deviceID], sb)
		if len(h.conns[deviceID]) == 0 {
			delete(h.conns, deviceID)
		}
		h.mu.Unlock()
	}
}

// Notify wakes all connections of the given devices (non-blocking).
func (h *Hub) Notify(deviceIDs ...string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, d := range deviceIDs {
		for sb := range h.conns[d] {
			select {
			case sb.wake <- struct{}{}:
			default:
			}
		}
	}
}

// Push hands an ephemeral envelope to the connected devices; it is dropped
// for devices that are offline or too slow (best effort, like "typing").
func (h *Hub) Push(e *deliveryv1.Envelope, deviceIDs ...string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, d := range deviceIDs {
		for sb := range h.conns[d] {
			select {
			case sb.eph <- e:
			default:
			}
		}
	}
}

// WSHandler streams queued Envelopes (binary protobuf frames) to the device
// and marks them delivered when the client acks them (ClientFrame.ack).
// Unacked entries are re-sent on the next connection: at-least-once delivery,
// clients de-duplicate by Envelope.id.
func (s *Service) WSHandler() http.Handler {
	return authmiddleware.JWTMiddleware(s.secret)(http.HandlerFunc(s.serveWS))
}

func (s *Service) serveWS(w http.ResponseWriter, r *http.Request) {
	did, ok := authmiddleware.DeviceIDFromContext(r.Context())
	if !ok {
		http.Error(w, "device-bound token required", http.StatusForbidden)
		return
	}
	if err := devicestatus.RequireActive(r.Context(), s.devices, did); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(1 << 20)
	s.serveFrames(r.Context(), did, keepAliveFrom(r), "ws", wsFrames{conn})
}

// wsFrames: one WebSocket binary message per frame.
type wsFrames struct{ c *websocket.Conn }

func (f wsFrames) ReadFrame(ctx context.Context) ([]byte, error) {
	for {
		typ, data, err := f.c.Read(ctx)
		if err != nil {
			return nil, err
		}
		if typ == websocket.MessageBinary {
			return data, nil
		}
	}
}

func (f wsFrames) WriteFrame(ctx context.Context, b []byte) error {
	return f.c.Write(ctx, websocket.MessageBinary, b)
}

func (f wsFrames) Fail(reason string) { f.c.Close(websocket.StatusInternalError, reason) }

// frameConn is a realtime connection carrying binary frames: Envelopes to
// the device (an empty frame is a heartbeat), ClientFrames (acks) back.
// WebSocket (TCP) and WebTransport (QUIC) both implement it.
type frameConn interface {
	ReadFrame(ctx context.Context) ([]byte, error)
	WriteFrame(ctx context.Context, b []byte) error
	Fail(reason string)
}

// serveFrames streams queued Envelopes to the device and marks them
// delivered when the client acks them (ClientFrame.ack). Unacked entries
// are re-sent on the next connection: at-least-once delivery, clients
// de-duplicate by Envelope.id.
func (s *Service) serveFrames(parent context.Context, did string, keepAlive time.Duration, kind string, conn frameConn) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	sb, unsubscribe := s.hub.subscribe(did)
	defer unsubscribe()

	// Reader: acks (and pings) from the client.
	pings := make(chan struct{}, 1)
	go func() {
		defer cancel()
		for {
			data, err := conn.ReadFrame(ctx)
			if err != nil {
				return
			}
			var f deliveryv1.ClientFrame
			if proto.Unmarshal(data, &f) != nil {
				continue
			}
			if a := f.GetAck(); a != nil {
				if err := s.ack(ctx, did, a.Ids); err != nil {
					s.log.Warn().Err(err).Msg("ack failed")
				}
			}
			if f.GetPing() != nil {
				select {
				case pings <- struct{}{}:
				default:
				}
			}
		}
	}()

	s.log.Info().Str("device", did).Str("via", kind).Msg("realtime connected")
	defer s.log.Info().Str("device", did).Str("via", kind).Msg("realtime disconnected")

	var lastSent int64
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	var heartbeat <-chan time.Time
	if keepAlive > 0 {
		t := time.NewTicker(keepAlive)
		defer t.Stop()
		heartbeat = t.C
	}
	lastWrite := time.Now()
	write := func(b []byte) bool {
		wctx, wcancel := context.WithTimeout(ctx, 10*time.Second)
		defer wcancel()
		if conn.WriteFrame(wctx, b) != nil {
			return false
		}
		lastWrite = time.Now()
		return true
	}
	for {
		for {
			env, err := s.pending(ctx, did, lastSent, 100)
			if err != nil {
				if ctx.Err() == nil {
					s.log.Error().Err(err).Msg("queue read failed")
					conn.Fail("queue error")
				}
				return
			}
			for _, e := range env {
				b, _ := proto.Marshal(e)
				if !write(b) {
					return
				}
				lastSent = e.Id
			}
			if len(env) < 100 {
				break
			}
		}
	wait:
		select {
		case <-ctx.Done():
			return
		case <-sb.wake:
		case <-ticker.C:
		case e := <-sb.eph:
			b, _ := proto.Marshal(e)
			if !write(b) {
				return
			}
			goto wait
		case <-pings:
			if !write(nil) {
				return
			}
			goto wait
		case <-heartbeat:
			// Only when silent: traffic already keeps the connection alive.
			if time.Since(lastWrite) >= keepAlive-time.Second {
				if !write(nil) {
					return
				}
			}
			goto wait
		}
	}
}
