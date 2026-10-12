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
-- Known signature keys of devices (trust on first use + manual verification).
-- status: 'seen' (TOFU), 'verified' (compared out of band), 'conflict' (the
-- device appeared with another key: conflict_key; possible MITM).
CREATE TABLE IF NOT EXISTS device_keys (
    user_id      TEXT NOT NULL,
    device_id    TEXT NOT NULL,
    sig_key      BLOB NOT NULL,
    status       TEXT NOT NULL,
    conflict_key BLOB,
    first_seen   INTEGER NOT NULL,
    PRIMARY KEY (user_id, device_id)
);
-- Earliest time (unix ms) this device knows a user to be in a group. History
-- is only shared with a user's new devices from this point on.
CREATE TABLE IF NOT EXISTS member_first_seen (
    group_id TEXT NOT NULL,
    user_id  TEXT NOT NULL,
    ts       INTEGER NOT NULL,
    PRIMARY KEY (group_id, user_id)
);
`

// migrations upgrade existing databases; index i brings user_version to i+1.
var migrations = []string{
	// origin: '' received/sent directly, 'backup' restored from the history
	// backup, 'shared' re-sent by another member (shared_by = its user id).
	`ALTER TABLE messages ADD COLUMN origin TEXT NOT NULL DEFAULT '';
	 ALTER TABLE messages ADD COLUMN shared_by TEXT NOT NULL DEFAULT '';`,
	// peer: the other user of a direct chat (kind is derived from the id).
	`ALTER TABLE groups ADD COLUMN peer TEXT NOT NULL DEFAULT '';`,
	// Public profiles (cached), avatar blob cached per avatar id.
	`ALTER TABLE users ADD COLUMN display_name TEXT NOT NULL DEFAULT '';
	 ALTER TABLE users ADD COLUMN bio TEXT NOT NULL DEFAULT '';
	 ALTER TABLE users ADD COLUMN avatar_id TEXT NOT NULL DEFAULT '';
	 ALTER TABLE users ADD COLUMN avatar BLOB;
	 ALTER TABLE users ADD COLUMN avatar_cached TEXT NOT NULL DEFAULT '';
	 ALTER TABLE users ADD COLUMN fetched_at INTEGER NOT NULL DEFAULT 0;`,
	// Telegram-like message features: replies, edits, deletion for
	// everyone (a tombstone stops restored/shared copies from coming back),
	// forwards, reactions and read marks.
	`ALTER TABLE messages ADD COLUMN reply_to TEXT NOT NULL DEFAULT '';
	 ALTER TABLE messages ADD COLUMN edited_at INTEGER NOT NULL DEFAULT 0;
	 ALTER TABLE messages ADD COLUMN deleted INTEGER NOT NULL DEFAULT 0;
	 ALTER TABLE messages ADD COLUMN fwd_from TEXT NOT NULL DEFAULT '';
	 CREATE TABLE reactions (
	     group_id TEXT NOT NULL,
	     msg_uid  TEXT NOT NULL,
	     user_id  TEXT NOT NULL,
	     emoji    TEXT NOT NULL,
	     PRIMARY KEY (group_id, msg_uid, user_id)
	 );
	 CREATE TABLE read_marks (
	     group_id TEXT NOT NULL,
	     user_id  TEXT NOT NULL,
	     upto     INTEGER NOT NULL,
	     PRIMARY KEY (group_id, user_id)
	 );`,
	// Chat deletion: hidden_at hides the chat until a newer message arrives;
	// left_at marks a group this account left (its messages are dropped).
	`ALTER TABLE groups ADD COLUMN hidden_at INTEGER NOT NULL DEFAULT 0;
	 ALTER TABLE groups ADD COLUMN left_at INTEGER NOT NULL DEFAULT 0;`,
	// Group info (description, photo as a media reference) and the invite
	// link's secret, all shared among the members through MLS.
	`ALTER TABLE groups ADD COLUMN about TEXT NOT NULL DEFAULT '';
	 ALTER TABLE groups ADD COLUMN avatar TEXT NOT NULL DEFAULT '';
	 ALTER TABLE groups ADD COLUMN invite TEXT NOT NULL DEFAULT '';`,
}

func migrate(db *sql.DB) error {
	var v int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		return err
	}
	for ; v < len(migrations); v++ {
		if _, err := db.Exec(migrations[v]); err != nil {
			return fmt.Errorf("client db migration %d: %w", v+1, err)
		}
		if _, err := db.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, v+1)); err != nil {
			return err
		}
	}
	return nil
}

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
	if err := migrate(db); err != nil {
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
	Peer      string // direct chats: the other user's id
	HiddenAt  int64  // deleted ("hidden") by the user at this time (unix ms), 0 = never
	LeftAt    int64  // the account left this group at this time (unix ms), 0 = member
	About     string // group description
	Avatar    string // group photo: MediaRef JSON ("" = none)
	Invite    string // invite link secret ("" = no link)
}

func (t *Tx) UpsertGroup(g Group) error {
	_, err := t.tx.Exec(`INSERT INTO groups (id, name, epoch, active, created_at, peer) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET epoch=excluded.epoch, active=excluded.active,
		name=CASE WHEN excluded.name <> '' THEN excluded.name ELSE groups.name END,
		peer=CASE WHEN excluded.peer <> '' THEN excluded.peer ELSE groups.peer END`,
		g.ID, g.Name, int64(g.Epoch), g.Active, g.CreatedAt, g.Peer)
	return err
}

// InsertGroupIfMissing creates a group row unless it already exists.
func (t *Tx) InsertGroupIfMissing(g Group) error {
	_, err := t.tx.Exec(`INSERT INTO groups (id, name, epoch, active, created_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(id) DO NOTHING`, g.ID, g.Name, int64(g.Epoch), g.Active, g.CreatedAt)
	return err
}

func (s *Store) Groups(ctx context.Context) ([]Group, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, epoch, active, created_at, peer, hidden_at, left_at, about, avatar, invite FROM groups ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Group
	for rows.Next() {
		var g Group
		var ep int64
		if err := rows.Scan(&g.ID, &g.Name, &ep, &g.Active, &g.CreatedAt, &g.Peer, &g.HiddenAt, &g.LeftAt, &g.About, &g.Avatar, &g.Invite); err != nil {
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
	err := s.db.QueryRowContext(ctx, `SELECT id, name, epoch, active, created_at, peer, hidden_at, left_at, about, avatar, invite FROM groups WHERE id=?`, id).
		Scan(&g.ID, &g.Name, &ep, &g.Active, &g.CreatedAt, &g.Peer, &g.HiddenAt, &g.LeftAt, &g.About, &g.Avatar, &g.Invite)
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
	Origin       string // "", "backup" or "shared"
	SharedBy     string // user id of the member who re-sent it (Origin "shared")
	ReplyTo      string `json:",omitempty"` // uid of the message this one answers
	EditedAt     int64  `json:",omitempty"` // unix ms of the last edit
	Deleted      bool   `json:",omitempty"` // deleted for everyone (tombstone)
	FwdFrom      string `json:",omitempty"` // forwarded: name of the original author
}

// InsertMessage stores a message; duplicates (group_id, uid) are ignored.
func (t *Tx) InsertMessage(m Message) error {
	_, err := t.tx.Exec(`INSERT INTO messages (uid, group_id, sender_user, sender_device, kind, body, sent_at, outgoing, reply_to, fwd_from)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(group_id, uid) DO NOTHING`,
		m.UID, m.GroupID, m.SenderUser, m.SenderDevice, m.Kind, m.Body, m.SentAt, m.Outgoing, m.ReplyTo, m.FwdFrom)
	return err
}

// FixOutgoing marks the account's own messages as outgoing (earlier
// versions stored messages sent from another own device as incoming).
func (t *Tx) FixOutgoing(userID string) error {
	_, err := t.tx.Exec(`UPDATE messages SET outgoing=1 WHERE sender_user=? AND outgoing=0`, userID)
	return err
}

// EditMessage replaces the text of a message by its author (a newer edit
// wins); the message is backed up again.
func (t *Tx) EditMessage(groupID, uid, sender, body string, at int64) (bool, error) {
	r, err := t.tx.Exec(`UPDATE messages SET body=?, edited_at=?, backed_up=0
		WHERE group_id=? AND uid=? AND sender_user=? AND deleted=0 AND edited_at < ?`, body, at, groupID, uid, sender, at)
	if err != nil {
		return false, err
	}
	n, _ := r.RowsAffected()
	return n > 0, nil
}

// DeleteMessage turns a message into a tombstone. sender "" deletes any
// message (local "delete for me"); otherwise only the author's own.
func (t *Tx) DeleteMessage(groupID, uid, sender string) (bool, error) {
	q := `UPDATE messages SET body='', deleted=1, backed_up=0 WHERE group_id=? AND uid=? AND deleted=0`
	args := []any{groupID, uid}
	if sender != "" {
		q += ` AND sender_user=?`
		args = append(args, sender)
	}
	r, err := t.tx.Exec(q, args...)
	if err != nil {
		return false, err
	}
	n, _ := r.RowsAffected()
	if n > 0 {
		_, err = t.tx.Exec(`DELETE FROM reactions WHERE group_id=? AND msg_uid=?`, groupID, uid)
	}
	return n > 0, err
}

// ClearMessages deletes (as tombstones, so the history backup carries the
// deletion) the messages of a group sent up to a time.
func (t *Tx) ClearMessages(groupID string, upto int64) (int64, error) {
	r, err := t.tx.Exec(`UPDATE messages SET body='', deleted=1, backed_up=0 WHERE group_id=? AND sent_at<=? AND deleted=0`, groupID, upto)
	if err != nil {
		return 0, err
	}
	n, _ := r.RowsAffected()
	if n > 0 {
		_, err = t.tx.Exec(`DELETE FROM reactions WHERE group_id=? AND msg_uid IN
			(SELECT uid FROM messages WHERE group_id=? AND deleted=1)`, groupID, groupID)
	}
	return n, err
}

// SetChatFlags records that the chat was hidden / left at a time (the
// newest time wins, so merging copies from other devices is order-free).
// A zero value leaves the flag as it is.
func (t *Tx) SetChatFlags(groupID string, hiddenAt, leftAt int64) error {
	_, err := t.tx.Exec(`UPDATE groups SET hidden_at=max(hidden_at, ?), left_at=max(left_at, ?) WHERE id=?`, hiddenAt, leftAt, groupID)
	return err
}

// GroupInfoUpdate changes the shared group info; nil fields stay as they are.
type GroupInfoUpdate struct {
	Name, About, Avatar, Invite *string
}

// SetGroupInfo applies a group info change (from MLS "meta" messages).
func (t *Tx) SetGroupInfo(groupID string, u GroupInfoUpdate) error {
	set := func(col string, v *string) error {
		if v == nil {
			return nil
		}
		_, err := t.tx.Exec(`UPDATE groups SET `+col+`=? WHERE id=?`, *v, groupID)
		return err
	}
	for _, f := range []struct {
		col string
		v   *string
	}{{"name", u.Name}, {"about", u.About}, {"avatar", u.Avatar}, {"invite", u.Invite}} {
		if err := set(f.col, f.v); err != nil {
			return err
		}
	}
	return nil
}

// ResetChatFlags shows a hidden chat again / clears "left" (joined again).
func (t *Tx) ResetChatFlags(groupID string, hidden, left bool) error {
	if hidden {
		if _, err := t.tx.Exec(`UPDATE groups SET hidden_at=0 WHERE id=?`, groupID); err != nil {
			return err
		}
	}
	if left {
		if _, err := t.tx.Exec(`UPDATE groups SET left_at=0 WHERE id=?`, groupID); err != nil {
			return err
		}
	}
	return nil
}

// SetReaction sets (emoji "" removes) a user's reaction to a message.
func (t *Tx) SetReaction(groupID, uid, userID, emoji string) error {
	if emoji == "" {
		_, err := t.tx.Exec(`DELETE FROM reactions WHERE group_id=? AND msg_uid=? AND user_id=?`, groupID, uid, userID)
		return err
	}
	_, err := t.tx.Exec(`INSERT INTO reactions (group_id, msg_uid, user_id, emoji) VALUES (?, ?, ?, ?)
		ON CONFLICT(group_id, msg_uid, user_id) DO UPDATE SET emoji=excluded.emoji`, groupID, uid, userID, emoji)
	return err
}

// Reaction is one user's reaction.
type Reaction struct {
	MsgUID string
	UserID string
	Emoji  string
}

// Reactions of a group's messages.
func (s *Store) Reactions(ctx context.Context, groupID string) ([]Reaction, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT msg_uid, user_id, emoji FROM reactions WHERE group_id=?`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Reaction
	for rows.Next() {
		var r Reaction
		if err := rows.Scan(&r.MsgUID, &r.UserID, &r.Emoji); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SetReadMark records that userID has read groupID up to ts (keeps the max).
func (t *Tx) SetReadMark(groupID, userID string, ts int64) error {
	_, err := t.tx.Exec(`INSERT INTO read_marks (group_id, user_id, upto) VALUES (?, ?, ?)
		ON CONFLICT(group_id, user_id) DO UPDATE SET upto=max(upto, excluded.upto)`, groupID, userID, ts)
	return err
}

// ReadMarks returns, per user, up to when they have read the group.
func (s *Store) ReadMarks(ctx context.Context, groupID string) (map[string]int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT user_id, upto FROM read_marks WHERE group_id=?`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var u string
		var ts int64
		if err := rows.Scan(&u, &ts); err != nil {
			return nil, err
		}
		out[u] = ts
	}
	return out, rows.Err()
}

// Message returns one message (deleted ones included).
func (s *Store) Message(ctx context.Context, groupID, uid string) (*Message, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+msgCols+` FROM messages WHERE group_id=? AND uid=?`, groupID, uid)
	if err != nil {
		return nil, err
	}
	ms, err := scanMessages(rows)
	if err != nil {
		return nil, err
	}
	if len(ms) == 0 {
		return nil, ErrNotFound
	}
	return &ms[0], nil
}

// InsertRestoredMessage stores a message restored from a backup (already
// backed up) and reports whether it was new.
func (t *Tx) InsertRestoredMessage(m Message) (bool, error) {
	r, err := t.tx.Exec(`INSERT INTO messages (uid, group_id, sender_user, sender_device, kind, body, sent_at, outgoing, backed_up, origin,
			reply_to, edited_at, deleted, fwd_from)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 1, 'backup', ?, ?, ?, ?) ON CONFLICT(group_id, uid) DO NOTHING`,
		m.UID, m.GroupID, m.SenderUser, m.SenderDevice, m.Kind, m.Body, m.SentAt, m.Outgoing, m.ReplyTo, m.EditedAt, m.Deleted, m.FwdFrom)
	if err != nil {
		return false, err
	}
	n, _ := r.RowsAffected()
	if n == 0 {
		// Known already: a later edit or deletion in the backup still applies.
		if m.Deleted {
			_, err = t.tx.Exec(`UPDATE messages SET body='', deleted=1 WHERE group_id=? AND uid=?`, m.GroupID, m.UID)
		} else if m.EditedAt > 0 {
			_, err = t.tx.Exec(`UPDATE messages SET body=?, edited_at=? WHERE group_id=? AND uid=? AND deleted=0 AND edited_at < ?`,
				m.Body, m.EditedAt, m.GroupID, m.UID, m.EditedAt)
		}
		return false, err
	}
	return !m.Deleted, nil
}

// InsertSharedMessage stores a message re-sent by another member; it reports
// whether the message was new.
func (t *Tx) InsertSharedMessage(m Message, sharedBy string) (bool, error) {
	r, err := t.tx.Exec(`INSERT INTO messages (uid, group_id, sender_user, sender_device, kind, body, sent_at, outgoing, origin, shared_by,
			reply_to, edited_at, fwd_from)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'shared', ?, ?, ?, ?) ON CONFLICT(group_id, uid) DO NOTHING`,
		m.UID, m.GroupID, m.SenderUser, m.SenderDevice, m.Kind, m.Body, m.SentAt, m.Outgoing, sharedBy, m.ReplyTo, m.EditedAt, m.FwdFrom)
	if err != nil {
		return false, err
	}
	n, _ := r.RowsAffected()
	return n > 0, nil
}

func scanMessages(rows *sql.Rows) ([]Message, error) {
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.Seq, &m.UID, &m.GroupID, &m.SenderUser, &m.SenderDevice, &m.Kind, &m.Body, &m.SentAt, &m.Outgoing, &m.Origin, &m.SharedBy,
			&m.ReplyTo, &m.EditedAt, &m.Deleted, &m.FwdFrom); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

const msgCols = `seq, uid, group_id, sender_user, sender_device, kind, body, sent_at, outgoing, origin, shared_by,
	reply_to, edited_at, deleted, fwd_from`

// Messages returns the last `limit` messages of a group, oldest first.
func (s *Store) Messages(ctx context.Context, groupID string, limit int) ([]Message, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT * FROM (SELECT `+msgCols+` FROM messages WHERE group_id=? AND deleted=0
		ORDER BY sent_at DESC, seq DESC LIMIT ?) ORDER BY sent_at, seq`, groupID, limit)
	if err != nil {
		return nil, err
	}
	return scanMessages(rows)
}

// MessagesSince returns up to limit messages of a group with sent_at >= from,
// oldest first.
func (s *Store) MessagesSince(ctx context.Context, groupID string, from int64, limit int) ([]Message, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+msgCols+` FROM messages WHERE group_id=? AND sent_at >= ? AND deleted=0
		ORDER BY sent_at, seq LIMIT ?`, groupID, from, limit)
	if err != nil {
		return nil, err
	}
	return scanMessages(rows)
}

// LastMessageTime returns the newest sent_at of a group (0 if none).
func (s *Store) LastMessageTime(ctx context.Context, groupID string) (int64, error) {
	var ts sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT max(sent_at) FROM messages WHERE group_id=? AND deleted=0`, groupID).Scan(&ts)
	return ts.Int64, err
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

// Profile is a cached public profile.
type Profile struct {
	UserID      string
	Username    string
	DisplayName string
	Bio         string
	AvatarID    string
	FetchedAt   int64 // unix ms; 0: only the username is known
}

// PutProfile stores a fetched profile.
func (t *Tx) PutProfile(p Profile) error {
	_, err := t.tx.Exec(`INSERT INTO users (user_id, username, display_name, bio, avatar_id, fetched_at)
		VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT(user_id) DO UPDATE SET username=excluded.username,
		display_name=excluded.display_name, bio=excluded.bio, avatar_id=excluded.avatar_id,
		fetched_at=excluded.fetched_at`, p.UserID, p.Username, p.DisplayName, p.Bio, p.AvatarID, p.FetchedAt)
	return err
}

func (s *Store) Profile(ctx context.Context, userID string) (*Profile, error) {
	p := Profile{UserID: userID}
	err := s.db.QueryRowContext(ctx, `SELECT username, display_name, bio, avatar_id, fetched_at FROM users WHERE user_id=?`,
		userID).Scan(&p.Username, &p.DisplayName, &p.Bio, &p.AvatarID, &p.FetchedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &p, err
}

// PutAvatar caches the photo with the given avatar id.
func (t *Tx) PutAvatar(userID, avatarID string, img []byte) error {
	_, err := t.tx.Exec(`UPDATE users SET avatar=?, avatar_cached=? WHERE user_id=?`, img, avatarID, userID)
	return err
}

// Avatar returns the cached photo if it is the current one.
func (s *Store) Avatar(ctx context.Context, userID, avatarID string) ([]byte, error) {
	var img []byte
	err := s.db.QueryRowContext(ctx, `SELECT avatar FROM users WHERE user_id=? AND avatar_cached=? AND avatar IS NOT NULL`,
		userID, avatarID).Scan(&img)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return img, err
}

func (s *Store) Username(ctx context.Context, userID string) (string, error) {
	var n string
	err := s.db.QueryRowContext(ctx, `SELECT username FROM users WHERE user_id=?`, userID).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return n, err
}

// ---- membership history -----------------------------------------------------

// SeenMember records that userID is in groupID since ts, keeping the earliest.
func (t *Tx) SeenMember(groupID, userID string, ts int64) error {
	_, err := t.tx.Exec(`INSERT INTO member_first_seen (group_id, user_id, ts) VALUES (?, ?, ?)
		ON CONFLICT(group_id, user_id) DO UPDATE SET ts = min(ts, excluded.ts)`, groupID, userID, ts)
	return err
}

// FirstSeen returns when userID was first known in groupID.
func (s *Store) FirstSeen(ctx context.Context, groupID, userID string) (int64, error) {
	var ts int64
	err := s.db.QueryRowContext(ctx, `SELECT ts FROM member_first_seen WHERE group_id=? AND user_id=?`, groupID, userID).Scan(&ts)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return ts, err
}

// HasPendingBackup reports whether messages wait for the history backup.
func (s *Store) HasPendingBackup(ctx context.Context) (bool, error) {
	var ok bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM messages WHERE backed_up=0)`).Scan(&ok)
	return ok, err
}

// ---- device keys ---------------------------------------------------------------

const (
	KeySeen     = "seen"
	KeyVerified = "verified"
	KeyConflict = "conflict"
)

type DeviceKey struct {
	UserID      string
	DeviceID    string
	Key         []byte
	Status      string
	ConflictKey []byte
	FirstSeen   int64
}

func (s *Store) DeviceKeys(ctx context.Context, userID string) ([]DeviceKey, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT user_id, device_id, sig_key, status, conflict_key, first_seen
		FROM device_keys WHERE user_id=? ORDER BY first_seen, device_id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DeviceKey
	for rows.Next() {
		var k DeviceKey
		if err := rows.Scan(&k.UserID, &k.DeviceID, &k.Key, &k.Status, &k.ConflictKey, &k.FirstSeen); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

func (s *Store) DeviceKey(ctx context.Context, userID, deviceID string) (*DeviceKey, error) {
	k := DeviceKey{}
	err := s.db.QueryRowContext(ctx, `SELECT user_id, device_id, sig_key, status, conflict_key, first_seen
		FROM device_keys WHERE user_id=? AND device_id=?`, userID, deviceID).
		Scan(&k.UserID, &k.DeviceID, &k.Key, &k.Status, &k.ConflictKey, &k.FirstSeen)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &k, err
}

func (t *Tx) PutDeviceKey(k DeviceKey) error {
	_, err := t.tx.Exec(`INSERT INTO device_keys (user_id, device_id, sig_key, status, conflict_key, first_seen)
		VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT(user_id, device_id) DO UPDATE SET
		sig_key=excluded.sig_key, status=excluded.status, conflict_key=excluded.conflict_key`,
		k.UserID, k.DeviceID, k.Key, k.Status, k.ConflictKey, k.FirstSeen)
	return err
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
