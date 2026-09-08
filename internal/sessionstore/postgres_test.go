package sessionstore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

type fakeDatabase struct {
	token, query string
	data         []byte
	expiry       time.Time
	userID       *uuid.UUID
	err          error
}

func (f *fakeDatabase) Exec(_ context.Context, query string, args ...any) (pgconn.CommandTag, error) {
	f.query, f.token, f.data, f.expiry, f.userID = query, args[0].(string), args[1].([]byte), args[2].(time.Time), args[3].(*uuid.UUID)
	return pgconn.CommandTag{}, f.err
}

type fakeDelegate struct {
	data    []byte
	found   bool
	all     map[string][]byte
	err     error
	stopped bool
}

func (f *fakeDelegate) FindCtx(context.Context, string) ([]byte, bool, error) {
	return f.data, f.found, f.err
}
func (f *fakeDelegate) CommitCtx(context.Context, string, []byte, time.Time) error { return f.err }
func (f *fakeDelegate) DeleteCtx(context.Context, string) error                    { return f.err }
func (f *fakeDelegate) AllCtx(context.Context) (map[string][]byte, error)          { return f.all, f.err }
func (f *fakeDelegate) StopCleanup()                                               { f.stopped = true }

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

func TestPostgresStoreIndexesSubjectAndDelegatesReadsAndDeletes(t *testing.T) {
	codec := scs.GobCodec{}
	id := uuid.New()
	expiry := time.Now().Add(time.Hour)
	data, err := codec.Encode(expiry, map[string]any{"user_id": id.String()})
	if err != nil {
		t.Fatal(err)
	}
	db := &fakeDatabase{}
	delegate := &fakeDelegate{data: data, found: true, all: map[string][]byte{"token": data}}
	store := &PostgresStore{pool: db, codec: codec, delegate: delegate}

	if err = store.CommitCtx(context.Background(), "token", data, expiry); err != nil {
		t.Fatal(err)
	}
	if db.token != "token" || db.userID == nil || *db.userID != id || db.expiry != expiry || len(db.data) == 0 || db.query == "" {
		t.Fatalf("indexed commit=%+v", db)
	}
	if got, found, findErr := store.Find("token"); findErr != nil || !found || len(got) == 0 {
		t.Fatalf("find found=%v err=%v", found, findErr)
	}
	if got, allErr := store.All(); allErr != nil || len(got) != 1 {
		t.Fatalf("all=%v err=%v", got, allErr)
	}
	if err = store.Delete("token"); err != nil {
		t.Fatal(err)
	}
	store.StopCleanup()
	if !delegate.stopped {
		t.Fatal("cleanup was not stopped")
	}
}

func TestPostgresStoreRejectsInvalidPayloadAndPropagatesStorageErrors(t *testing.T) {
	codec := scs.GobCodec{}
	storageErr := errors.New("storage unavailable")
	store := &PostgresStore{pool: &fakeDatabase{err: storageErr}, codec: codec, delegate: &fakeDelegate{err: storageErr}}
	if err := store.Commit("token", []byte("invalid"), time.Now()); err == nil {
		t.Fatal("invalid payload accepted")
	}
	data, err := codec.Encode(time.Now().Add(time.Hour), map[string]any{"user_id": uuid.New().String()})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Commit("token", data, time.Now()); !errors.Is(err, storageErr) {
		t.Fatalf("commit error=%v", err)
	}
	if _, _, err = store.FindCtx(context.Background(), "token"); !errors.Is(err, storageErr) {
		t.Fatalf("find error=%v", err)
	}
	if _, err = store.AllCtx(context.Background()); !errors.Is(err, storageErr) {
		t.Fatalf("all error=%v", err)
	}
	if err = store.DeleteCtx(context.Background(), "token"); !errors.Is(err, storageErr) {
		t.Fatalf("delete error=%v", err)
	}
}
