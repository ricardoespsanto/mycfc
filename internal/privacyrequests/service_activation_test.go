package privacyrequests

import (
	"context"
	"errors"
	"strings"
	"testing"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

type activationReadinessStore struct {
	ready    bool
	readyErr error
}

func (activationReadinessStore) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errors.New("unexpected exec")
}
func (activationReadinessStore) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("unexpected query")
}
func (s activationReadinessStore) QueryRow(_ context.Context, query string, _ ...any) pgx.Row {
	return activationReadinessRow{activation: strings.Contains(query, "FROM privacy_request_activation"), ready: s.ready, err: s.readyErr}
}

type activationReadinessRow struct {
	activation bool
	ready      bool
	err        error
}

func (r activationReadinessRow) Scan(dest ...any) error {
	if r.activation {
		*dest[0].(*bool) = true
		*dest[1].(*string) = "policy-v2"
		*dest[2].(*bool) = true
		*dest[3].(*bool) = true
		*dest[4].(*uuid.UUID) = uuid.New()
		*dest[5].(*pgtype.Timestamptz) = pgtype.Timestamptz{}
		*dest[6].(**uuid.UUID) = nil
		return nil
	}
	if r.err != nil {
		return r.err
	}
	*dest[0].(*bool) = r.ready
	return nil
}

func TestActivePolicyRejectsDatabaseReadinessFailureAndRevocation(t *testing.T) {
	for _, store := range []activationReadinessStore{{ready: false}, {readyErr: errors.New("readiness unavailable")}} {
		_, err := activePolicy(context.Background(), dbgen.New(store))
		if !errors.Is(err, ErrPolicyUnresolved) {
			t.Fatalf("active policy error=%v", err)
		}
	}
}
