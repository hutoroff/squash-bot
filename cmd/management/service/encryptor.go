package service

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
)

// Encryptor performs AES-256-GCM encryption and decryption.
// The 32-byte key must never be stored in the database.
type Encryptor struct {
	key []byte
}

// NewEncryptor creates an Encryptor from a 64-character hex-encoded key (= 32 bytes).
// Returns an error if the key is empty or has the wrong length.
func NewEncryptor(hexKey string) (*Encryptor, error) {
	if hexKey == "" {
		return nil, errors.New("CREDENTIALS_ENCRYPTION_KEY is not set")
	}
	key, err := hex.DecodeString(hexKey)
	if err != nil {
		return nil, fmt.Errorf("CREDENTIALS_ENCRYPTION_KEY is not valid hex: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("CREDENTIALS_ENCRYPTION_KEY must be 32 bytes (64 hex chars), got %d bytes", len(key))
	}
	return &Encryptor{key: key}, nil
}

// Encrypt encrypts plaintext with AES-256-GCM.
// The output is base64(nonce || ciphertext || tag).
func (e *Encryptor) Encrypt(plaintext string) (string, error) {
	block, err := aes.NewCipher(e.key)
	if err != nil {
		return "", fmt.Errorf("create AES cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("create GCM: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}
	sealed := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt decrypts a value produced by Encrypt.
func (e *Encryptor) Decrypt(ciphertext string) (string, error) {
	data, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", fmt.Errorf("base64 decode: %w", err)
	}
	block, err := aes.NewCipher(e.key)
	if err != nil {
		return "", fmt.Errorf("create AES cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("create GCM: %w", err)
	}
	ns := gcm.NonceSize()
	if len(data) < ns {
		return "", errors.New("ciphertext too short")
	}
	nonce, sealed := data[:ns], data[ns:]
	plain, err := gcm.Open(nil, nonce, sealed, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt: %w", err)
	}
	return string(plain), nil
}
