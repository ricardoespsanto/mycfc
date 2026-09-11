package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/cfcoimbra/mycfc/internal/db"
	"github.com/cfcoimbra/mycfc/internal/privacyrequests"
)

const (
	maximumSourceBytes = 32 << 20
	terraformVersion   = "1.15.8"
)

type commonOptions struct {
	imageDigest, policyVersion, observedAt, signingKeyID string
	evidenceRef, evidenceFile, evidenceHead, kmsKeyARN   string
	privateKeyFile, output                               string
}

type stateDocument struct {
	Version          int    `json:"version"`
	TerraformVersion string `json:"terraform_version"`
	Serial           uint64 `json:"serial"`
	Lineage          string `json:"lineage"`
}

type planDocument struct {
	FormatVersion    string                  `json:"format_version"`
	TerraformVersion string                  `json:"terraform_version"`
	Complete         bool                    `json:"complete"`
	Errored          bool                    `json:"errored"`
	Variables        map[string]planVariable `json:"variables"`
	ResourceChanges  []resourceChange        `json:"resource_changes"`
	ResourceDrift    []resourceChange        `json:"resource_drift"`
}

type planVariable struct {
	Value json.RawMessage `json:"value"`
}

type resourceChange struct {
	Change struct {
		Actions []string `json:"actions"`
	} `json:"change"`
}

type evidenceHead struct {
	VersionID            string `json:"VersionId"`
	ContentLength        int64  `json:"ContentLength"`
	ChecksumSHA256       string `json:"ChecksumSHA256"`
	ServerSideEncryption string `json:"ServerSideEncryption"`
	SSEKMSKeyID          string `json:"SSEKMSKeyId"`
}

type infrastructureObservation struct {
	Contract                     string `json:"contract"`
	ImageDigest                  string `json:"image_digest"`
	ProductionStateSerial        uint64 `json:"production_state_serial"`
	HetznerStateSerial           uint64 `json:"hetzner_state_serial"`
	ProductionStateSHA256        string `json:"production_state_sha256"`
	HetznerStateSHA256           string `json:"hetzner_state_sha256"`
	ProductionPlanSHA256         string `json:"production_plan_sha256"`
	HetznerPlanSHA256            string `json:"hetzner_plan_sha256"`
	WorkerIdentityEnabled        bool   `json:"worker_identity_enabled"`
	S3VersionDeletionEnabled     bool   `json:"s3_version_deletion_enabled"`
	LedgerBrokerInvokeEnabled    bool   `json:"ledger_broker_invoke_enabled"`
	WorkerMonitoringEnabled      bool   `json:"worker_monitoring_enabled"`
	RestoreInfrastructureEnabled bool   `json:"restore_infrastructure_enabled"`
	RestoreLedgerWriteEnabled    bool   `json:"restore_ledger_write_enabled"`
}

type providerSource struct {
	Contract       string            `json:"contract"`
	InventoryScope string            `json:"inventory_scope"`
	InventoryState string            `json:"inventory_state"`
	Providers      []json.RawMessage `json:"providers"`
}

type providerObservation struct {
	Contract               string `json:"contract"`
	Result                 string `json:"result"`
	RegistryState          string `json:"registry_state"`
	RegistrationCount      int64  `json:"registration_count"`
	ProviderRegistrySHA256 string `json:"provider_registry_sha256"`
	InventoryContract      string `json:"inventory_contract"`
}

type schemaObservation struct {
	Contract              string `json:"contract"`
	SchemaMigrationDigest string `json:"schema_migration_digest"`
	BaselineThrough       string `json:"baseline_includes_through"`
	MigrationCount        int    `json:"migration_count"`
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "privacy_activation_artifact_failed")
		os.Exit(1)
	}
}

func run(args []string, output io.Writer) error {
	if len(args) == 0 {
		return errors.New("artifact command required")
	}
	switch args[0] {
	case "infrastructure-observe", "infrastructure-sign":
		return runInfrastructure(args[0], args[1:], output)
	case "provider-observe", "provider-sign":
		return runProvider(args[0], args[1:], output)
	case "schema-observe", "schema-sign":
		return runSchema(args[0], args[1:], output)
	default:
		return errors.New("artifact command rejected")
	}
}

func runInfrastructure(command string, args []string, output io.Writer) error {
	set := flag.NewFlagSet(command, flag.ContinueOnError)
	set.SetOutput(io.Discard)
	common := addCommonFlags(set, command == "infrastructure-sign")
	productionState := set.String("production-state", "", "")
	hetznerState := set.String("hetzner-state", "", "")
	productionPlan := set.String("production-plan", "", "")
	hetznerPlan := set.String("hetzner-plan", "", "")
	if set.Parse(args) != nil || set.NArg() != 0 {
		return errors.New("infrastructure evidence arguments rejected")
	}
	observation, err := observeInfrastructure(*common, *productionState, *hetznerState, *productionPlan, *hetznerPlan)
	if err != nil {
		return err
	}
	if command == "infrastructure-observe" {
		return writeCanonical(*common, observation, output)
	}
	return signArtifact(*common, observation, infrastructureArtifact(*common, observation), output)
}

func runProvider(command string, args []string, output io.Writer) error {
	set := flag.NewFlagSet(command, flag.ContinueOnError)
	set.SetOutput(io.Discard)
	common := addCommonFlags(set, command == "provider-sign")
	registry := set.String("registry", "", "")
	if set.Parse(args) != nil || set.NArg() != 0 {
		return errors.New("provider evidence arguments rejected")
	}
	observation, err := observeProvider(*registry)
	if err != nil {
		return err
	}
	if command == "provider-observe" {
		return writeCanonical(*common, observation, output)
	}
	return signArtifact(*common, observation, providerArtifact(*common, observation), output)
}

func runSchema(command string, args []string, output io.Writer) error {
	set := flag.NewFlagSet(command, flag.ContinueOnError)
	set.SetOutput(io.Discard)
	common := addCommonFlags(set, command == "schema-sign")
	versions := set.String("schema-versions", "", "")
	if set.Parse(args) != nil || set.NArg() != 0 {
		return errors.New("schema evidence arguments rejected")
	}
	observation, err := observeSchema(*versions)
	if err != nil {
		return err
	}
	if command == "schema-observe" {
		return writeCanonical(*common, observation, output)
	}
	return signArtifact(*common, observation, schemaArtifact(*common, observation), output)
}

func addCommonFlags(set *flag.FlagSet, signing bool) *commonOptions {
	options := &commonOptions{}
	set.StringVar(&options.imageDigest, "image-digest", "", "")
	set.StringVar(&options.policyVersion, "policy-version", "", "")
	set.StringVar(&options.observedAt, "observed-at", "", "")
	set.StringVar(&options.signingKeyID, "signing-key-id", "", "")
	set.StringVar(&options.evidenceRef, "evidence-ref", "", "")
	set.StringVar(&options.evidenceFile, "evidence-file", "", "")
	set.StringVar(&options.evidenceHead, "evidence-head", "", "")
	set.StringVar(&options.kmsKeyARN, "kms-key-arn", "", "")
	set.StringVar(&options.privateKeyFile, "private-key-file", "", "")
	set.StringVar(&options.output, "output", "", "")
	if !signing {
		options.evidenceRef = "observe"
	}
	return options
}

func observeInfrastructure(common commonOptions, productionStatePath, hetznerStatePath, productionPlanPath, hetznerPlanPath string) (infrastructureObservation, error) {
	if !validImageDigest(common.imageDigest) {
		return infrastructureObservation{}, errors.New("selected image digest rejected")
	}
	productionStateBytes, productionState, err := loadState(productionStatePath)
	if err != nil {
		return infrastructureObservation{}, err
	}
	hetznerStateBytes, hetznerState, err := loadState(hetznerStatePath)
	if err != nil {
		return infrastructureObservation{}, err
	}
	productionPlanBytes, productionPlan, err := loadPlan(productionPlanPath)
	if err != nil {
		return infrastructureObservation{}, err
	}
	hetznerPlanBytes, hetznerPlan, err := loadPlan(hetznerPlanPath)
	if err != nil {
		return infrastructureObservation{}, err
	}
	productionGates := []string{"privacy_worker_infrastructure_enabled", "privacy_worker_s3_deletion_enabled", "privacy_worker_ledger_broker_invoke_enabled", "privacy_worker_monitoring_enabled"}
	hetznerGates := []string{"privacy_restore_infrastructure_enabled", "privacy_restore_ledger_write_enabled"}
	for _, gate := range productionGates {
		if !planBool(productionPlan, gate) {
			return infrastructureObservation{}, errors.New("production capability plan is not ready")
		}
	}
	for _, gate := range hetznerGates {
		if !planBool(hetznerPlan, gate) {
			return infrastructureObservation{}, errors.New("restore capability plan is not ready")
		}
	}
	if planString(productionPlan, "image_digest") != common.imageDigest {
		return infrastructureObservation{}, errors.New("production plan image digest mismatch")
	}
	return infrastructureObservation{
		Contract: "mycfc/privacy-infrastructure-observation/v1", ImageDigest: common.imageDigest,
		ProductionStateSerial: productionState.Serial, HetznerStateSerial: hetznerState.Serial,
		ProductionStateSHA256: digest(productionStateBytes), HetznerStateSHA256: digest(hetznerStateBytes),
		ProductionPlanSHA256: digest(productionPlanBytes), HetznerPlanSHA256: digest(hetznerPlanBytes),
		WorkerIdentityEnabled: true, S3VersionDeletionEnabled: true, LedgerBrokerInvokeEnabled: true,
		WorkerMonitoringEnabled: true, RestoreInfrastructureEnabled: true, RestoreLedgerWriteEnabled: true,
	}, nil
}

func observeProvider(path string) (providerObservation, error) {
	payload, err := readBounded(path, maximumSourceBytes)
	if err != nil {
		return providerObservation{}, err
	}
	var source providerSource
	if err = decodeExact(payload, &source); err != nil || source.Contract != "mycfc/privacy-provider-registry-source/v2" ||
		source.InventoryScope != "SUBJECT_SPECIFIC_EXTERNAL_INTEGRATIONS" || source.InventoryState != "COMPLETE" ||
		source.Providers == nil || len(source.Providers) != 0 {
		return providerObservation{}, errors.New("provider registry source rejected")
	}
	return providerObservation{Contract: "mycfc/privacy-provider-observation/v2", Result: "READY", RegistryState: "READY",
		RegistrationCount: 0, ProviderRegistrySHA256: digest(payload), InventoryContract: source.Contract}, nil
}

func observeSchema(versionsPath string) (schemaObservation, error) {
	payload, err := readBounded(versionsPath, maximumSourceBytes)
	if err != nil {
		return schemaObservation{}, err
	}
	if bytes.Contains(payload, []byte{'\r'}) || len(payload) == 0 || payload[len(payload)-1] == '\n' {
		return schemaObservation{}, errors.New("schema version inventory is not canonical")
	}
	versions := strings.Split(string(payload), "\n")
	want := db.EmbeddedMigrationInventory()
	if len(want) < 2 || strings.Join(versions, "\n") != strings.Join(want, "\n") || digest(payload) != db.EmbeddedMigrationDigest() {
		return schemaObservation{}, errors.New("schema version inventory rejected")
	}
	const baselineThrough = "202609110003_privacy_activation_emergency_fence"
	if !containsExact(versions, "reset-baseline-v1") || !containsExact(versions, baselineThrough) {
		return schemaObservation{}, errors.New("schema version inventory mismatch")
	}
	return schemaObservation{Contract: "mycfc/schema-migration-observation/v1", SchemaMigrationDigest: digest(payload),
		BaselineThrough: baselineThrough, MigrationCount: len(want) - 1}, nil
}

func containsExact(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func signArtifact(common commonOptions, observation, artifact any, output io.Writer) error {
	if err := validateCommon(common); err != nil {
		return err
	}
	canonicalObservation, err := json.Marshal(observation)
	if err != nil {
		return errors.New("canonical evidence rejected")
	}
	stored, err := readBounded(common.evidenceFile, maximumSourceBytes)
	if err != nil || !bytes.Equal(stored, append(canonicalObservation, '\n')) {
		return errors.New("stored evidence does not match source observation")
	}
	evidenceSHA, err := verifyEvidenceObject(common, stored)
	if err != nil {
		return err
	}
	artifactObject, err := objectMap(artifact)
	if err != nil {
		return err
	}
	artifactObject["evidence_sha256"] = evidenceSHA
	privateKey, err := loadPrivateKey(common.privateKeyFile)
	if err != nil {
		return err
	}
	canonical, err := json.Marshal(artifactObject)
	if err != nil {
		return errors.New("activation artifact rejected")
	}
	artifactObject["signature_ed25519"] = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, canonical))
	return writeCanonical(common, artifactObject, output)
}

func infrastructureArtifact(common commonOptions, observation infrastructureObservation) map[string]any {
	artifact := envelope(common, "mycfc/privacy-infrastructure-posture/v1", "SUCCEEDED")
	artifact["production_state_serial"] = observation.ProductionStateSerial
	artifact["hetzner_state_serial"] = observation.HetznerStateSerial
	artifact["production_state_sha256"] = observation.ProductionStateSHA256
	artifact["hetzner_state_sha256"] = observation.HetznerStateSHA256
	artifact["production_plan_sha256"] = observation.ProductionPlanSHA256
	artifact["hetzner_plan_sha256"] = observation.HetznerPlanSHA256
	artifact["worker_identity_enabled"] = observation.WorkerIdentityEnabled
	artifact["s3_version_deletion_enabled"] = observation.S3VersionDeletionEnabled
	artifact["ledger_broker_invoke_enabled"] = observation.LedgerBrokerInvokeEnabled
	artifact["worker_monitoring_enabled"] = observation.WorkerMonitoringEnabled
	artifact["restore_infrastructure_enabled"] = observation.RestoreInfrastructureEnabled
	artifact["restore_ledger_write_enabled"] = observation.RestoreLedgerWriteEnabled
	return artifact
}

func providerArtifact(common commonOptions, observation providerObservation) map[string]any {
	artifact := envelope(common, "mycfc/privacy-provider-registry/v2", "SUCCEEDED")
	artifact["registry_state"] = observation.RegistryState
	artifact["registration_count"] = observation.RegistrationCount
	artifact["provider_registry_sha256"] = observation.ProviderRegistrySHA256
	artifact["inventory_contract"] = observation.InventoryContract
	return artifact
}

func schemaArtifact(common commonOptions, observation schemaObservation) map[string]any {
	artifact := envelope(common, "mycfc/schema-migration-inventory/v1", "SUCCEEDED")
	artifact["schema_migration_digest"] = observation.SchemaMigrationDigest
	artifact["baseline_includes_through"] = observation.BaselineThrough
	return artifact
}

func envelope(common commonOptions, contract, result string) map[string]any {
	return map[string]any{
		"contract": contract, "result": result, "observed_at": common.observedAt, "policy_version": common.policyVersion,
		"executor_version": privacyrequests.SupportedExecutorVersion, "plan_schema_version": privacyrequests.SupportedPlanSchemaVersion,
		"image_digest": common.imageDigest, "evidence_ref": common.evidenceRef, "signing_key_id": common.signingKeyID,
	}
}

func validateCommon(common commonOptions) error {
	observed, err := time.Parse(time.RFC3339, common.observedAt)
	if err != nil || observed.Format(time.RFC3339) != common.observedAt || !validImageDigest(common.imageDigest) ||
		strings.TrimSpace(common.policyVersion) == "" || strings.TrimSpace(common.signingKeyID) == "" || !validKMSKeyARN(common.kmsKeyARN) {
		return errors.New("activation artifact common fields rejected")
	}
	return nil
}

func verifyEvidenceObject(common commonOptions, evidence []byte) (string, error) {
	u, err := url.Parse(common.evidenceRef)
	if err != nil || u.Scheme != "s3" || u.Host == "" || u.Path == "" || u.Path == "/" || u.User != nil || u.Fragment != "" ||
		len(u.Query()) != 1 || len(u.Query()["versionId"]) != 1 || strings.TrimSpace(u.Query().Get("versionId")) == "" ||
		u.RawQuery != "versionId="+url.QueryEscape(u.Query().Get("versionId")) {
		return "", errors.New("immutable evidence reference rejected")
	}
	headPayload, err := readBounded(common.evidenceHead, 1<<20)
	if err != nil {
		return "", err
	}
	var head evidenceHead
	if json.Unmarshal(headPayload, &head) != nil || head.VersionID != u.Query().Get("versionId") || head.ContentLength != int64(len(evidence)) ||
		head.ServerSideEncryption != "aws:kms" || head.SSEKMSKeyID != common.kmsKeyARN {
		return "", errors.New("immutable evidence metadata rejected")
	}
	digestBytes := sha256.Sum256(evidence)
	if head.ChecksumSHA256 != base64.StdEncoding.EncodeToString(digestBytes[:]) {
		return "", errors.New("immutable evidence checksum rejected")
	}
	return hex.EncodeToString(digestBytes[:]), nil
}

func loadState(path string) ([]byte, stateDocument, error) {
	payload, err := readBounded(path, maximumSourceBytes)
	var state stateDocument
	if err != nil || json.Unmarshal(payload, &state) != nil || state.Version != 4 || state.TerraformVersion != terraformVersion || state.Serial == 0 || state.Lineage == "" {
		return nil, state, errors.New("Terraform state source rejected")
	}
	return payload, state, nil
}

func loadPlan(path string) ([]byte, planDocument, error) {
	payload, err := readBounded(path, maximumSourceBytes)
	var plan planDocument
	if err != nil || json.Unmarshal(payload, &plan) != nil || plan.FormatVersion == "" || plan.TerraformVersion != terraformVersion || !plan.Complete || plan.Errored ||
		plan.Variables == nil || plan.ResourceChanges == nil || len(plan.ResourceDrift) != 0 || !onlyNoOpChanges(plan.ResourceChanges) {
		return nil, plan, errors.New("Terraform plan source rejected")
	}
	return payload, plan, nil
}

func onlyNoOpChanges(changes []resourceChange) bool {
	for _, resource := range changes {
		if len(resource.Change.Actions) != 1 || resource.Change.Actions[0] != "no-op" {
			return false
		}
	}
	return true
}

func planBool(plan planDocument, name string) bool {
	value, ok := plan.Variables[name]
	if !ok {
		return false
	}
	var result bool
	return json.Unmarshal(value.Value, &result) == nil && result
}

func planString(plan planDocument, name string) string {
	value, ok := plan.Variables[name]
	if !ok {
		return ""
	}
	var result string
	if json.Unmarshal(value.Value, &result) != nil {
		return ""
	}
	return result
}

func loadPrivateKey(path string) (ed25519.PrivateKey, error) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("artifact signing key unavailable or insecure")
	}
	payload, err := readBounded(path, 1024)
	if err != nil {
		return nil, err
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(payload)))
	if err != nil || len(decoded) != ed25519.PrivateKeySize {
		return nil, errors.New("artifact signing key rejected")
	}
	return ed25519.PrivateKey(decoded), nil
}

func readBounded(path string, limit int64) ([]byte, error) {
	file, err := os.Open(strings.TrimSpace(path))
	if err != nil {
		return nil, errors.New("evidence source unavailable")
	}
	defer file.Close()
	payload, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || len(payload) == 0 || int64(len(payload)) > limit {
		return nil, errors.New("evidence source rejected")
	}
	return payload, nil
}

func decodeExact(payload []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil {
		return errors.New("evidence source rejected")
	}
	var extra any
	if !errors.Is(decoder.Decode(&extra), io.EOF) {
		return errors.New("evidence source rejected")
	}
	return nil
}

func objectMap(value any) (map[string]any, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, errors.New("activation artifact rejected")
	}
	var result map[string]any
	if json.Unmarshal(payload, &result) != nil {
		return nil, errors.New("activation artifact rejected")
	}
	return result, nil
}

func writeCanonical(common commonOptions, value any, output io.Writer) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return errors.New("evidence output rejected")
	}
	if common.output == "" {
		_, err = fmt.Fprintln(output, string(payload))
		return err
	}
	file, err := os.OpenFile(common.output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return errors.New("evidence output unavailable")
	}
	defer file.Close()
	if _, err = file.Write(append(payload, '\n')); err != nil {
		return errors.New("evidence output failed")
	}
	return nil
}

func digest(payload []byte) string {
	value := sha256.Sum256(payload)
	return hex.EncodeToString(value[:])
}

func validImageDigest(value string) bool {
	if !strings.HasPrefix(value, "sha256:") {
		return false
	}
	decoded, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil && len(decoded) == sha256.Size && value == strings.ToLower(value)
}

func validKMSKeyARN(value string) bool {
	parts := strings.Split(value, ":")
	return len(parts) == 6 && parts[0] == "arn" && parts[2] == "kms" && parts[3] != "" && parts[4] != "" && strings.HasPrefix(parts[5], "key/") && len(parts[5]) > len("key/")
}
