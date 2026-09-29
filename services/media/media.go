// Package media implements media-service: encrypted, content-addressed
// media blobs in MinIO (bucket `media`). The decryption key only travels
// inside MLS application messages; the content hash acts as the locator.
package media

import (
	"context"
	"embed"
	"errors"
	"io/fs"
	"net/http"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5/pgxpool"

	mediav1 "github.com/kantracity/kantrae2e/gen/kantra/media/v1"
	"github.com/kantracity/kantrae2e/gen/kantra/media/v1/mediav1connect"
	"github.com/kantracity/kantrae2e/pkg/authmiddleware"
	"github.com/kantracity/kantrae2e/pkg/blobstore"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Migrations returns the schema migrations of this service.
func Migrations() fs.FS {
	sub, _ := fs.Sub(migrations, "migrations")
	return sub
}

const (
	Schema       = "media"
	Bucket       = "media"
	MaxMediaSize = 25 << 20
)

type Service struct {
	pool   *pgxpool.Pool
	blobs  blobstore.Store
	secret []byte
}

func New(pool *pgxpool.Pool, blobs blobstore.Store, secret []byte) *Service {
	return &Service{pool: pool, blobs: blobs, secret: secret}
}

func (s *Service) Handler() (string, http.Handler) {
	return mediav1connect.NewMediaServiceHandler(s,
		connect.WithInterceptors(authmiddleware.Interceptor(s.secret)),
		connect.WithReadMaxBytes(MaxMediaSize+1024))
}

func (s *Service) PutMedia(ctx context.Context, req *mediav1.PutMediaRequest) (*mediav1.PutMediaResponse, error) {
	uid, err := authmiddleware.RequireUser(ctx)
	if err != nil {
		return nil, err
	}
	if len(req.Data) == 0 || len(req.Data) > MaxMediaSize {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("bad media size"))
	}
	if !blobstore.ValidHash(req.Hash) || blobstore.Hash(req.Data) != req.Hash {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("hash does not match BLAKE3(data)"))
	}
	if err := s.blobs.Put(ctx, req.Hash, req.Data); err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, err)
	}
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO media_objects (hash, owner_user_id, size_bytes) VALUES ($1, $2, $3)
		 ON CONFLICT (hash) DO NOTHING`, req.Hash, uid, len(req.Data)); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return &mediav1.PutMediaResponse{}, nil
}

func (s *Service) GetMedia(ctx context.Context, req *mediav1.GetMediaRequest) (*mediav1.GetMediaResponse, error) {
	if _, err := authmiddleware.RequireUser(ctx); err != nil {
		return nil, err
	}
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM media_objects WHERE hash=$1)`, req.Hash).Scan(&exists); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if !exists {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("no such media"))
	}
	data, err := s.blobs.Get(ctx, req.Hash)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, err)
	}
	return &mediav1.GetMediaResponse{Data: data}, nil
}
