package core

// Telegram-like message extras, all inside the MLS-encrypted payload:
//   - link previews: made by the SENDER's device (it fetches the page; the
//     recipients never contact the site) and sent with the message;
//   - polls: a "poll" message, votes are "vote" control messages;
//   - silent messages: no notification for the recipients;
//   - pinned messages: "pin" control messages (or only on this device).

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/kantracity/kantrae2e/client/store"
)

// Extra is stored as JSON with the message (store.Message.Extra).
type Extra struct {
	Link   *LinkPreview `json:"link,omitempty"`
	Poll   *Poll        `json:"poll,omitempty"`
	Silent bool         `json:"silent,omitempty"`
}

func (x *Extra) json() string {
	if x == nil {
		return ""
	}
	b, _ := json.Marshal(x)
	return string(b)
}

// ParseExtra reads a message's extras (nil if none).
func ParseExtra(m *store.Message) *Extra {
	if m == nil || m.Extra == "" {
		return nil
	}
	var x Extra
	if json.Unmarshal([]byte(m.Extra), &x) != nil {
		return nil
	}
	x.sanitize()
	return &x
}

// sanitize drops what another member could abuse: links that are not
// http(s) (javascript: in the preview card), oversized images and polls
// out of bounds. Extras come from other people's apps.
func (x *Extra) sanitize() {
	if l := x.Link; l != nil {
		u, err := url.Parse(l.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			x.Link = nil
		} else {
			l.Site, l.Title, l.Desc = clip(l.Site, 100), clip(l.Title, 200), clip(l.Desc, 400)
			if len(l.Image) > MaxThumb {
				l.Image = nil
			}
		}
	}
	if p := x.Poll; p != nil {
		if len(p.Options) < 2 || len(p.Options) > 10 || strings.TrimSpace(p.Question) == "" {
			x.Poll = nil
			return
		}
		p.Question = clip(p.Question, 300)
		for i := range p.Options {
			p.Options[i] = clip(p.Options[i], 100)
		}
	}
}

// LinkPreview of the first link of a text message.
type LinkPreview struct {
	URL   string `json:"url"`
	Site  string `json:"site,omitempty"`
	Title string `json:"title,omitempty"`
	Desc  string `json:"desc,omitempty"`
	Image []byte `json:"img,omitempty"` // small JPEG (fits MaxThumb)
}

// Poll: a question with 2-10 options.
type Poll struct {
	Question  string   `json:"q"`
	Options   []string `json:"o"`
	Multiple  bool     `json:"multi,omitempty"`
	Anonymous bool     `json:"anon,omitempty"`
}

var ErrBadPoll = errors.New("a poll needs a question and 2-10 different options")

// SendPoll sends a poll to a chat.
func (c *Client) SendPoll(ctx context.Context, groupID string, p Poll) (*store.Message, error) {
	p.Question = strings.TrimSpace(p.Question)
	var opts []string
	seen := map[string]bool{}
	for _, o := range p.Options {
		o = strings.TrimSpace(o)
		if o != "" && !seen[o] && len([]rune(o)) <= 100 {
			seen[o] = true
			opts = append(opts, o)
		}
	}
	p.Options = opts
	if p.Question == "" || len([]rune(p.Question)) > 255 || len(opts) < 2 || len(opts) > 10 {
		return nil, ErrBadPoll
	}
	if err := c.refreshForSend(ctx); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.requireLogin(); err != nil {
		return nil, err
	}
	// The body is the question: it shows in search and chat previews.
	return c.sendLocked(ctx, groupID, payload{Type: "poll", Body: p.Question, Extra: &Extra{Poll: &p}})
}

// Vote chooses options of a poll (none: retract the vote).
func (c *Client) Vote(ctx context.Context, groupID, uid string, options []int) error {
	m, err := c.ownMessage(ctx, groupID, uid)
	if err != nil {
		return err
	}
	x := ParseExtra(m)
	if m.Kind != "poll" || x == nil || x.Poll == nil {
		return errors.New("not a poll")
	}
	seen := map[int]bool{}
	var opts []int
	for _, o := range options {
		if o >= 0 && o < len(x.Poll.Options) && !seen[o] {
			seen[o] = true
			opts = append(opts, o)
		}
	}
	if len(opts) > 1 && !x.Poll.Multiple {
		return errors.New("only one option can be chosen")
	}
	a := c.Account()
	if a == nil {
		return ErrNotLoggedIn
	}
	b, _ := json.Marshal(opts)
	return c.sendControl(ctx, groupID, payload{Type: "vote", Target: uid, Options: opts}, func(tx *store.Tx) error {
		return tx.SetVote(groupID, uid, a.UserID, string(b))
	})
}

// Votes of a poll: user id -> chosen options.
func (c *Client) Votes(ctx context.Context, groupID, uid string) (map[string][]int, error) {
	raw, err := c.st.Votes(ctx, groupID, uid)
	if err != nil {
		return nil, err
	}
	out := map[string][]int{}
	for u, o := range raw {
		var opts []int
		if json.Unmarshal([]byte(o), &opts) == nil && len(opts) > 0 {
			out[u] = opts
		}
	}
	return out, nil
}

// Pin pins (or unpins) a message: for everybody in the chat, or only for
// this account (forAll false: kept on this device).
func (c *Client) Pin(ctx context.Context, groupID, uid string, pin, forAll bool) error {
	if _, err := c.ownMessage(ctx, groupID, uid); err != nil {
		return err
	}
	at := int64(0)
	if pin {
		at = time.Now().UnixMilli()
	}
	local := func(tx *store.Tx) error { return tx.SetPinned(groupID, uid, at) }
	if !forAll {
		return c.st.Update(ctx, local)
	}
	return c.sendControl(ctx, groupID, payload{Type: "pin", Target: uid, Unpin: !pin}, local)
}

// Pinned messages of a chat, newest pin first.
func (c *Client) Pinned(ctx context.Context, groupID string) ([]store.Message, error) {
	return c.st.Pinned(ctx, groupID)
}

// Search finds messages of a chat by text (on this device only).
func (c *Client) Search(ctx context.Context, groupID, q string, limit int) ([]store.Message, error) {
	return c.st.Search(ctx, groupID, q, limit)
}
