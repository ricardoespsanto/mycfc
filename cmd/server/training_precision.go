package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/cfcoimbra/mycfc/internal/db"
	"github.com/cfcoimbra/mycfc/internal/trainingcompat"
)

// This command uses the serving identity and narrow runtime verifier. It never
// reads the migration ledger, receipt or private capability-control tables.
func trainingPrecisionStatus(ctx context.Context) error {
	cfg, e := loadDatabaseCommandConfig(ctx)
	if e != nil {
		return fmt.Errorf("load precision status configuration: %w", e)
	}
	databaseURL, e := cfg.ResolvedDatabaseURL()
	if e != nil {
		return e
	}
	conn, e := connectDatabaseCommand(ctx, databaseURL)
	if e != nil {
		return fmt.Errorf("connect precision status database: %w", e)
	}
	defer conn.Close(ctx)
	state, e := db.VerifyTrainingScheduleRuntime(ctx, conn, db.TrainingScheduleCapabilities{ReaderVersion: trainingcompat.ReaderGeneration, WriterVersion: trainingcompat.WriterGeneration})
	if e != nil {
		return e
	}
	return json.NewEncoder(os.Stdout).Encode(struct {
		Contract             string  `json:"contract"`
		DatabaseIncarnation  string  `json:"database_incarnation"`
		SchemaContract       string  `json:"schema_contract"`
		StateEpoch           int64   `json:"state_epoch"`
		RequiredReader       int32   `json:"required_reader"`
		FloorCommitID        *string `json:"floor_commit_id"`
		FloorManifestSHA256  *string `json:"floor_manifest_sha256"`
		PartialV2Enabled     bool    `json:"partial_v2_enabled"`
		EverPartialV2Enabled bool    `json:"ever_partial_v2_enabled"`
	}{Contract: "mycfc/training-schedule-status/v1", DatabaseIncarnation: state.DatabaseIncarnation.String(), SchemaContract: state.SchemaContract, StateEpoch: state.StateEpoch, RequiredReader: state.RequiredReader, FloorCommitID: trainingFloorID(state), FloorManifestSHA256: state.FloorManifestSHA256, PartialV2Enabled: state.PartialV2Enabled, EverPartialV2Enabled: state.EverPartialV2Enabled})
}

func trainingFloorID(state db.TrainingScheduleState) *string {
	if state.FloorCommitID == nil {
		return nil
	}
	value := state.FloorCommitID.String()
	return &value
}
