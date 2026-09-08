package privacyrequests

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"net/mail"
	"net/url"
	"strings"
)

// Delivery contains no case details, decisions, references or subject identity.
// The protected recipient survives account closure so communication is independent of login.
type Delivery struct {
	Recipient  string `json:"recipient"`
	ContactURL string `json:"contact_url"`
}

func deliveryCipher(key []byte) (cipher.AEAD, error) {
	if len(key) < 32 {
		return nil, ErrInvalid
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("mycfc/privacy-request-email/v1"))
	b, err := aes.NewCipher(mac.Sum(nil))
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(b)
}
func validDelivery(d Delivery) bool {
	a, err := mail.ParseAddress(d.Recipient)
	u, e := url.Parse(d.ContactURL)
	return err == nil && a.Address == d.Recipient && e == nil && u.Host != "" && (u.Scheme == "https" || u.Scheme == "http") && !strings.ContainsAny(d.ContactURL, "\r\n")
}
func SealDelivery(key []byte, d Delivery) ([]byte, error) {
	if !validDelivery(d) {
		return nil, ErrInvalid
	}
	a, err := deliveryCipher(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, a.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, err
	}
	p, _ := json.Marshal(d)
	return a.Seal(nonce, nonce, p, []byte("privacy-notification-v1")), nil
}
func OpenDelivery(key, payload []byte) (Delivery, error) {
	var d Delivery
	a, err := deliveryCipher(key)
	if err != nil || len(payload) < a.NonceSize() {
		return d, ErrInvalid
	}
	p, err := a.Open(nil, payload[:a.NonceSize()], payload[a.NonceSize():], []byte("privacy-notification-v1"))
	if err != nil || json.Unmarshal(p, &d) != nil || !validDelivery(d) {
		return Delivery{}, ErrInvalid
	}
	return d, nil
}
