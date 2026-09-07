package activity

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"strings"
)

// AESGCMVault seals provider credentials with an application-managed key. The
// provider and MyCFC account identity are authenticated as associated data so
// ciphertext cannot be moved between providers or people.
type AESGCMVault struct {
	activeKeyID string
	keys        map[string]cipher.AEAD
	rand        io.Reader
}

func NewAESGCMVault(key []byte, keyID string) (*AESGCMVault, error) {
	return NewAESGCMKeyring(keyID, map[string][]byte{keyID: key})
}

// NewAESGCMKeyring seals with activeKeyID and keeps older keys available while
// stored credential envelopes are rotated.
func NewAESGCMKeyring(activeKeyID string, keys map[string][]byte) (*AESGCMVault, error) {
	activeKeyID = strings.TrimSpace(activeKeyID)
	if activeKeyID == "" || len(activeKeyID) > 120 {
		return nil, errors.New("activity credential key id must contain 1 to 120 characters")
	}
	aeads := make(map[string]cipher.AEAD, len(keys))
	for keyID, key := range keys {
		keyID = strings.TrimSpace(keyID)
		if keyID == "" || len(keyID) > 120 {
			return nil, errors.New("activity credential key id must contain 1 to 120 characters")
		}
		if len(key) != 32 {
			return nil, errors.New("activity credential key must contain exactly 32 bytes")
		}
		block, err := aes.NewCipher(key)
		if err != nil {
			return nil, fmt.Errorf("create activity credential cipher: %w", err)
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			return nil, fmt.Errorf("create activity credential AEAD: %w", err)
		}
		aeads[keyID] = aead
	}
	if _, ok := aeads[activeKeyID]; !ok {
		return nil, errors.New("active activity credential key is unavailable")
	}
	return &AESGCMVault{activeKeyID: activeKeyID, keys: aeads, rand: rand.Reader}, nil
}

func (v *AESGCMVault) Seal(_ context.Context, provider Provider, userID string, credentials Secret) (SealedCredentials, error) {
	if v == nil || v.keys[v.activeKeyID] == nil {
		return SealedCredentials{}, errors.New("activity credential vault is unavailable")
	}
	if _, err := ParseProvider(string(provider)); err != nil {
		return SealedCredentials{}, err
	}
	if strings.TrimSpace(userID) == "" {
		return SealedCredentials{}, errors.New("activity credential user id must not be empty")
	}
	plain := credentials.Bytes()
	defer clear(plain)
	if len(plain) == 0 {
		return SealedCredentials{}, errors.New("activity credentials must not be empty")
	}
	aead := v.keys[v.activeKeyID]
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(v.rand, nonce); err != nil {
		return SealedCredentials{}, fmt.Errorf("generate activity credential nonce: %w", err)
	}
	sealed := aead.Seal(nonce, nonce, plain, credentialAAD(provider, userID))
	return SealedCredentials{Ciphertext: sealed, KeyID: v.activeKeyID}, nil
}

func (v *AESGCMVault) Open(_ context.Context, provider Provider, userID string, credentials SealedCredentials) (Secret, error) {
	if v == nil || len(v.keys) == 0 {
		return Secret{}, errors.New("activity credential vault is unavailable")
	}
	if err := credentials.Validate(); err != nil {
		return Secret{}, err
	}
	aead := v.keys[credentials.KeyID]
	if aead == nil {
		return Secret{}, errors.New("activity credential key id is unavailable")
	}
	if len(credentials.Ciphertext) <= aead.NonceSize() {
		return Secret{}, errors.New("activity credential ciphertext is malformed")
	}
	nonce := credentials.Ciphertext[:aead.NonceSize()]
	ciphertext := credentials.Ciphertext[aead.NonceSize():]
	plain, err := aead.Open(nil, nonce, ciphertext, credentialAAD(provider, userID))
	if err != nil {
		return Secret{}, errors.New("open activity credentials")
	}
	secret := NewSecret(plain)
	clear(plain)
	return secret, nil
}

func credentialAAD(provider Provider, userID string) []byte {
	return []byte("mycfc:activity-credentials:v1:" + string(provider) + ":" + userID)
}
