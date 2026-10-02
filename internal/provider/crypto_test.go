package provider

import (
	"bytes"
	"testing"
)

func testMasterKey(t *testing.T) []byte {
	t.Helper()
	key := bytes.Repeat([]byte{0x42}, 32)
	return key
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key := testMasterKey(t)
	plaintext := "sk-abc123def456ghi789"
	enc, err := Encrypt(key, plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if bytes.Contains(enc, []byte(plaintext)) {
		t.Fatal("ciphertext contains plaintext")
	}
	got, err := Decrypt(key, enc)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if got != plaintext {
		t.Errorf("Decrypt = %q, want %q", got, plaintext)
	}
}

func TestEncryptRandomNonce(t *testing.T) {
	key := testMasterKey(t)
	a, err := Encrypt(key, "same-plaintext")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	b, err := Encrypt(key, "same-plaintext")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if bytes.Equal(a, b) {
		t.Error("two encryptions of the same plaintext produced identical output")
	}
}

func TestDecryptErrors(t *testing.T) {
	key := testMasterKey(t)
	enc, err := Encrypt(key, "sk-secret")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	if _, err := Decrypt(key, []byte("short")); err == nil {
		t.Error("Decrypt short blob: expected error")
	}

	tampered := append([]byte(nil), enc...)
	tampered[len(tampered)-1] ^= 0xff
	if _, err := Decrypt(key, tampered); err == nil {
		t.Error("Decrypt tampered ciphertext: expected error")
	}

	otherKey := bytes.Repeat([]byte{0x24}, 32)
	if _, err := Decrypt(otherKey, enc); err == nil {
		t.Error("Decrypt with wrong key: expected error")
	}

	if _, err := Encrypt([]byte("too-short"), "x"); err == nil {
		t.Error("Encrypt with short master key: expected error")
	}
}

func TestMaskKey(t *testing.T) {
	cases := []struct {
		key  string
		want string
	}{
		{"sk-abcdef1234", "sk-****1234"},
		{"abcdefgh", "abc****efgh"},
		{"abcdefg", "****"},
		{"abc", "****"},
		{"", "****"},
	}
	for _, tc := range cases {
		if got := MaskKey(tc.key); got != tc.want {
			t.Errorf("MaskKey(%q) = %q, want %q", tc.key, got, tc.want)
		}
	}
}
