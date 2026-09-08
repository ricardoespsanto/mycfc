// Package sessionstore provides the application's PostgreSQL-backed session store.
package sessionstore

import (
	"context"
	"errors"
	"time"

	"github.com/alexedwards/scs/pgxstore"
	"github.com/alexedwards/scs/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type database interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

type delegateStore interface {
	FindCtx(context.Context, string) ([]byte, bool, error)
	CommitCtx(context.Context, string, []byte, time.Time) error
	DeleteCtx(context.Context, string) error
	AllCtx(context.Context) (map[string][]byte, error)
	StopCleanup()
}

// PostgresStore preserves the SCS opaque payload while maintaining a separate,
// indexed subject reference for security revocation. No other session value is
// copied into relational columns.
type PostgresStore struct {
	pool     database
	codec    scs.Codec
	delegate delegateStore
}

func New(pool *pgxpool.Pool, codec scs.Codec) *PostgresStore {
	return &PostgresStore{pool: pool, codec: codec, delegate: pgxstore.New(pool)}
}

func (s *PostgresStore) FindCtx(ctx context.Context, token string) ([]byte, bool, error) {
	return s.delegate.FindCtx(ctx, token)
}

func (s *PostgresStore) CommitCtx(ctx context.Context, token string, data []byte, expiry time.Time) error {
	userID, err := sessionUserID(s.codec, data)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO sessions(token, data, expiry, user_id, subject_indexed)
		VALUES($1, $2, $3, $4, true)
		ON CONFLICT(token) DO UPDATE
		SET data = EXCLUDED.data, expiry = EXCLUDED.expiry,
			user_id = EXCLUDED.user_id, subject_indexed = true`, token, data, expiry, userID)
	return err
}

func (s *PostgresStore) DeleteCtx(ctx context.Context, token string) error {
	return s.delegate.DeleteCtx(ctx, token)
}

func (s *PostgresStore) AllCtx(ctx context.Context) (map[string][]byte, error) {
	return s.delegate.AllCtx(ctx)
}

// Find satisfies scs.Store. SCS uses the context-aware variants above; these
// context-free methods remain safe for direct use in tests and tools.
func (s *PostgresStore) Find(token string) ([]byte, bool, error) {
	return s.FindCtx(context.Background(), token)
}

func (s *PostgresStore) Commit(token string, data []byte, expiry time.Time) error {
	return s.CommitCtx(context.Background(), token, data, expiry)
}

func (s *PostgresStore) Delete(token string) error {
	return s.DeleteCtx(context.Background(), token)
}

func (s *PostgresStore) All() (map[string][]byte, error) {
	return s.AllCtx(context.Background())
}

func (s *PostgresStore) StopCleanup() {
	s.delegate.StopCleanup()
}

func sessionUserID(codec scs.Codec, data []byte) (*uuid.UUID, error) {
	if codec == nil {
		return nil, errors.New("session codec is required")
	}
	_, values, err := codec.Decode(data)
	if err != nil {
		return nil, errors.New("decode session subject")
	}
	raw, ok := values["user_id"]
	if !ok {
		return nil, nil
	}
	text, ok := raw.(string)
	if !ok || text == "" {
		return nil, errors.New("invalid session subject")
	}
	id, err := uuid.Parse(text)
	if err != nil || id == uuid.Nil {
		return nil, errors.New("invalid session subject")
	}
	return &id, nil
}

var (
	_ scs.CtxStore         = (*PostgresStore)(nil)
	_ scs.IterableCtxStore = (*PostgresStore)(nil)
)
