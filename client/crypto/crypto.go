// Package crypto holds the client's local envelope crypto, independent of
// MLS epochs: the history key (random, shared between the account's devices
// and optionally wrapped with a recovery phrase) and per-file media keys.
// MLS epoch secrets are never used here and vice versa.
package crypto

import (
	"crypto/rand"
	"errors"
	"strings"

	"github.com/tyler-smith/go-bip39"
	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/chacha20poly1305"
)

const KeySize = chacha20poly1305.KeySize

var ErrBadPhrase = errors.New("invalid recovery phrase")

// NewRecoveryPhrase returns a fresh 12-word BIP-39 mnemonic (128 bits).
func NewRecoveryPhrase() (string, error) {
	entropy, err := bip39.NewEntropy(128)
	if err != nil {
		return "", err
	}
	return bip39.NewMnemonic(entropy)
}

// NormalizePhrase lowercases and collapses whitespace.
func NormalizePhrase(p string) string {
	return strings.Join(strings.Fields(strings.ToLower(p)), " ")
}

// Argon2id parameters (RFC 9106 "second recommended option").
const (
	argonTime    = 3
	argonMemory  = 64 * 1024 // KiB
	argonThreads = 4
)

// RecoveryKeys derives, from the recovery phrase, a key that wraps the
// history key and an independent verifier that proves knowledge of the
// phrase to the server. The salt binds both to the account.
func RecoveryKeys(phrase, userID string) (wrapKey, verifier []byte, err error) {
	phrase = NormalizePhrase(phrase)
	if !bip39.IsMnemonicValid(phrase) {
		return nil, nil, ErrBadPhrase
	}
	salt := []byte("kantra/recovery/v1/" + userID)
	k := argon2.IDKey([]byte(phrase), salt, argonTime, argonMemory, argonThreads, 2*KeySize)
	return k[:KeySize], k[KeySize:], nil
}

// NewKey returns a random symmetric key (media keys).
func NewKey() []byte {
	k := make([]byte, KeySize)
	if _, err := rand.Read(k); err != nil {
		panic(err)
	}
	return k
}

// Seal encrypts with XChaCha20-Poly1305; output is nonce || ciphertext.
func Seal(key, plaintext, ad []byte) ([]byte, error) {
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize(), aead.NonceSize()+len(plaintext)+aead.Overhead())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return aead.Seal(nonce, nonce, plaintext, ad), nil
}

// Open reverses Seal.
func Open(key, sealed, ad []byte) ([]byte, error) {
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, err
	}
	if len(sealed) < aead.NonceSize()+aead.Overhead() {
		return nil, errors.New("ciphertext too short")
	}
	return aead.Open(nil, sealed[:aead.NonceSize()], sealed[aead.NonceSize():], ad)
}
