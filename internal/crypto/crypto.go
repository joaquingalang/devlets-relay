// Package crypto implements the relay's end-to-end encryption (Part A AR1-AR3).
// The server never sees the key; every binary value is base64url without padding.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
)

const (
	keyLen   = 32
	nonceLen = 12
	readInfo = "devlets-relay-read-v1"
	codeInfo = "devlets-relay-code-v1"
	Version  = 1
)

// Envelope is the wire form of one encrypted message.
type Envelope struct {
	V          int    `json:"v"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

// B64 encodes as base64url without padding.
func B64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// UnB64 decodes base64url without padding.
func UnB64(s string) ([]byte, error) { return base64.RawURLEncoding.DecodeString(s) }

// NewKey returns 32 random bytes.
func NewKey() ([]byte, error) {
	k := make([]byte, keyLen)
	if _, err := rand.Read(k); err != nil {
		return nil, err
	}
	return k, nil
}

// ReadToken derives the read token from the key.
func ReadToken(key []byte) string {
	out, err := hkdf.Key(sha256.New, key, nil, readInfo, 32)
	if err != nil {
		return "" // unreachable for a 32-byte length
	}
	return B64(out)
}

// ConfirmCode derives the 6-digit code both ends show while pairing (RH2).
func ConfirmCode(key []byte) string {
	out, err := hkdf.Key(sha256.New, key, nil, codeInfo, 4)
	if err != nil {
		return "" // unreachable for a 4-byte length
	}
	return fmt.Sprintf("%06d", binary.BigEndian.Uint32(out)%1_000_000)
}

// TokenHash is base64url(SHA-256(token string)).
func TokenHash(token string) string {
	h := sha256.Sum256([]byte(token))
	return B64(h[:])
}

func aad(boxID string, v int) []byte { return []byte(boxID + strconv.Itoa(v)) }

func newGCM(key []byte) (cipher.AEAD, error) {
	if len(key) != keyLen {
		return nil, errors.New("crypto: key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// Seal encrypts with a fresh random nonce.
func Seal(key []byte, boxID string, v int, plaintext []byte) (Envelope, error) {
	g, err := newGCM(key)
	if err != nil {
		return Envelope{}, err
	}
	nonce := make([]byte, nonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return Envelope{}, err
	}
	return Envelope{V: v, Nonce: B64(nonce), Ciphertext: B64(g.Seal(nil, nonce, plaintext, aad(boxID, v)))}, nil
}

// SealWithNonce is deterministic; for tests only. It panics on a bad key or nonce.
func SealWithNonce(key, nonce []byte, boxID string, v int, plaintext []byte) Envelope {
	g, err := newGCM(key)
	if err != nil || len(nonce) != nonceLen {
		panic("crypto: SealWithNonce needs a 32-byte key and 12-byte nonce")
	}
	return Envelope{V: v, Nonce: B64(nonce), Ciphertext: B64(g.Seal(nil, nonce, plaintext, aad(boxID, v)))}
}

// Open decrypts and authenticates an envelope.
func Open(key []byte, boxID string, env Envelope) ([]byte, error) {
	if env.V != Version {
		return nil, errors.New("crypto: unsupported envelope version")
	}
	g, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonce, err := UnB64(env.Nonce)
	if err != nil || len(nonce) != nonceLen {
		return nil, errors.New("crypto: bad nonce")
	}
	ct, err := UnB64(env.Ciphertext)
	if err != nil {
		return nil, errors.New("crypto: bad ciphertext encoding")
	}
	pt, err := g.Open(nil, nonce, ct, aad(boxID, env.V))
	if err != nil {
		return nil, errors.New("crypto: decryption failed")
	}
	return pt, nil
}
