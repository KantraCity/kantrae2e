// Package auth implements auth-service: accounts, login, devices.
package auth

import (
	"context"
	"embed"
	"errors"
	"io/fs"
	"net/http"
	"regexp"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	authv1 "github.com/kantracity/kantrae2e/gen/kantra/auth/v1"
	"github.com/kantracity/kantrae2e/gen/kantra/auth/v1/authv1connect"
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

const Schema = "auth"

var usernameRe = regexp.MustCompile(`^[a-zA-Z0-9_.-]{3,32}$`)

type Service struct {
	pool     *pgxpool.Pool
	secret   []byte
	tokenTTL time.Duration
}

func New(pool *pgxpool.Pool, secret []byte, tokenTTL time.Duration) *Service {
	return &Service{pool: pool, secret: secret, tokenTTL: tokenTTL}
}

// Handler mounts the connect service. Register and Login are public.
func (s *Service) Handler() (string, http.Handler) {
	return authv1connect.NewAuthServiceHandler(s, connect.WithInterceptors(
		authmiddleware.Interceptor(s.secret,
			authv1connect.AuthServiceRegisterProcedure,
			authv1connect.AuthServiceLoginProcedure,
		),
	))
}

func invalid(msg string) error {
	return connect.NewError(connect.CodeInvalidArgument, errors.New(msg))
}

func internal(err error) error {
	return connect.NewError(connect.CodeInternal, err)
}

func (s *Service) Register(ctx context.Context, req *authv1.RegisterRequest) (*authv1.RegisterResponse, error) {
	if !usernameRe.MatchString(req.Username) {
		return nil, invalid("username must be 3-32 chars of [a-zA-Z0-9_.-]")
	}
	if len(req.Password) < 8 || len(req.Password) > 72 {
		return nil, invalid("password must be 8-72 bytes")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, internal(err)
	}
	var id string
	err = s.pool.QueryRow(ctx,
		`INSERT INTO users (username, password_hash) VALUES ($1, $2) RETURNING id`,
		req.Username, string(hash)).Scan(&id)
	if db.IsUniqueViolation(err) {
		return nil, connect.NewError(connect.CodeAlreadyExists, errors.New("username taken"))
	}
	if err != nil {
		return nil, internal(err)
	}
	tok, err := authmiddleware.Issue(s.secret, id, "", s.tokenTTL)
	if err != nil {
		return nil, internal(err)
	}
	return &authv1.RegisterResponse{UserId: id, Token: tok}, nil
}

// dummyHash equalizes timing between unknown users and wrong passwords.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("dummy-password"), bcrypt.DefaultCost)

func (s *Service) Login(ctx context.Context, req *authv1.LoginRequest) (*authv1.LoginResponse, error) {
	var id, hash string
	err := s.pool.QueryRow(ctx, `SELECT id, password_hash FROM users WHERE username=$1`, req.Username).Scan(&id, &hash)
	unauth := connect.NewError(connect.CodeUnauthenticated, errors.New("invalid credentials"))
	if errors.Is(err, pgx.ErrNoRows) {
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(req.Password))
		return nil, unauth
	}
	if err != nil {
		return nil, internal(err)
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)) != nil {
		return nil, unauth
	}
	if req.DeviceId != "" {
		var active bool
		err := s.pool.QueryRow(ctx,
			`SELECT revoked_at IS NULL FROM devices WHERE id=$1 AND user_id=$2`, req.DeviceId, id).Scan(&active)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && !active) {
			return nil, connect.NewError(connect.CodePermissionDenied, errors.New("device unknown or revoked"))
		}
		if err != nil {
			return nil, invalid("bad device id")
		}
	}
	tok, err := authmiddleware.Issue(s.secret, id, req.DeviceId, s.tokenTTL)
	if err != nil {
		return nil, internal(err)
	}
	return &authv1.LoginResponse{UserId: id, Token: tok}, nil
}

func (s *Service) RegisterDevice(ctx context.Context, req *authv1.RegisterDeviceRequest) (*authv1.RegisterDeviceResponse, error) {
	uid, err := authmiddleware.RequireUser(ctx)
	if err != nil {
		return nil, err
	}
	if req.DeviceName == "" || len(req.DeviceName) > 64 {
		return nil, invalid("device_name must be 1-64 chars")
	}
	var id string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO devices (user_id, device_name) VALUES ($1, $2) RETURNING id`, uid, req.DeviceName).Scan(&id); err != nil {
		return nil, internal(err)
	}
	tok, err := authmiddleware.Issue(s.secret, uid, id, s.tokenTTL)
	if err != nil {
		return nil, internal(err)
	}
	return &authv1.RegisterDeviceResponse{DeviceId: id, Token: tok}, nil
}

func (s *Service) ListDevices(ctx context.Context, _ *authv1.ListDevicesRequest) (*authv1.ListDevicesResponse, error) {
	uid, err := authmiddleware.RequireUser(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx,
		`SELECT id, device_name, created_at, revoked_at FROM devices WHERE user_id=$1 ORDER BY created_at`, uid)
	if err != nil {
		return nil, internal(err)
	}
	defer rows.Close()
	resp := &authv1.ListDevicesResponse{}
	for rows.Next() {
		var d authv1.Device
		var created time.Time
		var revoked *time.Time
		if err := rows.Scan(&d.Id, &d.DeviceName, &created, &revoked); err != nil {
			return nil, internal(err)
		}
		d.CreatedAt = created.Unix()
		if revoked != nil {
			d.RevokedAt = revoked.Unix()
		}
		resp.Devices = append(resp.Devices, &d)
	}
	return resp, rows.Err()
}

func (s *Service) RevokeDevice(ctx context.Context, req *authv1.RevokeDeviceRequest) (*authv1.RevokeDeviceResponse, error) {
	uid, err := authmiddleware.RequireUser(ctx)
	if err != nil {
		return nil, err
	}
	tag, err := s.pool.Exec(ctx,
		`UPDATE devices SET revoked_at = now() WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL`, req.DeviceId, uid)
	if err != nil {
		return nil, invalid("bad device id")
	}
	if tag.RowsAffected() == 0 {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("no such active device"))
	}
	return &authv1.RevokeDeviceResponse{}, nil
}

func (s *Service) LookupUser(ctx context.Context, req *authv1.LookupUserRequest) (*authv1.LookupUserResponse, error) {
	if _, err := authmiddleware.RequireUser(ctx); err != nil {
		return nil, err
	}
	resp := &authv1.LookupUserResponse{}
	var err error
	switch {
	case req.Username != "" && req.UserId == "":
		err = s.pool.QueryRow(ctx, `SELECT id, username FROM users WHERE username=$1`, req.Username).Scan(&resp.UserId, &resp.Username)
	case req.UserId != "" && req.Username == "":
		if _, perr := uuid.Parse(req.UserId); perr != nil {
			return nil, invalid("bad user_id")
		}
		err = s.pool.QueryRow(ctx, `SELECT id, username FROM users WHERE id=$1`, req.UserId).Scan(&resp.UserId, &resp.Username)
	default:
		return nil, invalid("exactly one of username / user_id is required")
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("no such user"))
	}
	if err != nil {
		return nil, internal(err)
	}
	return resp, nil
}

func (s *Service) RefreshToken(ctx context.Context, _ *authv1.RefreshTokenRequest) (*authv1.RefreshTokenResponse, error) {
	uid, err := authmiddleware.RequireUser(ctx)
	if err != nil {
		return nil, err
	}
	did, _ := authmiddleware.DeviceIDFromContext(ctx)
	if did != "" {
		var active bool
		err := s.pool.QueryRow(ctx,
			`SELECT revoked_at IS NULL FROM devices WHERE id=$1 AND user_id=$2`, did, uid).Scan(&active)
		if err != nil || !active {
			return nil, connect.NewError(connect.CodePermissionDenied, errors.New("device unknown or revoked"))
		}
	}
	tok, err := authmiddleware.Issue(s.secret, uid, did, s.tokenTTL)
	if err != nil {
		return nil, internal(err)
	}
	return &authv1.RefreshTokenResponse{Token: tok}, nil
}
