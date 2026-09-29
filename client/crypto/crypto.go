// Package crypto holds the client's local envelope crypto, independent of
// MLS epochs: the history key (seed phrase -> Argon2id) and per-file media
// keys. MLS epoch secrets are never used here and vice versa.
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

var ErrBadPhrase = errors.New("invalid seed phrase")

// NewSeedPhrase returns a fresh 24-word BIP-39 mnemonic (256 bits).
func NewSeedPhrase() (string, error) {
	entropy, err := bip39.NewEntropy(256)
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

// HistoryKey derives the history backup key from the seed phrase. The salt
// binds the key to the account, so equal phrases of different users differ.
func HistoryKey(phrase, userID string) ([]byte, error) {
	phrase = NormalizePhrase(phrase)
	if !bip39.IsMnemonicValid(phrase) {
		return nil, ErrBadPhrase
	}
	salt := []byte("kantra/history-key/v1/" + userID)
	return argon2.IDKey([]byte(phrase), salt, argonTime, argonMemory, argonThreads, KeySize), nil
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
