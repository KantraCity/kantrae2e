// Package history implements history-service: encrypted, content-addressed
// history backup chunks in MinIO (bucket `history`) with a manifest in Postgres.
package history

import (
	"context"
	"embed"
	"errors"
	"io/fs"
	"net/http"
	"time"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5/pgxpool"

	historyv1 "github.com/kantracity/kantrae2e/gen/kantra/history/v1"
	"github.com/kantracity/kantrae2e/gen/kantra/history/v1/historyv1connect"
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
	Schema       = "history"
	Bucket       = "history"
	MaxChunkSize = 8 << 20
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
	return historyv1connect.NewHistoryServiceHandler(s,
		connect.WithInterceptors(authmiddleware.Interceptor(s.secret)),
		connect.WithReadMaxBytes(MaxChunkSize+1024))
}

func (s *Service) PutChunk(ctx context.Context, req *historyv1.PutChunkRequest) (*historyv1.PutChunkResponse, error) {
	uid, err := authmiddleware.RequireUser(ctx)
	if err != nil {
		return nil, err
	}
	if len(req.Data) == 0 || len(req.Data) > MaxChunkSize {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("bad chunk size"))
	}
	if !blobstore.ValidHash(req.Hash) || blobstore.Hash(req.Data) != req.Hash {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("hash does not match BLAKE3(data)"))
	}
	// Object first, manifest second: a manifest entry never points to a
	// missing object. Re-uploading the same chunk is idempotent.
	if err := s.blobs.Put(ctx, req.Hash, req.Data); err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, err)
	}
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO history_manifest (user_id, chunk_hash, size_bytes) VALUES ($1, $2, $3)
		 ON CONFLICT (user_id, chunk_hash) DO NOTHING`, uid, req.Hash, len(req.Data)); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return &historyv1.PutChunkResponse{}, nil
}

func (s *Service) GetChunk(ctx context.Context, req *historyv1.GetChunkRequest) (*historyv1.GetChunkResponse, error) {
	uid, err := authmiddleware.RequireUser(ctx)
	if err != nil {
		return nil, err
	}
	var owned bool
	if err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM history_manifest WHERE user_id=$1 AND chunk_hash=$2)`, uid, req.Hash).Scan(&owned); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if !owned {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("no such chunk"))
	}
	data, err := s.blobs.Get(ctx, req.Hash)
	if errors.Is(err, blobstore.ErrNotFound) {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, err)
	}
	return &historyv1.GetChunkResponse{Data: data}, nil
}

func (s *Service) GetManifest(ctx context.Context, _ *historyv1.GetManifestRequest) (*historyv1.GetManifestResponse, error) {
	uid, err := authmiddleware.RequireUser(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx,
		`SELECT chunk_hash, size_bytes, created_at FROM history_manifest WHERE user_id=$1 ORDER BY created_at, chunk_hash`, uid)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	defer rows.Close()
	resp := &historyv1.GetManifestResponse{}
	for rows.Next() {
		var e historyv1.ManifestEntry
		var created time.Time
		if err := rows.Scan(&e.ChunkHash, &e.SizeBytes, &created); err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		e.CreatedAt = created.Unix()
		resp.Entries = append(resp.Entries, &e)
	}
	return resp, rows.Err()
}
