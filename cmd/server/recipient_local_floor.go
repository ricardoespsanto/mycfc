package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"

	"github.com/cfcoimbra/mycfc/internal/db"
	"github.com/cfcoimbra/mycfc/internal/trainingcompat"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func prepareLocalRecipientFloor(ctx context.Context) error {
	op, cancel := context.WithTimeout(ctx, recipientCLIOperationTimeout)
	defer cancel()
	target, err := loadRecipientCommandTarget(op, "migration")
	if err != nil {
		return err
	}
	if !strictUnstampedLocalConnection(target.Connection) {
		return errors.New("local floor requires wholly unstamped canonical local/test configuration and owned literal-loopback target")
	}
	migration, err := pgx.ConnectConfig(op, target.Connection)
	if err != nil {
		return errors.New("local floor migration connection rejected")
	}
	defer migration.Close(op)
	bootstrap, err := localBootstrapClone(op, migration)
	if err != nil {
		return err
	}
	defer bootstrap.Close(op)
	binding, err := localManifestBinding(op, bootstrap, target.Roles)
	if err != nil {
		return err
	}
	record, err := openLocalRecipientManifest(binding, false)
	if err != nil {
		return err
	}
	defer record.Close()
	m := &record.Value
	if m.PublisherOID == nil || m.OriginalLogin == nil || m.Phase == "captured" {
		return errors.New("local fixture bootstrap provenance incomplete")
	}
	inspected, err := inspectRecipientConnection(op, migration, target.Database, target.Roles, db.RecipientInspectFinal)
	if err != nil {
		return err
	}
	if inspected.Publisher.OID != *m.PublisherOID || inspected.ScheduleState == nil || inspected.ScheduleState.RequiredReader > trainingcompat.ReaderGeneration {
		return errors.New("local fixture role/floor binding changed")
	}
	s := *inspected.ScheduleState
	if m.Intent == nil && s.RequiredReader >= 3 {
		if m.Phase != "restored" && m.Phase != "bootstrapped" {
			return errors.New("local floor state requires recorded intent inspection")
		}
		return completeLocalAuthoringPreparation(op, migration, bootstrap, target, record, s)
	}
	if m.Intent == nil {
		m.Intent = &localRecipientFloorIntent{Incarnation: s.DatabaseIncarnation.String(), ExpectedEpoch: s.StateEpoch, Commit: uuid.NewString(), MinimumReader: 3}
		immutable := struct {
			Binding localRecipientManifest    `json:"binding"`
			Intent  localRecipientFloorIntent `json:"intent"`
		}{*m, *m.Intent}
		immutable.Binding.Intent = nil
		immutable.Binding.Phase = ""
		encoded, err := json.Marshal(immutable)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(encoded)
		m.Intent.ManifestSHA256 = hex.EncodeToString(digest[:])
		if err = record.Save(); err != nil {
			return err
		}
	}
	intent := m.Intent
	id, err := uuid.Parse(intent.Incarnation)
	if err != nil || id == uuid.Nil {
		return errors.New("local floor intent incarnation invalid")
	}
	commit, err := uuid.Parse(intent.Commit)
	if err != nil || commit == uuid.Nil {
		return errors.New("local floor intent commit invalid")
	}
	if intent.MinimumReader != 3 || len(intent.ManifestSHA256) != 64 || s.DatabaseIncarnation != id {
		return errors.New("local floor intent binding changed")
	}
	floorMatches := func(state db.TrainingScheduleState) bool {
		epochMatches := state.StateEpoch == intent.ExpectedEpoch+1
		if m.AuthoringExpectedEpoch != nil && *m.AuthoringExpectedEpoch == intent.ExpectedEpoch+1 && state.PartialV2Enabled && state.StateEpoch == *m.AuthoringExpectedEpoch+1 {
			epochMatches = true
		}
		return state.DatabaseIncarnation == id && epochMatches && state.RequiredReader == 3 && state.FloorCommitID != nil && *state.FloorCommitID == commit && state.FloorManifestSHA256 != nil && *state.FloorManifestSHA256 == intent.ManifestSHA256
	}
	if m.Phase == "restored" && floorMatches(s) && inspected.Publisher.CanLogin == *m.OriginalLogin {
		return completeLocalAuthoringPreparation(op, migration, bootstrap, target, record, s)
	}
	if !floorMatches(s) && s.StateEpoch != intent.ExpectedEpoch {
		return errors.New("local floor state changed; inspect recorded intent without replaying old epoch")
	}
	// LOGIN is cluster-wide. Refuse all-database work before changing it; no
	// termination and no claim that target-database statistics prove global drain.
	err = withRecipientPolicySession(op, bootstrap, func(owned context.Context) error {
		if err := requireNoLocalPublisherWork(owned, bootstrap, m.PublisherRole, *m.PublisherOID); err != nil {
			return err
		}
		_, err := setRecipientPublisherLogin(owned, bootstrap, m.PublisherRole, *m.PublisherOID, false)
		if err != nil {
			return err
		}
		m.Phase = "fenced"
		return record.Save()
	})
	if err != nil {
		return err
	}
	err = withRecipientPolicySession(op, migration, func(owned context.Context) error {
		current, err := inspectRecipientConnection(owned, migration, target.Database, target.Roles, db.RecipientInspectFinal)
		if err != nil {
			return err
		}
		if current.ScheduleState == nil {
			return errors.New("local floor final state missing")
		}
		if floorMatches(*current.ScheduleState) {
			s = *current.ScheduleState
			return nil
		}
		s, err = commitRecipientFloor(owned, migration, current, recipientFloorInput{Incarnation: id, Commit: commit, Epoch: intent.ExpectedEpoch, Manifest: intent.ManifestSHA256, Reader: 3})
		return err
	})
	if err != nil {
		return err
	}
	m.Phase = "floor_committed"
	if err = record.Save(); err != nil {
		return err
	}
	return completeLocalAuthoringPreparation(op, migration, bootstrap, target, record, s)
}

func requireNoLocalPublisherWork(ctx context.Context, c *pgx.Conn, role string, oid uint32) error {
	var safe bool
	err := c.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_roles WHERE oid OPERATOR(pg_catalog.=) $1::pg_catalog.oid AND rolname OPERATOR(pg_catalog.=) $2::pg_catalog.name) AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_stat_activity WHERE usesysid OPERATOR(pg_catalog.=) $1::pg_catalog.oid) AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_prepared_xacts WHERE owner OPERATOR(pg_catalog.=) $2::pg_catalog.name)`, oid, role).Scan(&safe)
	if err != nil || !safe {
		return errors.New("stop every local application using this publisher across all databases, then retry the recorded local operation")
	}
	return nil
}
func emitLocalRecipientFloor(s db.TrainingScheduleState) error {
	return json.NewEncoder(os.Stdout).Encode(struct {
		Contract string                  `json:"contract"`
		Unsigned bool                    `json:"unsigned"`
		State    recipientScheduleStatus `json:"schedule_state"`
	}{"mycfc/local-recipient-fixture/v1", true, recipientStatusObject(s)})
}
