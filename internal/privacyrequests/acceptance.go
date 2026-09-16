package privacyrequests

import (
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/internal/storage"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

const AcceptanceContract = "mycfc/privacy-synthetic-acceptance/v1"

var ErrAcceptance = errors.New("synthetic privacy acceptance failed")
var acceptanceImage = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
var acceptanceDigest = regexp.MustCompile(`^[0-9a-f]{64}$`)

// AcceptanceOptions deliberately has no person, request, execution or fixture ID.
// Those identities and the single-request capability originate in PostgreSQL.
type AcceptanceOptions struct {
	Mode, ExpectedDatabase, AppRole, ImageDigest, SchemaDigest string
	Operator                                                   *pgx.Conn
	AppConfig                                                  *pgxpool.Config
	Worker                                                     *pgxpool.Pool
	Ledger                                                     TombstoneLedger
	Protector                                                  *TombstoneProtector
	Event                                                      func(context.Context, string) error
}

type AcceptanceEvidence struct {
	Contract         string    `json:"contract"`
	Mode             string    `json:"mode"`
	Outcome          string    `json:"outcome"`
	ImageDigest      string    `json:"image_digest"`
	SchemaDigest     string    `json:"schema_digest"`
	FixtureSHA256    string    `json:"fixture_sha256"`
	ManifestSHA256   string    `json:"manifest_sha256,omitempty"`
	PolicySHA256     string    `json:"policy_sha256"`
	StartedAt        time.Time `json:"started_at"`
	ObservedAt       time.Time `json:"observed_at"`
	SimulatedNotices int64     `json:"simulated_notices"`
	Checkpoints      int64     `json:"checkpoints"`
	Conditions       []string  `json:"conditions"`
}

type SignedAcceptanceEvidence struct {
	Contract      string          `json:"contract"`
	KeyID         string          `json:"key_id"`
	Payload       json.RawMessage `json:"payload"`
	PayloadSHA256 string          `json:"payload_sha256"`
	Signature     string          `json:"signature"`
}

func SignAcceptanceEvidence(value AcceptanceEvidence, keyID string, key ed25519.PrivateKey) (SignedAcceptanceEvidence, error) {
	if !keyIdentifier.MatchString(keyID) || len(key) != ed25519.PrivateKeySize || value.Contract != AcceptanceContract || !acceptanceDigest.MatchString(value.FixtureSHA256) || !AcceptanceMode(value.Mode) ||
		(value.Outcome != "COMPLETED" && !(value.Mode == "canary-failure" && value.Outcome == "CANARY_VERIFIED")) || value.ObservedAt.Before(value.StartedAt) || value.ObservedAt.IsZero() || value.StartedAt.IsZero() {
		return SignedAcceptanceEvidence{}, ErrAcceptance
	}
	if value.Mode == "canary-failure" && (value.Outcome != "CANARY_VERIFIED" || value.ManifestSHA256 != "" || value.SimulatedNotices != 0 || value.Checkpoints != 0) {
		return SignedAcceptanceEvidence{}, ErrAcceptance
	}
	if value.Outcome == "COMPLETED" && (!acceptanceDigest.MatchString(value.ManifestSHA256) || value.SimulatedNotices < 1 || value.Checkpoints < 1) {
		return SignedAcceptanceEvidence{}, ErrAcceptance
	}
	expected := acceptanceConditions(value.Mode)
	if !slices.Equal(expected, value.Conditions) || !acceptanceImage.MatchString(value.ImageDigest) || !acceptanceDigest.MatchString(value.SchemaDigest) || !acceptanceDigest.MatchString(value.PolicySHA256) {
		return SignedAcceptanceEvidence{}, ErrAcceptance
	}
	if !hmac.Equal(ed25519.NewKeyFromSeed(key[:32]), key) {
		return SignedAcceptanceEvidence{}, ErrAcceptance
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return SignedAcceptanceEvidence{}, ErrAcceptance
	}
	digest := sha256.Sum256(payload)
	message := append([]byte(AcceptanceContract+"\x00"+keyID+"\x00"), payload...)
	return SignedAcceptanceEvidence{Contract: AcceptanceContract, KeyID: keyID, Payload: payload, PayloadSHA256: hex.EncodeToString(digest[:]), Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(key, message))}, nil
}

func acceptanceConditions(mode string) []string {
	switch mode {
	case "run":
		return []string{}
	case "canary-retry":
		return []string{"retry", "recovery"}
	case "canary-failure":
		return []string{"failure"}
	case "canary-aged":
		return []string{"aged", "recovery"}
	case "canary-heartbeat":
		return []string{"heartbeat_missing", "recovery"}
	case "canary-recovery":
		return []string{"heartbeat_missing", "retry", "recovery"}
	}
	return nil
}

func AcceptanceMode(mode string) bool {
	switch mode {
	case "run", "canary-retry", "canary-failure", "canary-aged", "canary-heartbeat", "canary-recovery":
		return true
	}
	return false
}

type acceptanceFixture struct {
	id, subject, reviewer, executor, worker uuid.UUID
	proof, marker                           []byte
}
type acceptanceRun struct {
	options    AcceptanceOptions
	fixture    acceptanceFixture
	app        *pgxpool.Pool
	service    Service
	worker     ExecutionWorker
	completion CompletionWorker
	objects    ObjectExecutionWorker
	providers  ProviderExecutionWorker
	tombstones TombstoneExportWorker
	approval   uuid.UUID
	execution  uuid.UUID
	evidence   AcceptanceEvidence
}

// RunSyntheticAcceptance exercises the actual service and production executors.
// The only external mutation is the authenticated ledger write for the generated
// fixture. Any unexpected object target fails closed without calling S3.
func RunSyntheticAcceptance(ctx context.Context, o AcceptanceOptions) (result AcceptanceEvidence, err error) {
	if !AcceptanceMode(o.Mode) || o.Operator == nil || o.AppConfig == nil || o.Worker == nil || o.Ledger == nil || o.Protector == nil || o.ExpectedDatabase == "" || o.AppRole == "" || o.AppRole == "mycfc_privacy_executor" || o.AppRole == "mycfc_privacy_acceptance" || !acceptanceImage.MatchString(o.ImageDigest) || !acceptanceDigest.MatchString(o.SchemaDigest) {
		return result, ErrAcceptance
	}
	var database, role string
	if err = o.Operator.QueryRow(ctx, `SELECT current_database(),session_user`).Scan(&database, &role); err != nil || database != o.ExpectedDatabase || role != "mycfc_privacy_acceptance" {
		return result, ErrAcceptance
	}
	if err = o.Worker.QueryRow(ctx, `SELECT current_database(),session_user`).Scan(&database, &role); err != nil || database != o.ExpectedDatabase || role != "mycfc_privacy_executor" {
		return result, ErrAcceptance
	}
	config := o.AppConfig.Copy()
	config.MaxConns = 4
	config.MinConns = 0
	initial, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return result, ErrAcceptance
	}
	defer initial.Close()
	if err = initial.QueryRow(ctx, `SELECT current_database(),session_user`).Scan(&database, &role); err != nil || database != o.ExpectedDatabase || role != o.AppRole {
		return result, ErrAcceptance
	}
	for _, connection := range []interface {
		QueryRow(context.Context, string, ...any) pgx.Row
	}{o.Operator, o.Worker, initial} {
		var elevated bool
		if e := connection.QueryRow(ctx, `SELECT rolsuper OR rolcreatedb OR rolcreaterole OR rolreplication OR rolbypassrls FROM pg_roles WHERE rolname=session_user`).Scan(&elevated); e != nil || elevated {
			return result, ErrAcceptance
		}
	}
	var activationMutation, workerPeopleMutation bool
	if e := initial.QueryRow(ctx, `SELECT has_table_privilege(current_user,'privacy_activation_approvals','INSERT')`).Scan(&activationMutation); e != nil || activationMutation {
		return result, ErrAcceptance
	}
	if e := o.Worker.QueryRow(ctx, `SELECT has_table_privilege(current_user,'users','UPDATE')`).Scan(&workerPeopleMutation); e != nil || workerPeopleMutation {
		return result, ErrAcceptance
	}
	activation, err := dbgen.New(initial).GetPrivacyActivation(ctx)
	if err != nil {
		return result, fmt.Errorf("acceptance activation read: %w", err)
	}
	if activation.ApprovalID == uuid.Nil {
		return result, ErrAcceptance
	}
	// Read the activation before and after creation so an activation change cannot
	// be mistaken for the image/schema binding checked inside acceptance_create.
	password := make([]byte, 32)
	if _, err = rand.Read(password); err != nil {
		return result, ErrAcceptance
	}
	passwordText := base64.RawURLEncoding.EncodeToString(password)
	hash, err := bcrypt.GenerateFromPassword([]byte(passwordText), bcrypt.DefaultCost)
	if err != nil {
		return result, ErrAcceptance
	}
	r := acceptanceRun{options: o, approval: activation.ApprovalID, evidence: AcceptanceEvidence{Contract: AcceptanceContract, Mode: o.Mode, ImageDigest: o.ImageDigest, SchemaDigest: o.SchemaDigest, StartedAt: time.Now().UTC(), Conditions: []string{}}}
	f := &r.fixture
	err = o.Operator.QueryRow(ctx, `SELECT * FROM privacy_protected.acceptance_create($1,$2,$3)`, string(hash), o.ImageDigest, o.SchemaDigest).Scan(&f.id, &f.subject, &f.reviewer, &f.executor, &f.worker, &f.proof, &f.marker)
	if err != nil {
		return result, fmt.Errorf("acceptance create: %w", err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cleanupConnection, e := pgx.ConnectConfig(cleanup, o.Operator.Config())
		if e == nil {
			defer cleanupConnection.Close(cleanup)
			_, e = cleanupConnection.Exec(cleanup, `SELECT privacy_protected.acceptance_finish($1,$2)`, f.worker, f.proof)
		}
		if e != nil {
			result = AcceptanceEvidence{}
			err = fmt.Errorf("acceptance cleanup: %w (prior: %v)", e, err)
		}
	}()
	mac := hmac.New(sha256.New, f.proof)
	fmt.Fprintf(mac, "mycfc/privacy-acceptance-fixture/v1:%s:%s:%s:%s:%s:%s:%s", f.id, f.subject, f.reviewer, f.executor, f.worker, o.ImageDigest, o.SchemaDigest)
	if len(f.proof) != 32 || !hmac.Equal(mac.Sum(nil), f.marker) {
		return result, ErrAcceptance
	}
	r.evidence.FixtureSHA256 = hex.EncodeToString(f.marker)
	config = config.Copy()
	config.ConnConfig.RuntimeParams["mycfc.acceptance_proof"] = hex.EncodeToString(f.proof)
	r.app, err = pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return result, ErrAcceptance
	}
	defer r.app.Close()
	secret := make([]byte, 32)
	if _, err = rand.Read(secret); err != nil {
		return result, ErrAcceptance
	}
	credentialSecret := make([]byte, 32)
	if _, err = rand.Read(credentialSecret); err != nil {
		return result, ErrAcceptance
	}
	private, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return result, ErrAcceptance
	}
	objects, err := NewX25519ObjectTargetProtector("acceptance-object", private.PublicKey().Bytes(), "acceptance-digest", secret)
	if err != nil {
		return result, ErrAcceptance
	}
	providers, err := NewX25519ProviderTargetProtector("acceptance-provider", private.PublicKey().Bytes(), "acceptance-digest", secret, "acceptance-credential", credentialSecret)
	if err != nil {
		return result, ErrAcceptance
	}
	registry, err := NewProviderExecutionRegistry()
	if err != nil {
		return result, ErrAcceptance
	}
	r.service = Service{Pool: r.app, Enabled: true, Key: secret, ContactURL: "https://invalid.invalid/legal/direitos", ExecutionCapabilities: ProductionExecutionCapabilities(), ObjectTargets: objects, ProviderTargets: providers, ProviderRegistry: registry}
	r.worker = ExecutionWorker{Pool: o.Worker, WorkerRef: f.worker, AcceptanceProof: f.proof, LeaseDuration: time.Minute, MaxAttempts: 5}
	r.completion = CompletionWorker{Pool: o.Worker, WorkerRef: f.worker, Key: secret, DetailBaseURL: "https://invalid.invalid/legal/direitos"}
	r.objects = ObjectExecutionWorker{Pool: o.Worker, Objects: acceptanceNoObjects{}, WorkerRef: f.worker, PrivateKey: private.Bytes(), TranscriptKeyID: "acceptance-object", TranscriptKey: secret}
	r.providers = ProviderExecutionWorker{Pool: o.Worker, Registry: registry, WorkerRef: f.worker, PrivateKey: private.Bytes(), TranscriptKeyID: "acceptance-provider", TranscriptKey: secret, CredentialDigestKeys: map[string][]byte{"acceptance-credential": credentialSecret}}
	r.tombstones = TombstoneExportWorker{Store: o.Worker, Ledger: o.Ledger, Protector: o.Protector, WorkerRef: f.worker}
	if err = r.bound(ctx); err != nil {
		return result, err
	}
	policy, err := r.service.Available(ctx)
	if err != nil || !policy.AccountClosureEnabled {
		return result, fmt.Errorf("acceptance policy: %w", errors.Join(ErrAcceptance, err))
	}
	policyJSON, _ := json.Marshal(policy)
	policyDigest := sha256.Sum256(policyJSON)
	r.evidence.PolicySHA256 = hex.EncodeToString(policyDigest[:])
	request, err := r.service.Submit(ctx, SubmitInput{ActorID: f.subject, SubjectID: f.subject, RequestKey: uuid.New(), CredentialVersion: 1, Password: passwordText, IP: "127.0.0.1", PolicyVersion: policy.Version, Scope: Scope{Kind: AccountClosure}})
	if err != nil {
		return result, fmt.Errorf("acceptance submit: %w", err)
	}
	for _, action := range []string{"claim", "verify", "approve"} {
		if err = r.bound(ctx); err != nil {
			return result, err
		}
		input := ReviewInput{ActorID: f.reviewer, Reference: request.PublicRef, Version: request.Version, Action: action}
		if action == "verify" {
			input.IdentityVerified = true
			input.IdentityMethod = "IN_PERSON"
			input.Explanation = "Generated synthetic acceptance fixture; no real person verified."
		}
		if action == "approve" {
			input.PolicyVersion = policy.Version
			input.Explanation = "Synthetic acceptance simulation under the active policy."
			input.Decisions = map[string]CategoryDecision{}
			for _, category := range policy.Categories {
				input.Decisions[category.Key] = CategoryDecision{Outcome: "APPROVE"}
			}
		}
		request, err = r.service.Change(ctx, input)
		if err != nil {
			return result, fmt.Errorf("acceptance %s: %w", action, err)
		}
	}
	if err = r.bound(ctx); err != nil {
		return result, err
	}
	execution, err := r.service.StartExecution(ctx, StartInput{ActorID: f.executor, Reference: request.PublicRef, Version: request.Version, Confirmed: true})
	if err != nil {
		return result, fmt.Errorf("acceptance start: %w", err)
	}
	r.execution = execution.ID
	if err = r.canary(ctx); err != nil {
		return result, err
	}
	if o.Mode == "canary-failure" {
		r.evidence.Outcome = "CANARY_VERIFIED"
		r.evidence.ObservedAt = time.Now().UTC()
		return r.evidence, nil
	}
	if err = r.complete(ctx); err != nil {
		return result, err
	}
	if err = r.bound(ctx); err != nil {
		return result, err
	}
	var marker, manifest []byte
	if err = o.Operator.QueryRow(ctx, `SELECT * FROM privacy_protected.acceptance_observe($1,$2)`, f.worker, f.proof).Scan(&marker, &manifest, &r.evidence.SimulatedNotices, &r.evidence.Checkpoints); err != nil || !hmac.Equal(marker, f.marker) || len(manifest) != 32 || r.evidence.Checkpoints < 1 {
		return result, ErrAcceptance
	}
	r.evidence.ManifestSHA256 = hex.EncodeToString(manifest)
	r.evidence.Outcome = "COMPLETED"
	r.evidence.ObservedAt = time.Now().UTC()
	if o.Mode != "run" {
		if err = r.event(ctx, "recovery"); err != nil {
			return result, err
		}
	}
	return r.evidence, nil
}

func (r *acceptanceRun) bound(ctx context.Context) error {
	row, err := dbgen.New(r.app).GetPrivacyActivation(ctx)
	if err != nil || row.ApprovalID == uuid.Nil || row.ApprovalID != r.approval || !row.Enabled || !row.FulfilmentReady {
		return fmt.Errorf("acceptance approval binding: %w", errors.Join(ErrAcceptance, err))
	}
	ready, err := r.completion.ActivationReady(ctx)
	if err != nil || !ready {
		return fmt.Errorf("acceptance worker readiness: %w", errors.Join(ErrAcceptance, err))
	}
	return nil
}
func (r *acceptanceRun) event(ctx context.Context, kind string) error {
	r.evidence.Conditions = append(r.evidence.Conditions, kind)
	if r.options.Event != nil {
		return r.options.Event(ctx, "privacy_acceptance_canary_"+kind+"_observed")
	}
	return nil
}
func acceptanceWait(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
func (r *acceptanceRun) canary(ctx context.Context) error {
	mode := r.options.Mode
	if mode == "run" {
		return nil
	}
	if err := r.bound(ctx); err != nil {
		return err
	}
	if mode == "canary-aged" {
		// Observe actual database time. No UPDATE of scheduling timestamps, global
		// queue operation or artificial clock is available to this runner.
		for {
			var aged bool
			err := r.options.Worker.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM privacy_erasure_category_jobs WHERE execution_id=$1 AND status='PENDING' AND updated_at<=clock_timestamp()-interval '15 minutes')`, r.execution).Scan(&aged)
			if err != nil {
				return ErrAcceptance
			}
			if aged {
				break
			}
			if err = r.bound(ctx); err != nil {
				return err
			}
			if err = acceptanceWait(ctx, 5*time.Second); err != nil {
				return err
			}
		}
		return r.event(ctx, "aged")
	}
	if mode == "canary-heartbeat" || mode == "canary-recovery" {
		r.worker.LeaseDuration = time.Second
		lease, err := r.worker.Claim(ctx)
		if err != nil {
			return err
		}
		if err = acceptanceWait(ctx, 1100*time.Millisecond); err != nil {
			return err
		}
		if _, err = r.worker.Heartbeat(ctx, lease); !errors.Is(err, ErrLeaseLost) {
			return ErrAcceptance
		}
		if err = r.event(ctx, "heartbeat_missing"); err != nil {
			return err
		}
		r.worker.LeaseDuration = time.Minute
		recovered, err := r.worker.Claim(ctx)
		if err != nil || recovered.Job.ID != lease.Job.ID || recovered.Job.LeaseEpoch <= lease.Job.LeaseEpoch {
			return ErrAcceptance
		}
		if err = r.perform(ctx, recovered); err != nil {
			return err
		}
		if mode == "canary-heartbeat" {
			return nil
		}
	}
	lease, err := r.worker.Claim(ctx)
	if err != nil {
		return err
	}
	failure := ExecutionFailure{Classification: FailureRetryable, Stage: FailureStageExecute, Code: FailureActionFailed}
	if mode == "canary-failure" {
		failure.Classification = FailureTerminal
	}
	execution, err := r.worker.FailJob(ctx, lease, failure)
	if err != nil {
		return err
	}
	if mode == "canary-failure" {
		if execution.Status != "TERMINAL_FAILED" {
			return ErrAcceptance
		}
		return r.event(ctx, "failure")
	}
	var status string
	if err = r.options.Worker.QueryRow(ctx, `SELECT status FROM privacy_erasure_category_jobs WHERE id=$1 AND execution_id=$2`, lease.Job.ID, r.execution).Scan(&status); err != nil || status != "RETRY_WAIT" {
		return ErrAcceptance
	}
	return r.event(ctx, "retry")
}
func (r *acceptanceRun) perform(ctx context.Context, lease ExecutionLease) error {
	if lease.Job.ExecutionID != r.execution || lease.Job.WorkerRef != r.fixture.worker {
		return ErrAcceptance
	}
	for _, checkpoint := range lease.Checkpoints {
		if checkpoint.Status == "SUCCEEDED" {
			continue
		}
		if err := r.bound(ctx); err != nil {
			return err
		}
		// Refresh immediately before each bounded operation; a lost lease stops the
		// fixture, and ordinary workers are unable to acquire it.
		if _, err := r.worker.Heartbeat(ctx, lease); err != nil {
			return err
		}
		operationCtx, cancel := context.WithTimeout(ctx, 40*time.Second)
		var err error
		switch checkpoint.OperationCode {
		case "OBJECT_VERSION_DELETE":
			_, err = r.objects.CompleteCheckpoint(operationCtx, lease)
		case "PROVIDER_RECIPIENT_NOTIFY":
			_, err = r.providers.CompleteCheckpoint(operationCtx, lease)
		case "BACKUP_TOMBSTONE_REPLAY":
			err = r.tombstones.Export(operationCtx, lease)
		default:
			_, err = r.worker.CompleteCheckpoint(operationCtx, lease, checkpoint.OperationCode, checkpoint.ActionVersion)
		}
		cancel()
		if err != nil {
			return fmt.Errorf("acceptance checkpoint %s: %w", checkpoint.OperationCode, err)
		}
	}
	if err := r.bound(ctx); err != nil {
		return err
	}
	_, err := r.worker.CompleteJob(ctx, lease)
	return err
}
func (r *acceptanceRun) complete(ctx context.Context) error {
	for {
		if err := r.bound(ctx); err != nil {
			return err
		}
		lease, err := r.worker.Claim(ctx)
		if errors.Is(err, pgx.ErrNoRows) {
			var status string
			if err = r.options.Worker.QueryRow(ctx, `SELECT status FROM privacy_erasure_executions WHERE id=$1`, r.execution).Scan(&status); err != nil {
				return ErrAcceptance
			}
			if status == "SUCCEEDED" {
				break
			}
			if status == "TERMINAL_FAILED" {
				return ErrAcceptance
			}
			if err = acceptanceWait(ctx, time.Second); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if err = r.perform(ctx, lease); err != nil {
			return err
		}
	}
	if err := r.bound(ctx); err != nil {
		return err
	}
	if err := r.tombstones.ExportClosure(ctx, r.execution); err != nil {
		return err
	}
	if err := r.bound(ctx); err != nil {
		return err
	}
	_, err := r.completion.Complete(ctx, r.execution)
	return err
}

type acceptanceNoObjects struct{}

func (acceptanceNoObjects) DeleteAllVersions(context.Context, string) (storage.VersionDeletionEvidence, error) {
	return storage.VersionDeletionEvidence{}, ErrAcceptance
}
