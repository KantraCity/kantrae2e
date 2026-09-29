// Package directory implements directory-service: KeyPackage distribution.
package directory

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/http"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	directoryv1 "github.com/kantracity/kantrae2e/gen/kantra/directory/v1"
	"github.com/kantracity/kantrae2e/gen/kantra/directory/v1/directoryv1connect"
	"github.com/kantracity/kantrae2e/pkg/authmiddleware"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Migrations returns the schema migrations of this service.
func Migrations() fs.FS {
	sub, _ := fs.Sub(migrations, "migrations")
	return sub
}

const Schema = "directory"

const (
	MaxBatch          = 100
	MaxStockPerDevice = 200
	MaxKeyPackageSize = 16 << 10
)

type Service struct {
	pool   *pgxpool.Pool
	secret []byte
}

func New(pool *pgxpool.Pool, secret []byte) *Service {
	return &Service{pool: pool, secret: secret}
}

func (s *Service) Handler() (string, http.Handler) {
	return directoryv1connect.NewDirectoryServiceHandler(s,
		connect.WithInterceptors(authmiddleware.Interceptor(s.secret)))
}

func (s *Service) publish(ctx context.Context, kps [][]byte) (int64, error) {
	uid, did, err := authmiddleware.RequireDevice(ctx)
	if err != nil {
		return 0, err
	}
	if len(kps) == 0 || len(kps) > MaxBatch {
		return 0, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("batch must contain 1-%d key packages", MaxBatch))
	}
	for _, kp := range kps {
		if len(kp) == 0 || len(kp) > MaxKeyPackageSize {
			return 0, connect.NewError(connect.CodeInvalidArgument, errors.New("bad key package size"))
		}
	}
	var available int64
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		// Serialize publishers of the same device so the stock limit holds.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, did); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM key_packages WHERE device_id=$1 AND used_at IS NULL`, did).Scan(&available); err != nil {
			return err
		}
		if available+int64(len(kps)) > MaxStockPerDevice {
			return connect.NewError(connect.CodeResourceExhausted, fmt.Errorf("stock limit %d reached", MaxStockPerDevice))
		}
		rows := make([][]any, len(kps))
		for i, kp := range kps {
			rows[i] = []any{uid, did, kp}
		}
		_, err := tx.CopyFrom(ctx, pgx.Identifier{"key_packages"},
			[]string{"user_id", "device_id", "payload"}, pgx.CopyFromRows(rows))
		return err
	})
	if err != nil {
		var ce *connect.Error
		if errors.As(err, &ce) {
			return 0, ce
		}
		return 0, connect.NewError(connect.CodeInternal, err)
	}
	return available + int64(len(kps)), nil
}

func (s *Service) PublishKeyPackages(ctx context.Context, req *directoryv1.PublishKeyPackagesRequest) (*directoryv1.PublishKeyPackagesResponse, error) {
	n, err := s.publish(ctx, req.KeyPackages)
	if err != nil {
		return nil, err
	}
	return &directoryv1.PublishKeyPackagesResponse{Available: n}, nil
}

func (s *Service) RefillKeyPackages(ctx context.Context, req *directoryv1.RefillKeyPackagesRequest) (*directoryv1.RefillKeyPackagesResponse, error) {
	n, err := s.publish(ctx, req.KeyPackages)
	if err != nil {
		return nil, err
	}
	return &directoryv1.RefillKeyPackagesResponse{Available: n}, nil
}

func (s *Service) CountKeyPackages(ctx context.Context, _ *directoryv1.CountKeyPackagesRequest) (*directoryv1.CountKeyPackagesResponse, error) {
	_, did, err := authmiddleware.RequireDevice(ctx)
	if err != nil {
		return nil, err
	}
	var n int64
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM key_packages WHERE device_id=$1 AND used_at IS NULL`, did).Scan(&n); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return &directoryv1.CountKeyPackagesResponse{Available: n}, nil
}

// claimSQL hands out one KeyPackage exactly once: the row is locked and
// marked used in a single statement; concurrent callers skip locked rows
// instead of receiving the same package.
const claimSQL = `
UPDATE key_packages SET used_at = now()
WHERE id = (
    SELECT id FROM key_packages
    WHERE user_id = $1 AND device_id = $2 AND used_at IS NULL
    ORDER BY id
    LIMIT 1
    FOR UPDATE SKIP LOCKED
)
RETURNING payload`

type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func claim(ctx context.Context, q querier, userID, deviceID string) ([]byte, error) {
	var payload []byte
	err := q.QueryRow(ctx, claimSQL, userID, deviceID).Scan(&payload)
	return payload, err
}

func (s *Service) FetchKeyPackage(ctx context.Context, req *directoryv1.FetchKeyPackageRequest) (*directoryv1.FetchKeyPackageResponse, error) {
	if _, err := authmiddleware.RequireUser(ctx); err != nil {
		return nil, err
	}
	kp, err := claim(ctx, s.pool, req.UserId, req.DeviceId)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("no key package available"))
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	return &directoryv1.FetchKeyPackageResponse{KeyPackage: kp}, nil
}

func (s *Service) FetchUserKeyPackages(ctx context.Context, req *directoryv1.FetchUserKeyPackagesRequest) (*directoryv1.FetchUserKeyPackagesResponse, error) {
	if _, err := authmiddleware.RequireUser(ctx); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx,
		`SELECT DISTINCT device_id::text FROM key_packages WHERE user_id=$1 AND used_at IS NULL`, req.UserId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	devices, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	resp := &directoryv1.FetchUserKeyPackagesResponse{}
	for _, d := range devices {
		kp, err := claim(ctx, s.pool, req.UserId, d)
		if errors.Is(err, pgx.ErrNoRows) {
			continue // drained concurrently
		}
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		resp.KeyPackages = append(resp.KeyPackages, &directoryv1.DeviceKeyPackage{DeviceId: d, KeyPackage: kp})
	}
	if len(resp.KeyPackages) == 0 {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("no key packages available"))
	}
	return resp, nil
}
