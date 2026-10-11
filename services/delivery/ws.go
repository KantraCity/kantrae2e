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

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	sb, unsubscribe := s.hub.subscribe(did)
	defer unsubscribe()

	// Reader: acks from the client.
	go func() {
		defer cancel()
		for {
			typ, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			if typ != websocket.MessageBinary {
				continue
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
		}
	}()

	s.log.Info().Str("device", did).Msg("ws connected")
	defer s.log.Info().Str("device", did).Msg("ws disconnected")

	var lastSent int64
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	keepAlive := keepAliveFrom(r)
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
		if conn.Write(wctx, websocket.MessageBinary, b) != nil {
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
					conn.Close(websocket.StatusInternalError, "queue error")
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
