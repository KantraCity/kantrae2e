// Package auth implements auth-service: accounts, login, devices.
package auth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"errors"
	"io/fs"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

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
	name, err := cleanName(req.DisplayName)
	if err != nil {
		return nil, err
	}
	if name == "" {
		name = req.Username
	}
	if taken, err := s.usernameTaken(ctx, req.Username, ""); err != nil {
		return nil, internal(err)
	} else if taken {
		return nil, connect.NewError(connect.CodeAlreadyExists, errors.New("username taken"))
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, internal(err)
	}
	var id string
	err = s.pool.QueryRow(ctx,
		`INSERT INTO users (username, password_hash, display_name) VALUES ($1, $2, $3) RETURNING id`,
		req.Username, string(hash), name).Scan(&id)
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
	// The account's first device is approved; later devices are pending
	// until an approved device (or the recovery key) approves them.
	var id string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO devices (user_id, device_name, approved_at)
		 SELECT $1, $2, CASE WHEN EXISTS (SELECT 1 FROM devices WHERE user_id = $1) THEN NULL ELSE now() END
		 RETURNING id`, uid, req.DeviceName).Scan(&id); err != nil {
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
		`SELECT id, device_name, created_at, revoked_at, approved_at IS NOT NULL FROM devices WHERE user_id=$1 ORDER BY created_at`, uid)
	if err != nil {
		return nil, internal(err)
	}
	defer rows.Close()
	resp := &authv1.ListDevicesResponse{}
	for rows.Next() {
		var d authv1.Device
		var created time.Time
		var revoked *time.Time
		if err := rows.Scan(&d.Id, &d.DeviceName, &created, &revoked, &d.Approved); err != nil {
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
	var u *authv1.User
	var err error
	switch {
	case req.Username != "" && req.UserId == "":
		u, err = scanUser(s.pool.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE lower(username)=lower($1)`,
			strings.TrimPrefix(req.Username, "@")))
	case req.UserId != "" && req.Username == "":
		if _, perr := uuid.Parse(req.UserId); perr != nil {
			return nil, invalid("bad user_id")
		}
		u, err = scanUser(s.pool.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE id=$1`, req.UserId))
	default:
		return nil, invalid("exactly one of username / user_id is required")
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("no such user"))
	}
	if err != nil {
		return nil, internal(err)
	}
	return &authv1.LookupUserResponse{UserId: u.UserId, Username: u.Username, DisplayName: u.DisplayName,
		Bio: u.Bio, AvatarId: u.AvatarId}, nil
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

// ---- device linking and recovery ------------------------------------------------

// requireApprovedDevice returns the caller's user and device id if the token
// is bound to an approved, non-revoked device.
func (s *Service) requireApprovedDevice(ctx context.Context) (string, string, error) {
	uid, did, err := authmiddleware.RequireDevice(ctx)
	if err != nil {
		return "", "", err
	}
	var approved bool
	err = s.pool.QueryRow(ctx,
		`SELECT approved_at IS NOT NULL FROM devices WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL`, did, uid).Scan(&approved)
	if err != nil || !approved {
		return "", "", connect.NewError(connect.CodePermissionDenied, errors.New("device is not approved"))
	}
	return uid, did, nil
}

func (s *Service) GetAccountStatus(ctx context.Context, _ *authv1.GetAccountStatusRequest) (*authv1.GetAccountStatusResponse, error) {
	uid, did, err := authmiddleware.RequireDevice(ctx)
	if err != nil {
		return nil, err
	}
	resp := &authv1.GetAccountStatusResponse{}
	err = s.pool.QueryRow(ctx,
		`SELECT COALESCE((SELECT approved_at IS NOT NULL FROM devices WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL), false),
		        EXISTS (SELECT 1 FROM recovery WHERE user_id=$2)`, did, uid).Scan(&resp.DeviceApproved, &resp.HasRecovery)
	if err != nil {
		return nil, internal(err)
	}
	return resp, nil
}

func (s *Service) ApproveDevice(ctx context.Context, req *authv1.ApproveDeviceRequest) (*authv1.ApproveDeviceResponse, error) {
	uid, _, err := s.requireApprovedDevice(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := uuid.Parse(req.DeviceId); err != nil {
		return nil, invalid("bad device id")
	}
	tag, err := s.pool.Exec(ctx,
		`UPDATE devices SET approved_at = now()
		 WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL AND approved_at IS NULL`, req.DeviceId, uid)
	if err != nil {
		return nil, internal(err)
	}
	if tag.RowsAffected() == 0 {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("no pending device with this id"))
	}
	return &authv1.ApproveDeviceResponse{}, nil
}

const (
	verifierSize   = 32
	maxWrappedSize = 1024
)

func verifierHash(v []byte) []byte {
	h := sha256.Sum256(append([]byte("kantra/recovery-verifier/v1"), v...))
	return h[:]
}

func (s *Service) SetRecovery(ctx context.Context, req *authv1.SetRecoveryRequest) (*authv1.SetRecoveryResponse, error) {
	uid, _, err := s.requireApprovedDevice(ctx)
	if err != nil {
		return nil, err
	}
	if len(req.Verifier) != verifierSize || len(req.WrappedKey) == 0 || len(req.WrappedKey) > maxWrappedSize {
		return nil, invalid("bad recovery data")
	}
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO recovery (user_id, verifier_hash, wrapped_key) VALUES ($1, $2, $3)
		 ON CONFLICT (user_id) DO UPDATE SET verifier_hash=excluded.verifier_hash, wrapped_key=excluded.wrapped_key, updated_at=now()`,
		uid, verifierHash(req.Verifier), req.WrappedKey); err != nil {
		return nil, internal(err)
	}
	return &authv1.SetRecoveryResponse{}, nil
}

func (s *Service) Recover(ctx context.Context, req *authv1.RecoverRequest) (*authv1.RecoverResponse, error) {
	uid, did, err := authmiddleware.RequireDevice(ctx)
	if err != nil {
		return nil, err
	}
	wrong := connect.NewError(connect.CodePermissionDenied, errors.New("wrong recovery key"))
	var hash, wrapped []byte
	err = s.pool.QueryRow(ctx, `SELECT verifier_hash, wrapped_key FROM recovery WHERE user_id=$1`, uid).Scan(&hash, &wrapped)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("no recovery key set up for this account"))
	}
	if err != nil {
		return nil, internal(err)
	}
	if subtle.ConstantTimeCompare(hash, verifierHash(req.Verifier)) != 1 {
		return nil, wrong
	}
	if _, err := s.pool.Exec(ctx,
		`UPDATE devices SET approved_at = COALESCE(approved_at, now()) WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL`, did, uid); err != nil {
		return nil, internal(err)
	}
	return &authv1.RecoverResponse{WrappedKey: wrapped}, nil
}

func (s *Service) SearchUsers(ctx context.Context, req *authv1.SearchUsersRequest) (*authv1.SearchUsersResponse, error) {
	if _, err := authmiddleware.RequireUser(ctx); err != nil {
		return nil, err
	}
	q := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(req.Query), "@"))
	resp := &authv1.SearchUsersResponse{}
	if len(q) < 2 || len(q) > 32 {
		return resp, nil
	}
	esc := strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(q)
	// Like Telegram's global search: @username prefix, or a word of the name.
	rows, err := s.pool.Query(ctx,
		`SELECT `+userCols+` FROM users
		 WHERE lower(username) LIKE $1 ESCAPE '\' OR lower(display_name) LIKE $1 ESCAPE '\'
		    OR lower(display_name) LIKE $3 ESCAPE '\'
		 ORDER BY lower(username) = $2 DESC, lower(username) LIKE $1 ESCAPE '\' DESC, length(username), username LIMIT 20`,
		esc+"%", q, "% "+esc+"%")
	if err != nil {
		return nil, internal(err)
	}
	defer rows.Close()
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, internal(err)
		}
		resp.Users = append(resp.Users, u)
	}
	return resp, rows.Err()
}

// ---- profile ---------------------------------------------------------------------

const userCols = `id::text, username, display_name, bio, avatar_id`

func scanUser(row pgx.Row) (*authv1.User, error) {
	var u authv1.User
	err := row.Scan(&u.UserId, &u.Username, &u.DisplayName, &u.Bio, &u.AvatarId)
	return &u, err
}

const (
	maxNameRunes = 64
	maxBioRunes  = 140 // Telegram's limit
	maxAvatar    = 512 << 10
)

func cleanName(n string) (string, error) {
	n = strings.Join(strings.Fields(n), " ")
	if utf8.RuneCountInString(n) > maxNameRunes {
		return "", invalid("name is too long")
	}
	return n, nil
}

func (s *Service) usernameTaken(ctx context.Context, username, exceptID string) (bool, error) {
	var taken bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM users WHERE lower(username)=lower($1) AND id::text <> $2)`,
		username, exceptID).Scan(&taken)
	return taken, err
}

func (s *Service) user(ctx context.Context, id string) (*authv1.User, error) {
	u, err := scanUser(s.pool.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE id=$1`, id))
	if err != nil {
		return nil, internal(err)
	}
	return u, nil
}

func (s *Service) GetProfiles(ctx context.Context, req *authv1.GetProfilesRequest) (*authv1.GetProfilesResponse, error) {
	if _, err := authmiddleware.RequireUser(ctx); err != nil {
		return nil, err
	}
	if len(req.UserIds) > 200 {
		return nil, invalid("too many user ids")
	}
	var ids []uuid.UUID
	for _, id := range req.UserIds {
		if u, err := uuid.Parse(id); err == nil {
			ids = append(ids, u)
		}
	}
	rows, err := s.pool.Query(ctx, `SELECT `+userCols+` FROM users WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, internal(err)
	}
	defer rows.Close()
	resp := &authv1.GetProfilesResponse{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, internal(err)
		}
		resp.Users = append(resp.Users, u)
	}
	return resp, rows.Err()
}

func (s *Service) UpdateProfile(ctx context.Context, req *authv1.UpdateProfileRequest) (*authv1.UpdateProfileResponse, error) {
	uid, _, err := s.requireApprovedDevice(ctx)
	if err != nil {
		return nil, err
	}
	name, err := cleanName(req.DisplayName)
	if err != nil {
		return nil, err
	}
	if name == "" {
		return nil, invalid("name is required")
	}
	bio := strings.TrimSpace(req.Bio)
	if utf8.RuneCountInString(bio) > maxBioRunes {
		return nil, invalid("bio is too long")
	}
	if _, err := s.pool.Exec(ctx, `UPDATE users SET display_name=$2, bio=$3 WHERE id=$1`, uid, name, bio); err != nil {
		return nil, internal(err)
	}
	u, err := s.user(ctx, uid)
	return &authv1.UpdateProfileResponse{User: u}, err
}

func (s *Service) SetUsername(ctx context.Context, req *authv1.SetUsernameRequest) (*authv1.SetUsernameResponse, error) {
	uid, _, err := s.requireApprovedDevice(ctx)
	if err != nil {
		return nil, err
	}
	name := strings.TrimPrefix(strings.TrimSpace(req.Username), "@")
	if !usernameRe.MatchString(name) {
		return nil, invalid("username must be 3-32 chars of [a-zA-Z0-9_.-]")
	}
	if taken, err := s.usernameTaken(ctx, name, uid); err != nil {
		return nil, internal(err)
	} else if taken {
		return nil, connect.NewError(connect.CodeAlreadyExists, errors.New("username taken"))
	}
	_, err = s.pool.Exec(ctx, `UPDATE users SET username=$2 WHERE id=$1`, uid, name)
	if db.IsUniqueViolation(err) {
		return nil, connect.NewError(connect.CodeAlreadyExists, errors.New("username taken"))
	}
	if err != nil {
		return nil, internal(err)
	}
	u, err := s.user(ctx, uid)
	return &authv1.SetUsernameResponse{User: u}, err
}

func (s *Service) SetAvatar(ctx context.Context, req *authv1.SetAvatarRequest) (*authv1.SetAvatarResponse, error) {
	uid, _, err := s.requireApprovedDevice(ctx)
	if err != nil {
		return nil, err
	}
	var img []byte
	id := ""
	if len(req.Image) > 0 {
		if len(req.Image) > maxAvatar {
			return nil, invalid("photo is larger than 512 KiB")
		}
		switch http.DetectContentType(req.Image) {
		case "image/jpeg", "image/png", "image/webp":
		default:
			return nil, invalid("photo must be JPEG, PNG or WebP")
		}
		img = req.Image
		sum := sha256.Sum256(img)
		id = hex.EncodeToString(sum[:8])
	}
	if _, err := s.pool.Exec(ctx, `UPDATE users SET avatar=$2, avatar_id=$3 WHERE id=$1`, uid, img, id); err != nil {
		return nil, internal(err)
	}
	u, err := s.user(ctx, uid)
	return &authv1.SetAvatarResponse{User: u}, err
}

func (s *Service) GetAvatar(ctx context.Context, req *authv1.GetAvatarRequest) (*authv1.GetAvatarResponse, error) {
	if _, err := authmiddleware.RequireUser(ctx); err != nil {
		return nil, err
	}
	if _, err := uuid.Parse(req.UserId); err != nil {
		return nil, invalid("bad user_id")
	}
	resp := &authv1.GetAvatarResponse{}
	err := s.pool.QueryRow(ctx, `SELECT avatar_id, coalesce(avatar, ''::bytea) FROM users WHERE id=$1`, req.UserId).
		Scan(&resp.AvatarId, &resp.Image)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && resp.AvatarId == "") {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("no photo"))
	}
	if err != nil {
		return nil, internal(err)
	}
	return resp, nil
}

// ---- internal API -------------------------------------------------------------

// Internal implements AuthInternalService for other services.
type Internal struct {
	pool  *pgxpool.Pool
	token string
}

func NewInternal(pool *pgxpool.Pool, token string) *Internal {
	return &Internal{pool: pool, token: token}
}

// Handler mounts the internal service, guarded by the shared token.
func (s *Internal) Handler() (string, http.Handler) {
	return authv1connect.NewAuthInternalServiceHandler(s, connect.WithInterceptors(internalAuth(s.token)))
}

func internalAuth(token string) connect.UnaryInterceptorFunc {
	want := []byte("Internal " + token)
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			got := []byte(req.Header().Get("Authorization"))
			if subtle.ConstantTimeCompare(got, want) != 1 {
				return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("internal token required"))
			}
			return next(ctx, req)
		}
	}
}

const maxStatusBatch = 1000

func (s *Internal) DeviceStatus(ctx context.Context, req *authv1.DeviceStatusRequest) (*authv1.DeviceStatusResponse, error) {
	if len(req.DeviceIds) > maxStatusBatch {
		return nil, invalid("too many device ids")
	}
	var ids []string
	for _, id := range req.DeviceIds {
		if _, err := uuid.Parse(id); err == nil {
			ids = append(ids, id)
		}
	}
	resp := &authv1.DeviceStatusResponse{}
	if len(ids) == 0 {
		return resp, nil
	}
	rows, err := s.pool.Query(ctx,
		`SELECT id::text, approved_at IS NOT NULL FROM devices WHERE id = ANY($1::uuid[]) AND revoked_at IS NULL`, ids)
	if err != nil {
		return nil, internal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var approved bool
		if err := rows.Scan(&id, &approved); err != nil {
			return nil, internal(err)
		}
		resp.ActiveDeviceIds = append(resp.ActiveDeviceIds, id)
		if approved {
			resp.ApprovedDeviceIds = append(resp.ApprovedDeviceIds, id)
		}
	}
	return resp, rows.Err()
}
