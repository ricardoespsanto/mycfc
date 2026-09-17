package privacyrequests

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
)

// ProductionPolicyVersion identifies the club-adopted internal catalogue. It
// grants no application authority and cannot substitute for activation evidence.
const ProductionPolicyVersion = "club-2026-09-15-v1"

//go:embed policies/club-2026-09-15-v1.json
var productionPolicyJSON []byte

// ProductionPolicy returns a fresh strictly decoded snapshot. Exception grounds
// remain absent until their exact facts are approved in a new policy version.
func ProductionPolicy() (AdoptedPolicy, error) {
	var policy AdoptedPolicy
	decoder := json.NewDecoder(bytes.NewReader(productionPolicyJSON))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&policy); err != nil {
		return policy, errors.New("production privacy policy rejected")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF || policy.Version != ProductionPolicyVersion || policy.Validate() != nil {
		return AdoptedPolicy{}, errors.New("production privacy policy rejected")
	}
	return policy, nil
}

// ProductionPolicyArtifact returns independent canonical source bytes for the
// existing file-based import path and a SHA-256 binding for operator review.
func ProductionPolicyArtifact() ([]byte, string, error) {
	if _, err := ProductionPolicy(); err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(productionPolicyJSON)
	return bytes.Clone(productionPolicyJSON), hex.EncodeToString(sum[:]), nil
}
