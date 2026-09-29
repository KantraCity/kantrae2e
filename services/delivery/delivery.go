// Package delivery implements delivery-service: routing of opaque MLS
// messages, strict per-group Commit ordering and the offline queue.
package delivery

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	deliveryv1 "github.com/kantracity/kantrae2e/gen/kantra/delivery/v1"
	"github.com/kantracity/kantrae2e/gen/kantra/delivery/v1/deliveryv1connect"
	"github.com/kantracity/kantrae2e/pkg/authmiddleware"
	"github.com/kantracity/kantrae2e/pkg/db"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Migrations returns the schema migrations of this service.
func Migrations() fs.FS {
	sub, _ := fs.Sub(migrations, "migrations")
	return sub
}

const Schema = "delivery"

const (
	MaxPayload    = 1 << 20
	MaxGroupIDLen = 64
	// Application messages may lag behind the current epoch by this much
	// (matches the prior-epoch retention of the clients).
	EpochLag = 2
)

type Service struct {
	pool   *pgxpool.Pool
	secret []byte
	hub    *Hub
	log    zerolog.Logger
}

func New(pool *pgxpool.Pool, secret []byte, log zerolog.Logger) *Service {
	return &Service{pool: pool, secret: secret, hub: NewHub(), log: log}
}

// Handler mounts the connect service.
func (s *Service) Handler() (string, http.Handler) {
	return deliveryv1connect.NewDeliveryServiceHandler(s,
		connect.WithInterceptors(authmiddleware.Interceptor(s.secret)))
}

func errf(code connect.Code, format string, args ...any) error {
	return connect.NewError(code, fmt.Errorf(format, args...))
}

func asConnect(err error) error {
	var ce *connect.Error
	if errors.As(err, &ce) {
		return ce
	}
	return connect.NewError(connect.CodeInternal, err)
}

func validGroupID(id string) error {
	if id == "" || len(id) > MaxGroupIDLen {
		return errf(connect.CodeInvalidArgument, "group_id must be 1-%d bytes", MaxGroupIDLen)
	}
	return nil
}

func validDevices(ids []string) error {
	for _, id := range ids {
		if _, err := uuid.Parse(id); err != nil {
			return errf(connect.CodeInvalidArgument, "bad device id %q", id)
		}
	}
	return nil
}

func requireMember(ctx context.Context, tx pgx.Tx, groupID, deviceID string) error {
	var ok bool
	err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM group_members WHERE group_id=$1 AND device_id=$2)`, groupID, deviceID).Scan(&ok)
	if err != nil {
		return err
	}
	if !ok {
		return errf(connect.CodePermissionDenied, "not a member of the group")
	}
	return nil
}

func insertMessage(ctx context.Context, tx pgx.Tx, groupID string, epoch uint64, typ deliveryv1.MessageType, payload []byte, senderUser, sender string) (int64, error) {
	var id int64
	err := tx.QueryRow(ctx,
		`INSERT INTO group_messages (group_id, epoch, message_type, payload, sender_user_id, sender_device_id)
		 VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		groupID, int64(epoch), int16(typ), payload, senderUser, sender).Scan(&id)
	return id, err
}

// storeGroupInfo remembers the GroupInfo of `epoch`, or forgets the stale one
// when the committer did not provide it.
func storeGroupInfo(ctx context.Context, tx pgx.Tx, groupID string, epoch uint64, gi []byte) error {
	if len(gi) == 0 {
		_, err := tx.Exec(ctx, `UPDATE groups SET group_info=NULL, group_info_epoch=NULL WHERE id=$1`, groupID)
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE groups SET group_info=$2, group_info_epoch=$3 WHERE id=$1`, groupID, gi, int64(epoch))
	return err
}

// requireUserInGroup checks that the caller's user has a device in the group
// and that the calling device has not been removed from it.
func requireUserInGroup(ctx context.Context, tx pgx.Tx, groupID, userID, deviceID string) error {
	var inGroup, banned bool
	err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM group_members WHERE group_id=$1 AND user_id=$2),
		        EXISTS (SELECT 1 FROM group_bans WHERE group_id=$1 AND device_id=$3)`,
		groupID, userID, deviceID).Scan(&inGroup, &banned)
	if err != nil {
		return err
	}
	if !inGroup || banned {
		return errf(connect.CodePermissionDenied, "your account is not in this group")
	}
	return nil
}

// casEpoch advances the group epoch iff it equals `epoch` (optimistic lock).
func casEpoch(ctx context.Context, tx pgx.Tx, groupID string, epoch uint64) error {
	tag, err := tx.Exec(ctx,
		`UPDATE groups SET current_epoch = current_epoch + 1 WHERE id = $1 AND current_epoch = $2`,
		groupID, int64(epoch))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		var cur int64
		if err := tx.QueryRow(ctx, `SELECT current_epoch FROM groups WHERE id=$1`, groupID).Scan(&cur); err != nil {
			return errf(connect.CodeNotFound, "no such group")
		}
		return errf(connect.CodeAborted, "epoch conflict: group is at epoch %d, commit was for %d", cur, epoch)
	}
	return nil
}

// fanOut queues message id for all current members except the sender that
// were already in the group at `epoch`, and returns the recipients.
func fanOut(ctx context.Context, tx pgx.Tx, groupID string, messageID int64, sender string, epoch uint64) ([]string, error) {
	rows, err := tx.Query(ctx,
		`INSERT INTO message_queue (device_id, message_id)
		 SELECT device_id, $2 FROM group_members
		 WHERE group_id=$1 AND device_id <> $3 AND joined_epoch <= $4
		 RETURNING device_id::text`, groupID, messageID, sender, int64(epoch))
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

func (s *Service) CreateGroup(ctx context.Context, req *deliveryv1.CreateGroupRequest) (*deliveryv1.CreateGroupResponse, error) {
	uid, did, err := authmiddleware.RequireDevice(ctx)
	if err != nil {
		return nil, err
	}
	if err := validGroupID(req.GroupId); err != nil {
		return nil, err
	}
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO groups (id) VALUES ($1)`, req.GroupId); err != nil {
			if db.IsUniqueViolation(err) {
				return errf(connect.CodeAlreadyExists, "group exists")
			}
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO group_members (group_id, device_id, user_id) VALUES ($1, $2, $3)`,
			req.GroupId, did, uid); err != nil {
			return err
		}
		return storeGroupInfo(ctx, tx, req.GroupId, 0, req.GroupInfo)
	})
	if err != nil {
		return nil, asConnect(err)
	}
	return &deliveryv1.CreateGroupResponse{}, nil
}

// SendCommit is the ordering point of the whole system (roadmap 2.3).
func (s *Service) SendCommit(ctx context.Context, req *deliveryv1.SendCommitRequest) (*deliveryv1.SendCommitResponse, error) {
	uid, did, err := authmiddleware.RequireDevice(ctx)
	if err != nil {
		return nil, err
	}
	switch {
	case validGroupID(req.GroupId) != nil:
		return nil, validGroupID(req.GroupId)
	case len(req.Commit) == 0 || len(req.Commit) > MaxPayload || len(req.Welcome) > MaxPayload:
		return nil, errf(connect.CodeInvalidArgument, "bad payload size")
	case (len(req.Welcome) > 0) != (len(req.AddedDeviceIds) > 0):
		return nil, errf(connect.CodeInvalidArgument, "welcome and added_device_ids go together")
	case len(req.AddedUserIds) != 0 && len(req.AddedUserIds) != len(req.AddedDeviceIds):
		return nil, errf(connect.CodeInvalidArgument, "added_user_ids must match added_device_ids")
	case len(req.GroupInfo) > MaxPayload:
		return nil, errf(connect.CodeInvalidArgument, "bad group_info size")
	}
	if err := validDevices(append(append(append([]string{}, req.AddedDeviceIds...), req.RemovedDeviceIds...), req.AddedUserIds...)); err != nil {
		return nil, err
	}

	var recipients []string
	newEpoch := req.Epoch + 1
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := requireMember(ctx, tx, req.GroupId, did); err != nil {
			return err
		}
		// Optimistic lock: only the first Commit for this epoch wins.
		if err := casEpoch(ctx, tx, req.GroupId, req.Epoch); err != nil {
			return err
		}

		msgID, err := insertMessage(ctx, tx, req.GroupId, req.Epoch, deliveryv1.MessageType_MESSAGE_TYPE_COMMIT, req.Commit, uid, did)
		if err != nil {
			return err
		}
		// Removed members still get this commit so they learn about it.
		if recipients, err = fanOut(ctx, tx, req.GroupId, msgID, did, req.Epoch); err != nil {
			return err
		}

		for _, r := range req.RemovedDeviceIds {
			tag, err := tx.Exec(ctx, `DELETE FROM group_members WHERE group_id=$1 AND device_id=$2`, req.GroupId, r)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				return errf(connect.CodeInvalidArgument, "device %s is not a member", r)
			}
			// A removed device may not rejoin by itself (External Commit).
			if _, err := tx.Exec(ctx, `INSERT INTO group_bans (group_id, device_id) VALUES ($1, $2)
				ON CONFLICT DO NOTHING`, req.GroupId, r); err != nil {
				return err
			}
		}
		for i, a := range req.AddedDeviceIds {
			var addedUser any
			if len(req.AddedUserIds) > 0 {
				addedUser = req.AddedUserIds[i]
			}
			// Being added explicitly lifts an earlier ban.
			if _, err := tx.Exec(ctx, `DELETE FROM group_bans WHERE group_id=$1 AND device_id=$2`, req.GroupId, a); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO group_members (group_id, device_id, joined_epoch, user_id) VALUES ($1, $2, $3, $4)`,
				req.GroupId, a, int64(newEpoch), addedUser); err != nil {
				if db.IsUniqueViolation(err) {
					return errf(connect.CodeInvalidArgument, "device %s is already a member", a)
				}
				return err
			}
		}
		if len(req.Welcome) > 0 {
			wID, err := insertMessage(ctx, tx, req.GroupId, newEpoch, deliveryv1.MessageType_MESSAGE_TYPE_WELCOME, req.Welcome, uid, did)
			if err != nil {
				return err
			}
			for _, a := range req.AddedDeviceIds {
				if _, err := tx.Exec(ctx, `INSERT INTO message_queue (device_id, message_id) VALUES ($1, $2)`, a, wID); err != nil {
					return err
				}
			}
			recipients = append(recipients, req.AddedDeviceIds...)
		}
		return storeGroupInfo(ctx, tx, req.GroupId, newEpoch, req.GroupInfo)
	})
	if err != nil {
		return nil, asConnect(err)
	}
	s.log.Info().Str("group", req.GroupId).Uint64("epoch", newEpoch).
		Int("added", len(req.AddedDeviceIds)).Int("removed", len(req.RemovedDeviceIds)).Msg("commit accepted")
	s.hub.Notify(recipients...)
	return &deliveryv1.SendCommitResponse{NewEpoch: newEpoch}, nil
}

func (s *Service) SendApplication(ctx context.Context, req *deliveryv1.SendApplicationRequest) (*deliveryv1.SendApplicationResponse, error) {
	uid, did, err := authmiddleware.RequireDevice(ctx)
	if err != nil {
		return nil, err
	}
	if err := validGroupID(req.GroupId); err != nil {
		return nil, err
	}
	if len(req.Payload) == 0 || len(req.Payload) > MaxPayload {
		return nil, errf(connect.CodeInvalidArgument, "bad payload size")
	}
	var msgID int64
	var recipients []string
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := requireMember(ctx, tx, req.GroupId, did); err != nil {
			return err
		}
		// Share-lock the group row so a concurrent commit cannot change
		// membership between the epoch check and the fan-out.
		var cur int64
		if err := tx.QueryRow(ctx, `SELECT current_epoch FROM groups WHERE id=$1 FOR SHARE`, req.GroupId).Scan(&cur); err != nil {
			return err
		}
		if int64(req.Epoch) > cur || int64(req.Epoch) < cur-EpochLag {
			return errf(connect.CodeFailedPrecondition, "stale or future epoch %d (current %d)", req.Epoch, cur)
		}
		msgID, err = insertMessage(ctx, tx, req.GroupId, req.Epoch, deliveryv1.MessageType_MESSAGE_TYPE_APPLICATION, req.Payload, uid, did)
		if err != nil {
			return err
		}
		recipients, err = fanOut(ctx, tx, req.GroupId, msgID, did, req.Epoch)
		return err
	})
	if err != nil {
		return nil, asConnect(err)
	}
	s.hub.Notify(recipients...)
	return &deliveryv1.SendApplicationResponse{MessageId: msgID}, nil
}

func (s *Service) FetchWelcome(ctx context.Context, req *deliveryv1.FetchWelcomeRequest) (*deliveryv1.FetchWelcomeResponse, error) {
	_, did, err := authmiddleware.RequireDevice(ctx)
	if err != nil {
		return nil, err
	}
	if req.DeviceId != "" && req.DeviceId != did {
		return nil, errf(connect.CodePermissionDenied, "can only fetch own welcome")
	}
	var w []byte
	err = s.pool.QueryRow(ctx,
		`SELECT m.payload FROM message_queue q JOIN group_messages m ON m.id = q.message_id
		 WHERE q.device_id=$1 AND m.group_id=$2 AND m.message_type=$3
		 ORDER BY q.id DESC LIMIT 1`, did, req.GroupId, int16(deliveryv1.MessageType_MESSAGE_TYPE_WELCOME)).Scan(&w)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errf(connect.CodeNotFound, "no welcome")
	}
	if err != nil {
		return nil, asConnect(err)
	}
	return &deliveryv1.FetchWelcomeResponse{Welcome: w}, nil
}

// pending returns undelivered envelopes of a device with queue id > after.
func (s *Service) pending(ctx context.Context, deviceID string, after int64, limit int) ([]*deliveryv1.Envelope, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT q.id, m.group_id, m.epoch, m.message_type, m.payload, m.sender_device_id::text,
		        COALESCE(m.sender_user_id::text, ''), m.created_at
		 FROM message_queue q JOIN group_messages m ON m.id = q.message_id
		 WHERE q.device_id=$1 AND q.delivered_at IS NULL AND q.id > $2
		 ORDER BY q.id LIMIT $3`, deviceID, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*deliveryv1.Envelope
	for rows.Next() {
		var e deliveryv1.Envelope
		var epoch int64
		var typ int16
		var created time.Time
		if err := rows.Scan(&e.Id, &e.GroupId, &epoch, &typ, &e.Payload, &e.SenderDeviceId, &e.SenderUserId, &created); err != nil {
			return nil, err
		}
		e.Epoch, e.Type, e.CreatedAt = uint64(epoch), deliveryv1.MessageType(typ), created.Unix()
		out = append(out, &e)
	}
	return out, rows.Err()
}

func (s *Service) ack(ctx context.Context, deviceID string, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := s.pool.Exec(ctx,
		`UPDATE message_queue SET delivered_at = now()
		 WHERE device_id=$1 AND id = ANY($2) AND delivered_at IS NULL`, deviceID, ids)
	return err
}

func (s *Service) FetchPending(ctx context.Context, req *deliveryv1.FetchPendingRequest) (*deliveryv1.FetchPendingResponse, error) {
	_, did, err := authmiddleware.RequireDevice(ctx)
	if err != nil {
		return nil, err
	}
	limit := int(req.Limit)
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	env, err := s.pending(ctx, did, 0, limit)
	if err != nil {
		return nil, asConnect(err)
	}
	return &deliveryv1.FetchPendingResponse{Envelopes: env}, nil
}

func (s *Service) Ack(ctx context.Context, req *deliveryv1.AckRequest) (*deliveryv1.AckResponse, error) {
	_, did, err := authmiddleware.RequireDevice(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.ack(ctx, did, req.Ids); err != nil {
		return nil, asConnect(err)
	}
	return &deliveryv1.AckResponse{}, nil
}

func (s *Service) GetGroup(ctx context.Context, req *deliveryv1.GetGroupRequest) (*deliveryv1.GetGroupResponse, error) {
	_, did, err := authmiddleware.RequireDevice(ctx)
	if err != nil {
		return nil, err
	}
	resp := &deliveryv1.GetGroupResponse{}
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := requireMember(ctx, tx, req.GroupId, did); err != nil {
			return err
		}
		var cur int64
		if err := tx.QueryRow(ctx, `SELECT current_epoch FROM groups WHERE id=$1`, req.GroupId).Scan(&cur); err != nil {
			return err
		}
		resp.CurrentEpoch = uint64(cur)
		rows, err := tx.Query(ctx, `SELECT device_id::text FROM group_members WHERE group_id=$1 ORDER BY joined_at`, req.GroupId)
		if err != nil {
			return err
		}
		resp.MemberDeviceIds, err = pgx.CollectRows(rows, pgx.RowTo[string])
		return err
	})
	if err != nil {
		return nil, asConnect(err)
	}
	return resp, nil
}

// ---- multi-device -----------------------------------------------------------

func (s *Service) ListMyGroups(ctx context.Context, _ *deliveryv1.ListMyGroupsRequest) (*deliveryv1.ListMyGroupsResponse, error) {
	uid, did, err := authmiddleware.RequireDevice(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx,
		`SELECT g.id, g.current_epoch, bool_or(m.device_id = $2)
		 FROM group_members m JOIN groups g ON g.id = m.group_id
		 WHERE m.user_id = $1
		   AND NOT EXISTS (SELECT 1 FROM group_bans b WHERE b.group_id = g.id AND b.device_id = $2)
		 GROUP BY g.id, g.current_epoch ORDER BY g.id`, uid, did)
	if err != nil {
		return nil, asConnect(err)
	}
	defer rows.Close()
	resp := &deliveryv1.ListMyGroupsResponse{}
	for rows.Next() {
		var g deliveryv1.MyGroup
		var epoch int64
		if err := rows.Scan(&g.GroupId, &epoch, &g.DeviceIsMember); err != nil {
			return nil, asConnect(err)
		}
		g.CurrentEpoch = uint64(epoch)
		resp.Groups = append(resp.Groups, &g)
	}
	return resp, rows.Err()
}

func (s *Service) GetGroupInfo(ctx context.Context, req *deliveryv1.GetGroupInfoRequest) (*deliveryv1.GetGroupInfoResponse, error) {
	uid, did, err := authmiddleware.RequireDevice(ctx)
	if err != nil {
		return nil, err
	}
	resp := &deliveryv1.GetGroupInfoResponse{}
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := requireUserInGroup(ctx, tx, req.GroupId, uid, did); err != nil {
			return err
		}
		var gi []byte
		var giEpoch *int64
		var cur int64
		if err := tx.QueryRow(ctx, `SELECT group_info, group_info_epoch, current_epoch FROM groups WHERE id=$1`,
			req.GroupId).Scan(&gi, &giEpoch, &cur); err != nil {
			return err
		}
		if len(gi) == 0 || giEpoch == nil || *giEpoch != cur {
			return errf(connect.CodeNotFound, "no GroupInfo for the current epoch")
		}
		resp.GroupInfo, resp.Epoch = gi, uint64(cur)
		return nil
	})
	if err != nil {
		return nil, asConnect(err)
	}
	return resp, nil
}

func (s *Service) ExternalJoin(ctx context.Context, req *deliveryv1.ExternalJoinRequest) (*deliveryv1.ExternalJoinResponse, error) {
	uid, did, err := authmiddleware.RequireDevice(ctx)
	if err != nil {
		return nil, err
	}
	if err := validGroupID(req.GroupId); err != nil {
		return nil, err
	}
	if len(req.Commit) == 0 || len(req.Commit) > MaxPayload || len(req.GroupInfo) == 0 || len(req.GroupInfo) > MaxPayload {
		return nil, errf(connect.CodeInvalidArgument, "bad payload size")
	}
	newEpoch := req.Epoch + 1
	var recipients []string
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		// Policy: only a new device of a user that is already in the group.
		if err := requireUserInGroup(ctx, tx, req.GroupId, uid, did); err != nil {
			return err
		}
		var member bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM group_members WHERE group_id=$1 AND device_id=$2)`,
			req.GroupId, did).Scan(&member); err != nil {
			return err
		}
		if member {
			return errf(connect.CodeAlreadyExists, "device is already a member")
		}
		if err := casEpoch(ctx, tx, req.GroupId, req.Epoch); err != nil {
			return err
		}
		msgID, err := insertMessage(ctx, tx, req.GroupId, req.Epoch, deliveryv1.MessageType_MESSAGE_TYPE_COMMIT, req.Commit, uid, did)
		if err != nil {
			return err
		}
		if recipients, err = fanOut(ctx, tx, req.GroupId, msgID, did, req.Epoch); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO group_members (group_id, device_id, joined_epoch, user_id) VALUES ($1, $2, $3, $4)`,
			req.GroupId, did, int64(newEpoch), uid); err != nil {
			return err
		}
		return storeGroupInfo(ctx, tx, req.GroupId, newEpoch, req.GroupInfo)
	})
	if err != nil {
		return nil, asConnect(err)
	}
	s.log.Info().Str("group", req.GroupId).Uint64("epoch", newEpoch).Msg("external join accepted")
	s.hub.Notify(recipients...)
	return &deliveryv1.ExternalJoinResponse{NewEpoch: newEpoch}, nil
}

func (s *Service) RequestJoin(ctx context.Context, req *deliveryv1.RequestJoinRequest) (*deliveryv1.RequestJoinResponse, error) {
	uid, did, err := authmiddleware.RequireDevice(ctx)
	if err != nil {
		return nil, err
	}
	if err := validGroupID(req.GroupId); err != nil {
		return nil, err
	}
	if len(req.KeyPackage) == 0 || len(req.KeyPackage) > MaxPayload {
		return nil, errf(connect.CodeInvalidArgument, "bad key package size")
	}
	var recipients []string
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := requireUserInGroup(ctx, tx, req.GroupId, uid, did); err != nil {
			return err
		}
		var cur int64
		if err := tx.QueryRow(ctx, `SELECT current_epoch FROM groups WHERE id=$1 FOR SHARE`, req.GroupId).Scan(&cur); err != nil {
			return err
		}
		msgID, err := insertMessage(ctx, tx, req.GroupId, uint64(cur), deliveryv1.MessageType_MESSAGE_TYPE_JOIN_REQUEST, req.KeyPackage, uid, did)
		if err != nil {
			return err
		}
		recipients, err = fanOut(ctx, tx, req.GroupId, msgID, did, uint64(cur))
		return err
	})
	if err != nil {
		return nil, asConnect(err)
	}
	s.hub.Notify(recipients...)
	return &deliveryv1.RequestJoinResponse{}, nil
}
