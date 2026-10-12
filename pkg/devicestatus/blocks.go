package devicestatus

import (
	"context"
	"time"

	authv1 "github.com/kantracity/kantrae2e/gen/kantra/auth/v1"
)

// BlockChecker tells whether either of two users has blocked the other.
type BlockChecker interface {
	Blocked(ctx context.Context, userA, userB string) (bool, error)
}

// BlockTTL bounds how long a (un)block takes to reach the other services.
const BlockTTL = 10 * time.Second

type blockEntry struct {
	blocked bool
	at      time.Time
}

func pairKey(a, b string) string {
	if a > b {
		a, b = b, a
	}
	return a + "/" + b
}

// Blocked asks auth-service (cached for BlockTTL).
func (c *Client) Blocked(ctx context.Context, userA, userB string) (bool, error) {
	k := pairKey(userA, userB)
	now := c.now()
	ttl := min(BlockTTL, c.TTL) // TTL 0 (tests) disables caching
	c.mu.Lock()
	if e, ok := c.blocks[k]; ok && now.Sub(e.at) < ttl {
		c.mu.Unlock()
		return e.blocked, nil
	}
	c.mu.Unlock()
	r, err := c.c.Blocked(ctx, &authv1.BlockedRequest{UserA: userA, UserB: userB})
	if err != nil {
		return false, err
	}
	c.mu.Lock()
	if c.blocks == nil {
		c.blocks = map[string]blockEntry{}
	}
	c.blocks[k] = blockEntry{blocked: r.Blocked, at: now}
	c.mu.Unlock()
	return r.Blocked, nil
}

// IsBlocked: a nil checker (tests, old deployments) blocks nobody.
func IsBlocked(ctx context.Context, ch BlockChecker, userA, userB string) (bool, error) {
	if ch == nil || userA == "" || userB == "" || userA == userB {
		return false, nil
	}
	return ch.Blocked(ctx, userA, userB)
}
