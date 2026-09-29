// Package store is the client's local SQLite database (pure Go, no cgo):
// account data, persisted MLS state, groups and decrypted messages.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	_ "modernc.org/sqlite"

	"github.com/kantracity/kantrae2e/client/mls"
)

var ErrNotFound = errors.New("store: not found")

const schema = `
CREATE TABLE IF NOT EXISTS kv (
    k TEXT PRIMARY KEY,
    v BLOB NOT NULL
);
-- MLS state (opaque blobs from mls-rs, written through the change log).
CREATE TABLE IF NOT EXISTS mls_groups (
    group_id BLOB PRIMARY KEY,
    state    BLOB NOT NULL
);
CREATE TABLE IF NOT EXISTS mls_epochs (
    group_id BLOB NOT NULL,
    epoch_id INTEGER NOT NULL,
    data     BLOB NOT NULL,
    PRIMARY KEY (group_id, epoch_id)
);
CREATE TABLE IF NOT EXISTS mls_key_packages (
    id   BLOB PRIMARY KEY,
    data BLOB NOT NULL
);
-- Application data.
CREATE TABLE IF NOT EXISTS groups (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL DEFAULT '',
    epoch      INTEGER NOT NULL DEFAULT 0,
    active     INTEGER NOT NULL DEFAULT 1,
    created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS messages (
    seq            INTEGER PRIMARY KEY AUTOINCREMENT,
    uid            TEXT NOT NULL,
    group_id       TEXT NOT NULL,
    sender_user    TEXT NOT NULL,
    sender_device  TEXT NOT NULL,
    kind           TEXT NOT NULL,
    body           TEXT NOT NULL,
    sent_at        INTEGER NOT NULL,
    outgoing       INTEGER NOT NULL,
    backed_up      INTEGER NOT NULL DEFAULT 0,
    UNIQUE (group_id, uid)
);
CREATE INDEX IF NOT EXISTS messages_group_idx ON messages(group_id, sent_at, seq);
CREATE TABLE IF NOT EXISTS users (
    user_id  TEXT PRIMARY KEY,
    username TEXT NOT NULL
);
-- Queue ids already processed (at-least-once delivery de-duplication).
CREATE TABLE IF NOT EXISTS processed_envelopes (
    id INTEGER PRIMARY KEY
);
-- History chunks already restored.
CREATE TABLE IF NOT EXISTS restored_chunks (
    hash TEXT PRIMARY KEY
);
`

type Store struct {
	db *sql.DB
}

// Open opens (or creates) the database at path.
func Open(path string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_txlock=immediate", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // one writer; keeps MLS state writes strictly ordered
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// Tx is a write transaction.
type Tx struct{ tx *sql.Tx }

// Update runs fn in a transaction.
func (s *Store) Update(ctx context.Context, fn func(*Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(&Tx{tx}); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// ---- key/value ----------------------------------------------------------

func (s *Store) Get(ctx context.Context, k string) ([]byte, error) {
	var v []byte
	err := s.db.QueryRowContext(ctx, `SELECT v FROM kv WHERE k=?`, k).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return v, err
}

func (t *Tx) Set(k string, v []byte) error {
	_, err := t.tx.Exec(`INSERT INTO kv (k, v) VALUES (?, ?) ON CONFLICT(k) DO UPDATE SET v=excluded.v`, k, v)
	return err
}

// ---- MLS state ------------------------------------------------------------

// ApplyMLSChanges persists the change log of the MLS client.
func (t *Tx) ApplyMLSChanges(changes []mls.Change) error {
	for _, ch := range changes {
		var err error
		switch ch.Kind {
		case mls.ChangeGroupWrite:
			if _, err = t.tx.Exec(`INSERT INTO mls_groups (group_id, state) VALUES (?, ?)
				ON CONFLICT(group_id) DO UPDATE SET state=excluded.state`, ch.GroupID, ch.State); err != nil {
				return err
			}
			for _, e := range ch.Epochs {
				if _, err = t.tx.Exec(`INSERT INTO mls_epochs (group_id, epoch_id, data) VALUES (?, ?, ?)
					ON CONFLICT(group_id, epoch_id) DO UPDATE SET data=excluded.data`, ch.GroupID, int64(e.ID), e.Data); err != nil {
					return err
				}
			}
			_, err = t.tx.Exec(`DELETE FROM mls_epochs WHERE group_id=? AND epoch_id < ?`, ch.GroupID, int64(ch.KeepFrom))
		case mls.ChangeKeyPackageInsert:
			_, err = t.tx.Exec(`INSERT OR REPLACE INTO mls_key_packages (id, data) VALUES (?, ?)`, ch.KeyPackageID, ch.KeyPackageData)
		case mls.ChangeKeyPackageDelete:
			_, err = t.tx.Exec(`DELETE FROM mls_key_packages WHERE id=?`, ch.KeyPackageID)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// DeleteMLSGroup removes the persisted state of a group.
func (t *Tx) DeleteMLSGroup(groupID []byte) error {
	if _, err := t.tx.Exec(`DELETE FROM mls_epochs WHERE group_id=?`, groupID); err != nil {
		return err
	}
	_, err := t.tx.Exec(`DELETE FROM mls_groups WHERE group_id=?`, groupID)
	return err
}

// LoadMLS feeds all persisted MLS state into c.
func (s *Store) LoadMLS(ctx context.Context, c *mls.Client) error {
	rows, err := s.db.QueryContext(ctx, `SELECT group_id, state FROM mls_groups`)
	if err != nil {
		return err
	}
	type grp struct{ id, state []byte }
	var groups []grp
	for rows.Next() {
		var g grp
		if err := rows.Scan(&g.id, &g.state); err != nil {
			rows.Close()
			return err
		}
		groups = append(groups, g)
	}
	rows.Close()
	for _, g := range groups {
		er, err := s.db.QueryContext(ctx, `SELECT epoch_id, data FROM mls_epochs WHERE group_id=? ORDER BY epoch_id`, g.id)
		if err != nil {
			return err
		}
		var eps []mls.Epoch
		for er.Next() {
			var e mls.Epoch
			var id int64
			if err := er.Scan(&id, &e.Data); err != nil {
				er.Close()
				return err
			}
			e.ID = uint64(id)
			eps = append(eps, e)
		}
		er.Close()
		if err := c.LoadGroup(g.id, g.state, eps); err != nil {
			return err
		}
	}
	kr, err := s.db.QueryContext(ctx, `SELECT id, data FROM mls_key_packages`)
	if err != nil {
		return err
	}
	defer kr.Close()
	for kr.Next() {
		var id, data []byte
		if err := kr.Scan(&id, &data); err != nil {
			return err
		}
		if err := c.LoadKeyPackage(id, data); err != nil {
			return err
		}
	}
	return kr.Err()
}

// ---- groups -----------------------------------------------------------------

type Group struct {
	ID        string
	Name      string
	Epoch     uint64
	Active    bool
	CreatedAt int64
}

func (t *Tx) UpsertGroup(g Group) error {
	_, err := t.tx.Exec(`INSERT INTO groups (id, name, epoch, active, created_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET epoch=excluded.epoch, active=excluded.active,
		name=CASE WHEN excluded.name <> '' THEN excluded.name ELSE groups.name END`,
		g.ID, g.Name, int64(g.Epoch), g.Active, g.CreatedAt)
	return err
}

// InsertGroupIfMissing creates a group row unless it already exists.
func (t *Tx) InsertGroupIfMissing(g Group) error {
	_, err := t.tx.Exec(`INSERT INTO groups (id, name, epoch, active, created_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(id) DO NOTHING`, g.ID, g.Name, int64(g.Epoch), g.Active, g.CreatedAt)
	return err
}

func (s *Store) Groups(ctx context.Context) ([]Group, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, epoch, active, created_at FROM groups ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Group
	for rows.Next() {
		var g Group
		var ep int64
		if err := rows.Scan(&g.ID, &g.Name, &ep, &g.Active, &g.CreatedAt); err != nil {
			return nil, err
		}
		g.Epoch = uint64(ep)
		out = append(out, g)
	}
	return out, rows.Err()
}

func (s *Store) Group(ctx context.Context, id string) (*Group, error) {
	var g Group
	var ep int64
	err := s.db.QueryRowContext(ctx, `SELECT id, name, epoch, active, created_at FROM groups WHERE id=?`, id).
		Scan(&g.ID, &g.Name, &ep, &g.Active, &g.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	g.Epoch = uint64(ep)
	return &g, err
}

// ---- messages ---------------------------------------------------------------

type Message struct {
	Seq          int64
	UID          string
	GroupID      string
	SenderUser   string
	SenderDevice string
	Kind         string // "text", "media", "system"
	Body         string // text, or JSON for media
	SentAt       int64  // unix millis
	Outgoing     bool
}

// InsertMessage stores a message; duplicates (group_id, uid) are ignored.
func (t *Tx) InsertMessage(m Message) error {
	_, err := t.tx.Exec(`INSERT INTO messages (uid, group_id, sender_user, sender_device, kind, body, sent_at, outgoing)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(group_id, uid) DO NOTHING`,
		m.UID, m.GroupID, m.SenderUser, m.SenderDevice, m.Kind, m.Body, m.SentAt, m.Outgoing)
	return err
}

// InsertRestoredMessage stores a message restored from a backup (already backed up).
func (t *Tx) InsertRestoredMessage(m Message) error {
	_, err := t.tx.Exec(`INSERT INTO messages (uid, group_id, sender_user, sender_device, kind, body, sent_at, outgoing, backed_up)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 1) ON CONFLICT(group_id, uid) DO NOTHING`,
		m.UID, m.GroupID, m.SenderUser, m.SenderDevice, m.Kind, m.Body, m.SentAt, m.Outgoing)
	return err
}

func scanMessages(rows *sql.Rows) ([]Message, error) {
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.Seq, &m.UID, &m.GroupID, &m.SenderUser, &m.SenderDevice, &m.Kind, &m.Body, &m.SentAt, &m.Outgoing); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

const msgCols = `seq, uid, group_id, sender_user, sender_device, kind, body, sent_at, outgoing`

// Messages returns the last `limit` messages of a group, oldest first.
func (s *Store) Messages(ctx context.Context, groupID string, limit int) ([]Message, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT * FROM (SELECT `+msgCols+` FROM messages WHERE group_id=?
		ORDER BY sent_at DESC, seq DESC LIMIT ?) ORDER BY sent_at, seq`, groupID, limit)
	if err != nil {
		return nil, err
	}
	return scanMessages(rows)
}

// NotBackedUp returns up to limit messages not yet in the history backup.
func (s *Store) NotBackedUp(ctx context.Context, limit int) ([]Message, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+msgCols+` FROM messages WHERE backed_up=0 ORDER BY seq LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	return scanMessages(rows)
}

func (t *Tx) MarkBackedUp(seqs []int64) error {
	for _, s := range seqs {
		if _, err := t.tx.Exec(`UPDATE messages SET backed_up=1 WHERE seq=?`, s); err != nil {
			return err
		}
	}
	return nil
}

// ---- users --------------------------------------------------------------------

func (t *Tx) PutUser(userID, username string) error {
	_, err := t.tx.Exec(`INSERT INTO users (user_id, username) VALUES (?, ?)
		ON CONFLICT(user_id) DO UPDATE SET username=excluded.username`, userID, username)
	return err
}

func (s *Store) Username(ctx context.Context, userID string) (string, error) {
	var n string
	err := s.db.QueryRowContext(ctx, `SELECT username FROM users WHERE user_id=?`, userID).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return n, err
}

// ---- delivery de-duplication ------------------------------------------------

func (s *Store) Processed(ctx context.Context, id int64) (bool, error) {
	var ok bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM processed_envelopes WHERE id=?)`, id).Scan(&ok)
	return ok, err
}

func (t *Tx) MarkProcessed(id int64) error {
	_, err := t.tx.Exec(`INSERT OR IGNORE INTO processed_envelopes (id) VALUES (?)`, id)
	return err
}

// ---- history restore -----------------------------------------------------------

func (s *Store) ChunkRestored(ctx context.Context, hash string) (bool, error) {
	var ok bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM restored_chunks WHERE hash=?)`, hash).Scan(&ok)
	return ok, err
}

func (t *Tx) MarkChunkRestored(hash string) error {
	_, err := t.tx.Exec(`INSERT OR IGNORE INTO restored_chunks (hash) VALUES (?)`, hash)
	return err
}
