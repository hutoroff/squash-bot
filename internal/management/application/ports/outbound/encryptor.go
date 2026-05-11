package outbound

// CredentialEncryptor encrypts and decrypts venue credential passwords.
type CredentialEncryptor interface {
	Encrypt(plaintext string) (string, error)
	Decrypt(ciphertext string) (string, error)
}
