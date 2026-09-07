package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/cfcoimbra/mycfc/internal/activity"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/internal/polar"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrPolarIdentityConflict = errors.New("Polar identity is already connected")
	ErrPolarSyncInProgress   = errors.New("Polar sync is already in progress")
	ErrPolarSyncCancelled    = errors.New("Polar sync was cancelled")
	ErrPolarSyncTooSoon      = errors.New("Polar sync was requested too recently")
	ErrPolarOAuthStale       = errors.New("Polar OAuth initiation is stale")
)

type PolarConnection struct {
	Status               string
	Version              int64
	LastSuccessfulSyncAt *time.Time
	LastErrorCode        string
	CredentialExpiresAt  *time.Time
}

type PolarIntegrationService interface {
	Connection(context.Context, uuid.UUID) (PolarConnection, error)
	Connect(context.Context, uuid.UUID, polar.Credentials, string, int64) error
	Sync(context.Context, uuid.UUID) error
	Disconnect(context.Context, uuid.UUID) error
}

type PostgresPolarIntegrationService struct {
	Pool   *pgxpool.Pool
	Client *polar.Client
	Vault  activity.CredentialVault
	Now    func() time.Time
}

func (s PostgresPolarIntegrationService) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s PostgresPolarIntegrationService) Connection(ctx context.Context, userID uuid.UUID) (PolarConnection, error) {
	connection, err := dbgen.New(s.Pool).GetActivityConnectionForUser(ctx, dbgen.GetActivityConnectionForUserParams{UserID: userID, Provider: string(polar.ProviderCode)})
	if err != nil {
		return PolarConnection{}, err
	}
	result := PolarConnection{Status: connection.Status, Version: connection.CredentialVersion}
	if connection.LastSuccessfulSyncAt.Valid {
		value := connection.LastSuccessfulSyncAt.Time
		result.LastSuccessfulSyncAt = &value
	}
	if connection.CredentialExpiresAt.Valid {
		value := connection.CredentialExpiresAt.Time
		result.CredentialExpiresAt = &value
	}
	if connection.LastErrorCode != nil {
		result.LastErrorCode = *connection.LastErrorCode
	}
	return result, nil
}

func (s PostgresPolarIntegrationService) Connect(ctx context.Context, userID uuid.UUID, credentials polar.Credentials, memberID string, expectedVersion int64) error {
	queries := dbgen.New(s.Pool)
	remoteID := polarRemoteID(credentials)
	existing, existingErr := queries.GetActivityConnectionForUser(ctx, dbgen.GetActivityConnectionForUserParams{UserID: userID, Provider: string(polar.ProviderCode)})
	if existingErr != nil && !errors.Is(existingErr, pgx.ErrNoRows) {
		return existingErr
	}
	if (expectedVersion == 0 && existingErr == nil) || (expectedVersion > 0 && (existingErr != nil || existing.CredentialVersion != expectedVersion)) {
		return ErrPolarOAuthStale
	}
	if existingErr == nil && !polarConnectionAcceptsIdentity(existing, remoteID) {
		return ErrPolarIdentityConflict
	}
	owner, ownerErr := queries.GetActivityConnectionByProviderIdentity(ctx, dbgen.GetActivityConnectionByProviderIdentityParams{Provider: string(polar.ProviderCode), ProviderUserID: remoteID})
	if ownerErr != nil && !errors.Is(ownerErr, pgx.ErrNoRows) {
		return ownerErr
	}
	if ownerErr == nil && owner.UserID != userID {
		return ErrPolarIdentityConflict
	}

	registered := existingErr != nil || existing.ProviderUserID != remoteID || existing.Status == "DISCONNECTED"
	if registered {
		if err := s.Client.RegisterUser(ctx, credentials, memberID); err != nil {
			return err
		}
	}
	cleanup := func() {
		if !registered {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		_ = s.Client.DeregisterUser(cleanupCtx, credentials)
	}
	secret, err := credentials.Secret()
	if err != nil {
		cleanup()
		return err
	}
	sealed, err := s.Vault.Seal(ctx, polar.ProviderCode, userID.String(), secret)
	if err != nil {
		cleanup()
		return err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		cleanup()
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	abort := func(err error) error {
		_ = tx.Rollback(context.WithoutCancel(ctx))
		cleanup()
		return err
	}
	txQueries := dbgen.New(tx)
	locked, lockedErr := txQueries.GetActivityConnectionForUserForUpdate(ctx, dbgen.GetActivityConnectionForUserForUpdateParams{UserID: userID, Provider: string(polar.ProviderCode)})
	if expectedVersion == 0 {
		if lockedErr == nil {
			return abort(ErrPolarOAuthStale)
		}
		if !errors.Is(lockedErr, pgx.ErrNoRows) {
			return abort(lockedErr)
		}
	} else if lockedErr != nil || locked.CredentialVersion != expectedVersion || !polarConnectionAcceptsIdentity(locked, remoteID) {
		return abort(ErrPolarOAuthStale)
	}
	owner, ownerErr = txQueries.GetActivityConnectionByProviderIdentity(ctx, dbgen.GetActivityConnectionByProviderIdentityParams{Provider: string(polar.ProviderCode), ProviderUserID: remoteID})
	if ownerErr != nil && !errors.Is(ownerErr, pgx.ErrNoRows) {
		return abort(ownerErr)
	}
	if ownerErr == nil && owner.UserID != userID {
		return abort(ErrPolarIdentityConflict)
	}
	keyID := sealed.KeyID
	_, err = txQueries.UpsertActivityConnection(ctx, dbgen.UpsertActivityConnectionParams{
		UserID: userID, Provider: string(polar.ProviderCode), ProviderUserID: remoteID,
		CredentialsCiphertext: sealed.Ciphertext, CredentialKeyID: &keyID,
		CredentialExpiresAt: pgtype.Timestamptz{Time: credentials.ExpiresAt, Valid: true},
		Scopes:              []string{polar.AuthorizationScope},
	})
	if isUniqueViolation(err) {
		return abort(ErrPolarIdentityConflict)
	}
	if err != nil {
		return abort(err)
	}
	if err := tx.Commit(ctx); err != nil {
		cleanup()
		return err
	}
	return nil
}

func (s PostgresPolarIntegrationService) Sync(ctx context.Context, userID uuid.UUID) error {
	queries := dbgen.New(s.Pool)
	connection, err := queries.GetActivityConnectionForUser(ctx, dbgen.GetActivityConnectionForUserParams{UserID: userID, Provider: string(polar.ProviderCode)})
	if err != nil {
		return err
	}
	if connection.Status != "ACTIVE" || connection.CredentialKeyID == nil || len(connection.CredentialsCiphertext) == 0 {
		return &polar.ProviderError{Kind: polar.ErrorReauthorization}
	}
	if connection.LastSuccessfulSyncAt.Valid {
		age := s.now().Sub(connection.LastSuccessfulSyncAt.Time)
		if age >= 0 && age < time.Minute {
			return ErrPolarSyncTooSoon
		}
	}
	if connection.LastErrorAt.Valid {
		age := s.now().Sub(connection.LastErrorAt.Time)
		if age >= 0 && age < 30*time.Second {
			return ErrPolarSyncTooSoon
		}
	}
	if connection.ProviderRetryAfter.Valid && s.now().Before(connection.ProviderRetryAfter.Time) {
		return ErrPolarSyncTooSoon
	}
	staleBefore := s.now().Add(-5 * time.Minute)
	_, _ = queries.CancelStaleActivitySyncJobs(ctx, dbgen.CancelStaleActivitySyncJobsParams{CancelledAt: timestamp(s.now()), ConnectionID: connection.ID, UserID: userID, Provider: string(polar.ProviderCode), StaleBefore: timestamp(staleBefore)})
	job, err := queries.CreateActivitySyncJob(ctx, dbgen.CreateActivitySyncJobParams{IdempotencyKey: uuid.New(), ConnectionID: connection.ID, Reason: "MANUAL"})
	if isUniqueViolation(err) {
		return ErrPolarSyncInProgress
	}
	if err != nil {
		return err
	}
	startedAt := s.now()
	_, err = queries.StartActivitySyncJob(ctx, dbgen.StartActivitySyncJobParams{StartedAt: pgtype.Timestamptz{Time: startedAt, Valid: true}, ID: job.ID, ConnectionID: connection.ID, UserID: userID, Provider: string(polar.ProviderCode), ExpectedCredentialVersion: connection.CredentialVersion})
	if err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_, _ = queries.CancelActivitySyncJob(cleanupCtx, dbgen.CancelActivitySyncJobParams{CancelledAt: timestamp(s.now()), ID: job.ID, ConnectionID: connection.ID, UserID: userID, Provider: string(polar.ProviderCode), ExpectedCredentialVersion: connection.CredentialVersion})
		return ErrPolarSyncCancelled
	}
	secret, err := s.Vault.Open(ctx, polar.ProviderCode, userID.String(), activity.SealedCredentials{Ciphertext: connection.CredentialsCiphertext, KeyID: *connection.CredentialKeyID})
	if err != nil {
		return s.failSync(ctx, connection, job.ID, "credential_unavailable", false, err)
	}
	page, err := (polar.Adapter{Client: s.Client}).SyncRecent(ctx, secret, activity.SyncRequest{Since: startedAt.AddDate(0, 0, -30), Until: startedAt.Add(time.Second), Limit: 500})
	if err != nil {
		kind := polar.ErrorKindOf(err)
		return s.failSync(ctx, connection, job.ID, string(kind), kind == polar.ErrorReauthorization || kind == polar.ErrorConsent, err)
	}
	if err := s.persistSync(ctx, connection, job.ID, page, startedAt); err != nil {
		return s.failSync(ctx, connection, job.ID, "persistence_failed", false, err)
	}
	return nil
}

func (s PostgresPolarIntegrationService) persistSync(ctx context.Context, connection dbgen.ActivityConnection, jobID uuid.UUID, page activity.SyncPage, completedAt time.Time) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	queries := dbgen.New(tx)
	guard := func(err error) error {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrPolarSyncCancelled
		}
		return err
	}
	for _, item := range page.Activities {
		params := dbgen.UpsertSyncedActivityParams{ConnectionID: connection.ID, UserID: connection.UserID, Provider: connection.Provider, ProviderActivityID: item.ProviderActivityID, StartsAt: timestamp(item.StartsAt), EndsAt: timestamp(item.EndsAt), Sport: item.Sport, NormalizedSport: item.NormalizedSport, DurationSeconds: int32(item.DurationSeconds), DistanceMetres: item.DistanceMetres, AverageHeartRate: item.AverageHeartRate, MaximumHeartRate: item.MaximumHeartRate, ProviderMetrics: item.ProviderMetricsJSON, RawSummary: item.RawSummaryJSON, PayloadSha256: item.PayloadSHA256[:], NormalizationVersion: int32(item.NormalizationVersion), ExpectedCredentialVersion: connection.CredentialVersion, SyncJobID: jobID}
		if item.ProviderUpdatedAt != nil {
			params.ProviderUpdatedAt = timestamp(*item.ProviderUpdatedAt)
		}
		if item.MovingSeconds != nil {
			value := int32(*item.MovingSeconds)
			params.MovingDurationSeconds = &value
		}
		if item.DeletedAt != nil {
			params.DeletedAt = timestamp(*item.DeletedAt)
		}
		if _, err := queries.UpsertSyncedActivity(ctx, params); err != nil {
			return guard(err)
		}
	}
	var oldest *time.Time
	for _, item := range page.LoadObservations {
		availability := "AVAILABLE"
		if item.AvailabilityStatus == "LOAD_STATUS_NOT_AVAILABLE" {
			availability = "UNAVAILABLE"
		}
		shortWindow, longWindow := int16(7), int16(28)
		if item.Strain7Days == nil {
			shortWindow = 0
		}
		if item.Tolerance28Days == nil {
			longWindow = 0
		}
		payload, _ := json.Marshal(struct {
			Date      string          `json:"date"`
			Status    string          `json:"status"`
			Load      *float64        `json:"load"`
			Strain    *float64        `json:"strain_7_days"`
			Tolerance *float64        `json:"tolerance_28_days"`
			Ratio     *float64        `json:"ratio"`
			Metrics   json.RawMessage `json:"metrics"`
		}{item.ObservedOn.Format("2006-01-02"), item.AvailabilityStatus, item.LoadValue, item.Strain7Days, item.Tolerance28Days, item.Ratio, item.ProviderMetricsJSON})
		hash := sha256.Sum256(payload)
		params := dbgen.UpsertActivityLoadObservationParams{LoadKind: item.Method, ObservedOn: pgtype.Date{Time: item.ObservedOn, Valid: true}, Availability: availability, ProviderStatus: item.AvailabilityStatus, LoadValue: item.LoadValue, ShortTermLoad: item.Strain7Days, LongTermLoad: item.Tolerance28Days, LoadRatio: item.Ratio, ProviderMetrics: item.ProviderMetricsJSON, PayloadSha256: hash[:], FetchedAt: timestamp(item.FetchedAt), ConnectionID: connection.ID, UserID: connection.UserID, Provider: connection.Provider, ExpectedCredentialVersion: connection.CredentialVersion, SyncJobID: jobID}
		if shortWindow > 0 {
			params.ShortTermWindowDays = &shortWindow
		}
		if longWindow > 0 {
			params.LongTermWindowDays = &longWindow
		}
		if item.SourceUpdatedAt != nil {
			params.SourceUpdatedAt = timestamp(*item.SourceUpdatedAt)
		}
		if _, err := queries.UpsertActivityLoadObservation(ctx, params); err != nil {
			return guard(err)
		}
		if oldest == nil || item.ObservedOn.Before(*oldest) {
			value := item.ObservedOn
			oldest = &value
		}
	}
	if page.Complete && oldest != nil {
		_, err := queries.PruneActivityLoadObservationsBefore(ctx, dbgen.PruneActivityLoadObservationsBeforeParams{ConnectionID: connection.ID, UserID: connection.UserID, Provider: connection.Provider, ExpectedCredentialVersion: connection.CredentialVersion, SyncJobID: jobID, LoadKind: "polar_cardio_load_trimp", OldestDate: pgtype.Date{Time: *oldest, Valid: true}})
		if err != nil {
			return err
		}
	}
	if _, err := queries.RecordActivityConnectionSyncSuccess(ctx, dbgen.RecordActivityConnectionSyncSuccessParams{SucceededAt: timestamp(completedAt), ID: connection.ID, UserID: connection.UserID, Provider: connection.Provider, ExpectedCredentialVersion: connection.CredentialVersion}); err != nil {
		return guard(err)
	}
	if _, err := queries.CompleteActivitySyncJob(ctx, dbgen.CompleteActivitySyncJobParams{FinishedAt: timestamp(completedAt), ID: jobID, ConnectionID: connection.ID, UserID: connection.UserID, Provider: connection.Provider, ExpectedCredentialVersion: connection.CredentialVersion}); err != nil {
		return guard(err)
	}
	return tx.Commit(ctx)
}

func (s PostgresPolarIntegrationService) failSync(ctx context.Context, connection dbgen.ActivityConnection, jobID uuid.UUID, code string, reauthorize bool, source error) error {
	now := s.now()
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	tx, err := s.Pool.Begin(cleanupCtx)
	if err != nil {
		return source
	}
	defer tx.Rollback(cleanupCtx)
	queries := dbgen.New(tx)
	message := "A sincronização com a Polar não foi concluída."
	_, _ = queries.FailActivitySyncJob(cleanupCtx, dbgen.FailActivitySyncJobParams{ErrorCode: &code, ErrorMessage: &message, FinishedAt: timestamp(now), ID: jobID, ConnectionID: connection.ID, UserID: connection.UserID, Provider: connection.Provider, ExpectedCredentialVersion: connection.CredentialVersion})
	retryAfter := pgtype.Timestamptz{}
	if retry := polar.RetryAfterOf(source); retry != nil {
		retryAfter = timestamp(*retry)
	}
	_, _ = queries.RecordActivityConnectionError(cleanupCtx, dbgen.RecordActivityConnectionErrorParams{RequiresReauthorization: reauthorize, ErrorCode: &code, ErrorMessage: &message, FailedAt: timestamp(now), ProviderRetryAfter: retryAfter, ID: connection.ID, UserID: connection.UserID, Provider: connection.Provider, ExpectedCredentialVersion: connection.CredentialVersion})
	_ = tx.Commit(cleanupCtx)
	return source
}

func (s PostgresPolarIntegrationService) Disconnect(ctx context.Context, userID uuid.UUID) error {
	queries := dbgen.New(s.Pool)
	connection, err := queries.GetActivityConnectionForUser(ctx, dbgen.GetActivityConnectionForUserParams{UserID: userID, Provider: string(polar.ProviderCode)})
	if err != nil {
		return err
	}
	var remoteErr error
	if connection.CredentialKeyID != nil && len(connection.CredentialsCiphertext) > 0 {
		secret, openErr := s.Vault.Open(ctx, polar.ProviderCode, userID.String(), activity.SealedCredentials{Ciphertext: connection.CredentialsCiphertext, KeyID: *connection.CredentialKeyID})
		if openErr == nil {
			remoteErr = (polar.Adapter{Client: s.Client}).Disconnect(ctx, secret)
		} else {
			remoteErr = openErr
		}
	}
	_, localErr := queries.DisconnectActivityConnection(ctx, dbgen.DisconnectActivityConnectionParams{DisconnectedAt: timestamp(s.now()), ID: connection.ID, UserID: userID})
	if localErr != nil {
		return localErr
	}
	return remoteErr
}

func polarRemoteID(credentials polar.Credentials) string {
	return strconv.FormatInt(credentials.RemoteUserID, 10)
}

func polarConnectionAcceptsIdentity(connection dbgen.ActivityConnection, remoteID string) bool {
	return connection.ProviderUserID == remoteID
}

func timestamp(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value.UTC(), Valid: true}
}
