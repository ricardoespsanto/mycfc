package handlers

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

const focusedWorkoutReviewPurpose = "mycfc/focused-water-create/v1"
const focusedWorkoutReviewTTL = 15 * time.Minute

type focusedWorkoutPayload struct {
	Proposal focusedWaterProposal `json:"proposal"`
	Draft    url.Values           `json:"draft"`
}
type focusedWorkoutSigner struct{ key [32]byte }

var focusedWorkoutReviews = func() focusedWorkoutSigner {
	var s focusedWorkoutSigner
	if _, err := rand.Read(s.key[:]); err != nil {
		panic("workout signing key unavailable")
	}
	return s
}()

func (s focusedWorkoutSigner) sign(payload focusedWorkoutPayload, now time.Time) (string, string, error) {
	if err := validateFocusedPayload(payload); err != nil {
		return "", "", err
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", "", err
	}
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
	mac.Write([]byte(focusedWorkoutReviewPurpose + "." + encoded))
	token := "fwr1." + encoded + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	// Reserve actual native encoded transport, including the compatibility CSRF
	// field. JSON length alone is not the URL-encoded confirmation body size.
	transport := url.Values{"intent": {"confirm"}, "proposal": {string(raw)}, "review_token": {token}, "gorilla.csrf.Token": {strings.Repeat("x", 256)}}
	editor := url.Values{}
	for k, v := range payload.Draft {
		editor[k] = append([]string(nil), v...)
	}
	c := payload.Proposal.Context
	editor.Set("command", "edit")
	editor.Set("prior_proposal", string(raw))
	editor.Set("prior_review_token", token)
	editor.Set("gorilla.csrf.Token", strings.Repeat("x", 256))
	for k, v := range map[string]string{"group_id": c.GroupID.String(), "plan_id": c.PlanID.String(), "session_id": "", "day": c.Day, "expected_plan_updated_at": c.PlanUpdatedAt.Format(time.RFC3339Nano), "expected_session_updated_at": "", "source_digest": c.Digest(), "database_incarnation": c.DatabaseIncarnation.String(), "state_epoch": strconv.FormatInt(c.StateEpoch, 10)} {
		editor.Set(k, v)
	}
	if len(transport.Encode()) > focusedWorkoutMaxRequestBytes || len(editor.Encode()) > focusedWorkoutMaxRequestBytes {
		return "", "", errFocusedWorkoutLimit
	}

	return string(raw), token, nil
}
func (s focusedWorkoutSigner) verify(raw, token string, actor, plan uuid.UUID, now time.Time) (focusedWorkoutPayload, focusedWorkoutReviewed, error) {
	var payload focusedWorkoutPayload
	var reviewed focusedWorkoutReviewed
	if len(raw) > focusedWorkoutMaxRequestBytes || len(token) > 512 || now.Unix() < 0 {
		return payload, reviewed, errFocusedWorkoutInvalid
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != "fwr1" {
		return payload, reviewed, errFocusedWorkoutInvalid
	}
	bytesPayload, err := base64.RawURLEncoding.Strict().DecodeString(parts[1])
	if err != nil || len(bytesPayload) != 48 || base64.RawURLEncoding.EncodeToString(bytesPayload) != parts[1] {
		return payload, reviewed, errFocusedWorkoutInvalid
	}
	signature, err := base64.RawURLEncoding.Strict().DecodeString(parts[2])
	if err != nil || len(signature) != 32 || base64.RawURLEncoding.EncodeToString(signature) != parts[2] {
		return payload, reviewed, errFocusedWorkoutInvalid
	}
	mac := hmac.New(sha256.New, s.key[:])
	mac.Write([]byte(focusedWorkoutReviewPurpose + "." + parts[1]))
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return payload, reviewed, errFocusedWorkoutInvalid
	}
	issued, expiry := binary.BigEndian.Uint64(bytesPayload[:8]), binary.BigEndian.Uint64(bytesPayload[8:16])
	if issued > uint64(now.Unix()) || expiry <= uint64(now.Unix()) || expiry-issued != uint64(focusedWorkoutReviewTTL/time.Second) {
		return payload, reviewed, errFocusedWorkoutInvalid
	}
	sum := sha256.Sum256([]byte(raw))
	if !hmac.Equal(sum[:], bytesPayload[16:]) {
		return payload, reviewed, errFocusedWorkoutInvalid
	}
	if err = focusedStrictJSON([]byte(raw), &payload); err != nil {
		return payload, reviewed, errFocusedWorkoutInvalid
	}
	canonical, _ := json.Marshal(payload)
	if !bytes.Equal(canonical, []byte(raw)) {
		return payload, reviewed, errFocusedWorkoutInvalid
	}
	if payload.Proposal.Context.ActorID != actor || payload.Proposal.Context.PlanID != plan || validateFocusedPayload(payload) != nil {
		return focusedWorkoutPayload{}, reviewed, errFocusedWorkoutInvalid
	}
	reviewed = focusedWorkoutReviewed{Proposal: payload.Proposal, Token: token, verified: true, expiresAt: time.Unix(int64(expiry), 0)}
	return payload, reviewed, nil
}
func validateFocusedPayload(payload focusedWorkoutPayload) error {
	p := payload.Proposal
	c := p.Context
	if c.ActorID == uuid.Nil || c.PlanID == uuid.Nil || c.GroupID == uuid.Nil || c.SeasonID == uuid.Nil || c.DatabaseIncarnation == uuid.Nil || c.StateEpoch < 0 || c.SchemaContract != "schedule-p1" || !c.AuthoringEnabled || c.PlanUpdatedAt.IsZero() || !c.PlanUpdatedAt.Equal(c.PlanUpdatedAt.Truncate(time.Microsecond)) || !validWorkspaceDate(c.Day) {
		return errFocusedWorkoutInvalid
	}
	day, _ := time.Parse("2006-01-02", c.Day)
	if day.Before(c.WeekStart) || !day.Before(c.WeekStart.AddDate(0, 0, 7)) {
		return errFocusedWorkoutInvalid
	}
	draft, err := parseFocusedWorkoutDraft(payload.Draft)
	if err != nil {
		return err
	}
	expected, errs := normalizeFocusedWater(c, draft)
	if len(errs) > 0 {
		return errFocusedWorkoutInvalid
	}
	ids := map[uuid.UUID]bool{}
	for _, id := range []uuid.UUID{p.SessionID, p.Activity.ID} {
		if id == uuid.Nil || ids[id] {
			return errFocusedWorkoutInvalid
		}
		ids[id] = true
	}
	for _, phase := range p.Phases {
		if phase.ID == uuid.Nil || ids[phase.ID] {
			return errFocusedWorkoutInvalid
		}
		ids[phase.ID] = true
	}
	comparison := p
	comparison.SessionID = uuid.Nil
	comparison.Activity.ID = uuid.Nil
	comparison.Phases = append([]focusedWaterPhase(nil), p.Phases...)
	if comparison.Phases == nil {
		comparison.Phases = []focusedWaterPhase{}
	}
	for i := range comparison.Phases {
		comparison.Phases[i].ID = uuid.Nil
	}
	a, _ := json.Marshal(comparison)
	b, _ := json.Marshal(expected)
	if !bytes.Equal(a, b) {
		return errFocusedWorkoutInvalid
	}
	return nil
}
func focusedStrictJSON(raw []byte, dest any) error {
	if !utf8.Valid(raw) || len(raw) > focusedWorkoutMaxRequestBytes {
		return errFocusedWorkoutInvalid
	}
	tokens := json.NewDecoder(bytes.NewReader(raw))
	var walk func(int) error
	walk = func(depth int) error {
		if depth > 64 {
			return errFocusedWorkoutInvalid
		}
		token, err := tokens.Token()
		if err != nil {
			return err
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delimiter {
		case '{':
			seen := map[string]bool{}
			for tokens.More() {
				keyToken, e := tokens.Token()
				if e != nil {
					return e
				}
				key, ok := keyToken.(string)
				if !ok || seen[key] {
					return errFocusedWorkoutInvalid
				}
				seen[key] = true
				if e = walk(depth + 1); e != nil {
					return e
				}
			}
		case '[':
			for tokens.More() {
				if e := walk(depth + 1); e != nil {
					return e
				}
			}
		default:
			return errFocusedWorkoutInvalid
		}
		_, err = tokens.Token()
		return err
	}
	if err := walk(0); err != nil {
		return err
	}
	if _, err := tokens.Token(); err != io.EOF {
		return errFocusedWorkoutInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(dest) != nil {
		return errFocusedWorkoutInvalid
	}
	return nil
}
