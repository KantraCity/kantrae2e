package auth

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func TestTURNCredential(t *testing.T) {
	exp := time.Unix(1_900_000_000, 0)
	user, pass := TURNCredential("s3cret", "user-1", exp)
	if user != "1900000000:user-1" {
		t.Fatalf("user %q", user)
	}
	mac := hmac.New(sha1.New, []byte("s3cret"))
	mac.Write([]byte(user))
	if pass != base64.StdEncoding.EncodeToString(mac.Sum(nil)) {
		t.Fatal("password is not HMAC-SHA1(secret, user)")
	}
}

func TestICEFromEnv(t *testing.T) {
	if c := ICEFromEnv("", "x"); len(c.URLs) != 0 {
		t.Fatalf("no host: %+v", c)
	}
	c := ICEFromEnv("chat.example.com", "x")
	got := strings.Join(c.URLs, " ")
	want := "stun:chat.example.com:3478 turn:chat.example.com:3478?transport=udp turn:chat.example.com:3478?transport=tcp"
	if got != want {
		t.Fatalf("urls %q", got)
	}
	if c := ICEFromEnv("chat.example.com:5349", ""); len(c.URLs) != 1 || c.URLs[0] != "stun:chat.example.com:5349" {
		t.Fatalf("stun only: %+v", c)
	}
}
