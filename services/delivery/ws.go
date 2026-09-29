package delivery

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"google.golang.org/protobuf/proto"

	deliveryv1 "github.com/kantracity/kantrae2e/gen/kantra/delivery/v1"
	"github.com/kantracity/kantrae2e/pkg/authmiddleware"
)

// WSPath is the realtime endpoint (roadmap 2.3).
const WSPath = "/v1/ws"

// pollInterval is a safety net for missed in-process notifications
// (e.g. messages accepted by another instance of the service).
const pollInterval = 15 * time.Second

// Hub wakes up WebSocket connections of devices that have new queue entries.
type Hub struct {
	mu    sync.Mutex
	conns map[string]map[chan struct{}]struct{}
}

func NewHub() *Hub { return &Hub{conns: map[string]map[chan struct{}]struct{}{}} }

func (h *Hub) subscribe(deviceID string) (chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	h.mu.Lock()
	if h.conns[deviceID] == nil {
		h.conns[deviceID] = map[chan struct{}]struct{}{}
	}
	h.conns[deviceID][ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.conns[deviceID], ch)
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
		for ch := range h.conns[d] {
			select {
			case ch <- struct{}{}:
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
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(1 << 20)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	wake, unsubscribe := s.hub.subscribe(did)
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
				wctx, wcancel := context.WithTimeout(ctx, 10*time.Second)
				err := conn.Write(wctx, websocket.MessageBinary, b)
				wcancel()
				if err != nil {
					return
				}
				lastSent = e.Id
			}
			if len(env) < 100 {
				break
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-wake:
		case <-ticker.C:
		}
	}
}
