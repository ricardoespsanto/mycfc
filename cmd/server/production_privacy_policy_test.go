package main

import (
	"bytes"
	"io"
	"testing"

	"github.com/cfcoimbra/mycfc/internal/privacyrequests"
)

func TestProductionPrivacyPolicyUsesExistingStrictImportParser(t *testing.T) {
	raw, _, err := privacyrequests.ProductionPolicyArtifact()
	if err != nil {
		t.Fatal(err)
	}
	p, err := readPrivacyPolicy(func(string) (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(raw)), nil }, "production-policy.json")
	if err != nil || p.Validate() != nil || p.Version != privacyrequests.ProductionPolicyVersion {
		t.Fatal("production artifact rejected", err)
	}
	for _, bad := range [][]byte{append(bytes.Clone(raw), []byte("{}")...), bytes.Replace(raw, []byte(`"version":`), []byte(`"unsupported":true,"version":`), 1)} {
		if _, err = readPrivacyPolicy(func(string) (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(bad)), nil }, "production-policy.json"); err == nil {
			t.Fatal("unrecognized policy material accepted")
		}
	}
}
