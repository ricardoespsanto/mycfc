//go:build integration

package app

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cfcoimbra/mycfc/internal/config"
	"github.com/cfcoimbra/mycfc/internal/db"
	"github.com/cfcoimbra/mycfc/internal/trainingcompat"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Run only inside an isolated root container with an explicitly supplied low
// web fixture URL and compile-stamped test binary. No host admission file is used.
func TestStampedTrainingAdmissionTrustedProjectionAndRevocation(t *testing.T) {
	if os.Getenv("TEST_TRAINING_ADMISSION_ROOT") != "1" {
		t.Skip("dedicated root-container admission fixture only")
	}
	if os.Geteuid() != 0 {
		t.Fatal("trusted projection fixture requires isolated root container")
	}
	cap := trainingcompat.Compiled()
	if cap.Version == "" || cap.Candidate == "" {
		t.Fatal("test binary must be compile-stamped")
	}
	dsn := os.Getenv("TEST_TRAINING_ADMISSION_WEB_URL")
	if dsn == "" {
		t.Fatal("isolated low web URL required")
	}
	pool, e := pgxpool.New(t.Context(), dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer pool.Close()
	state, e := db.VerifyTrainingRecipientRuntime(t.Context(), pool, db.TrainingScheduleCapabilities{ReaderVersion: trainingcompat.ReaderGeneration, WriterVersion: trainingcompat.WriterGeneration})
	if e != nil {
		t.Fatal(e)
	}
	dir, e := os.MkdirTemp("/run", "mycfc-admission-test-")
	if e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(dir)
	if e = os.Chmod(dir, 0755); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(dir, "admission.json")
	var floor *string
	if state.FloorCommitID != nil {
		v := state.FloorCommitID.String()
		floor = &v
	}
	p := trainingcompat.Admission{Contract: trainingcompat.AdmissionContract, Admission: "admitted", RestoreGeneration: 8, ReconciledRestoreGeneration: 8, DatabaseIncarnation: state.DatabaseIncarnation.String(), FloorCommitID: floor, FloorManifestSHA256: state.FloorManifestSHA256, MinimumStateEpoch: state.StateEpoch, MinimumReader: state.RequiredReader, Images: []trainingcompat.AdmissionImage{{Digest: "sha256:" + strings.Repeat("a", 64), Capabilities: cap}}}
	write := func(value trainingcompat.Admission) {
		t.Helper()
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		temporary := filepath.Join(dir, ".next")
		if err = os.WriteFile(temporary, raw, 0444); err != nil {
			t.Fatal(err)
		}
		if err = os.Chmod(temporary, 0444); err != nil {
			t.Fatal(err)
		}
		if err = os.Rename(temporary, path); err != nil {
			t.Fatal(err)
		}
	}
	write(p)
	// Exercise the actual startup and routed middleware, using real low-role
	// PostgreSQL and trusted files. Only unrelated configuration/AWS discovery is
	// supplied by the test; compatibility verification and app assembly are real.
	previousConfig, previousAWS := loadApplicationConfig, loadApplicationAWS
	t.Cleanup(func() { loadApplicationConfig, loadApplicationAWS = previousConfig, previousAWS })
	cfg := applicationStartupTestConfig()
	cfg.DatabaseURL = config.Secret(dsn)
	cfg.AppVersion = cap.Version
	cfg.GITSHA = cap.Candidate
	cfg.TrainingScheduleAdmissionFile = path
	key, keyErr := ecdh.X25519().GenerateKey(rand.Reader)
	if keyErr != nil {
		t.Fatal(keyErr)
	}
	cfg.MediaUploadPublicKeyB64 = base64.StdEncoding.EncodeToString(key.PublicKey().Bytes())
	cfg.MediaUploadEncryptionKeyID = "upload-test-key"
	cfg.MediaUploadDigestKeyID = "upload-test-digest"
	cfg.MediaUploadDigestKeyB64 = config.Secret(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32)))
	loadApplicationConfig = func(context.Context) (config.Config, error) { return cfg, nil }
	loadApplicationAWS = func(context.Context, ...func(*awsconfig.LoadOptions) error) (aws.Config, error) {
		return aws.Config{Region: "eu-west-1"}, nil
	}
	application, startErr := New(t.Context())
	if startErr != nil {
		t.Fatalf("actual stamped app startup rejected trusted admission: %v", startErr)
	}
	defer application.Close()
	routed := func(method, target string, want int) {
		t.Helper()
		request := httptest.NewRequest(method, target, nil)
		request.AddCookie(&http.Cookie{Name: application.Sessions.Cookie.Name, Value: strings.Repeat("a", 43)})
		response := httptest.NewRecorder()
		application.Server.Handler.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("actual routed %s code%d want%d", target, response.Code, want)
		}
	}
	routed(http.MethodGet, "/health/ready", 200)
	routed(http.MethodGet, "/login", 200)
	blocked := p
	blocked.Admission = "blocked"
	write(blocked)
	if failed, err := New(t.Context()); err == nil {
		failed.Close()
		t.Fatal("actual app startup accepted blocked external fence")
	}
	routed(http.MethodGet, "/login", 503)
	write(p)
	application.Pool.Close()
	routed(http.MethodGet, "/health/live", 200)
	routed(http.MethodHead, "/health/live", 200)
	probe := newTrainingReadiness(pool, config.Config{AppEnv: "production", TrainingScheduleAdmissionFile: path})
	if e = probe.Ping(t.Context()); e != nil {
		t.Fatalf("trusted stamped admission rejected: %v", e)
	}
	reached := 0
	guard := trainingAdmissionGuard(probe, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached++; w.WriteHeader(200) }))
	call := func(want int) {
		t.Helper()
		w := httptest.NewRecorder()
		guard.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/login", nil))
		if w.Code != want {
			t.Fatalf("code%d want%d", w.Code, want)
		}
	}
	call(200)
	bad := p
	bad.Admission = "blocked"
	write(bad)
	call(503)
	bad = p
	bad.ReconciledRestoreGeneration = 7
	write(bad)
	call(503) // copied DB facts still match
	bad = p
	bad.MinimumStateEpoch++
	write(bad)
	call(503)
	bad = p
	bad.Images = append([]trainingcompat.AdmissionImage(nil), p.Images...)
	bad.Images[0].Capabilities.Candidate = strings.Repeat("b", 40)
	write(bad)
	call(503)
	write(p)
	if e = os.Chmod(path, 0644); e != nil {
		t.Fatal(e)
	}
	call(503)
	if e = os.Remove(path); e != nil {
		t.Fatal(e)
	}
	call(503)
	write(p)
	target := filepath.Join(dir, "target.json")
	if e = os.Rename(path, target); e != nil {
		t.Fatal(e)
	}
	if e = os.Symlink(target, path); e != nil {
		t.Fatal(e)
	}
	call(503)
	if reached != 1 {
		t.Fatalf("blocked requests reached serving path %d", reached)
	}
	// Liveness is process-only even while a restore fence blocks all serving.
	w := httptest.NewRecorder()
	guard.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health/live", nil))
	if w.Code != 200 {
		t.Fatal("liveness blocked")
	}
	if e = probe.Ping(context.Background()); e == nil {
		t.Fatal("readiness cached prior admission")
	}
}
