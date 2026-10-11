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
const MaxFileSize = 64 << 20

// MediaRef travels inside an MLS application message. The key never
// reaches the media service, which only stores ciphertext.
type MediaRef struct {
	Hash string `json:"hash"` // BLAKE3 of the ciphertext (object key)
	Key  []byte `json:"key"`  // XChaCha20-Poly1305 key
	Name string `json:"name"`
	Size int    `json:"size"`
	// Presentation (all optional, like Telegram's media attributes).
	Mime string `json:"mime,omitempty"`
	// Kind: "photo", "video", "voice", "round" (video message) or "file".
	Kind     string  `json:"kind,omitempty"`
	Width    int     `json:"w,omitempty"`
	Height   int     `json:"h,omitempty"`
	Duration float64 `json:"dur,omitempty"` // seconds
	// Thumb is a tiny preview (JPEG, a few KB) shown before the download.
	Thumb []byte `json:"thumb,omitempty"`
	// Waveform of a voice message (0..255 per bar).
	Waveform []byte `json:"wave,omitempty"`
	Caption  string `json:"caption,omitempty"`
}

// MediaInput describes a file to send.
type MediaInput struct {
	Name     string
	Data     []byte
	Mime     string
	Kind     string
	Width    int
	Height   int
	Duration float64
	Thumb    []byte
	Waveform []byte
	Caption  string
}

// MaxThumb bounds the inline preview.
const MaxThumb = 16 << 10

// SendFile encrypts data with a fresh key, uploads the ciphertext to the
// media service and sends the reference through the group's MLS session.
func (c *Client) SendFile(ctx context.Context, groupID, name string, data []byte) (*store.Message, error) {
	return c.SendMedia(ctx, groupID, MediaInput{Name: name, Data: data}, SendOptions{})
}

// SendMedia sends a photo, video, voice/video message or file.
func (c *Client) SendMedia(ctx context.Context, groupID string, in MediaInput, opt SendOptions) (*store.Message, error) {
	data := in.Data
	if len(in.Thumb) > MaxThumb {
		in.Thumb = nil
	}
	if in.Kind == "" {
		in.Kind = "file"
	}
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
	return c.sendLocked(ctx, groupID, payload{Type: "media", ReplyTo: opt.ReplyTo, Media: &MediaRef{Hash: hash, Key: key,
		Name: in.Name, Size: len(data), Mime: in.Mime, Kind: in.Kind, Width: in.Width, Height: in.Height,
		Duration: in.Duration, Thumb: in.Thumb, Waveform: in.Waveform, Caption: in.Caption}})
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
