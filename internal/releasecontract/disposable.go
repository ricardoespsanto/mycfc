// Package releasecontract binds this temporary disposable release to the
// immutable binary identity supplied by the signed-tag publisher. Remove the
// reset implementation after this release; do not carry it into real-data use.
package releasecontract

import "regexp"

// These are binary build inputs, not host environment flags. An unstamped
// development binary cannot request a reset. Runtime input cannot select a
// different candidate/version. The database is deliberately not configurable.
var Version, Candidate string

const Database = "mycfc"
const OldWebRole = "mycfc_app"
const WebRole = "mycfc_disposable_web_20261001"
const PredecessorDigest = "8ad238f2a1e36976bebd8f6950c935c9fa5b72862fe7cb14d2ad1149d37d1a4e"
const FinalDigest = "41eca3f0ca37f6e6ba5279fe1d931ff589de72b2a8586a77e31445e8a8b29ed7"
const BaselineDigest = "0846ee526863b67e1e3a3dea1cea52d35fb3e0af58994d5f69b0469bc816e98a"
const ReviewedParent = "e90033d"

var versionPattern = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?$`)
var candidatePattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

func Matches(version, candidate, database string) bool {
	return versionPattern.MatchString(Version) && candidatePattern.MatchString(Candidate) &&
		version == Version && candidate == Candidate && database == Database
}

func AppRole(role string) string {
	if role == OldWebRole && versionPattern.MatchString(Version) && candidatePattern.MatchString(Candidate) {
		return WebRole
	}
	return role
}
