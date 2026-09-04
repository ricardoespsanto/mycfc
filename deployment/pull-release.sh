#!/bin/sh
set -eu

env_file=${MYCFC_ENV_FILE:-/etc/mycfc/mycfc.env}
deployment_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
compose_file="$deployment_dir/compose.yaml"
state_dir=${MYCFC_DEPLOYMENT_STATE_DIR:-/etc/mycfc/deployment}
runtime_dir=${MYCFC_RUNTIME_DIR:-/run}
release_credentials_file=${MYCFC_RELEASE_AWS_CREDENTIALS_FILE:-/etc/mycfc/release-aws/credentials}
release_aws_profile=${MYCFC_RELEASE_AWS_PROFILE:-mycfc-release}
active_slot_file="$state_dir/active-slot"
failed_digest_file="$state_dir/failed-release-digest"
last_attempt_digest_file="$state_dir/last-attempt-digest"
last_attempt_result_file="$state_dir/last-attempt-result"
last_attempt_at_file="$state_dir/last-attempt-at"
last_attempt_file="$state_dir/last-attempt"
timeline_digest_file="$state_dir/release-timeline-digest"
timeline_tag_file="$state_dir/release-timeline-tag"
upstream_file="$state_dir/caddy-upstream.caddy"
lock_file="$runtime_dir/mycfc-pull-release.lock"
agent_started_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)
backup_file=
route_backup=
release_digest=
candidate_slot=
candidate_service=
candidate_started=false
route_switched=false
release_updated=false

log() {
	printf '%s\n' "$*"
	logger -t mycfc-pull-release -- "$*"
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

rollback() {
	status=$?
	trap - EXIT HUP INT TERM
	if [ "$route_switched" = true ] && [ -n "$route_backup" ] && [ -f "$route_backup" ]; then
		log 'candidate failed after traffic switch; restoring the previous Caddy upstream'
		temporary=$(mktemp "$state_dir/.upstream.XXXXXX")
		cp "$route_backup" "$temporary"
		chmod 0644 "$temporary"
		mv "$temporary" "$upstream_file"
		reload_caddy || log 'Caddy upstream rollback failed'
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
		if [ -n "$release_digest" ]; then
			write_state_value "$failed_digest_file" "$release_digest"
			log "quarantined failed release digest $release_digest"
		fi
	fi
	if [ "$status" -ne 0 ] && [ -n "$release_digest" ]; then
		record_attempt failed
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
case "$release_digest" in
	sha256:*) ;;
	*) log 'release has no valid digest'; exit 1 ;;
esac
begin_release_timeline "$released_at"
record_attempt checking
trap rollback EXIT HUP INT TERM
if [ -f "$failed_digest_file" ] && [ "$(cat "$failed_digest_file")" = "$release_digest" ]; then
	record_attempt quarantined
	log "release $release_digest previously failed validation; waiting for a replacement release"
	exit 0
fi

docker pull "$ECR_REPOSITORY_URL:$release_tag"
image=$(docker image inspect --format '{{index .RepoDigests 0}}' "$ECR_REPOSITORY_URL:$release_tag")
case "$image" in
	*"@$release_digest") ;;
	*) log 'pulled image digest does not match ECR release metadata'; exit 1 ;;
esac
record_timeline_milestone image-pulled

case "$active_slot" in
	blue|green) active_container="mycfc-production-app-$active_slot-1" ;;
	legacy) active_container='mycfc-production-app-1' ;;
esac
running_image=$(docker inspect --format '{{.Config.Image}}' "$active_container" 2>/dev/null || true)
if [ "$active_slot" != legacy ] && [ "${MYCFC_IMAGE:-}" = "$image" ] && [ "$running_image" = "$image" ]; then
	record_attempt succeeded
	log "release $release_digest is already deployed in the $active_slot slot"
	exit 0
fi

sha=$(docker image inspect --format '{{index .Config.Labels "org.opencontainers.image.revision"}}' "$ECR_REPOSITORY_URL:$release_tag")
case "$sha" in
	????????????????????????????????????????) ;;
	*) log 'release has no valid git SHA label'; exit 1 ;;
esac
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
sed "s|^MYCFC_IMAGE=.*|MYCFC_IMAGE=$image|; s|^APP_VERSION=.*|APP_VERSION=$release_tag|; s|^APP_RELEASED_AT=.*|APP_RELEASED_AT=$released_at|; s|^GIT_SHA=.*|GIT_SHA=$sha|" "$next_file" >"$updated_file"
mv "$updated_file" "$next_file"
chmod 600 "$next_file"
chown root:root "$next_file"
mv "$next_file" "$env_file"
export MYCFC_IMAGE="$image"
export APP_VERSION="$release_tag"
export APP_RELEASED_AT="$released_at"
export GIT_SHA="$sha"
release_updated=true

log "preparing SHA $sha with digest $release_digest in the $candidate_slot slot"
docker compose --env-file "$env_file" -f "$compose_file" up -d --wait postgres
docker compose --env-file "$env_file" -f "$compose_file" --profile release run --rm db-bootstrap
docker compose --env-file "$env_file" -f "$compose_file" --profile release run --rm migrate
record_timeline_milestone migration-completed
docker compose --env-file "$env_file" -f "$compose_file" --profile "$candidate_slot" \
	up -d --no-deps --force-recreate "$candidate_service"
candidate_started=true

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

route_backup=$(mktemp "$runtime_dir/mycfc-caddy-upstream.XXXXXX")
cp "$upstream_file" "$route_backup"
caddy_running=$(docker inspect --format '{{.State.Running}}' mycfc-production-caddy-1 2>/dev/null || true)
write_upstream "$candidate_slot"
route_switched=true
if [ "$caddy_running" = true ]; then
	reload_caddy
else
	docker compose --env-file "$env_file" -f "$compose_file" up -d --no-deps caddy
fi
record_timeline_milestone traffic-switched

for path in /health/live /health/ready /login "$asset_path"; do
	if ! check_caddy_path "$path"; then
		log "post-switch Caddy check failed for $path"
		exit 1
	fi
done

write_state_value "$active_slot_file" "$candidate_slot"
rm -f "$failed_digest_file" "$route_backup"
route_backup=
route_switched=false
release_updated=false
trap - EXIT HUP INT TERM
record_attempt succeeded
record_timeline_milestone deployment-completed
log "deployed SHA $sha with digest $release_digest in the $candidate_slot slot"
