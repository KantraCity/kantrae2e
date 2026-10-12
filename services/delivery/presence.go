package delivery

import (
	"context"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	deliveryv1 "github.com/kantracity/kantrae2e/gen/kantra/delivery/v1"
	"github.com/kantracity/kantrae2e/pkg/authmiddleware"
)

// Presence like Telegram's: "online" while the app is open on one of the
// user's devices (the client calls SetActive every minute while it is, and
// SetActive(false) when it goes to the background), otherwise "last seen"
// at the time of the last activity — as the user's privacy allows.

// activeFor: a device counts as online this long after its last SetActive.
const activeFor = 75 * time.Second

type presenceTracker struct {
	mu     sync.Mutex
	active map[string]devActivity // device id -> activity
}

type devActivity struct {
	user  string
	until time.Time
}

func (p *presenceTracker) set(user, device string, on bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.active == nil {
		p.active = map[string]devActivity{}
	}
	if on {
		p.active[device] = devActivity{user: user, until: time.Now().Add(activeFor)}
	} else {
		delete(p.active, device)
	}
}

func (p *presenceTracker) online(users map[string]bool) map[string]bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	out := map[string]bool{}
	for d, a := range p.active {
		if now.After(a.until) {
			delete(p.active, d)
			continue
		}
		if users[a.user] {
			out[a.user] = true
		}
	}
	return out
}

func (s *Service) SetActive(ctx context.Context, req *deliveryv1.SetActiveRequest) (*deliveryv1.SetActiveResponse, error) {
	uid, did, err := authmiddleware.RequireDevice(ctx)
	if err != nil {
		return nil, err
	}
	s.presence.set(uid, did, req.Active)
	// Last seen = the last moment the app was open.
	if _, err := s.pool.Exec(ctx, `INSERT INTO presence (user_id, last_seen) VALUES ($1, now())
		ON CONFLICT (user_id) DO UPDATE SET last_seen = now()`, uid); err != nil {
		return nil, asConnect(err)
	}
	return &deliveryv1.SetActiveResponse{}, nil
}

func (s *Service) SetPresencePrivacy(ctx context.Context, req *deliveryv1.SetPresencePrivacyRequest) (*deliveryv1.SetPresencePrivacyResponse, error) {
	uid, err := authmiddleware.RequireUser(ctx)
	if err != nil {
		return nil, err
	}
	v := req.Visibility
	if v < deliveryv1.PresenceVisibility_PRESENCE_VISIBILITY_UNSPECIFIED || v > deliveryv1.PresenceVisibility_PRESENCE_VISIBILITY_NOBODY {
		return nil, errf(connect.CodeInvalidArgument, "bad visibility")
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO presence (user_id, visibility) VALUES ($1, $2)
		ON CONFLICT (user_id) DO UPDATE SET visibility = $2`, uid, int16(v)); err != nil {
		return nil, asConnect(err)
	}
	return &deliveryv1.SetPresencePrivacyResponse{}, nil
}

func (s *Service) GetPresencePrivacy(ctx context.Context, _ *deliveryv1.GetPresencePrivacyRequest) (*deliveryv1.GetPresencePrivacyResponse, error) {
	uid, err := authmiddleware.RequireUser(ctx)
	if err != nil {
		return nil, err
	}
	var v int16
	err = s.pool.QueryRow(ctx, `SELECT visibility FROM presence WHERE user_id=$1`, uid).Scan(&v)
	if err != nil && !isNoRows(err) {
		return nil, asConnect(err)
	}
	if v == 0 {
		v = int16(deliveryv1.PresenceVisibility_PRESENCE_VISIBILITY_EVERYBODY)
	}
	return &deliveryv1.GetPresencePrivacyResponse{Visibility: deliveryv1.PresenceVisibility(v)}, nil
}

// GetPresence: like Telegram, whoever hides their own last seen does not see
// the others' either ("recently" instead).
func (s *Service) GetPresence(ctx context.Context, req *deliveryv1.GetPresenceRequest) (*deliveryv1.GetPresenceResponse, error) {
	uid, err := authmiddleware.RequireUser(ctx)
	if err != nil {
		return nil, err
	}
	if len(req.UserIds) > 200 {
		return nil, errf(connect.CodeInvalidArgument, "too many users")
	}
	want := map[string]bool{}
	var ids []string
	for _, id := range req.UserIds {
		if _, err := uuid.Parse(id); err == nil && !want[id] {
			want[id] = true
			ids = append(ids, id)
		}
	}
	out := &deliveryv1.GetPresenceResponse{}
	if len(ids) == 0 {
		return out, nil
	}
	var mine int16
	_ = s.pool.QueryRow(ctx, `SELECT visibility FROM presence WHERE user_id=$1`, uid).Scan(&mine)
	selfHidden := mine == int16(deliveryv1.PresenceVisibility_PRESENCE_VISIBILITY_NOBODY)

	rows, err := s.pool.Query(ctx, `
		SELECT u.id::text, p.last_seen, COALESCE(p.visibility, 0),
		       EXISTS (SELECT 1 FROM group_members a JOIN group_members b ON a.group_id = b.group_id
		               WHERE a.user_id = u.id AND b.user_id = $2)
		FROM unnest($1::uuid[]) AS u(id) LEFT JOIN presence p ON p.user_id = u.id`, ids, uid)
	if err != nil {
		return nil, asConnect(err)
	}
	defer rows.Close()
	online := s.presence.online(want)
	for rows.Next() {
		var id string
		var last *time.Time
		var vis int16
		var shared bool
		if err := rows.Scan(&id, &last, &vis, &shared); err != nil {
			return nil, asConnect(err)
		}
		visible := id == uid || !selfHidden && (vis <= int16(deliveryv1.PresenceVisibility_PRESENCE_VISIBILITY_EVERYBODY) ||
			vis == int16(deliveryv1.PresenceVisibility_PRESENCE_VISIBILITY_CHATS) && shared)
		p := &deliveryv1.Presence{UserId: id, Hidden: !visible}
		if visible {
			p.Online = online[id]
			if last != nil {
				p.LastSeen = last.Unix()
			}
		}
		out.Presence = append(out.Presence, p)
	}
	return out, rows.Err()
}
