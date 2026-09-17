package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/cfcoimbra/mycfc/internal/db"
	"github.com/cfcoimbra/mycfc/internal/privacyrequests"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type activationBrokerDatabase interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Close()
}

var openActivationBrokerDatabase = func(ctx context.Context, databaseURL string) (activationBrokerDatabase, error) {
	return pgxpool.New(ctx, databaseURL)
}

func prepareApprovalMaterial(ctx context.Context, databaseURL string, release privacyrequests.ActivationReleaseBinding,
	sourceSHA, registrySHA256 string, registry privacyrequests.ActivationSignerRegistry, now time.Time) ([]byte, privacyrequests.ActivationApprovalMaterial, error) {
	pool, err := openActivationBrokerDatabase(ctx, databaseURL)
	if err != nil {
		return nil, privacyrequests.ActivationApprovalMaterial{}, errors.New("open privacy activation broker database")
	}
	defer pool.Close()
	var ids []uuid.UUID
	var setDigest, activationDigest, schemaDigest []byte
	var executor, plan, image string
	err = pool.QueryRow(ctx, `SELECT evidence_ids,evidence_set_sha256,activation_sha256,executor_version,plan_schema_version,image_digest,schema_migration_digest FROM privacy_activation_broker_material($1)`, release.PolicyVersion).
		Scan(&ids, &setDigest, &activationDigest, &executor, &plan, &image, &schemaDigest)
	if err != nil || executor != release.ExecutorVersion || plan != release.PlanSchemaVersion || image != release.ImageDigest ||
		hex.EncodeToString(schemaDigest) != release.SchemaMigrationDigest {
		return nil, privacyrequests.ActivationApprovalMaterial{}, errors.New("privacy activation broker material rejected")
	}
	raw, material, err := privacyrequests.GenerateActivationApprovalMaterial(privacyrequests.ActivationApprovalMaterial{
		SourceSHA: sourceSHA, PolicyVersion: release.PolicyVersion, EvidenceIDs: ids,
		EvidenceSetSHA256: hex.EncodeToString(setDigest), ActivationSHA256: hex.EncodeToString(activationDigest),
		ExecutorVersion: executor, PlanSchemaVersion: plan, ImageDigest: image,
		SchemaMigrationDigest: hex.EncodeToString(schemaDigest), SignerRegistrySHA256: registrySHA256,
	}, now)
	if err != nil {
		return nil, privacyrequests.ActivationApprovalMaterial{}, err
	}
	materialDigest := sha256.Sum256(raw)
	registryDigest, _ := hex.DecodeString(registrySHA256)
	executorSigner := registry.Signers.Executor
	administratorSigner := registry.Signers.Administrator
	executorPublicDigest, _ := hex.DecodeString(executorSigner.PublicKeySPKI256)
	administratorPublicDigest, _ := hex.DecodeString(administratorSigner.PublicKeySPKI256)
	var registered uuid.UUID
	err = pool.QueryRow(ctx, `SELECT privacy_activation_broker_register_ceremony(
		$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15::jsonb,$16,$17,
		$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29)`,
		material.CeremonyID, material.ProposalID, material.SourceSHA, material.PolicyVersion, material.EvidenceIDs,
		setDigest, activationDigest, material.ExecutorVersion, material.PlanSchemaVersion, material.ImageDigest, schemaDigest,
		registryDigest, materialDigest[:], raw, string(raw), material.PreparedAt, material.CeremonyExpiresAt,
		executorSigner.ActorRef, executorSigner.SigningKeyID, executorSigner.KMSKeyARN, executorPublicDigest,
		int64(executorSigner.GitHubActorID), executorSigner.GitHubEnvironment,
		administratorSigner.ActorRef, administratorSigner.SigningKeyID, administratorSigner.KMSKeyARN, administratorPublicDigest,
		int64(administratorSigner.GitHubActorID), administratorSigner.GitHubEnvironment).Scan(&registered)
	if err != nil || registered != material.CeremonyID {
		return nil, privacyrequests.ActivationApprovalMaterial{}, errors.New("privacy activation ceremony registration rejected")
	}
	return raw, material, nil
}

func activateApprovedMaterial(ctx context.Context, databaseURL string, materialRaw []byte,
	material privacyrequests.ActivationApprovalMaterial, registry privacyrequests.ActivationSignerRegistry,
	executorRaw, administratorRaw, executorPublic, administratorPublic []byte, now time.Time) error {
	bundle, err := privacyrequests.VerifyActivationApprovalBundle(materialRaw, material, registry, executorRaw, administratorRaw,
		executorPublic, administratorPublic, now)
	if err != nil {
		return err
	}
	executorNonceDigest := sha256.Sum256(bundle.ExecutorNonce)
	administratorNonceDigest := sha256.Sum256(bundle.AdminNonce)
	pool, err := openActivationBrokerDatabase(ctx, databaseURL)
	if err != nil {
		return errors.New("open privacy activation broker database")
	}
	defer pool.Close()
	var approvalID uuid.UUID
	err = pool.QueryRow(ctx, `SELECT privacy_activation_broker_activate($1,$2,$3::jsonb,$4,$5::jsonb,$6,$7)`,
		material.CeremonyID, executorRaw, string(executorRaw), administratorRaw, string(administratorRaw),
		executorNonceDigest[:], administratorNonceDigest[:]).Scan(&approvalID)
	if err != nil || approvalID == uuid.Nil {
		return errors.New("privacy activation broker rejected approvals")
	}
	return nil
}

func loadSignerRegistry(path, pinnedSHA256 string) (privacyrequests.ActivationSignerRegistry, error) {
	raw, err := readSecret(path)
	if err != nil {
		return privacyrequests.ActivationSignerRegistry{}, err
	}
	registry, err := privacyrequests.ParseActivationSignerRegistry(raw, strings.TrimSpace(pinnedSHA256))
	if err != nil {
		return privacyrequests.ActivationSignerRegistry{}, err
	}
	return registry, nil
}

func writeExclusive(path string, payload []byte) error {
	path = strings.TrimSpace(path)
	if path == "" || len(payload) == 0 {
		return errors.New("privacy activation output rejected")
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return errors.New("privacy activation output unavailable")
	}
	remove := true
	defer func() {
		_ = file.Close()
		if remove {
			_ = os.Remove(path)
		}
	}()
	if _, err = file.Write(payload); err != nil || file.Sync() != nil {
		return errors.New("privacy activation output unavailable")
	}
	remove = false
	return nil
}

func trustedRelease(getenv func(string) string) (privacyrequests.ActivationReleaseBinding, error) {
	release := privacyrequests.ActivationReleaseBinding{
		PolicyVersion:   strings.TrimSpace(getenv("PRIVACY_ACTIVATION_POLICY_VERSION")),
		ExecutorVersion: privacyrequests.SupportedExecutorVersion, PlanSchemaVersion: privacyrequests.SupportedPlanSchemaVersion,
		ImageDigest:           strings.TrimSpace(getenv("PRIVACY_ACTIVATION_CURRENT_IMAGE_DIGEST")),
		SchemaMigrationDigest: db.EmbeddedMigrationDigest(),
	}
	if release.PolicyVersion == "" || len(release.ImageDigest) != 71 || !strings.HasPrefix(release.ImageDigest, "sha256:") {
		return release, errors.New("privacy activation current release rejected")
	}
	return release, nil
}
