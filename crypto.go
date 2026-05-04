package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"math"
)

// Cipher wraps AES-256-GCM for at-rest encryption of entry fields.
// Nil receivers are not allowed; check for nil at call sites instead.
type Cipher struct {
	aead cipher.AEAD
}

// NewCipher decodes a base64 32-byte key and returns an AES-256-GCM AEAD.
func NewCipher(b64Key string) (*Cipher, error) {
	raw, err := base64.StdEncoding.DecodeString(b64Key)
	if err != nil {
		return nil, fmt.Errorf("decode key: %w", err)
	}
	if len(raw) != 32 {
		return nil, fmt.Errorf("encryption key must be 32 bytes (got %d)", len(raw))
	}
	block, err := aes.NewCipher(raw)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Cipher{aead: aead}, nil
}

// Encrypt produces nonce || ciphertext || authTag. The aad is bound into the
// AEAD tag so swapping ciphertexts between rows fails to authenticate.
func (c *Cipher) Encrypt(plaintext, aad []byte) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(nonce)+len(plaintext)+c.aead.Overhead())
	out = append(out, nonce...)
	out = c.aead.Seal(out, nonce, plaintext, aad)
	return out, nil
}

// Decrypt parses nonce || ciphertext || authTag and verifies aad.
func (c *Cipher) Decrypt(blob, aad []byte) ([]byte, error) {
	ns := c.aead.NonceSize()
	if len(blob) < ns+c.aead.Overhead() {
		return nil, fmt.Errorf("ciphertext too short")
	}
	nonce, ct := blob[:ns], blob[ns:]
	return c.aead.Open(nil, nonce, ct, aad)
}

// encodeGeo packs lat/lon as 16 bytes: big-endian Float64bits(lat) || Float64bits(lon).
// Matches the layout EntryHash v2 already commits to.
func encodeGeo(lat, lon float64) []byte {
	var buf [16]byte
	binary.BigEndian.PutUint64(buf[0:8], math.Float64bits(lat))
	binary.BigEndian.PutUint64(buf[8:16], math.Float64bits(lon))
	return buf[:]
}

func decodeGeo(b []byte) (lat, lon float64, err error) {
	if len(b) != 16 {
		return 0, 0, fmt.Errorf("geo blob: want 16 bytes, got %d", len(b))
	}
	lat = math.Float64frombits(binary.BigEndian.Uint64(b[0:8]))
	lon = math.Float64frombits(binary.BigEndian.Uint64(b[8:16]))
	return lat, lon, nil
}
