package handlers

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Shared process-local, purpose-bound envelope for the same native task flow.
// Semantic parsers remain responsible for their typed normalized payload.
func (s focusedWorkoutSigner) signNative(raw []byte, draft url.Values, c FocusedWorkoutContext, purpose string, now time.Time) (string, string, error) {
	fields := map[string]string{"group_id": c.GroupID.String(), "plan_id": c.PlanID.String(), "session_id": "", "day": c.Day, "expected_plan_updated_at": c.PlanUpdatedAt.Format(time.RFC3339Nano), "expected_session_updated_at": "", "source_digest": c.Digest(), "database_incarnation": c.DatabaseIncarnation.String(), "state_epoch": strconv.FormatInt(c.StateEpoch, 10)}
	return s.signNativeFields(raw, draft, fields, purpose, now)
}
func (s focusedWorkoutSigner) signNativeFields(raw []byte, draft url.Values, fields map[string]string, purpose string, now time.Time) (string, string, error) {
	if len(raw) > focusedWorkoutMaxRequestBytes || now.Unix() < 0 {
		return "", "", errFocusedWorkoutLimit
	}
	sum := sha256.Sum256(raw)
	b := make([]byte, 48)
	binary.BigEndian.PutUint64(b[:8], uint64(now.Unix()))
	binary.BigEndian.PutUint64(b[8:16], uint64(now.Add(focusedWorkoutReviewTTL).Unix()))
	copy(b[16:], sum[:])
	encoded := base64.RawURLEncoding.EncodeToString(b)
	mac := hmac.New(sha256.New, s.key[:])
	mac.Write([]byte(purpose + "." + encoded))
	token := "fwr1." + encoded + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	confirm := url.Values{"intent": {"confirm"}, "proposal": {string(raw)}, "review_token": {token}, "gorilla.csrf.Token": {strings.Repeat("x", 256)}}
	editor := cloneFocusedValues(draft)
	editor.Set("command", "edit")
	editor.Set("prior_proposal", string(raw))
	editor.Set("prior_review_token", token)
	editor.Set("gorilla.csrf.Token", strings.Repeat("x", 256))
	for k, v := range fields {
		editor.Set(k, v)
	}
	if len(confirm.Encode()) > focusedWorkoutMaxRequestBytes || len(editor.Encode()) > focusedWorkoutMaxRequestBytes {
		return "", "", errFocusedWorkoutLimit
	}
	return string(raw), token, nil
}
func (s focusedWorkoutSigner) verifyNative(raw, token, purpose string, now time.Time) (time.Time, error) {
	if len(raw) > focusedWorkoutMaxRequestBytes || len(token) > 512 || now.Unix() < 0 {
		return time.Time{}, errFocusedWorkoutInvalid
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != "fwr1" {
		return time.Time{}, errFocusedWorkoutInvalid
	}
	b, err := base64.RawURLEncoding.Strict().DecodeString(parts[1])
	if err != nil || len(b) != 48 || base64.RawURLEncoding.EncodeToString(b) != parts[1] {
		return time.Time{}, errFocusedWorkoutInvalid
	}
	signature, err := base64.RawURLEncoding.Strict().DecodeString(parts[2])
	if err != nil || len(signature) != 32 || base64.RawURLEncoding.EncodeToString(signature) != parts[2] {
		return time.Time{}, errFocusedWorkoutInvalid
	}
	mac := hmac.New(sha256.New, s.key[:])
	mac.Write([]byte(purpose + "." + parts[1]))
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return time.Time{}, errFocusedWorkoutInvalid
	}
	issued, expiry := binary.BigEndian.Uint64(b[:8]), binary.BigEndian.Uint64(b[8:16])
	if issued > uint64(now.Unix()) || expiry <= uint64(now.Unix()) || expiry-issued != uint64(focusedWorkoutReviewTTL/time.Second) {
		return time.Time{}, errFocusedWorkoutInvalid
	}
	sum := sha256.Sum256([]byte(raw))
	if !hmac.Equal(sum[:], b[16:]) {
		return time.Time{}, errFocusedWorkoutInvalid
	}
	return time.Unix(int64(expiry), 0), nil
}
