package service

import (
	cryptoadapter "github.com/hutoroff/squash-bot/internal/management/adapters/outbound/crypto"
	"github.com/hutoroff/squash-bot/internal/management/application/ports/outbound"
)

// NewEncryptor forwards to the crypto adapter. Kept as a shim until Phase 3.
func NewEncryptor(hexKey string) (outbound.CredentialEncryptor, error) {
	return cryptoadapter.NewEncryptor(hexKey)
}
