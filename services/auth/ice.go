package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"

	authv1 "github.com/kantracity/kantrae2e/gen/kantra/auth/v1"
	"github.com/kantracity/kantrae2e/pkg/authmiddleware"
)

// TURN for calls (WebRTC). The TURN server (coturn, use-auth-secret) and
// this service share a secret; credentials are the standard "TURN REST API"
// ones: username "<expiry unix>:<user id>", password
// base64(HMAC-SHA1(secret, username)). They expire, so a leaked one is of
// little use. Media relayed by TURN is DTLS-SRTP encrypted end to end.

// ICEConfig: URLs like "turn:chat.example.com:3478?transport=udp"; Secret
// empty disables TURN (only the STUN URLs are handed out).
type ICEConfig struct {
	URLs   []string
	Secret string
	TTL    time.Duration
}

// WithICE configures the call servers (see ICEConfig).
func (s *Service) WithICE(c ICEConfig) *Service {
	if c.TTL == 0 {
		c.TTL = 12 * time.Hour
	}
	s.ice = c
	return s
}

// ICEFromEnv: TURN_HOST (host[:port], port 3478 by default) and TURN_SECRET.
func ICEFromEnv(host, secret string) ICEConfig {
	host = strings.TrimSpace(host)
	if host == "" {
		return ICEConfig{}
	}
	if !strings.Contains(host, ":") {
		host += ":3478"
	}
	c := ICEConfig{URLs: []string{"stun:" + host}, Secret: secret}
	if secret != "" {
		c.URLs = append(c.URLs, "turn:"+host+"?transport=udp", "turn:"+host+"?transport=tcp")
	}
	return c
}

func (s *Service) GetIceServers(ctx context.Context, _ *authv1.GetIceServersRequest) (*authv1.GetIceServersResponse, error) {
	uid, err := authmiddleware.RequireUser(ctx)
	if err != nil {
		return nil, err
	}
	if len(s.ice.URLs) == 0 {
		return &authv1.GetIceServersResponse{}, nil
	}
	var stun, turn []string
	for _, u := range s.ice.URLs {
		if strings.HasPrefix(u, "turn") {
			turn = append(turn, u)
		} else {
			stun = append(stun, u)
		}
	}
	out := &authv1.GetIceServersResponse{}
	if len(stun) > 0 {
		out.Servers = append(out.Servers, &authv1.IceServer{Urls: stun})
	}
	if len(turn) > 0 && s.ice.Secret != "" {
		user, pass := TURNCredential(s.ice.Secret, uid, time.Now().Add(s.ice.TTL))
		out.Servers = append(out.Servers, &authv1.IceServer{Urls: turn, Username: user, Credential: pass})
	}
	if len(out.Servers) == 0 {
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("no call servers configured"))
	}
	return out, nil
}

// TURNCredential returns a TURN REST API username/password valid until exp.
func TURNCredential(secret, userID string, exp time.Time) (string, string) {
	user := fmt.Sprintf("%d:%s", exp.Unix(), userID)
	mac := hmac.New(sha1.New, []byte(secret))
	mac.Write([]byte(user))
	return user, base64.StdEncoding.EncodeToString(mac.Sum(nil))
}
