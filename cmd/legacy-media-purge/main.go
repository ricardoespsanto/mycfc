package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/cfcoimbra/mycfc/internal/storage"
)

// Destructive execution intentionally ships disabled. Enabling it is a
// separate reviewed source change and still does not bypass the exact typed
// confirmation required by the storage operation.
const legacyMediaPurgeExecutionEnabled = false

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "legacy-media-purge:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, output io.Writer) error {
	request, err := parseRequest(args)
	if err != nil {
		return err
	}
	bucket := strings.TrimSpace(os.Getenv("S3_BUCKET_NAME"))
	evidenceKeyID := strings.TrimSpace(os.Getenv("LEGACY_MEDIA_PURGE_EVIDENCE_KEY_ID"))
	evidenceKey, err := decodeEvidenceKey(os.Getenv("LEGACY_MEDIA_PURGE_EVIDENCE_KEY_B64"))
	if err != nil {
		return err
	}
	loadOptions := []func(*awsconfig.LoadOptions) error{}
	if region := strings.TrimSpace(os.Getenv("AWS_REGION")); region != "" {
		loadOptions = append(loadOptions, awsconfig.WithRegion(region))
	}
	awsConfig, err := awsconfig.LoadDefaultConfig(ctx, loadOptions...)
	if err != nil {
		return errors.New("load legacy media purge storage configuration")
	}
	runner, err := storage.NewLegacyMediaPurge(s3.NewFromConfig(awsConfig), bucket, evidenceKeyID, evidenceKey)
	if err != nil {
		return err
	}
	evidence, err := runner.Run(ctx, storage.LegacyMediaPurgeRequest{
		Execute: request.execute, Enabled: legacyMediaPurgeExecutionEnabled, Confirmation: request.confirmation, ExpectedInventoryDigest: request.inventoryDigest,
	})
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	if err = encoder.Encode(evidence); err != nil {
		return errors.New("write legacy media purge evidence")
	}
	return nil
}

type commandRequest struct {
	execute         bool
	confirmation    string
	inventoryDigest string
}

func parseRequest(args []string) (commandRequest, error) {
	flags := flag.NewFlagSet("legacy-media-purge", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	execute := flags.Bool("execute", false, "")
	confirmation := flags.String("confirm", "", "")
	inventoryDigest := flags.String("inventory-digest", "", "")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return commandRequest{}, errors.New("usage: legacy-media-purge [--execute --inventory-digest SHA256 --confirm " + storage.LegacyMediaPurgeConfirmation + "]")
	}
	if !*execute && (*confirmation != "" || *inventoryDigest != "") {
		return commandRequest{}, errors.New("--confirm and --inventory-digest are accepted only with --execute")
	}
	return commandRequest{execute: *execute, confirmation: *confirmation, inventoryDigest: *inventoryDigest}, nil
}

func decodeEvidenceKey(raw string) ([]byte, error) {
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(raw))
	if err != nil || len(decoded) < 32 {
		return nil, errors.New("LEGACY_MEDIA_PURGE_EVIDENCE_KEY_B64 must decode to at least 32 bytes")
	}
	return decoded, nil
}
