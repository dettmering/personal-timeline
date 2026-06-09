package main

import (
	"bytes"
	"encoding/base64"
	"testing"
)

func testKey() string {
	return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, 32))
}

func newTestCipher(t *testing.T) *Cipher {
	t.Helper()
	c, err := NewCipher(testKey())
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	return c
}

func TestNewCipherKeyValidation(t *testing.T) {
	if _, err := NewCipher("not valid base64!!!"); err == nil {
		t.Fatal("expected error on invalid base64 key")
	}
	short := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x1}, 16))
	if _, err := NewCipher(short); err == nil {
		t.Fatal("expected error on 16-byte key")
	}
	if _, err := NewCipher(testKey()); err != nil {
		t.Fatalf("valid 32-byte key rejected: %v", err)
	}
}

func TestEncryptDecryptRoundtrip(t *testing.T) {
	c := newTestCipher(t)
	plaintext := []byte("secret journal entry #private")
	aad := []byte("entry-hash-bytes")

	blob, err := c.Encrypt(plaintext, aad)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if bytes.Contains(blob, plaintext) {
		t.Fatal("ciphertext leaks plaintext")
	}
	got, err := c.Decrypt(blob, aad)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("roundtrip mismatch: %q != %q", got, plaintext)
	}
}

func TestEncryptNonceUniqueness(t *testing.T) {
	c := newTestCipher(t)
	pt, aad := []byte("same"), []byte("aad")
	a, _ := c.Encrypt(pt, aad)
	b, _ := c.Encrypt(pt, aad)
	if bytes.Equal(a, b) {
		t.Fatal("two encryptions of identical plaintext produced identical blobs (nonce reuse)")
	}
}

func TestDecryptWrongAADFails(t *testing.T) {
	c := newTestCipher(t)
	blob, _ := c.Encrypt([]byte("data"), []byte("hash-A"))
	if _, err := c.Decrypt(blob, []byte("hash-B")); err == nil {
		t.Fatal("decrypt with wrong AAD must fail (prevents cross-row ciphertext swap)")
	}
}

func TestDecryptWrongKeyFails(t *testing.T) {
	c := newTestCipher(t)
	blob, _ := c.Encrypt([]byte("data"), []byte("aad"))

	otherKey := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x99}, 32))
	other, err := NewCipher(otherKey)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	if _, err := other.Decrypt(blob, []byte("aad")); err == nil {
		t.Fatal("decrypt with wrong key must fail the auth check")
	}
}

func TestDecryptTamperedFails(t *testing.T) {
	c := newTestCipher(t)
	blob, _ := c.Encrypt([]byte("data"), []byte("aad"))
	blob[len(blob)-1] ^= 0xFF // flip a bit in the auth tag
	if _, err := c.Decrypt(blob, []byte("aad")); err == nil {
		t.Fatal("decrypt of tampered ciphertext must fail")
	}
}

func TestDecryptTooShort(t *testing.T) {
	c := newTestCipher(t)
	if _, err := c.Decrypt([]byte{0x1, 0x2}, nil); err == nil {
		t.Fatal("decrypt of too-short blob must fail")
	}
}

func TestEncodeDecodeGeoRoundtrip(t *testing.T) {
	lat, lon := 52.5200, 13.4050
	blob := encodeGeo(lat, lon)
	if len(blob) != 16 {
		t.Fatalf("want 16-byte geo blob, got %d", len(blob))
	}
	gotLat, gotLon, err := decodeGeo(blob)
	if err != nil {
		t.Fatalf("decodeGeo: %v", err)
	}
	if gotLat != lat || gotLon != lon {
		t.Fatalf("geo roundtrip mismatch: (%v,%v) != (%v,%v)", gotLat, gotLon, lat, lon)
	}
}

func TestDecodeGeoWrongLength(t *testing.T) {
	if _, _, err := decodeGeo([]byte{0x1, 0x2, 0x3}); err == nil {
		t.Fatal("decodeGeo must reject non-16-byte input")
	}
}
