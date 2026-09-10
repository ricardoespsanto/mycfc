package main

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/cfcoimbra/mycfc/internal/storage"
)

func TestLegacyMediaPurgeCommandDefaultsToDryRunAndRequiresTypedExecuteConfirmation(t *testing.T) {
	request, err := parseRequest(nil)
	if err != nil || request.execute || request.confirmation != "" {
		t.Fatalf("default request=%+v err=%v", request, err)
	}
	digest := strings.Repeat("a", 64)
	request, err = parseRequest([]string{"--execute", "--inventory-digest", digest, "--confirm", storage.LegacyMediaPurgeConfirmation})
	if err != nil || !request.execute || request.confirmation != storage.LegacyMediaPurgeConfirmation || request.inventoryDigest != digest {
		t.Fatalf("execute request=%+v err=%v", request, err)
	}
	for _, args := range [][]string{{"--confirm", storage.LegacyMediaPurgeConfirmation}, {"--inventory-digest", digest}, {"unexpected"}, {"--unknown"}} {
		if _, err = parseRequest(args); err == nil {
			t.Fatalf("invalid arguments accepted: %v", args)
		}
	}
	if legacyMediaPurgeExecutionEnabled {
		t.Fatal("destructive legacy media execution must ship source-disabled")
	}
}

func TestLegacyMediaPurgeCommandRequiresSeparateEvidenceKey(t *testing.T) {
	if _, err := decodeEvidenceKey("not-base64"); err == nil || !strings.Contains(err.Error(), "EVIDENCE_KEY") {
		t.Fatalf("invalid evidence key error=%v", err)
	}
	raw := []byte(strings.Repeat("e", 32))
	decoded, err := decodeEvidenceKey(base64.StdEncoding.EncodeToString(raw))
	if err != nil || string(decoded) != string(raw) {
		t.Fatalf("decoded=%d err=%v", len(decoded), err)
	}
}
