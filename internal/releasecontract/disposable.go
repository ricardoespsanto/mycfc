// Package releasecontract binds this temporary disposable release to the
// immutable binary identity supplied by the signed-tag publisher. Remove the
// reset implementation after this release; do not carry it into real-data use.
package releasecontract

import (
	"regexp"
	"strconv"
	"strings"
)

// These are binary build inputs, not host environment flags. An unstamped
// development binary cannot request a reset. Runtime input cannot select a
// different candidate/version. The database is deliberately not configurable.
var Version, Candidate string

const Database = "mycfc"
const OldWebRole = "mycfc_app"
const WebRole = "mycfc_disposable_web_20261001"
const PredecessorDigest = "8ad238f2a1e36976bebd8f6950c935c9fa5b72862fe7cb14d2ad1149d37d1a4e"
const FinalDigest = "98de2a416ee0006262398c32d012d81c2b5fcb599c2e607bbb006692919fac84"
const BaselineDigest = "fc8f9dbb96dcfd7f49aa81e562bf53684416a32282a26d4d9a9d329f20c14a1f"

// FreshGenesisDigest is the recorded inventory of the original v2.0.2 receipt.
// The receipt remains immutable when a compatible forward migration advances
// the ledger; it is not rewritten to impersonate the new binary.
const FreshGenesisDigest = "41eca3f0ca37f6e6ba5279fe1d931ff589de72b2a8586a77e31445e8a8b29ed7"
const ReviewedParent = "e90033d"

// FreshGenesisVersion identifies the original signed production fresh initialization.
// A new genesis lineage or changed schema needs an explicit reviewed contract.
const FreshGenesisVersion = "v2.0.2"

// FreshGenesisCandidate identifies the signed source of that genesis receipt.
const FreshGenesisCandidate = "dbe7eb1771af741c3c41008bcf9e92986310ff3c"

var versionPattern = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?$`)
var candidatePattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

func Matches(version, candidate, database string) bool {
	return versionPattern.MatchString(Version) && candidatePattern.MatchString(Candidate) &&
		version == Version && candidate == Candidate && database == Database
}

// CompatibleFreshGenesis accepts the original signed genesis on a later
// stable stamped release, or a receipt created by the current release.
func CompatibleFreshGenesis(database, version, candidate, genesisVersion, genesisCandidate string) bool {
	if !Matches(version, candidate, database) {
		return false
	}
	if genesisVersion == version && genesisCandidate == candidate {
		return true
	}
	if genesisVersion != FreshGenesisVersion || genesisCandidate != FreshGenesisCandidate || strings.Contains(version, "-") {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(version, "v"), ".")
	if len(parts) != 3 {
		return false
	}
	major, e1 := strconv.ParseUint(parts[0], 10, 64)
	minor, e2 := strconv.ParseUint(parts[1], 10, 64)
	patch, e3 := strconv.ParseUint(parts[2], 10, 64)
	return e1 == nil && e2 == nil && e3 == nil && (major > 2 || major == 2 && (minor > 0 || minor == 0 && patch > 2))
}

func AppRole(role string) string {
	if role == OldWebRole && versionPattern.MatchString(Version) && candidatePattern.MatchString(Candidate) {
		return WebRole
	}
	return role
}
