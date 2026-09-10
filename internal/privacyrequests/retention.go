package privacyrequests

import (
	"context"
	"errors"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
)

var ErrRetentionUnavailable = errors.New("privacy retention maintenance unavailable")

type RetentionMaintenance struct {
	Store      dbgen.DBTX
	Enabled    bool
	WorkerRef  uuid.UUID
	BatchLimit int32
}

// Run executes one bounded database-clock batch. Scheduling and production
// enablement are deliberately outside this source-only component.
func (m RetentionMaintenance) Run(ctx context.Context) (dbgen.RunPrivacyRetentionRow, error) {
	var zero dbgen.RunPrivacyRetentionRow
	if !m.Enabled || m.Store == nil || m.WorkerRef == uuid.Nil || m.BatchLimit < 1 || m.BatchLimit > 10000 {
		return zero, ErrRetentionUnavailable
	}
	result, err := dbgen.New(m.Store).RunPrivacyRetention(ctx, dbgen.RunPrivacyRetentionParams{WorkerRef: m.WorkerRef, BatchLimit: m.BatchLimit})
	if err != nil {
		return zero, err
	}
	return result, nil
}
