package activity

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
)

// AESGCMVault encrypts each provider credential with a dedicated application
// key. The provider and member identity are authenticated data, so ciphertext
// cannot be replayed for a different connection.
type AESGCMVault struct {
	key   []byte
	keyID string
}

func NewAESGCMVault(key []byte, keyID string) (*AESGCMVault, error) {
	if len(key) != 32 {
		return nil, errors.New("activity credential key must be 32 bytes")
	}
	if keyID == "" {
		return nil, errors.New("activity credential key id must not be empty")
	}
	copied := make([]byte, len(key))
	copy(copied, key)
	return &AESGCMVault{key: copied, keyID: keyID}, nil
}

func (v *AESGCMVault) Seal(_ context.Context, provider Provider, userID string, credentials Secret) (SealedCredentials, error) {
	if v == nil {
		return SealedCredentials{}, errors.New("activity credential vault is not configured")
	}
	block, _ := aes.NewCipher(v.key) // NewAESGCMVault has already checked the AES-256 key length.
	aead, _ := cipher.NewGCM(block)  // AES always supports GCM's standard nonce and tag sizes.
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return SealedCredentials{}, fmt.Errorf("create activity credential nonce: %w", err)
	}
	ciphertext := aead.Seal(nonce, nonce, credentials.Bytes(), activityCredentialAAD(provider, userID, v.keyID))
	return SealedCredentials{Ciphertext: ciphertext, KeyID: v.keyID}, nil
}

func (v *AESGCMVault) Open(_ context.Context, provider Provider, userID string, credentials SealedCredentials) (Secret, error) {
	if v == nil || credentials.KeyID != v.keyID {
		return Secret{}, errors.New("activity credential key is unavailable")
	}
	block, _ := aes.NewCipher(v.key) // NewAESGCMVault has already checked the AES-256 key length.
	aead, _ := cipher.NewGCM(block)  // AES always supports GCM's standard nonce and tag sizes.
	if len(credentials.Ciphertext) < aead.NonceSize() {
		return Secret{}, errors.New("activity credential ciphertext is invalid")
	}
	nonce, ciphertext := credentials.Ciphertext[:aead.NonceSize()], credentials.Ciphertext[aead.NonceSize():]
	plaintext, err := aead.Open(nil, nonce, ciphertext, activityCredentialAAD(provider, userID, v.keyID))
	if err != nil {
		return Secret{}, errors.New("activity credential ciphertext is invalid")
	}
	return NewSecret(plaintext), nil
}

func activityCredentialAAD(provider Provider, userID, keyID string) []byte {
	return []byte("mycfc/activity-credential/v1|" + string(provider) + "|" + userID + "|" + keyID)
}
