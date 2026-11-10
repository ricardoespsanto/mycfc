package handlers

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func publicationReviewFixture(t *testing.T) (trainingPublicationReviewSigner, trainingPublicationReviewInput, time.Time) {
	t.Helper()
	signer, err := newTrainingPublicationReviewSigner()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 10, 14, 0, 0, 123456789, time.UTC)
	plan := uuid.New()
	input := trainingPublicationReviewInput{trainingPublicationReviewMetadata: trainingPublicationReviewMetadata{
		ActorID: uuid.New(), PlanID: plan, SourceUpdatedAt: now.Add(-time.Hour), LatestPublicationID: uuid.New(), LatestRevision: 2,
		ChangeSummary: "Plano revisto", Return: trainingPublicationReturnContext{GroupID: uuid.New(), WeekID: plan, SessionID: uuid.New(), Day: "2026-11-10"},
	}, Prescriptions: []StructuredPrescriptionInput{
		{SessionID: uuid.New(), MembershipID: uuid.New(), AthleteUserID: uuid.New(), Snapshot: []byte(`{"SchemaVersion":1,"Title":"Primeiro treino"}`)},
		{SessionID: uuid.New(), MembershipID: uuid.New(), AthleteUserID: uuid.New(), Snapshot: []byte(`{"SchemaVersion":1,"Title":"Segundo treino"}`)},
	}}
	return signer, input, now
}

func clonePublicationReviewInput(in trainingPublicationReviewInput) trainingPublicationReviewInput {
	in.Prescriptions = append([]StructuredPrescriptionInput(nil), in.Prescriptions...)
	return in
}

func TestTrainingPublicationReviewRoundTripOpaqueAndOrderIndependent(t *testing.T) {
	signer, input, now := publicationReviewFixture(t)
	input.ChangeSummary = "Resumo privado exclusivo"
	input.Prescriptions[0].Snapshot = []byte(`{"Title":"Nome privado exclusivo","GuardianID":"identidade privada"}`)
	token, err := signer.sign(input, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(token) > trainingPublicationReviewMaxTokenBytes {
		t.Fatalf("token size %d", len(token))
	}
	claims, err := signer.verify(token, input.trainingPublicationReviewMetadata, now)
	if err != nil {
		t.Fatal(err)
	}
	if !claims.expiresAt.Equal(time.Unix(now.Add(trainingPublicationReviewTTL).Unix(), 0)) {
		t.Fatal("wrong expiry")
	}
	if !claims.metadata.SourceUpdatedAt.Equal(time.UnixMicro(input.SourceUpdatedAt.UnixMicro())) {
		t.Fatal("source time was not normalized")
	}
	if err := claims.matches(input, now); err != nil {
		t.Fatal(err)
	}
	reordered := clonePublicationReviewInput(input)
	reordered.Prescriptions[0], reordered.Prescriptions[1] = reordered.Prescriptions[1], reordered.Prescriptions[0]
	second, err := signer.sign(reordered, now)
	if err != nil || second != token {
		t.Fatal("set ordering changed review", err)
	}
	if err := claims.matches(reordered, now); err != nil {
		t.Fatal(err)
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.Split(token, ".")[1])
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{input.ChangeSummary, "Nome privado exclusivo", "GuardianID", "identidade privada", input.ActorID.String(), input.PlanID.String(), input.Return.GroupID.String(), input.Return.Day} {
		if strings.Contains(token, secret) || bytes.Contains(payload, []byte(secret)) {
			t.Fatalf("review exposes private field")
		}
	}
	for _, id := range []uuid.UUID{input.ActorID, input.PlanID, input.Prescriptions[0].MembershipID, input.Prescriptions[0].AthleteUserID} {
		if bytes.Contains(payload, id[:]) {
			t.Fatal("token includes raw identifier")
		}
	}
	// Claims carry the opaque set digest: verification does not accept the
	// browser's omission of the list as evidence that the reviewed set was empty.
	omitted := input
	omitted.Prescriptions = nil
	if !errors.Is(claims.matches(omitted, now), errTrainingPublicationReviewChanged) {
		t.Fatal("omitted set accepted")
	}
}

func TestTrainingPublicationReviewMetadataIsBound(t *testing.T) {
	signer, input, now := publicationReviewFixture(t)
	token, err := signer.sign(input, now)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		change func(*trainingPublicationReviewMetadata)
	}{
		{"actor", func(m *trainingPublicationReviewMetadata) { m.ActorID = uuid.New() }},
		{"plan", func(m *trainingPublicationReviewMetadata) { m.PlanID = uuid.New(); m.Return.WeekID = m.PlanID }},
		{"source", func(m *trainingPublicationReviewMetadata) {
			m.SourceUpdatedAt = m.SourceUpdatedAt.Add(time.Microsecond)
		}},
		{"latest identity", func(m *trainingPublicationReviewMetadata) { m.LatestPublicationID = uuid.New() }},
		{"latest revision", func(m *trainingPublicationReviewMetadata) { m.LatestRevision++ }},
		{"latest none", func(m *trainingPublicationReviewMetadata) { m.LatestPublicationID = uuid.Nil; m.LatestRevision = 0 }},
		{"summary", func(m *trainingPublicationReviewMetadata) { m.ChangeSummary = "Outro resumo" }},
		{"group", func(m *trainingPublicationReviewMetadata) { m.Return.GroupID = uuid.New() }},
		{"session", func(m *trainingPublicationReviewMetadata) { m.Return.SessionID = uuid.New() }},
		{"day", func(m *trainingPublicationReviewMetadata) { m.Return.Day = "2026-11-11" }},
		{"collection", func(m *trainingPublicationReviewMetadata) { m.Return = trainingPublicationReturnContext{} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			changed := input.trainingPublicationReviewMetadata
			tc.change(&changed)
			if _, err := signer.verify(token, changed, now); !errors.Is(err, errTrainingPublicationReviewInvalid) {
				t.Fatal("changed metadata verified", err)
			}
			claims, err := signer.verify(token, input.trainingPublicationReviewMetadata, now)
			if err != nil {
				t.Fatal(err)
			}
			current := input
			current.trainingPublicationReviewMetadata = changed
			if !errors.Is(claims.matches(current, now), errTrainingPublicationReviewChanged) {
				t.Fatal("changed metadata matched")
			}
		})
	}
}

func TestTrainingPublicationReviewRejectsSameCountSubstitutionAndSnapshotChange(t *testing.T) {
	signer, input, now := publicationReviewFixture(t)
	token, err := signer.sign(input, now)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := signer.verify(token, input.trainingPublicationReviewMetadata, now)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		change func(*StructuredPrescriptionInput)
	}{
		{"session", func(p *StructuredPrescriptionInput) { p.SessionID = uuid.New() }},
		{"membership", func(p *StructuredPrescriptionInput) { p.MembershipID = uuid.New() }},
		{"subject", func(p *StructuredPrescriptionInput) { p.AthleteUserID = uuid.New() }},
		{"snapshot", func(p *StructuredPrescriptionInput) {
			p.Snapshot = []byte(`{"SchemaVersion":1,"Title":"Treino alterado"}`)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			changed := clonePublicationReviewInput(input)
			tc.change(&changed.Prescriptions[0])
			if len(changed.Prescriptions) != len(input.Prescriptions) {
				t.Fatal("fixture changed count")
			}
			if err := claims.matches(changed, now); !errors.Is(err, errTrainingPublicationReviewChanged) {
				t.Fatal("same-count substitution accepted", err)
			}
		})
	}
}

func TestTrainingPublicationReviewHashesRawJSONAndChecksDeclaredHash(t *testing.T) {
	signer, input, now := publicationReviewFixture(t)
	raw := []byte(`{"a":1,"b":2}`)
	digest := sha256.Sum256(raw)
	input.Prescriptions = input.Prescriptions[:1]
	input.Prescriptions[0].Snapshot = raw
	input.Prescriptions[0].SnapshotSHA256 = hex.EncodeToString(digest[:])
	token, err := signer.sign(input, now)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := signer.verify(token, input.trainingPublicationReviewMetadata, now)
	if err != nil {
		t.Fatal(err)
	}
	changed := clonePublicationReviewInput(input)
	// Equivalent JSONB content, different canonical raw bytes. The old declared
	// hash cannot conceal the difference or become the authoritative hash.
	changed.Prescriptions[0].Snapshot = []byte(`{ "b": 2, "a": 1 }`)
	if err := claims.matches(changed, now); !errors.Is(err, errTrainingPublicationReviewInvalid) {
		t.Fatal("caller hash trusted", err)
	}
	changed.Prescriptions[0].SnapshotSHA256 = ""
	if err := claims.matches(changed, now); !errors.Is(err, errTrainingPublicationReviewChanged) {
		t.Fatal("JSON was normalized before hashing", err)
	}
	for _, bad := range []string{"not a hash", strings.ToUpper(input.Prescriptions[0].SnapshotSHA256), strings.Repeat("0", 64)} {
		changed = clonePublicationReviewInput(input)
		changed.Prescriptions[0].SnapshotSHA256 = bad
		if _, err := signer.sign(changed, now); !errors.Is(err, errTrainingPublicationReviewInvalid) {
			t.Fatal("inconsistent declared hash accepted")
		}
	}
}

func TestTrainingPublicationReviewTimeNormalizationAndExistingSummarySemantics(t *testing.T) {
	signer, input, now := publicationReviewFixture(t)
	input.SourceUpdatedAt = time.Date(2026, 11, 10, 11, 30, 0, 123456999, time.FixedZone("offset", 3600))
	input.ChangeSummary = "  Duas\nlinhas\taceites  "
	token, err := signer.sign(input, now)
	if err != nil {
		t.Fatal(err)
	}
	expected := input.trainingPublicationReviewMetadata
	expected.SourceUpdatedAt = time.Date(2026, 11, 10, 10, 30, 0, 123456000, time.UTC)
	expected.ChangeSummary = strings.TrimSpace(expected.ChangeSummary)
	claims, err := signer.verify(token, expected, now)
	if err != nil {
		t.Fatal("same DB instant/summary changed binding", err)
	}
	input.trainingPublicationReviewMetadata = expected
	if err := claims.matches(input, now); err != nil {
		t.Fatal(err)
	}
	changed := input
	changed.SourceUpdatedAt = changed.SourceUpdatedAt.Add(time.Microsecond)
	if err := claims.matches(changed, now); !errors.Is(err, errTrainingPublicationReviewChanged) {
		t.Fatal("database microsecond change ignored")
	}
	for _, length := range []int{2, 500} {
		boundary := input
		boundary.ChangeSummary = strings.Repeat("ç", length)
		if _, err := signer.sign(boundary, now); err != nil {
			t.Fatalf("existing %d-rune boundary rejected: %v", length, err)
		}
	}
}

func TestTrainingPublicationReviewRejectsInvalidFields(t *testing.T) {
	signer, input, now := publicationReviewFixture(t)
	cases := []struct {
		name   string
		change func(*trainingPublicationReviewInput)
	}{
		{"nil actor", func(p *trainingPublicationReviewInput) { p.ActorID = uuid.Nil }},
		{"nil plan", func(p *trainingPublicationReviewInput) { p.PlanID = uuid.Nil }},
		{"zero time", func(p *trainingPublicationReviewInput) { p.SourceUpdatedAt = time.Time{} }},
		{"out of range time", func(p *trainingPublicationReviewInput) {
			p.SourceUpdatedAt = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
		}},
		{"negative revision", func(p *trainingPublicationReviewInput) { p.LatestRevision = -1 }},
		{"missing latest id", func(p *trainingPublicationReviewInput) { p.LatestPublicationID = uuid.Nil }},
		{"missing latest revision", func(p *trainingPublicationReviewInput) { p.LatestRevision = 0 }},
		{"short summary", func(p *trainingPublicationReviewInput) { p.ChangeSummary = " ç " }},
		{"long summary", func(p *trainingPublicationReviewInput) { p.ChangeSummary = strings.Repeat("ç", 501) }},
		{"bad utf8", func(p *trainingPublicationReviewInput) { p.ChangeSummary = "abc\xff" }},
		{"nul summary", func(p *trainingPublicationReviewInput) { p.ChangeSummary = "ab\x00cd" }},
		{"foreign week", func(p *trainingPublicationReviewInput) { p.Return.WeekID = uuid.New() }},
		{"orphan week", func(p *trainingPublicationReviewInput) { p.Return.GroupID = uuid.Nil }},
		{"orphan session", func(p *trainingPublicationReviewInput) {
			p.Return = trainingPublicationReturnContext{SessionID: uuid.New()}
		}},
		{"orphan day", func(p *trainingPublicationReviewInput) {
			p.Return = trainingPublicationReturnContext{Day: "2026-11-10"}
		}},
		{"invalid day", func(p *trainingPublicationReviewInput) { p.Return.Day = "2026-02-30" }},
		{"noncanonical day", func(p *trainingPublicationReviewInput) { p.Return.Day = "2026-1-01" }},
		{"URL day", func(p *trainingPublicationReviewInput) { p.Return.Day = "https://example.invalid" }},
		{"zero year", func(p *trainingPublicationReviewInput) { p.Return.Day = "0000-01-01" }},
		{"nil session", func(p *trainingPublicationReviewInput) { p.Prescriptions[0].SessionID = uuid.Nil }},
		{"nil membership", func(p *trainingPublicationReviewInput) { p.Prescriptions[0].MembershipID = uuid.Nil }},
		{"nil subject", func(p *trainingPublicationReviewInput) { p.Prescriptions[0].AthleteUserID = uuid.Nil }},
		{"empty snapshot", func(p *trainingPublicationReviewInput) { p.Prescriptions[0].Snapshot = nil }},
		{"invalid snapshot", func(p *trainingPublicationReviewInput) { p.Prescriptions[0].Snapshot = []byte(`{"a":`) }},
		{"array snapshot", func(p *trainingPublicationReviewInput) { p.Prescriptions[0].Snapshot = []byte(`[]`) }},
		{"null snapshot", func(p *trainingPublicationReviewInput) { p.Prescriptions[0].Snapshot = []byte(`null`) }},
		{"invalid snapshot utf8", func(p *trainingPublicationReviewInput) { p.Prescriptions[0].Snapshot = []byte("{\"a\":\"\xff\"}") }},
		{"duplicate tuple", func(p *trainingPublicationReviewInput) { p.Prescriptions[1] = p.Prescriptions[0] }},
		{"duplicate remapped tuple", func(p *trainingPublicationReviewInput) {
			p.Prescriptions[1] = p.Prescriptions[0]
			p.Prescriptions[1].AthleteUserID = uuid.New()
		}},
		{"membership remapped across sessions", func(p *trainingPublicationReviewInput) {
			p.Prescriptions[1].MembershipID = p.Prescriptions[0].MembershipID
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			changed := clonePublicationReviewInput(input)
			tc.change(&changed)
			if _, err := signer.sign(changed, now); !errors.Is(err, errTrainingPublicationReviewInvalid) {
				t.Fatal("invalid input accepted", err)
			}
		})
	}
}

func TestTrainingPublicationReviewExpiryRestartTamperAndUnverifiedClaims(t *testing.T) {
	signer, input, now := publicationReviewFixture(t)
	token, err := signer.sign(input, now)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := signer.verify(token, input.trainingPublicationReviewMetadata, now)
	if err != nil {
		t.Fatal(err)
	}
	expiry := time.Unix(now.Add(trainingPublicationReviewTTL).Unix(), 0)
	if _, err := signer.verify(token, input.trainingPublicationReviewMetadata, expiry.Add(-time.Nanosecond)); err != nil {
		t.Fatal("expired too soon", err)
	}
	for _, at := range []time.Time{expiry, expiry.Add(time.Second), now.Add(-time.Second), {}, time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)} {
		if _, err := signer.verify(token, input.trainingPublicationReviewMetadata, at); !errors.Is(err, errTrainingPublicationReviewInvalid) {
			t.Fatal("invalid verification time accepted", err)
		}
		if err := claims.matches(input, at); !errors.Is(err, errTrainingPublicationReviewInvalid) {
			t.Fatal("claims expiry not rechecked", err)
		}
	}
	restarted, err := newTrainingPublicationReviewSigner()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.verify(token, input.trainingPublicationReviewMetadata, now); !errors.Is(err, errTrainingPublicationReviewInvalid) {
		t.Fatal("restart retained signature")
	}
	parts := strings.Split(token, ".")
	tamperedPayload := parts[1]
	if tamperedPayload[0] == 'A' {
		tamperedPayload = "B" + tamperedPayload[1:]
	} else {
		tamperedPayload = "A" + tamperedPayload[1:]
	}
	badTokens := []string{"", token + ".extra", "tpr2." + parts[1] + "." + parts[2], "tpr1." + tamperedPayload + "." + parts[2], "tpr1." + parts[1] + "." + strings.Repeat("A", 43), "tpr1." + parts[1] + "=." + parts[2], strings.Repeat("a", trainingPublicationReviewMaxTokenBytes), strings.Repeat("a", trainingPublicationReviewMaxTokenBytes+1)}
	for _, bad := range badTokens {
		if _, err := signer.verify(bad, input.trainingPublicationReviewMetadata, now); !errors.Is(err, errTrainingPublicationReviewInvalid) {
			t.Fatal("tampered/malformed/oversized token verified")
		}
	}
	if err := (trainingPublicationReviewClaims{}).matches(input, now); !errors.Is(err, errTrainingPublicationReviewInvalid) {
		t.Fatal("unsigned claims accepted")
	}
	for _, badNow := range []time.Time{{}, time.Date(1969, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)} {
		if _, err := signer.sign(input, badNow); !errors.Is(err, errTrainingPublicationReviewInvalid) {
			t.Fatal("invalid signing clock accepted", err)
		}
	}
	if _, err := (trainingPublicationReviewSigner{}).sign(input, now); !errors.Is(err, errTrainingPublicationReviewInvalid) {
		t.Fatal("zero signer accepted")
	}
	if _, err := (trainingPublicationReviewSigner{}).verify(token, input.trainingPublicationReviewMetadata, now); !errors.Is(err, errTrainingPublicationReviewInvalid) {
		t.Fatal("zero signer verified")
	}
}

func TestTrainingPublicationReviewTupleAndAggregateByteBoundaries(t *testing.T) {
	signer, input, now := publicationReviewFixture(t)
	tuples := make([]StructuredPrescriptionInput, trainingPublicationReviewMaxTuples)
	for i := range tuples {
		tuples[i] = StructuredPrescriptionInput{SessionID: uuid.New(), MembershipID: uuid.New(), AthleteUserID: uuid.New(), Snapshot: []byte(`{}`)}
	}
	input.Prescriptions = tuples
	if _, err := signer.sign(input, now); err != nil {
		t.Fatal("exact tuple boundary rejected", err)
	}
	input.Prescriptions = append(append([]StructuredPrescriptionInput(nil), tuples...), StructuredPrescriptionInput{})
	if _, err := signer.sign(input, now); !errors.Is(err, errTrainingPublicationReviewLimit) {
		t.Fatal("over tuple boundary accepted", err)
	}
	// Shared immutable buffers keep the test cheap while accounting every tuple's
	// actual snapshot size. Each individual payload is below the DB item limit.
	const size = 128 << 10
	raw := []byte(`{"padding":"` + strings.Repeat("a", size-len(`{"padding":""}`)) + `"}`)
	if len(raw) != size {
		t.Fatal("wrong boundary fixture")
	}
	input.Prescriptions = make([]StructuredPrescriptionInput, trainingPublicationReviewMaxSnapshotBytes/size)
	for i := range input.Prescriptions {
		input.Prescriptions[i] = StructuredPrescriptionInput{SessionID: uuid.New(), MembershipID: uuid.New(), AthleteUserID: uuid.New(), Snapshot: raw}
	}
	token, err := signer.sign(input, now)
	if err != nil {
		t.Fatal("exact byte boundary rejected", err)
	}
	claims, err := signer.verify(token, input.trainingPublicationReviewMetadata, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := claims.matches(input, now); err != nil {
		t.Fatal(err)
	}
	over := clonePublicationReviewInput(input)
	over.Prescriptions[len(over.Prescriptions)-1].Snapshot = append(append([]byte(nil), raw...), ' ')
	if _, err := signer.sign(over, now); !errors.Is(err, errTrainingPublicationReviewLimit) {
		t.Fatal("over byte boundary accepted", err)
	}
	if err := claims.matches(over, now); !errors.Is(err, errTrainingPublicationReviewLimit) {
		t.Fatal("transaction match omitted byte bound", err)
	}
}

func TestTrainingPublicationReviewEmptySuccessorAndLatestBinding(t *testing.T) {
	signer, input, now := publicationReviewFixture(t)
	input.Prescriptions = nil
	token, err := signer.sign(input, now)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := signer.verify(token, input.trainingPublicationReviewMetadata, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := claims.matches(input, now); err != nil {
		t.Fatal(err)
	}
	// The helper does not create a revision/no-op. A successful save changing
	// latest invalidates this exact old review, even for a still-empty audience.
	current := input
	current.LatestPublicationID = uuid.New()
	current.LatestRevision++
	if err := claims.matches(current, now); !errors.Is(err, errTrainingPublicationReviewChanged) {
		t.Fatal("empty stale replay accepted")
	}
	// First-publication/no-recipient guidance and unchanged fresh review no-op are
	// future store decisions. Explicit none is still distinct from any successor.
	first := input
	first.LatestPublicationID = uuid.Nil
	first.LatestRevision = 0
	first.Return = trainingPublicationReturnContext{}
	if _, err := signer.sign(first, now); err != nil {
		t.Fatal("explicit absent latest encoding rejected", err)
	}
}

func TestTrainingPublicationReviewRejectsNoncanonicalBase64Segments(t *testing.T) {
	signer, input, now := publicationReviewFixture(t)
	token, err := signer.sign(input, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := signer.verify(token, input.trainingPublicationReviewMetadata, now); err != nil {
		t.Fatal(err)
	}
	original := strings.Split(token, ".")
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	mutations := []struct {
		name   string
		change func(string) string
	}{
		{"suffix LF", func(s string) string { return s + "\n" }},
		{"prefix CR", func(s string) string { return "\r" + s }},
		{"embedded CRLF", func(s string) string { return s[:5] + "\r\n" + s[5:] }},
		{"padding", func(s string) string { return s + "=" }},
		{"nonzero unused bits", func(s string) string {
			// Both fixed-width segments have two unused trailing bits. Flipping
			// just one keeps the decoded bytes under permissive base64 decoding.
			last := strings.IndexByte(alphabet, s[len(s)-1])
			return s[:len(s)-1] + string(alphabet[last^1])
		}},
	}
	for _, segment := range []int{1, 2} {
		name := "payload"
		if segment == 2 {
			name = "signature"
		}
		for _, mutation := range mutations {
			t.Run(name+"/"+mutation.name, func(t *testing.T) {
				parts := append([]string(nil), original...)
				parts[segment] = mutation.change(parts[segment])
				if segment == 1 {
					// Give even the malformed payload a valid MAC over its exact spelling.
					// This isolates canonical parsing from the separate tamper rejection.
					mac := hmac.New(sha256.New, signer.key[:])
					_, _ = mac.Write([]byte(trainingPublicationReviewPurpose + "." + parts[1]))
					parts[2] = base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
				}
				if _, err := signer.verify(strings.Join(parts, "."), input.trainingPublicationReviewMetadata, now); !errors.Is(err, errTrainingPublicationReviewInvalid) {
					t.Fatal("noncanonical segment accepted", err)
				}
			})
		}
	}
}
