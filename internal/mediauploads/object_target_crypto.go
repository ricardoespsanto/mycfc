package mediauploads

import (
	"bytes"
	"encoding/binary"
	"errors"
	"regexp"
)

const (
	ObjectTargetLocatorVersion = "object-key/v1"
	objectTargetAlgorithm      = "X25519-HKDF-SHA256-AES-256-GCM"
)

var (
	ErrInvalid    = errors.New("invalid protected media value")
	keyIdentifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$`)
	policyKey     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,119}$`)
)

// ObjectTargetEnvelope stores an encrypted exact object-key locator. It is
// retained for media upload provenance and cleanup only.
type ObjectTargetEnvelope struct {
	Version       string
	Algorithm     string
	KeyID         string
	Encapsulation []byte
	Nonce         []byte
	Ciphertext    []byte
}

// ObjectTargetDigest stores a keyed alias used to deduplicate protected media
// locators without exposing the underlying object key.
type ObjectTargetDigest struct {
	KeyID  string
	Digest []byte
}

func validObjectTargetSource(value string) bool {
	return value == "MEMBER_PROFILE_PHOTO" || value == "REPAIR_ATTACHMENT" || value == "EQUIPMENT_PHOTO"
}

func validObjectKey(value string) bool {
	return value != "" && len(value) <= 1024 && !bytes.ContainsRune([]byte(value), 0)
}

func encodeFields(fields ...string) []byte {
	var out bytes.Buffer
	for _, field := range fields {
		_ = binary.Write(&out, binary.BigEndian, uint32(len(field)))
		_, _ = out.WriteString(field)
	}
	return out.Bytes()
}

func decodeFields(payload []byte, count int) ([]string, bool) {
	fields := make([]string, 0, count)
	for len(payload) > 0 && len(fields) < count {
		if len(payload) < 4 {
			return nil, false
		}
		size := int(binary.BigEndian.Uint32(payload[:4]))
		payload = payload[4:]
		if size < 0 || size > len(payload) {
			return nil, false
		}
		fields = append(fields, string(payload[:size]))
		payload = payload[size:]
	}
	return fields, len(fields) == count && len(payload) == 0
}
