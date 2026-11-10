package app

import (
	"context"
	"net/http"
	"time"

	"github.com/cfcoimbra/mycfc/internal/config"
	"github.com/cfcoimbra/mycfc/internal/db"
	"github.com/cfcoimbra/mycfc/internal/handlers"
	"github.com/cfcoimbra/mycfc/internal/trainingcompat"
	"github.com/jackc/pgx/v5/pgxpool"
)

type trainingReadiness struct {
	pool             *pgxpool.Pool
	admissionFile    string
	requireAdmission bool
}

func newTrainingReadiness(pool *pgxpool.Pool, cfg config.Config) trainingReadiness {
	cap := trainingcompat.Compiled()
	localUnstamped := (cfg.AppEnv == "local" || cfg.AppEnv == "test") && cap.Version == "" && cap.Candidate == "" && cfg.TrainingScheduleAdmissionFile == ""
	required := !localUnstamped
	file := cfg.TrainingScheduleAdmissionFile
	if file == "" {
		file = trainingcompat.DefaultAdmissionFile
	}
	return trainingReadiness{pool: pool, admissionFile: file, requireAdmission: required}
}
func (p trainingReadiness) Ping(ctx context.Context) error {
	if e := p.pool.Ping(ctx); e != nil {
		return e
	}
	if e := db.VerifyDatedParticipationRuntime(ctx, p.pool); e != nil {
		return e
	}
	state, e := db.VerifyTrainingRhythmRuntime(ctx, p.pool, db.TrainingScheduleCapabilities{ReaderVersion: trainingcompat.ReaderGeneration, WriterVersion: trainingcompat.WriterGeneration})
	if e != nil {
		return e
	}
	if !p.requireAdmission {
		return nil
	} // only an entirely unstamped local/test binary
	admission, e := trainingcompat.ReadAdmission(p.admissionFile)
	if e != nil {
		return e
	}
	var floor *string
	if state.FloorCommitID != nil {
		value := state.FloorCommitID.String()
		floor = &value
	}
	return admission.Verify(trainingcompat.RuntimeState{DatabaseIncarnation: state.DatabaseIncarnation.String(), SchemaContract: state.SchemaContract, StateEpoch: state.StateEpoch, RequiredReader: state.RequiredReader, FloorCommitID: floor, FloorManifestSHA256: state.FloorManifestSHA256}, trainingcompat.Compiled())
}

type readinessProbe interface{ Ping(context.Context) error }

func trainingAdmissionGuard(probe readinessProbe, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if (r.Method == http.MethodGet || r.Method == http.MethodHead) && r.URL.Path == "/health/live" {
			(handlers.Health{}).Live(w, r)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if probe.Ping(ctx) != nil {
			w.Header().Set("Cache-Control", "private, no-store")
			w.Header().Set("Referrer-Policy", "no-referrer")
			http.Error(w, "indisponível", http.StatusServiceUnavailable)
			return
		}
		next.ServeHTTP(w, r)
	})
}
