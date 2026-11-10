package db

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"sort"
)

const TrainingRhythmReferencesMigration = "202611100006_training_rhythm_references"
const TrainingRhythmReaderGeneration int32 = 5

// VerifyTrainingRhythmRuntime adds actual explicit-reference semantics to the
// inherited admission/catalog/floor contract. It enables no gate.
func VerifyTrainingRhythmRuntime(ctx context.Context, q TrainingScheduleQueryReader, cap TrainingScheduleCapabilities) (TrainingScheduleState, error) {
	state, err := VerifyTrainingPlacementRuntime(ctx, q, cap)
	if err != nil {
		return state, err
	}
	if cap.ReaderVersion < TrainingRhythmReaderGeneration || state.RequiredReader < TrainingRhythmReaderGeneration {
		return state, errors.New("training rhythm reader floor not committed")
	}
	return state, nil
}

//go:embed training_rhythm_catalog.json
var trainingRhythmCatalogJSON []byte

func verifyTrainingRhythmDefinition(ctx context.Context, q TrainingScheduleQueryReader) error {
	var relations map[string]json.RawMessage
	if err := json.Unmarshal(trainingRhythmCatalogJSON, &relations); err != nil || len(relations) != 5 {
		return errors.New("training rhythm compiled catalog invalid")
	}
	names := make([]string, 0, len(relations))
	for name := range relations {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		var valid bool
		if err := q.QueryRow(ctx, placementRelationContractSQL, name, string(relations[name])).Scan(&valid); err != nil || !valid {
			return errors.New("training rhythm relation contract mismatch: " + name)
		}
	}
	for _, name := range []string{"touch_training_rhythm_profile_plans", "lock_training_rhythm_zone_parent", "guard_training_rhythm_profile_delete", "guard_training_rhythm_variation", "touch_training_rhythm_sporting_permissions"} {
		header, err := trainingScheduleExtractFunction(baselineSchema, "public."+name)
		if err != nil {
			return err
		}
		valid, err := trainingScheduleFunctionMatches(ctx, q, "public."+name+"()", header)
		if err != nil || !valid {
			return errors.New("training rhythm function contract mismatch: " + name)
		}
	}
	return nil
}
