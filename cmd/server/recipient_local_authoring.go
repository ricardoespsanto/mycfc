package main

import (
	"context"
	"errors"

	"github.com/cfcoimbra/mycfc/internal/db"
	"github.com/cfcoimbra/mycfc/internal/trainingcompat"
	"github.com/jackc/pgx/v5"
)

// Reuses the existing LOCAL target journal and original policy sessions. It is
// not a production activation endpoint or a runtime/web capability grant.
func completeLocalAuthoringPreparation(ctx context.Context, migration, bootstrap *pgx.Conn, target recipientCommandTarget, record *localRecipientManifestFile, state db.TrainingScheduleState) error {
	m := &record.Value
	authoring := state.SchemaContract == db.TrainingScheduleContractP && trainingcompat.WriterGeneration >= 2
	if state.RequiredReader != 3 || state.FloorCommitID == nil || state.FloorManifestSHA256 == nil || m.PublisherOID == nil || m.OriginalLogin == nil {
		return errors.New("local preparation floor/role binding rejected")
	}
	sameFloor := func(current db.TrainingScheduleState) bool {
		return current.DatabaseIncarnation == state.DatabaseIncarnation && current.RequiredReader == 3 && current.FloorCommitID != nil && *current.FloorCommitID == *state.FloorCommitID && current.FloorManifestSHA256 != nil && *current.FloorManifestSHA256 == *state.FloorManifestSHA256 && current.SchemaContract == state.SchemaContract
	}
	prepared := func(current db.TrainingScheduleState) bool {
		return authoring && sameFloor(current) && current.PartialV2Enabled && m.AuthoringExpectedEpoch != nil && current.StateEpoch == *m.AuthoringExpectedEpoch+1 && (m.AuthoringPreparedEpoch == nil || *m.AuthoringPreparedEpoch == current.StateEpoch)
	}
	if authoring && state.PartialV2Enabled && !prepared(state) {
		return errors.New("local authoring state has no matching recorded activation intent")
	}
	if m.Phase == "restored" && (!authoring || prepared(state)) && (!authoring || m.AuthoringPreparedEpoch != nil) {
		return emitLocalRecipientFloor(state)
	}
	if err := withRecipientPolicySession(ctx, bootstrap, func(owned context.Context) error {
		if err := requireNoLocalPublisherWork(owned, bootstrap, m.PublisherRole, *m.PublisherOID); err != nil {
			return err
		}
		if _, err := setRecipientPublisherLogin(owned, bootstrap, m.PublisherRole, *m.PublisherOID, false); err != nil {
			return err
		}
		m.Phase = "floor_committed"
		return record.Save()
	}); err != nil {
		return err
	}
	if authoring {
		if err := withRecipientPolicySession(ctx, migration, func(owned context.Context) error {
			inspected, err := inspectRecipientConnection(owned, migration, target.Database, target.Roles, db.RecipientInspectFinal)
			if err != nil {
				return err
			}
			if inspected.ScheduleState == nil || !sameFloor(*inspected.ScheduleState) || inspected.Publisher.OID != *m.PublisherOID || inspected.Publisher.CanLogin || inspected.Publisher.BackendCount != 0 || inspected.Publisher.PreparedCount != 0 {
				return errors.New("local authoring requires unchanged floor and fenced publisher")
			}
			current := *inspected.ScheduleState
			if !prepared(current) {
				if current.PartialV2Enabled || m.AuthoringPreparedEpoch != nil {
					return errors.New("local authoring state changed; do not replay or reopen it")
				}
				if m.AuthoringExpectedEpoch == nil {
					epoch := current.StateEpoch
					m.AuthoringExpectedEpoch = &epoch
					if err = record.Save(); err != nil {
						return err
					}
				}
				if *m.AuthoringExpectedEpoch != current.StateEpoch {
					return errors.New("local authoring epoch changed; inspect recorded intent")
				}
				var acknowledged int64
				if err = migration.QueryRow(owned, `SELECT training_schedule_control.set_enabled($1,$2,true)`, current.DatabaseIncarnation, current.StateEpoch).Scan(&acknowledged); err != nil {
					return errors.New("local authoring enable not acknowledged; publisher remains fenced; inspect and resume matching intent")
				}
				current, err = db.TrainingScheduleStatus(owned, migration)
				if err != nil {
					return err
				}
				if !prepared(current) || acknowledged != current.StateEpoch {
					return errors.New("local authoring acknowledgement mismatched")
				}
			}
			epoch := current.StateEpoch
			m.AuthoringPreparedEpoch = &epoch
			state = current
			return record.Save()
		}); err != nil {
			return err
		}
	}
	if err := withRecipientPolicySession(ctx, bootstrap, func(owned context.Context) error {
		inspected, err := inspectRecipientFromOriginal(owned, bootstrap, target.Database, target.Roles, db.RecipientInspectFinal)
		if err != nil {
			return err
		}
		if inspected.ScheduleState == nil || !sameFloor(*inspected.ScheduleState) || inspected.Publisher.OID != *m.PublisherOID {
			return errors.New("local preparation final binding changed")
		}
		if authoring && (!prepared(*inspected.ScheduleState) || m.AuthoringPreparedEpoch == nil) {
			return errors.New("local authoring result unconfirmed; do not restore publisher")
		}
		if !authoring && inspected.ScheduleState.StateEpoch != state.StateEpoch {
			return errors.New("local preparation epoch changed")
		}
		if err := requireNoLocalPublisherWork(owned, bootstrap, m.PublisherRole, *m.PublisherOID); err != nil {
			return err
		}
		if _, err = setRecipientPublisherLogin(owned, bootstrap, m.PublisherRole, *m.PublisherOID, *m.OriginalLogin); err != nil {
			return err
		}
		m.Phase = "restored"
		return record.Save()
	}); err != nil {
		return err
	}
	return emitLocalRecipientFloor(state)
}
