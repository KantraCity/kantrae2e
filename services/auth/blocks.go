package auth

import (
	"context"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	authv1 "github.com/kantracity/kantrae2e/gen/kantra/auth/v1"
	"github.com/kantracity/kantrae2e/pkg/authmiddleware"
)

func (s *Service) BlockUser(ctx context.Context, req *authv1.BlockUserRequest) (*authv1.BlockUserResponse, error) {
	uid, err := authmiddleware.RequireUser(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := uuid.Parse(req.UserId); err != nil || req.UserId == uid {
		return nil, invalid("bad user id")
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO blocks (user_id, blocked_id) SELECT $1, id FROM users WHERE id=$2
		ON CONFLICT DO NOTHING`, uid, req.UserId); err != nil {
		return nil, internal(err)
	}
	return &authv1.BlockUserResponse{}, nil
}

func (s *Service) UnblockUser(ctx context.Context, req *authv1.UnblockUserRequest) (*authv1.UnblockUserResponse, error) {
	uid, err := authmiddleware.RequireUser(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := uuid.Parse(req.UserId); err != nil {
		return nil, invalid("bad user id")
	}
	if _, err := s.pool.Exec(ctx, `DELETE FROM blocks WHERE user_id=$1 AND blocked_id=$2`, uid, req.UserId); err != nil {
		return nil, internal(err)
	}
	return &authv1.UnblockUserResponse{}, nil
}

func (s *Service) ListBlocked(ctx context.Context, _ *authv1.ListBlockedRequest) (*authv1.ListBlockedResponse, error) {
	uid, err := authmiddleware.RequireUser(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT blocked_id::text FROM blocks WHERE user_id=$1 ORDER BY created_at DESC`, uid)
	if err != nil {
		return nil, internal(err)
	}
	defer rows.Close()
	out := &authv1.ListBlockedResponse{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, internal(err)
		}
		out.UserIds = append(out.UserIds, id)
	}
	return out, rows.Err()
}

// Blocked (internal API): whether either user has blocked the other.
func (s *Internal) Blocked(ctx context.Context, req *authv1.BlockedRequest) (*authv1.BlockedResponse, error) {
	if _, err := uuid.Parse(req.UserA); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if _, err := uuid.Parse(req.UserB); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	var blocked bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM blocks WHERE (user_id=$1 AND blocked_id=$2) OR (user_id=$2 AND blocked_id=$1))`,
		req.UserA, req.UserB).Scan(&blocked)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return &authv1.BlockedResponse{Blocked: blocked}, nil
}
