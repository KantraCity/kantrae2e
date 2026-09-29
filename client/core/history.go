package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/kantracity/kantrae2e/client/crypto"
	"github.com/kantracity/kantrae2e/client/store"
	historyv1 "github.com/kantracity/kantrae2e/gen/kantra/history/v1"
	"github.com/kantracity/kantrae2e/pkg/blobstore"
)

// historyChunk is the plaintext of one backup chunk. It is encrypted with
// the history key (Argon2id of the seed phrase), never with MLS secrets:
// MLS forward secrecy makes old epoch keys unrecoverable by design.
type historyChunk struct {
	V        int               `json:"v"`
	Groups   []chunkGroup      `json:"groups"`
	Users    map[string]string `json:"users"`
	Messages []store.Message   `json:"messages"`
}

type chunkGroup struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

const (
	chunkMessages = 500
	historyAD     = "kantra/history/v1"
)

func (c *Client) historyKey(ctx context.Context) ([]byte, error) {
	k, err := c.st.Get(ctx, kvHistKey)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrNoHistoryKey
	}
	return k, err
}

// BackupHistory uploads all messages not yet backed up as encrypted chunks.
// It returns the number of messages uploaded.
func (c *Client) BackupHistory(ctx context.Context) (int, error) {
	key, err := c.historyKey(ctx)
	if err != nil {
		return 0, err
	}
	if err := c.refreshToken(ctx); err != nil {
		return 0, err
	}
	total := 0
	for {
		msgs, err := c.st.NotBackedUp(ctx, chunkMessages)
		if err != nil {
			return total, err
		}
		if len(msgs) == 0 {
			return total, nil
		}
		chunk := historyChunk{V: 1, Users: map[string]string{}}
		seen := map[string]bool{}
		seqs := make([]int64, 0, len(msgs))
		for i := range msgs {
			m := &msgs[i]
			seqs = append(seqs, m.Seq)
			m.Seq = 0
			if !seen[m.GroupID] {
				seen[m.GroupID] = true
				name := ""
				if g, err := c.st.Group(ctx, m.GroupID); err == nil {
					name = g.Name
				}
				chunk.Groups = append(chunk.Groups, chunkGroup{ID: m.GroupID, Name: name})
			}
			if _, ok := chunk.Users[m.SenderUser]; !ok {
				if n, err := c.st.Username(ctx, m.SenderUser); err == nil {
					chunk.Users[m.SenderUser] = n
				}
			}
		}
		chunk.Messages = msgs
		plain, err := json.Marshal(chunk)
		if err != nil {
			return total, err
		}
		sealed, err := crypto.Seal(key, plain, []byte(historyAD))
		if err != nil {
			return total, err
		}
		hash := blobstore.Hash(sealed)
		if _, err := c.hist.PutChunk(ctx, &historyv1.PutChunkRequest{Hash: hash, Data: sealed}); err != nil {
			return total, err
		}
		err = c.st.Update(ctx, func(tx *store.Tx) error {
			if err := tx.MarkBackedUp(seqs); err != nil {
				return err
			}
			return tx.MarkChunkRestored(hash) // our own chunk needs no restore
		})
		if err != nil {
			return total, err
		}
		total += len(msgs)
	}
}

// RestoreHistory downloads every chunk of the manifest that this device has
// not seen yet, decrypts it locally and merges the messages. It returns the
// number of messages in the restored chunks.
func (c *Client) RestoreHistory(ctx context.Context) (int, error) {
	key, err := c.historyKey(ctx)
	if err != nil {
		return 0, err
	}
	if err := c.refreshToken(ctx); err != nil {
		return 0, err
	}
	man, err := c.hist.GetManifest(ctx, &historyv1.GetManifestRequest{})
	if err != nil {
		return 0, err
	}
	total := 0
	for _, e := range man.Entries {
		done, err := c.st.ChunkRestored(ctx, e.ChunkHash)
		if err != nil {
			return total, err
		}
		if done {
			continue
		}
		r, err := c.hist.GetChunk(ctx, &historyv1.GetChunkRequest{Hash: e.ChunkHash})
		if err != nil {
			return total, err
		}
		if blobstore.Hash(r.Data) != e.ChunkHash {
			return total, fmt.Errorf("chunk %s: hash mismatch", e.ChunkHash)
		}
		plain, err := crypto.Open(key, r.Data, []byte(historyAD))
		if err != nil {
			return total, fmt.Errorf("chunk %s: cannot decrypt (wrong seed phrase?)", e.ChunkHash)
		}
		var chunk historyChunk
		if err := json.Unmarshal(plain, &chunk); err != nil {
			return total, err
		}
		err = c.st.Update(ctx, func(tx *store.Tx) error {
			for _, g := range chunk.Groups {
				// Restored groups are read-only until someone adds this device.
				if err := tx.InsertGroupIfMissing(store.Group{ID: g.ID, Name: g.Name}); err != nil {
					return err
				}
			}
			for uid, name := range chunk.Users {
				if err := tx.PutUser(uid, name); err != nil {
					return err
				}
			}
			for _, m := range chunk.Messages {
				m.Outgoing = m.SenderUser == c.acct.UserID
				if err := tx.InsertRestoredMessage(m); err != nil {
					return err
				}
			}
			return tx.MarkChunkRestored(e.ChunkHash)
		})
		if err != nil {
			return total, err
		}
		total += len(chunk.Messages)
	}
	return total, nil
}
