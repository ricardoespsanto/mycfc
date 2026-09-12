package guardianauthority

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/mail"
	"net/url"
	"strings"
	"time"
)

var ErrInvalidDelivery = errors.New("invalid guardian delivery")

// RenewalDelivery contains only the destination, the stable dashboard route,
// and the authority expiry needed by the reminder. It deliberately excludes
// names, relationship references, evidence, and decision details.
type RenewalDelivery struct {
	Recipient    string    `json:"recipient"`
	DashboardURL string    `json:"dashboard_url"`
	ExpiresAt    time.Time `json:"expires_at"`
}

func renewalDeliveryCipher(key []byte) (cipher.AEAD, error) {
	if len(key) < 32 {
		return nil, ErrInvalidDelivery
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte("mycfc/guardian-renewal-email/v1"))
	block, err := aes.NewCipher(mac.Sum(nil))
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func validRenewalDelivery(value RenewalDelivery) bool {
	address, addressErr := mail.ParseAddress(value.Recipient)
	link, linkErr := url.Parse(value.DashboardURL)
	return addressErr == nil && address.Address == value.Recipient &&
		linkErr == nil && link.Host != "" && (link.Scheme == "https" || link.Scheme == "http") &&
		!strings.ContainsAny(value.DashboardURL, "\r\n") && !value.ExpiresAt.IsZero()
}

func SealRenewalDelivery(key []byte, value RenewalDelivery) ([]byte, error) {
	if !validRenewalDelivery(value) {
		return nil, ErrInvalidDelivery
	}
	aead, err := renewalDeliveryCipher(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, err
	}
	plain, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return aead.Seal(nonce, nonce, plain, []byte("guardian-renewal-notification-v1")), nil
}

func OpenRenewalDelivery(key, payload []byte) (RenewalDelivery, error) {
	aead, err := renewalDeliveryCipher(key)
	if err != nil || len(payload) < aead.NonceSize() {
		return RenewalDelivery{}, ErrInvalidDelivery
	}
	plain, err := aead.Open(nil, payload[:aead.NonceSize()], payload[aead.NonceSize():], []byte("guardian-renewal-notification-v1"))
	if err != nil {
		return RenewalDelivery{}, ErrInvalidDelivery
	}
	var value RenewalDelivery
	if json.Unmarshal(plain, &value) != nil || !validRenewalDelivery(value) {
		return RenewalDelivery{}, ErrInvalidDelivery
	}
	return value, nil
}

// HandoffDelivery contains only the current recipient, a stable task or
// verification URL, and the date/time which makes the message relevant. It is
// independently encrypted from renewal mail so the two payload types cannot be
// substituted even when they share the same application key.
type HandoffDelivery struct {
	Recipient   string    `json:"recipient"`
	ActionURL   string    `json:"action_url"`
	EffectiveAt time.Time `json:"effective_at"`
}

func handoffDeliveryCipher(key []byte) (cipher.AEAD, error) {
	if len(key) < 32 {
		return nil, ErrInvalidDelivery
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte("mycfc/guardian-age-handoff-email/v1"))
	block, err := aes.NewCipher(mac.Sum(nil))
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func validHandoffDelivery(value HandoffDelivery) bool {
	address, addressErr := mail.ParseAddress(value.Recipient)
	link, linkErr := url.Parse(value.ActionURL)
	return addressErr == nil && address.Address == value.Recipient &&
		linkErr == nil && link.Host != "" && (link.Scheme == "https" || link.Scheme == "http") &&
		!strings.ContainsAny(value.ActionURL, "\r\n") && !value.EffectiveAt.IsZero()
}

func SealHandoffDelivery(key []byte, value HandoffDelivery) ([]byte, error) {
	if !validHandoffDelivery(value) {
		return nil, ErrInvalidDelivery
	}
	aead, err := handoffDeliveryCipher(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, err
	}
	plain, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return aead.Seal(nonce, nonce, plain, []byte("guardian-age-handoff-notification-v1")), nil
}

func OpenHandoffDelivery(key, payload []byte) (HandoffDelivery, error) {
	aead, err := handoffDeliveryCipher(key)
	if err != nil || len(payload) < aead.NonceSize() {
		return HandoffDelivery{}, ErrInvalidDelivery
	}
	plain, err := aead.Open(nil, payload[:aead.NonceSize()], payload[aead.NonceSize():], []byte("guardian-age-handoff-notification-v1"))
	if err != nil {
		return HandoffDelivery{}, ErrInvalidDelivery
	}
	var value HandoffDelivery
	if json.Unmarshal(plain, &value) != nil || !validHandoffDelivery(value) {
		return HandoffDelivery{}, ErrInvalidDelivery
	}
	return value, nil
}
