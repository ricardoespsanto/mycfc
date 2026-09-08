package sessionstore

import (
	"testing"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/google/uuid"
)

func TestSessionUserIDExtractsOnlyValidSubject(t *testing.T) {
	codec := scs.GobCodec{}
	id := uuid.New()
	for _, test := range []struct {
		name   string
		values map[string]any
		want   *uuid.UUID
		bad    bool
	}{
		{name: "authenticated", values: map[string]any{"user_id": id.String(), "credential_version": int64(3)}, want: &id},
		{name: "anonymous", values: map[string]any{"csrf": "opaque"}},
		{name: "wrong type", values: map[string]any{"user_id": 42}, bad: true},
		{name: "invalid UUID", values: map[string]any{"user_id": "not-a-uuid"}, bad: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			data, err := codec.Encode(time.Now().Add(time.Hour), test.values)
			if err != nil {
				t.Fatal(err)
			}
			got, err := sessionUserID(codec, data)
			if test.bad {
				if err == nil {
					t.Fatal("invalid subject accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if test.want == nil && got != nil || test.want != nil && (got == nil || *got != *test.want) {
				t.Fatalf("subject=%v want=%v", got, test.want)
			}
		})
	}
}

func TestSessionUserIDRejectsMalformedPayloadAndMissingCodec(t *testing.T) {
	if _, err := sessionUserID(scs.GobCodec{}, []byte("not-gob")); err == nil {
		t.Fatal("malformed payload accepted")
	}
	if _, err := sessionUserID(nil, nil); err == nil {
		t.Fatal("missing codec accepted")
	}
}
