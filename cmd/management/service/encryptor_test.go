package service

import (
	"strings"
	"testing"
)

const testHexKey = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"

// ── NewEncryptor ──────────────────────────────────────────────────────────────

func TestNewEncryptor_ValidKey(t *testing.T) {
	enc, err := NewEncryptor(testHexKey)
	if err != nil {
		t.Fatalf("valid 64-char hex key: got error %v", err)
	}
	if enc == nil {
		t.Fatal("expected non-nil Encryptor")
	}
}

func TestNewEncryptor_EmptyKey(t *testing.T) {
	_, err := NewEncryptor("")
	if err == nil {
		t.Error("empty key: want error, got nil")
	}
}

func TestNewEncryptor_InvalidHex(t *testing.T) {
	_, err := NewEncryptor(strings.Repeat("zz", 32)) // 64 chars but not valid hex
	if err == nil {
		t.Error("invalid hex: want error, got nil")
	}
}

func TestNewEncryptor_TooShort(t *testing.T) {
	_, err := NewEncryptor("deadbeef") // valid hex but only 4 bytes
	if err == nil {
		t.Error("short key: want error, got nil")
	}
}

func TestNewEncryptor_TooLong(t *testing.T) {
	// 33 bytes = 66 hex chars
	_, err := NewEncryptor(testHexKey + "ff")
	if err == nil {
		t.Error("too-long key: want error, got nil")
	}
}

// ── Encrypt / Decrypt roundtrip ───────────────────────────────────────────────

func TestEncryptDecrypt_Roundtrip(t *testing.T) {
	enc, _ := NewEncryptor(testHexKey)

	plaintext := "hunter2"
	ct, err := enc.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	got, err := enc.Decrypt(ct)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if got != plaintext {
		t.Errorf("roundtrip: got %q, want %q", got, plaintext)
	}
}

func TestEncrypt_ProducesDistinctCiphertexts(t *testing.T) {
	// Each Encrypt call generates a fresh random nonce, so identical plaintexts
	// must not produce identical ciphertexts.
	enc, _ := NewEncryptor(testHexKey)
	ct1, _ := enc.Encrypt("same")
	ct2, _ := enc.Encrypt("same")
	if ct1 == ct2 {
		t.Error("two encryptions of the same plaintext produced identical ciphertexts")
	}
}

func TestDecrypt_TamperedCiphertext(t *testing.T) {
	enc, _ := NewEncryptor(testHexKey)
	ct, _ := enc.Encrypt("secret")
	// Flip the last byte of the base64 to corrupt the GCM tag.
	tampered := ct[:len(ct)-1] + string(ct[len(ct)-1]^1)
	_, err := enc.Decrypt(tampered)
	if err == nil {
		t.Error("tampered ciphertext: want error, got nil")
	}
}

func TestDecrypt_NotBase64(t *testing.T) {
	enc, _ := NewEncryptor(testHexKey)
	_, err := enc.Decrypt("not-valid-base64!!!")
	if err == nil {
		t.Error("invalid base64: want error, got nil")
	}
}

func TestDecrypt_TooShort(t *testing.T) {
	enc, _ := NewEncryptor(testHexKey)
	// Base64 of a 3-byte payload — shorter than GCM nonce size (12 bytes).
	_, err := enc.Decrypt("AAEC")
	if err == nil {
		t.Error("too-short ciphertext: want error, got nil")
	}
}

func TestEncryptDecrypt_EmptyPlaintext(t *testing.T) {
	enc, _ := NewEncryptor(testHexKey)
	ct, err := enc.Encrypt("")
	if err != nil {
		t.Fatalf("Encrypt empty: %v", err)
	}
	got, err := enc.Decrypt(ct)
	if err != nil {
		t.Fatalf("Decrypt empty: %v", err)
	}
	if got != "" {
		t.Errorf("empty roundtrip: got %q, want empty string", got)
	}
}

func TestDecrypt_WrongKey(t *testing.T) {
	enc1, _ := NewEncryptor(testHexKey)
	otherKey := "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	enc2, _ := NewEncryptor(otherKey)

	ct, _ := enc1.Encrypt("secret")
	_, err := enc2.Decrypt(ct)
	if err == nil {
		t.Error("wrong key: want decrypt error, got nil")
	}
}
