package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/kantracity/kantrae2e/client/crypto"
	"github.com/kantracity/kantrae2e/client/store"
	mediav1 "github.com/kantracity/kantrae2e/gen/kantra/media/v1"
	"github.com/kantracity/kantrae2e/pkg/blobstore"
)

// MaxFileSize is the largest plaintext file accepted for sending.
const MaxFileSize = 24 << 20

// MediaRef travels inside an MLS application message. The key never
// reaches the media service, which only stores ciphertext.
type MediaRef struct {
	Hash string `json:"hash"` // BLAKE3 of the ciphertext (object key)
	Key  []byte `json:"key"`  // XChaCha20-Poly1305 key
	Name string `json:"name"`
	Size int    `json:"size"`
}

// SendFile encrypts data with a fresh key, uploads the ciphertext to the
// media service and sends the reference through the group's MLS session.
func (c *Client) SendFile(ctx context.Context, groupID, name string, data []byte) (*store.Message, error) {
	if len(data) == 0 || len(data) > MaxFileSize {
		return nil, fmt.Errorf("file must be 1..%d bytes", MaxFileSize)
	}
	if err := c.refreshToken(ctx); err != nil {
		return nil, err
	}
	key := crypto.NewKey()
	ct, err := crypto.Seal(key, data, []byte("kantra/media/v1"))
	if err != nil {
		return nil, err
	}
	hash := blobstore.Hash(ct)
	if _, err := c.media.PutMedia(ctx, &mediav1.PutMediaRequest{Hash: hash, Data: ct}); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.requireLogin(); err != nil {
		return nil, err
	}
	return c.sendLocked(ctx, groupID, payload{Type: "media", Media: &MediaRef{Hash: hash, Key: key, Name: name, Size: len(data)}})
}

// ParseMedia extracts the MediaRef of a media message.
func ParseMedia(m *store.Message) (*MediaRef, error) {
	if m.Kind != "media" {
		return nil, errors.New("not a media message")
	}
	var ref MediaRef
	if err := json.Unmarshal([]byte(m.Body), &ref); err != nil {
		return nil, err
	}
	return &ref, nil
}

// DownloadMedia fetches, verifies and decrypts a media message's file.
func (c *Client) DownloadMedia(ctx context.Context, m *store.Message) (name string, data []byte, err error) {
	ref, err := ParseMedia(m)
	if err != nil {
		return "", nil, err
	}
	if err := c.refreshToken(ctx); err != nil {
		return "", nil, err
	}
	r, err := c.media.GetMedia(ctx, &mediav1.GetMediaRequest{Hash: ref.Hash})
	if err != nil {
		return "", nil, err
	}
	if blobstore.Hash(r.Data) != ref.Hash {
		return "", nil, errors.New("media hash mismatch")
	}
	data, err = crypto.Open(ref.Key, r.Data, []byte("kantra/media/v1"))
	return ref.Name, data, err
}
