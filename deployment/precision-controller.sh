#!/bin/sh
# Bounded adapter for a reviewed controller. Never source an env/credential file.
# Inputs are protected, already authenticated local release/status evidence.
# The enclosing controller owns process drain, route switch and existing gates.
set -eu

deployment_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
helper="$deployment_dir/precision-admission.py"
action=${1:-}
[ "$#" -ge 5 ] || {
	printf '%s\n' 'usage: precision-controller.sh check STATUS MANIFEST CAPABILITIES CONTROLLER; fallback adds SLOT BINDER_MANIFEST BINDER_CAPABILITIES' >&2
	exit 2
}
shift
status_file=$1
manifest_file=$2
capabilities_file=$3
controller_file=$4
shift 4
case "$action:$#" in
	check:0) ;;
	fallback:3)
		fallback_slot=$1
		binder_manifest=$2
		binder_capabilities=$3
		case "$fallback_slot" in blue|green) ;; *) exit 2 ;; esac
		# Fallback must stay serialized through binding, restart and readiness.
		[ -n "${MYCFC_PRECISION_LOCK_FD:-}" ] || {
			printf '%s\n' 'precision_controller=refused reason=inherited_controller_lock_required' >&2
			exit 1
		}
		;;
	*) printf '%s\n' 'precision_controller=refused reason=invalid_action' >&2; exit 2 ;;
esac

# No network or process action occurs until the actual controller source hash,
# root host authority, projection, current DB facts and immutable image binding
# all pass. Existing controller lock fd9 can be inherited by explicit argument.
set -- check --status "$status_file" --controller "$controller_file" \
	--release "$manifest_file" "$capabilities_file" \
	--authority "${MYCFC_PRECISION_AUTHORITY:-/etc/mycfc/deployment/training-schedule-host.json}" \
	--projection "${MYCFC_PRECISION_PROJECTION:-/etc/mycfc/training-schedule-admission/admission.json}" \
	--lock-file "${MYCFC_PRECISION_LOCK_FILE:-/run/mycfc-pull-release.lock}"
if [ -n "${MYCFC_PRECISION_LOCK_FD:-}" ]; then
	case "$MYCFC_PRECISION_LOCK_FD" in *[!0-9]*|'') exit 2 ;; esac
	set -- "$@" --lock-fd "$MYCFC_PRECISION_LOCK_FD"
fi
python3 "$helper" "$@"

[ "$action" = fallback ] || exit 0

# Use the schema-current binder image (P on P schema), while binding the C
# fallback digest. C's older embedded migration digest is not a P ledger.
# Both immutable images must already be independently admitted by the operator.
set -- check --status "$status_file" --controller "$controller_file" \
	--release "$binder_manifest" "$binder_capabilities" \
	--authority "${MYCFC_PRECISION_AUTHORITY:-/etc/mycfc/deployment/training-schedule-host.json}" \
	--projection "${MYCFC_PRECISION_PROJECTION:-/etc/mycfc/training-schedule-admission/admission.json}" \
	--lock-file "${MYCFC_PRECISION_LOCK_FILE:-/run/mycfc-pull-release.lock}" \
	--lock-fd "$MYCFC_PRECISION_LOCK_FD"
python3 "$helper" "$@"

fallback_repository=$(jq -er .image.repository "$manifest_file")
fallback_digest=$(jq -er .image.digest "$manifest_file")
binder_repository=$(jq -er .image.repository "$binder_manifest")
binder_digest=$(jq -er .image.digest "$binder_manifest")
fallback_version=$(jq -er .version "$manifest_file")
fallback_candidate=$(jq -er .git_sha "$manifest_file")
fallback_released_at=$(jq -er .published_at "$manifest_file")
fallback_image="$fallback_repository@$fallback_digest"
binder_image="$binder_repository@$binder_digest"
env_file=${MYCFC_ENV_FILE:-/etc/mycfc/mycfc.env}
compose_file=${MYCFC_COMPOSE_FILE:-$deployment_dir/compose.yaml}

# This adapter never enables guardian intake or starts a worker. A fallback
# whose signed policy expects intake enabled needs a separate reviewed path.
[ "$(jq -r .expected_gates.guardian_intake "$manifest_file")" = false ] || exit 1
MYCFC_IMAGE="$binder_image" GUARDIAN_RUNTIME_IMAGE_DIGEST="$fallback_digest" \
	docker compose --env-file "$env_file" -f "$compose_file" --profile release \
	run --rm --no-deps guardian-release-bind
guardian_status=$(MYCFC_IMAGE="$binder_image" GUARDIAN_RUNTIME_IMAGE_DIGEST="$fallback_digest" \
	docker compose --env-file "$env_file" -f "$compose_file" --profile release \
	run --rm --no-deps guardian-release-bind guardian-release-status)
[ "$guardian_status" = guardian_intake_active=false ] || {
	printf '%s\n' 'precision_controller=refused reason=guardian_binding_not_verified' >&2
	exit 1
}

MYCFC_IMAGE="$fallback_image" APP_VERSION="$fallback_version" GIT_SHA="$fallback_candidate" APP_RELEASED_AT="$fallback_released_at" \
	docker compose --env-file "$env_file" -f "$compose_file" --profile "$fallback_slot" \
	up -d --no-deps --force-recreate "app-$fallback_slot"

# Route/environment restoration belongs to the enclosing reviewed controller,
# and may happen only after this exact fallback passes its current admission.
container="mycfc-production-app-$fallback_slot-1"
actual_image=$(docker inspect --format '{{.Config.Image}}' "$container")
[ "$actual_image" = "$fallback_image" ] || exit 1
address=$(docker inspect --format '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "$container")
case "$address" in ''|*[!0-9.]*) exit 1 ;; esac
curl --fail --silent --show-error --max-time 10 --output /dev/null "http://$address:8080/health/ready"
printf '%s\n' 'precision_controller=fallback_ready runtime_only=true'
