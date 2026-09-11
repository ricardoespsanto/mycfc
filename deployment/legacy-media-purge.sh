#!/bin/sh
set -eu

config_file=${MYCFC_LEGACY_MEDIA_PURGE_CONFIG_FILE:-/etc/mycfc/legacy-media-purge/environment}
purge_credentials_file=${MYCFC_LEGACY_MEDIA_PURGE_CREDENTIALS_FILE:-/etc/mycfc/legacy-media-purge/aws-credentials}
evidence_key_file=${MYCFC_LEGACY_MEDIA_PURGE_EVIDENCE_KEY_FILE:-/etc/mycfc/legacy-media-purge/evidence.key}
release_credentials_file=${MYCFC_RELEASE_AWS_CREDENTIALS_FILE:-/etc/mycfc/release-aws/credentials}
release_aws_profile=${MYCFC_RELEASE_AWS_PROFILE:-mycfc-release}
purge_aws_profile=${MYCFC_LEGACY_MEDIA_PURGE_AWS_PROFILE:-mycfc-legacy-media-purge}
runtime_dir=${MYCFC_RUNTIME_DIR:-/run}
evidence_dir=${MYCFC_LEGACY_MEDIA_PURGE_EVIDENCE_DIR:-/var/lib/mycfc/legacy-media-purge}
release_lock_file="$runtime_dir/mycfc-pull-release.lock"

log() {
	printf '%s\n' "$*"
	logger -t mycfc-legacy-media-purge -- "$*"
}

protected_file() {
	[ -f "$1" ] && [ ! -L "$1" ] && [ "$(stat -c '%u:%g:%a' "$1")" = '0:65532:440' ]
}

valid_sha256() {
	printf '%s\n' "$1" | grep -Eq '^[0-9a-f]{64}$'
}

valid_git_sha() {
	printf '%s\n' "$1" | grep -Eq '^[0-9a-f]{40}$'
}

if [ "$(id -u)" -ne 0 ]; then
	log 'event=legacy_media_purge_rejected reason=root_required'
	exit 1
fi

if [ "$#" -eq 0 ]; then
	set -- invalid
fi
case "$#:$1" in
	3:inventory) mode=DRY_RUN; image=$2; evidence_output=$3; set -- ;;
	4:execute) mode=EXECUTE; image=$2; approved_digest=$3; evidence_output=$4; set -- --execute --inventory-digest "$approved_digest" --confirm DELETE-ALL-LEGACY-MEDIA-VERSIONS ;;
	*)
		printf '%s\n' 'usage: legacy-media-purge.sh inventory IMAGE@SHA256 EVIDENCE.json' >&2
		printf '%s\n' '       legacy-media-purge.sh execute IMAGE@SHA256 APPROVED_DIGEST EVIDENCE.json' >&2
		exit 2
		;;
esac

if ! protected_file "$config_file" || ! protected_file "$purge_credentials_file" || ! protected_file "$evidence_key_file"; then
	log 'event=legacy_media_purge_rejected reason=protected_input_invalid'
	exit 1
fi
if [ ! -f "$release_credentials_file" ] || [ -L "$release_credentials_file" ] || [ "$(stat -c '%u:%a' "$release_credentials_file")" != '0:600' ]; then
	log 'event=legacy_media_purge_rejected reason=release_credentials_invalid'
	exit 1
fi

set -a
. "$config_file"
set +a
: "${AWS_REGION:?}"
: "${S3_BUCKET_NAME:?}"
: "${ECR_REPOSITORY_URL:?}"
: "${LEGACY_MEDIA_PURGE_EVIDENCE_KEY_ID:?}"
: "${LEGACY_MEDIA_PURGE_EXPECTED_SHA:?}"
: "${LEGACY_MEDIA_PURGE_EXPECTED_PRINCIPAL_ARN:?}"
repository=${image%@*}
image_digest=${image#*@sha256:}
if [ "$repository" != "$ECR_REPOSITORY_URL" ] || [ "$image" != "$repository@sha256:$image_digest" ] || ! valid_sha256 "$image_digest"; then
	log 'event=legacy_media_purge_rejected reason=immutable_image_required'
	exit 1
fi
if ! valid_git_sha "$LEGACY_MEDIA_PURGE_EXPECTED_SHA"; then
	log 'event=legacy_media_purge_rejected reason=expected_sha_invalid'
	exit 1
fi
if [ ! -d "$evidence_dir" ] || [ -L "$evidence_dir" ] || [ "$(stat -c '%u:%a' "$evidence_dir")" != '0:700' ]; then
	log 'event=legacy_media_purge_rejected reason=evidence_directory_invalid'
	exit 1
fi
evidence_dir=$(readlink -f -- "$evidence_dir")
evidence_parent=$(readlink -f -- "$(dirname -- "$evidence_output")" 2>/dev/null || true)
evidence_name=$(basename -- "$evidence_output")
case "$evidence_name" in
	[A-Za-z0-9]*.json) ;;
	*) evidence_parent= ;;
esac
if [ "$evidence_parent" != "$evidence_dir" ] || [ "$evidence_output" != "$evidence_dir/$evidence_name" ]; then
	log 'event=legacy_media_purge_rejected reason=evidence_path_outside_protected_directory'
	exit 1
fi
if [ -e "$evidence_output" ]; then
	log 'event=legacy_media_purge_rejected reason=evidence_path_exists'
	exit 1
fi
if [ "$mode" = EXECUTE ] && ! valid_sha256 "$approved_digest"; then
	log 'event=legacy_media_purge_rejected reason=approved_digest_invalid'
	exit 1
fi

exec 9>"$release_lock_file"
if ! flock -n 9; then
	log 'event=legacy_media_purge_rejected reason=release_or_purge_already_running'
	exit 1
fi
if systemctl is-active --quiet mycfc-pull-release.timer ||
	systemctl is-active --quiet mycfc-pull-release.service ||
	systemctl is-active --quiet mycfc-privacy-worker.service; then
	log 'event=legacy_media_purge_rejected reason=maintenance_gate_active'
	exit 1
fi
for application in mycfc-production-app-1 mycfc-production-app-blue-1 mycfc-production-app-green-1 mycfc-production-privacy-worker-1; do
	if [ "$(docker inspect --format '{{.State.Running}}' "$application" 2>/dev/null || true)" = true ]; then
		log 'event=legacy_media_purge_rejected reason=media_writer_active'
		exit 1
	fi
done

registry=${repository%%/*}
temporary=$(mktemp "$evidence_dir/.legacy-media-purge.XXXXXX")
docker_config=$(mktemp -d "$runtime_dir/mycfc-legacy-media-purge-docker.XXXXXX")
trap 'rm -f "$temporary"; rm -rf "$docker_config"' EXIT HUP INT TERM
export DOCKER_CONFIG="$docker_config"

log "event=legacy_media_purge_phase_started phase=image_pull mode=$mode"
unset AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY AWS_SESSION_TOKEN
AWS_SHARED_CREDENTIALS_FILE=$release_credentials_file AWS_PROFILE=$release_aws_profile \
	aws ecr get-login-password --region "$AWS_REGION" | docker login --username AWS --password-stdin "$registry" >/dev/null
docker pull "$image" >/dev/null
if ! command -v gh >/dev/null 2>&1 || ! gh attestation verify "oci://$image" \
	--repo ricardoespsanto/mycfc \
	--signer-workflow ricardoespsanto/mycfc/.github/workflows/legacy-media-purge-image.yml \
	--source-digest "$LEGACY_MEDIA_PURGE_EXPECTED_SHA" \
	--deny-self-hosted-runners >/dev/null; then
	log 'event=legacy_media_purge_failed phase=image_verify reason=trusted_provenance_missing'
	exit 1
fi
revision=$(docker image inspect --format '{{index .Config.Labels "org.opencontainers.image.revision"}}' "$image")
if [ "$revision" != "$LEGACY_MEDIA_PURGE_EXPECTED_SHA" ]; then
	log 'event=legacy_media_purge_failed phase=image_verify reason=revision_mismatch'
	exit 1
fi
log "event=legacy_media_purge_phase_completed phase=image_pull mode=$mode"

unset AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY AWS_SESSION_TOKEN
principal_arn=$(AWS_SHARED_CREDENTIALS_FILE=$purge_credentials_file AWS_PROFILE=$purge_aws_profile \
	aws sts get-caller-identity --query Arn --output text)
if [ "$principal_arn" != "$LEGACY_MEDIA_PURGE_EXPECTED_PRINCIPAL_ARN" ]; then
	log 'event=legacy_media_purge_rejected reason=purge_principal_mismatch'
	exit 1
fi

log "event=legacy_media_purge_phase_started phase=storage_operation mode=$mode"
if ! docker run --rm --read-only --user 65532:65532 --cap-drop ALL --security-opt no-new-privileges \
	--pids-limit 64 --memory 256m --network bridge \
	-e AWS_EC2_METADATA_DISABLED=true \
	-e AWS_REGION="$AWS_REGION" \
	-e AWS_PROFILE="$purge_aws_profile" \
	-e AWS_SHARED_CREDENTIALS_FILE=/run/legacy-media-purge/aws-credentials \
	-e S3_BUCKET_NAME="$S3_BUCKET_NAME" \
	-e LEGACY_MEDIA_PURGE_EVIDENCE_KEY_ID="$LEGACY_MEDIA_PURGE_EVIDENCE_KEY_ID" \
	-e LEGACY_MEDIA_PURGE_EVIDENCE_KEY_FILE=/run/legacy-media-purge/evidence.key \
	-v "$purge_credentials_file:/run/legacy-media-purge/aws-credentials:ro" \
	-v "$evidence_key_file:/run/legacy-media-purge/evidence.key:ro" \
	"$image" "$@" >"$temporary"; then
	log "event=legacy_media_purge_failed phase=storage_operation mode=$mode"
	exit 1
fi

if ! jq -e --arg mode "$mode" --arg key_id "$LEGACY_MEDIA_PURGE_EVIDENCE_KEY_ID" --arg approved_digest "${approved_digest:-}" '
  (keys | sort) == ["delete_markers", "deleted_markers", "deleted_versions", "evidence_key_id", "evidence_version", "inventory_digest", "list_calls", "mode", "prefixes", "stable_empty_scans", "versions"] and
  .evidence_version == "mycfc/legacy-media-purge-evidence/v1" and
  .mode == $mode and
  .evidence_key_id == $key_id and
  (.inventory_digest | test("^[0-9a-f]{64}$")) and
  (.prefixes | length == 3) and
  ([.prefixes[].prefix] | sort == ["equipment/", "profiles/", "repairs/"]) and
  ([.versions, .delete_markers, .deleted_versions, .deleted_markers, .list_calls, .stable_empty_scans] | all(type == "number" and . >= 0 and floor == .)) and
  (.versions + .delete_markers <= 750000) and
  (.deleted_versions <= .versions and .deleted_markers <= .delete_markers) and
  (.list_calls <= 39000 and .stable_empty_scans <= 6) and
  (.prefixes | all(
    (keys | sort) == ["delete_markers", "deleted_markers", "deleted_versions", "inventory_digest", "list_calls", "prefix", "stable_empty_scans", "versions"] and
    (.inventory_digest | test("^[0-9a-f]{64}$")) and
    ([.versions, .delete_markers, .deleted_versions, .deleted_markers, .list_calls, .stable_empty_scans] | all(type == "number" and . >= 0 and floor == .)) and
    (.versions + .delete_markers <= 250000) and
    (.deleted_versions <= .versions and .deleted_markers <= .delete_markers) and
    (.list_calls <= 13000 and .stable_empty_scans <= 2)
  )) and
  .versions == ([.prefixes[].versions] | add) and
  .delete_markers == ([.prefixes[].delete_markers] | add) and
  .deleted_versions == ([.prefixes[].deleted_versions] | add) and
  .deleted_markers == ([.prefixes[].deleted_markers] | add) and
  .list_calls == ([.prefixes[].list_calls] | add) and
  .stable_empty_scans == ([.prefixes[].stable_empty_scans] | add) and
  if $mode == "DRY_RUN" then
    .deleted_versions == 0 and .deleted_markers == 0 and
    (.prefixes | all(.deleted_versions == 0 and .deleted_markers == 0 and .stable_empty_scans == 0))
  else
    .inventory_digest == $approved_digest and
    .deleted_versions == .versions and .deleted_markers == .delete_markers and
    (.prefixes | all(.deleted_versions == .versions and .deleted_markers == .delete_markers and .stable_empty_scans >= 2))
  end
' "$temporary" >/dev/null; then
	log "event=legacy_media_purge_failed phase=evidence_validate mode=$mode"
	exit 1
fi

chmod 0600 "$temporary"
chown root:root "$temporary"
if ! ln "$temporary" "$evidence_output"; then
	log 'event=legacy_media_purge_failed phase=evidence_publish reason=destination_exists'
	exit 1
fi
rm -f "$temporary"
rm -rf "$docker_config"
trap - EXIT HUP INT TERM

versions=$(jq -r '.versions' "$evidence_output")
markers=$(jq -r '.delete_markers' "$evidence_output")
deleted_versions=$(jq -r '.deleted_versions' "$evidence_output")
deleted_markers=$(jq -r '.deleted_markers' "$evidence_output")
log "event=legacy_media_purge_succeeded mode=$mode versions=$versions delete_markers=$markers deleted_versions=$deleted_versions deleted_markers=$deleted_markers"
