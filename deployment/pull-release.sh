#!/bin/sh
set -eu

env_file=${MYCFC_ENV_FILE:-/etc/mycfc/mycfc.env}
deployment_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
compose_file="$deployment_dir/compose.yaml"
state_dir=${MYCFC_DEPLOYMENT_STATE_DIR:-/etc/mycfc/deployment}
runtime_dir=${MYCFC_RUNTIME_DIR:-/run}
release_credentials_file=${MYCFC_RELEASE_AWS_CREDENTIALS_FILE:-/etc/mycfc/release-aws/credentials}
guardian_release_bind_env_file=${MYCFC_GUARDIAN_RELEASE_BIND_ENV_FILE:-/etc/mycfc/guardian-release-bind.env}
release_aws_profile=${MYCFC_RELEASE_AWS_PROFILE:-mycfc-release}
active_slot_file="$state_dir/active-slot"
failed_digest_file="$state_dir/failed-release-digest"
last_attempt_digest_file="$state_dir/last-attempt-digest"
last_attempt_result_file="$state_dir/last-attempt-result"
last_attempt_at_file="$state_dir/last-attempt-at"
last_attempt_file="$state_dir/last-attempt"
timeline_digest_file="$state_dir/release-timeline-digest"
timeline_tag_file="$state_dir/release-timeline-tag"
deployment_receipt_file="$state_dir/deployment-receipt.json"
publication_manifest_file="$state_dir/release-publication.json"
upstream_file="$state_dir/caddy-upstream.caddy"
lock_file="$runtime_dir/mycfc-pull-release.lock"
restore_drill_command=${MYCFC_RESTORE_DRILL_COMMAND:-$deployment_dir/postgres-restore-drill.sh}
restore_attestation_verify_command=${MYCFC_RESTORE_ATTESTATION_VERIFY_COMMAND:-$deployment_dir/verify-privacy-restore-attestation.sh}
privacy_worker_command=${MYCFC_PRIVACY_WORKER_COMMAND:-$deployment_dir/privacy-worker.sh}
privacy_worker_activation_required_status=3
agent_started_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)
backup_file=
route_backup=
release_digest=
release_version=
schema_migration_digest=
publication_manifest_sha256=
candidate_slot=
candidate_service=
candidate_started=false
route_switched=false
traffic_switched=false
rollback_performed=false
release_updated=false
privacy_worker_stopped=false
guardian_intake_active=false
privacy_worker_active=false
privacy_worker_activation_required=false
current_phase=initialization

log() {
	printf '%s\n' "$*"
	logger -t mycfc-pull-release -- "$*"
}

run_phase() {
	phase=$1
	shift
	current_phase=$phase
	phase_started_epoch=$(date +%s)
	log "event=deployment_phase_started phase=$phase sha=${sha:-unknown} digest=${release_digest:-unknown} slot=${candidate_slot:-unknown}"
	"$@"
	phase_duration_seconds=$(($(date +%s) - phase_started_epoch))
	log "event=deployment_phase_completed phase=$phase duration_seconds=$phase_duration_seconds sha=${sha:-unknown} digest=${release_digest:-unknown} slot=${candidate_slot:-unknown}"
}

write_state_value() {
	target=$1
	value=$2
	temporary=$(mktemp "$state_dir/.state.XXXXXX")
	printf '%s\n' "$value" >"$temporary"
	chmod 0644 "$temporary"
	mv "$temporary" "$target"
}

record_attempt() {
	result=$1
	[ -n "$release_digest" ] || return 0
	attempted_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)
	# Keep the legacy fields during the rollout, then atomically commit the
	# digest-associated snapshot that new status readers prefer.
	write_state_value "$last_attempt_digest_file" "$release_digest"
	write_state_value "$last_attempt_result_file" "$result"
	write_state_value "$last_attempt_at_file" "$attempted_at"
	write_state_value "$last_attempt_file" "$(printf '%s\t%s\t%s' "$release_digest" "$result" "$attempted_at")"
}

write_deployment_receipt() {
	result=$1
	finished_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)
	case "$release_version:$sha:$release_digest:$schema_migration_digest:$publication_manifest_sha256:$release_tag" in
		v*:*:sha256:*:*:*:release-*) ;;
		*) return 0 ;;
	esac
	case "$candidate_slot" in blue|green) receipt_slot=$candidate_slot ;; *) receipt_slot=unknown ;; esac
	case "$result" in succeeded) failure_phase= ;; *) failure_phase=$current_phase ;; esac
	temporary=$(mktemp "$state_dir/.deployment-receipt.XXXXXX")
	jq -cS -n \
		--arg contract 'mycfc/deployment-receipt/v1' \
		--arg version "$release_version" \
		--arg sha "$sha" \
		--arg repository "$ECR_REPOSITORY_URL" \
		--arg digest "$release_digest" \
		--arg tag "$release_tag" \
		--arg schema "$schema_migration_digest" \
		--arg manifest "$publication_manifest_sha256" \
		--arg result "$result" \
		--arg slot "$receipt_slot" \
		--arg phase "$failure_phase" \
		--arg started "$agent_started_at" \
		--arg finished "$finished_at" \
		--argjson switched "$traffic_switched" \
		--argjson rolled_back "$rollback_performed" \
		--argjson guardian "$guardian_intake_active" \
		--argjson privacy "$privacy_worker_active" \
		--argjson activation_required "$privacy_worker_activation_required" \
		'{contract:$contract,version:$version,git_sha:$sha,image:{repository:$repository,digest:$digest},release_tag:$tag,schema_migration_digest:$schema,publication_manifest_sha256:$manifest,result:$result,slot:$slot,failure_phase:(if $phase == "" then null else $phase end),traffic_switched:$switched,rollback_performed:$rolled_back,actual_gates:{guardian_intake:$guardian,privacy_worker:$privacy},privacy_worker_activation_required:$activation_required,started_at:$started,finished_at:$finished}' >"$temporary"
	chmod 0644 "$temporary"
	mv "$temporary" "$deployment_receipt_file"
	receipt_sha256=$(sha256sum "$deployment_receipt_file" | awk '{print $1}')
	log "event=deployment_receipt result=$result version=$release_version sha=$sha digest=$release_digest schema_migration_digest=$schema_migration_digest manifest_sha256=$publication_manifest_sha256 slot=$receipt_slot failure_phase=${failure_phase:-none} traffic_switched=$traffic_switched rollback_performed=$rollback_performed guardian_intake_active=$guardian_intake_active privacy_worker_active=$privacy_worker_active privacy_worker_activation_required=$privacy_worker_activation_required started_at=$agent_started_at finished_at=$finished_at receipt_sha256=$receipt_sha256"
}

timeline_file() {
	printf '%s/release-%s-at\n' "$state_dir" "$1"
}

record_timeline_milestone() {
	milestone=$1
	case "$milestone" in
		agent-started|detected|image-pulled|migration-completed|candidate-ready|traffic-switched|deployment-completed) ;;
		*) log "invalid release timeline milestone: $milestone"; return 1 ;;
	esac
	milestone_file=$(timeline_file "$milestone")
	if [ ! -f "$milestone_file" ]; then
		milestone_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)
		write_state_value "$milestone_file" "$milestone_at"
		log "event=release_$milestone at=$milestone_at digest=$release_digest"
	fi
}

begin_release_timeline() {
	published_at=$1
	if [ ! -f "$timeline_digest_file" ] || [ "$(cat "$timeline_digest_file")" != "$release_digest" ] ||
		[ ! -f "$timeline_tag_file" ] || [ "$(cat "$timeline_tag_file")" != "$release_tag" ]; then
		for milestone in published agent-started detected image-pulled migration-completed candidate-ready traffic-switched deployment-completed; do
			rm -f "$(timeline_file "$milestone")"
		done
		write_state_value "$(timeline_file published)" "$published_at"
		write_state_value "$(timeline_file agent-started)" "$agent_started_at"
		# Identity files are the commit marker. Readers ignore the new timeline
		# until both initial timestamps are durable.
		write_state_value "$timeline_tag_file" "$release_tag"
		write_state_value "$timeline_digest_file" "$release_digest"
		log "event=release_agent-started at=$agent_started_at digest=$release_digest"
	fi
	record_timeline_milestone detected
}

write_upstream() {
	slot=$1
	case "$slot" in
		blue|green) target="app-$slot:8080" ;;
		legacy) target='app:8080' ;;
		*) log "invalid application slot: $slot"; return 1 ;;
	esac
	temporary=$(mktemp "$state_dir/.upstream.XXXXXX")
	cat >"$temporary" <<EOF
reverse_proxy $target {
	health_uri /health/live
	health_interval 10s
	health_timeout 2s
}
EOF
	chmod 0644 "$temporary"
	mv "$temporary" "$upstream_file"
}

reload_caddy() {
	docker compose --env-file "$env_file" -f "$compose_file" exec -T caddy \
		caddy reload --config /etc/caddy/Caddyfile --adapter caddyfile
}

check_caddy_path() {
	path=$1
	for _ in $(seq 1 15); do
		if docker compose --env-file "$env_file" -f "$compose_file" exec -T caddy \
			wget -q -O /dev/null --header="Host: $MYCFC_DOMAIN" "http://127.0.0.1$path"; then
			return 0
		fi
		sleep 2
	done
	return 1
}

verify_privacy_worker_active() {
	for _ in $(seq 1 10); do
		if ! systemctl is-active --quiet mycfc-privacy-worker.service; then
			return 1
		fi
		sleep 1
	done
}

verify_privacy_worker_inactive() {
	if systemctl is-active --quiet mycfc-privacy-worker.service; then
		return 1
	fi
}

observe_guardian_intake() {
	status=$(docker compose --env-file "$env_file" -f "$compose_file" --profile release \
		run --rm --no-deps guardian-release-bind guardian-release-status)
	case "$status" in
		guardian_intake_active=true) guardian_intake_active=true ;;
		guardian_intake_active=false) guardian_intake_active=false ;;
		*) log 'event=guardian_release_status_rejected reason=unknown_state'; return 1 ;;
	esac
	if [ "$guardian_intake_active" != "$expected_guardian_intake" ]; then
		log "event=guardian_release_status_rejected reason=policy_mismatch expected=$expected_guardian_intake actual=$guardian_intake_active"
		return 1
	fi
}

rollback() {
	status=$?
	trap - EXIT HUP INT TERM
	if [ "$status" -ne 0 ]; then
		log "event=deployment_failed phase=$current_phase exit_status=$status sha=${sha:-unknown} digest=${release_digest:-unknown} slot=${candidate_slot:-unknown} candidate_started=$candidate_started route_switched=$route_switched"
	fi
	if [ "$route_switched" = true ] && [ -n "$route_backup" ] && [ -f "$route_backup" ]; then
		log 'candidate failed after traffic switch; restoring the previous Caddy upstream'
		temporary=$(mktemp "$state_dir/.upstream.XXXXXX")
		cp "$route_backup" "$temporary"
		chmod 0644 "$temporary"
		mv "$temporary" "$upstream_file"
		reload_caddy || log 'Caddy upstream rollback failed'
		rollback_performed=true
	fi
	if [ "$candidate_started" = true ] && [ -n "$candidate_slot" ]; then
		docker compose --env-file "$env_file" -f "$compose_file" --profile "$candidate_slot" \
			stop "app-$candidate_slot" >/dev/null 2>&1 || log 'candidate stop failed'
	fi
	if [ "$release_updated" = true ]; then
		log 'deployment failed; restoring the previous release configuration'
		if [ -n "$candidate_slot" ]; then
			docker logs --tail 100 "mycfc-production-app-$candidate_slot-1" >&2 2>/dev/null || true
		fi
		cp "$backup_file" "$env_file"
		if [ "$privacy_worker_stopped" = true ]; then
			systemctl restart mycfc-privacy-worker.service || log 'privacy worker rollback restart failed'
		fi
		if [ -n "$release_digest" ]; then
			write_state_value "$failed_digest_file" "$release_digest"
			log "quarantined failed release digest $release_digest"
		fi
	fi
	if [ "$status" -ne 0 ] && [ -n "$release_digest" ]; then
		record_attempt failed
		write_deployment_receipt failed
	fi
	rm -f "$route_backup"
	exit "$status"
}

if [ "$(id -u)" -ne 0 ]; then
	log 'must run as root'
	exit 1
fi

if [ ! -f "$env_file" ] || [ "$(stat -c '%u:%a' "$env_file")" != '0:600' ]; then
	log 'missing or insecure environment file'
	exit 1
fi

if [ ! -f "$release_credentials_file" ] || [ "$(stat -c '%u:%a' "$release_credentials_file")" != '0:600' ]; then
	log 'missing or insecure release-agent AWS credentials file'
	exit 1
fi

if [ ! -f "$guardian_release_bind_env_file" ] || [ -L "$guardian_release_bind_env_file" ] ||
	[ "$(stat -c '%u:%a' "$guardian_release_bind_env_file" 2>/dev/null || true)" != '0:600' ] ||
	[ "$(grep -c '^GUARDIAN_RELEASE_BIND_DATABASE_URL=' "$guardian_release_bind_env_file" 2>/dev/null || true)" -ne 1 ] ||
	[ "$(grep -c '^GUARDIAN_RELEASE_BIND_EXPECTED_DATABASE=' "$guardian_release_bind_env_file" 2>/dev/null || true)" -ne 1 ]; then
	log 'event=guardian_release_bind_configuration_rejected'
	exit 1
fi
while IFS= read -r guardian_release_line || [ -n "$guardian_release_line" ]; do
	case "$guardian_release_line" in
		''|\#*) ;;
		GUARDIAN_RELEASE_BIND_DATABASE_URL=*|GUARDIAN_RELEASE_BIND_EXPECTED_DATABASE=*) ;;
		*) log 'event=guardian_release_bind_configuration_rejected'; exit 1 ;;
	esac
done <"$guardian_release_bind_env_file"
GUARDIAN_RELEASE_BIND_ENV_FILE=$guardian_release_bind_env_file
export GUARDIAN_RELEASE_BIND_ENV_FILE

mkdir -p "$state_dir"
chmod 0755 "$state_dir"

exec 9>"$lock_file"
if ! flock -n 9; then
	log 'another release check is already running'
	exit 0
fi

# This file is root-owned and mode 0600; it is the host's deployment configuration.
set -a
. "$env_file"
set +a
: "${AWS_REGION:?}"
: "${ECR_REPOSITORY_URL:?}"
: "${MYCFC_DOMAIN:?}"

case "${PRIVACY_RESTORE_PROMOTION_GATE_ENABLED:-false}" in
	true)
		if [ "${PRIVACY_RESTORE_DRILL_ENABLED:-false}" != true ] || [ "${BACKUP_MANIFEST_AUTH_ENABLED:-false}" != true ]; then
			log 'privacy restore promotion gate requires the authenticated restore drill'
			exit 1
		fi
		;;
	false) ;;
	*) log 'invalid privacy restore promotion gate setting'; exit 1 ;;
esac
case "${PRIVACY_WORKER_ENABLED:-false}" in
	true|false) ;;
	*) log 'invalid privacy worker setting'; exit 1 ;;
esac

# AWS environment credentials belong to the application runtime. All AWS CLI
# calls made by the deployment agent must use its narrower, root-owned profile.
unset AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY AWS_SESSION_TOKEN
export AWS_SHARED_CREDENTIALS_FILE="$release_credentials_file"
export AWS_PROFILE="$release_aws_profile"

if [ -f "$active_slot_file" ]; then
	active_slot=$(cat "$active_slot_file")
else
	active_slot=legacy
	write_state_value "$active_slot_file" "$active_slot"
fi
case "$active_slot" in
	blue|green|legacy) ;;
	*) log "invalid active application slot: $active_slot"; exit 1 ;;
esac
if [ ! -f "$upstream_file" ]; then
	write_upstream "$active_slot"
fi

registry=${ECR_REPOSITORY_URL%%/*}
repository_name=${ECR_REPOSITORY_URL#*/}
# The systemd unit makes home directories inaccessible, so keep the temporary
# ECR credential helper state under its writable runtime directory.
export DOCKER_CONFIG="$runtime_dir/mycfc-pull-release-docker"
mkdir -p "$DOCKER_CONFIG"
ecr_password=$(aws ecr get-login-password --region "$AWS_REGION")
printf '%s' "$ecr_password" | docker login --username AWS --password-stdin "$registry"

tags=$(aws ecr describe-images --region "$AWS_REGION" --repository-name "$repository_name" --query 'imageDetails[].imageTags[]' --output text)
release_tags=$(printf '%s\n' "$tags" | tr '\t' '\n' | awk '/^release-/')
release_tag=$(printf '%s\n' "$release_tags" | sort | tail -n 1)
case "$release_tag" in
	release-??????????????-????????????????????????????????????????) ;;
	*) log 'ECR has no valid release tag'; exit 1 ;;
esac
stamp=${release_tag#release-}
stamp=${stamp%%-*}
released_at="$(printf '%s-%s-%sT%s:%s:%sZ' "$(printf '%s' "$stamp" | cut -c1-4)" "$(printf '%s' "$stamp" | cut -c5-6)" "$(printf '%s' "$stamp" | cut -c7-8)" "$(printf '%s' "$stamp" | cut -c9-10)" "$(printf '%s' "$stamp" | cut -c11-12)" "$(printf '%s' "$stamp" | cut -c13-14)")"

release_digest=$(aws ecr describe-images --region "$AWS_REGION" --repository-name "$repository_name" --image-ids imageTag="$release_tag" --query 'imageDetails[0].imageDigest' --output text)
printf '%s' "$release_digest" | grep -Eq '^sha256:[0-9a-f]{64}$' || { log 'release has no valid digest'; exit 1; }
sha=${release_tag##*-}
printf '%s' "$sha" | grep -Eq '^[0-9a-f]{40}$' || { log 'release tag has no valid lowercase git SHA'; exit 1; }
case "$release_tag" in release-??????????????-"$sha") ;; *) log 'release tag does not bind its SHA'; exit 1 ;; esac

# A signed manifest image is published before the application release tag. The
# host verifies both immutable subjects before it executes any candidate code.
manifest_tag="manifest-$release_tag"
manifest_digest=$(aws ecr describe-images --region "$AWS_REGION" --repository-name "$repository_name" --image-ids imageTag="$manifest_tag" --query 'imageDetails[0].imageDigest' --output text)
printf '%s' "$manifest_digest" | grep -Eq '^sha256:[0-9a-f]{64}$' || { log 'release has no valid manifest digest'; exit 1; }

docker pull "$ECR_REPOSITORY_URL:$release_tag"
docker pull "$ECR_REPOSITORY_URL:$manifest_tag"
image=$(docker image inspect --format '{{index .RepoDigests 0}}' "$ECR_REPOSITORY_URL:$release_tag")
manifest_image=$(docker image inspect --format '{{index .RepoDigests 0}}' "$ECR_REPOSITORY_URL:$manifest_tag")
case "$image" in *"@$release_digest") ;; *) log 'pulled image digest does not match ECR release metadata'; exit 1 ;; esac
case "$manifest_image" in *"@$manifest_digest") ;; *) log 'pulled manifest digest does not match ECR metadata'; exit 1 ;; esac
command -v gh >/dev/null 2>&1 || { log 'GitHub CLI is required for release provenance verification'; exit 1; }
for verified_image in "$ECR_REPOSITORY_URL@$manifest_digest" "$ECR_REPOSITORY_URL@$release_digest"; do
	gh attestation verify "oci://$verified_image" \
		--repo ricardoespsanto/mycfc \
		--signer-workflow ricardoespsanto/mycfc/.github/workflows/deploy.yml \
		--source-digest "$sha" --deny-self-hosted-runners >/dev/null || {
		log "release provenance verification failed for $verified_image"
		exit 1
	}
done

manifest_container=$(docker create "$ECR_REPOSITORY_URL@$manifest_digest")
manifest_temporary=$(mktemp "$state_dir/.release-publication.XXXXXX")
if ! docker cp "$manifest_container:/release-publication.json" "$manifest_temporary"; then
	docker rm "$manifest_container" >/dev/null 2>&1 || true
	rm -f "$manifest_temporary"
	log 'signed release manifest could not be extracted'
	exit 1
fi
docker rm "$manifest_container" >/dev/null
expected_gates=$(jq -cS . "$deployment_dir/release-gates.json")
if ! jq -e --arg version "$(jq -r .version "$manifest_temporary")" \
	--arg sha "$sha" --arg repository "$ECR_REPOSITORY_URL" --arg digest "$release_digest" \
	--arg tag "$release_tag" --arg published "$released_at" --argjson gates "$expected_gates" '
	(keys | sort) == ["ci_run_id","contract","expected_gates","git_sha","git_tree_sha","image","issues","published_at","release_tag","schema","version"] and
	.contract == "mycfc/release-publication/v1" and .version == $version and
	($version | test("^v(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?$")) and
	.git_sha == $sha and (.git_tree_sha | test("^[0-9a-f]{40}$")) and
	.image == {repository:$repository,digest:$digest} and .release_tag == $tag and
	.published_at == $published and (.ci_run_id | type == "number" and . > 0) and
	(.issues | type == "array" and all(.[]; type == "number" and . > 0)) and
	.expected_gates == $gates and
	(.schema | keys | sort) == ["migration_digest","ordered_migrations"] and
	(.schema.migration_digest | test("^[0-9a-f]{64}$")) and
	(.schema.ordered_migrations | type == "array" and length > 0 and ([.[] | select(. == "reset-baseline-v1")] | length) == 1 and all(.[]; type == "string" and test("^(reset-baseline-v1|[0-9]{3,}_[A-Za-z0-9_-]+)$")) and . == sort)
' "$manifest_temporary" >/dev/null; then
	rm -f "$manifest_temporary"
	log 'signed release manifest does not match the selected release'
	exit 1
fi
publication_manifest_sha256=$(sha256sum "$manifest_temporary" | awk '{print $1}')
release_version=$(jq -r .version "$manifest_temporary")
schema_migration_digest=$(jq -r .schema.migration_digest "$manifest_temporary")
expected_guardian_intake=$(jq -r .expected_gates.guardian_intake "$manifest_temporary")
manifest_inventory_digest=$(printf '%s' "$(jq -r '.schema.ordered_migrations | join("\n")' "$manifest_temporary")" | sha256sum | awk '{print $1}')
[ "$manifest_inventory_digest" = "$schema_migration_digest" ] || {
	rm -f "$manifest_temporary"
	log 'signed release manifest migration inventory does not match its digest'
	exit 1
}
image_sha=$(docker image inspect --format '{{index .Config.Labels "org.opencontainers.image.revision"}}' "$ECR_REPOSITORY_URL:$release_tag")
image_version=$(docker image inspect --format '{{index .Config.Labels "org.opencontainers.image.version"}}' "$ECR_REPOSITORY_URL:$release_tag")
image_schema=$(docker image inspect --format '{{index .Config.Labels "org.mycfc.schema-migration-digest"}}' "$ECR_REPOSITORY_URL:$release_tag")
[ "$image_sha" = "$sha" ] && [ "$image_version" = "$release_version" ] && [ "$image_schema" = "$schema_migration_digest" ] || {
	rm -f "$manifest_temporary"
	log 'application labels do not match the signed release manifest'
	exit 1
}
chmod 0644 "$manifest_temporary"
mv "$manifest_temporary" "$publication_manifest_file"

begin_release_timeline "$released_at"
record_attempt checking
trap rollback EXIT HUP INT TERM
log "event=release_selected tag=$release_tag digest=$release_digest manifest_sha256=$publication_manifest_sha256 active_slot=$active_slot"
if [ -f "$failed_digest_file" ] && [ "$(cat "$failed_digest_file")" = "$release_digest" ]; then
	current_phase=quarantine
	record_attempt quarantined
	write_deployment_receipt quarantined
	log "release $release_digest previously failed validation; waiting for a replacement release"
	exit 0
fi
record_timeline_milestone image-pulled

case "$active_slot" in
	blue|green) active_container="mycfc-production-app-$active_slot-1" ;;
	legacy) active_container='mycfc-production-app-1' ;;
esac
running_image=$(docker inspect --format '{{.Config.Image}}' "$active_container" 2>/dev/null || true)
privacy_worker_image_current=true
if [ "${PRIVACY_WORKER_ENABLED:-false}" = true ]; then
	running_worker_image=$(docker inspect --format '{{.Config.Image}}' mycfc-production-privacy-worker-1 2>/dev/null || true)
	[ "$running_worker_image" = "$image" ] || privacy_worker_image_current=false
fi
if [ "$active_slot" != legacy ] && [ "${MYCFC_IMAGE:-}" = "$image" ] && [ "$running_image" = "$image" ] && [ "$privacy_worker_image_current" = true ]; then
	candidate_slot=$active_slot
	run_phase guardian_release_status observe_guardian_intake
	if [ "${PRIVACY_WORKER_ENABLED:-false}" = true ]; then
		if [ -f "$deployment_receipt_file" ] && jq -e --arg digest "$release_digest" --arg tag "$release_tag" '
			.image.digest == $digest and .release_tag == $tag and .privacy_worker_activation_required == true
		' "$deployment_receipt_file" >/dev/null 2>&1; then
			verify_privacy_worker_inactive
			privacy_worker_activation_required=true
		else
			verify_privacy_worker_active
			privacy_worker_active=true
		fi
	fi
	record_attempt succeeded
	write_deployment_receipt succeeded
	log "release $release_digest is already deployed in the $active_slot slot"
	exit 0
fi
case "$active_slot" in
	blue) candidate_slot=green ;;
	green|legacy) candidate_slot=blue ;;
esac
candidate_service="app-$candidate_slot"
candidate_container="mycfc-production-$candidate_service-1"

backup_file="${env_file}.previous"
cp "$env_file" "$backup_file"
chmod 600 "$backup_file"
chown root:root "$backup_file"

next_file=$(mktemp "${env_file}.next.XXXXXX")
cp "$env_file" "$next_file"
if ! grep -q '^APP_RELEASED_AT=' "$next_file"; then
	printf '\nAPP_RELEASED_AT=\n' >>"$next_file"
fi
if ! grep -q '^RELEASE_REPOSITORY=' "$next_file"; then
	printf 'RELEASE_REPOSITORY=ricardoespsanto/mycfc\n' >>"$next_file"
elif grep -q '^RELEASE_REPOSITORY=cfcoimbra/mycfc$' "$next_file"; then
	updated_file=$(mktemp "${env_file}.updated.XXXXXX")
	sed 's|^RELEASE_REPOSITORY=cfcoimbra/mycfc$|RELEASE_REPOSITORY=ricardoespsanto/mycfc|' "$next_file" >"$updated_file"
	mv "$updated_file" "$next_file"
fi
updated_file=$(mktemp "${env_file}.updated.XXXXXX")
sed "s|^MYCFC_IMAGE=.*|MYCFC_IMAGE=$image|; s|^APP_VERSION=.*|APP_VERSION=$release_version|; s|^APP_RELEASED_AT=.*|APP_RELEASED_AT=$released_at|; s|^GIT_SHA=.*|GIT_SHA=$sha|" "$next_file" >"$updated_file"
mv "$updated_file" "$next_file"
chmod 600 "$next_file"
chown root:root "$next_file"
mv "$next_file" "$env_file"
export MYCFC_IMAGE="$image"
export GUARDIAN_RUNTIME_IMAGE_DIGEST="$release_digest"
export APP_VERSION="$release_version"
export APP_RELEASED_AT="$released_at"
export GIT_SHA="$sha"
release_updated=true

log "event=release_preparing sha=$sha digest=$release_digest candidate_slot=$candidate_slot active_slot=$active_slot"
if [ "${PRIVACY_RESTORE_PROMOTION_GATE_ENABLED:-false}" = true ]; then
	# The candidate image proves it can migrate and replay the oldest valid
	# retained backup before any production migration or traffic change.
	run_phase privacy_restore_drill "$restore_drill_command" "$image"
	run_phase privacy_restore_attestation "$restore_attestation_verify_command" "$image"
fi
if [ "${PRIVACY_WORKER_ENABLED:-false}" = true ]; then
	run_phase privacy_worker_stop systemctl stop mycfc-privacy-worker.service
	privacy_worker_stopped=true
fi
run_phase postgres_ready docker compose --env-file "$env_file" -f "$compose_file" up -d --wait postgres
run_phase database_bootstrap docker compose --env-file "$env_file" -f "$compose_file" --profile release run --rm db-bootstrap
run_phase database_migrate docker compose --env-file "$env_file" -f "$compose_file" --profile release run --rm migrate
# Idempotent defence in depth; migrate already applies this boundary atomically
# before committing any newly created privacy execution tables.
run_phase database_harden docker compose --env-file "$env_file" -f "$compose_file" --profile release run --rm db-bootstrap harden-db
run_phase guardian_release_bind docker compose --env-file "$env_file" -f "$compose_file" --profile release run --rm guardian-release-bind
run_phase guardian_release_status observe_guardian_intake
record_timeline_milestone migration-completed
run_phase candidate_start docker compose --env-file "$env_file" -f "$compose_file" --profile "$candidate_slot" \
	up -d --no-deps --force-recreate "$candidate_service"
candidate_started=true

current_phase=candidate_validation
candidate_validation_started_epoch=$(date +%s)
log "event=deployment_phase_started phase=$current_phase sha=$sha digest=$release_digest slot=$candidate_slot"
candidate_ready=false
for _ in $(seq 1 30); do
	candidate_ip=$(docker inspect --format '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "$candidate_container" 2>/dev/null || true)
	if [ -n "$candidate_ip" ] && curl -fsS -o /dev/null "http://$candidate_ip:8080/health/ready"; then
		candidate_ready=true
		break
	fi
	sleep 2
done
if [ "$candidate_ready" != true ]; then
	log 'candidate failed its readiness check'
	exit 1
fi

for path in /health/live /health/ready; do
	if ! curl -fsS -o /dev/null "http://$candidate_ip:8080$path"; then
		log "candidate check failed for $path"
		exit 1
	fi
done
login_html=$(curl -fsS "http://$candidate_ip:8080/login")
asset_path=$(printf '%s\n' "$login_html" | sed -n 's#.*src="\(/assets/app-[0-9a-f]\{12\}\.js\)".*#\1#p')
if [ -z "$asset_path" ]; then
	log 'candidate login page did not reference a fingerprinted JavaScript asset'
	exit 1
fi
if ! curl -fsS -o /dev/null "http://$candidate_ip:8080$asset_path"; then
	log "candidate check failed for $asset_path"
	exit 1
fi
record_timeline_milestone candidate-ready
candidate_validation_duration_seconds=$(($(date +%s) - candidate_validation_started_epoch))
log "event=deployment_phase_completed phase=$current_phase duration_seconds=$candidate_validation_duration_seconds sha=$sha digest=$release_digest slot=$candidate_slot"
log "event=candidate_validated sha=$sha digest=$release_digest slot=$candidate_slot address=$candidate_ip asset=$asset_path"

current_phase=traffic_switch
log "event=deployment_phase_started phase=$current_phase sha=$sha digest=$release_digest slot=$candidate_slot"
route_backup=$(mktemp "$runtime_dir/mycfc-caddy-upstream.XXXXXX")
cp "$upstream_file" "$route_backup"
caddy_running=$(docker inspect --format '{{.State.Running}}' mycfc-production-caddy-1 2>/dev/null || true)
write_upstream "$candidate_slot"
route_switched=true
traffic_switched=true
if [ "$caddy_running" = true ]; then
	reload_caddy
else
	docker compose --env-file "$env_file" -f "$compose_file" up -d --no-deps caddy
fi
record_timeline_milestone traffic-switched
log "event=deployment_phase_completed phase=$current_phase sha=$sha digest=$release_digest slot=$candidate_slot"
log "event=traffic_switched sha=$sha digest=$release_digest from_slot=$active_slot to_slot=$candidate_slot"

current_phase=post_switch_validation
post_switch_started_epoch=$(date +%s)
log "event=deployment_phase_started phase=$current_phase sha=$sha digest=$release_digest slot=$candidate_slot"
for path in /health/live /health/ready /login "$asset_path"; do
	if ! check_caddy_path "$path"; then
		log "post-switch Caddy check failed for $path"
		exit 1
	fi
done
post_switch_duration_seconds=$(($(date +%s) - post_switch_started_epoch))
log "event=deployment_phase_completed phase=$current_phase duration_seconds=$post_switch_duration_seconds sha=$sha digest=$release_digest slot=$candidate_slot"

if [ "${PRIVACY_WORKER_ENABLED:-false}" = true ]; then
	current_phase=privacy_worker_readiness
	privacy_worker_readiness_started_epoch=$(date +%s)
	log "event=deployment_phase_started phase=$current_phase sha=$sha digest=$release_digest slot=$candidate_slot"
	if "$privacy_worker_command" readiness; then
		privacy_worker_readiness_duration_seconds=$(($(date +%s) - privacy_worker_readiness_started_epoch))
		log "event=deployment_phase_completed phase=$current_phase outcome=ready duration_seconds=$privacy_worker_readiness_duration_seconds sha=$sha digest=$release_digest slot=$candidate_slot"
		run_phase privacy_worker_restart systemctl restart mycfc-privacy-worker.service
		run_phase privacy_worker_verify verify_privacy_worker_active
		privacy_worker_active=true
		privacy_worker_activation_required=false
	else
		privacy_worker_readiness_status=$?
		if [ "$privacy_worker_readiness_status" -ne "$privacy_worker_activation_required_status" ]; then
			exit "$privacy_worker_readiness_status"
		fi
		privacy_worker_readiness_duration_seconds=$(($(date +%s) - privacy_worker_readiness_started_epoch))
		log "event=deployment_phase_completed phase=$current_phase outcome=activation-required duration_seconds=$privacy_worker_readiness_duration_seconds sha=$sha digest=$release_digest slot=$candidate_slot"
		run_phase privacy_worker_stage_inactive docker compose --env-file "$env_file" -f "$compose_file" --profile privacy-worker \
			create --no-build --no-deps --force-recreate privacy-worker
		run_phase privacy_worker_verify_inactive verify_privacy_worker_inactive
		privacy_worker_active=false
		privacy_worker_activation_required=true
		log "event=privacy_worker_activation_required worker_state=stopped readiness_exit_status=$privacy_worker_readiness_status sha=$sha digest=$release_digest slot=$candidate_slot"
	fi
fi

write_state_value "$active_slot_file" "$candidate_slot"
rm -f "$failed_digest_file" "$route_backup"
route_backup=
route_switched=false
release_updated=false
trap - EXIT HUP INT TERM
record_attempt succeeded
record_timeline_milestone deployment-completed
write_deployment_receipt succeeded
log "event=deployment_succeeded sha=$sha digest=$release_digest slot=$candidate_slot"
