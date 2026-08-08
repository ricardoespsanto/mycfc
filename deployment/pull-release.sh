#!/bin/sh
set -eu

env_file=/etc/mycfc/mycfc.env
deployment_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
compose_file="$deployment_dir/compose.yaml"
lock_file=/run/mycfc-pull-release.lock
backup_file=
release_updated=false

log() {
	printf '%s\n' "$*"
	logger -t mycfc-pull-release -- "$*"
}

rollback() {
	status=$?
	if [ "$release_updated" = true ]; then
		log "deployment failed; restoring the previous release"
		cp "$backup_file" "$env_file"
		docker compose --env-file "$env_file" -f "$compose_file" up -d --wait --force-recreate app || log "rollback compose update failed"
	fi
	exit "$status"
}

if [ "$(id -u)" -ne 0 ]; then
	log "must run as root"
	exit 1
fi

if [ ! -f "$env_file" ] || [ "$(stat -c '%u:%a' "$env_file")" != '0:600' ]; then
	log "missing or insecure environment file"
	exit 1
fi

exec 9>"$lock_file"
if ! flock -n 9; then
	log "another release check is already running"
	exit 0
fi

# This file is root-owned and mode 0600; it is the host's deployment configuration.
set -a
. "$env_file"
set +a
: "${AWS_REGION:?}"
: "${ECR_REPOSITORY_URL:?}"
: "${MYCFC_DOMAIN:?}"

registry=${ECR_REPOSITORY_URL%%/*}
repository_name=${ECR_REPOSITORY_URL#*/}
# The systemd unit makes home directories inaccessible, so keep the temporary
# ECR credential helper state under its writable runtime directory.
export DOCKER_CONFIG=/run/mycfc-pull-release-docker
mkdir -p "$DOCKER_CONFIG"
ecr_password=$(aws ecr get-login-password --region "$AWS_REGION")
printf '%s' "$ecr_password" | docker login --username AWS --password-stdin "$registry"

tags=$(aws ecr describe-images --region "$AWS_REGION" --repository-name "$repository_name" --query 'imageDetails[].imageTags[]' --output text)
release_tags=$(printf '%s\n' "$tags" | tr '\t' '\n' | awk '/^release-/')
release_tag=$(printf '%s\n' "$release_tags" | sort | tail -n 1)
case "$release_tag" in
	release-??????????????-????????????????????????????????????????) ;;
	*) log "ECR has no valid release tag"; exit 1 ;;
esac

docker pull "$ECR_REPOSITORY_URL:$release_tag"

image=$(docker image inspect --format '{{index .RepoDigests 0}}' "$ECR_REPOSITORY_URL:$release_tag")
digest=$(aws ecr describe-images --region "$AWS_REGION" --repository-name "$repository_name" --image-ids imageTag="$release_tag" --query 'imageDetails[0].imageDigest' --output text)
case "$digest" in
	sha256:*) ;;
	*) log "pulled production image has no digest"; exit 1 ;;
esac

case "$image" in
	*"@$digest") ;;
	*) log "pulled image digest does not match ECR release metadata"; exit 1 ;;
esac

running_image=$(docker inspect --format '{{.Config.Image}}' mycfc-production-app-1 2>/dev/null || true)
if [ "${MYCFC_IMAGE:-}" = "$image" ] && [ "$running_image" = "$image" ]; then
	log "release $digest is already deployed"
	exit 0
fi

sha=$(docker image inspect --format '{{index .Config.Labels "org.opencontainers.image.revision"}}' "$ECR_REPOSITORY_URL:$release_tag")
case "$sha" in
	????????????????????????????????????????) ;;
	*) log "release $digest has no valid git SHA label"; exit 1 ;;
esac
stamp=${release_tag#release-}
stamp=${stamp%%-*}
released_at="$(printf '%s-%s-%sT%s:%s:%sZ' "$(printf '%s' "$stamp" | cut -c1-4)" "$(printf '%s' "$stamp" | cut -c5-6)" "$(printf '%s' "$stamp" | cut -c7-8)" "$(printf '%s' "$stamp" | cut -c9-10)" "$(printf '%s' "$stamp" | cut -c11-12)" "$(printf '%s' "$stamp" | cut -c13-14)")"

backup_file="${env_file}.previous"
cp "$env_file" "$backup_file"
chmod 600 "$backup_file"
chown root:root "$backup_file"
trap rollback EXIT HUP INT TERM

next_file=$(mktemp "${env_file}.next.XXXXXX")
cp "$env_file" "$next_file"
if ! grep -q '^APP_RELEASED_AT=' "$next_file"; then
	printf '\nAPP_RELEASED_AT=\n' >> "$next_file"
fi
if ! grep -q '^RELEASE_REPOSITORY=' "$next_file"; then
	printf 'RELEASE_REPOSITORY=ricardoespsanto/mycfc\n' >> "$next_file"
elif grep -q '^RELEASE_REPOSITORY=cfcoimbra/mycfc$' "$next_file"; then
	sed -i 's|^RELEASE_REPOSITORY=cfcoimbra/mycfc$|RELEASE_REPOSITORY=ricardoespsanto/mycfc|' "$next_file"
fi
sed -i "s|^MYCFC_IMAGE=.*|MYCFC_IMAGE=$image|; s|^APP_VERSION=.*|APP_VERSION=$release_tag|; s|^APP_RELEASED_AT=.*|APP_RELEASED_AT=$released_at|; s|^GIT_SHA=.*|GIT_SHA=$sha|" "$next_file"
chmod 600 "$next_file"
chown root:root "$next_file"
"$deployment_dir/resolve-runtime-secrets.sh" "$next_file"
mv "$next_file" "$env_file"
release_updated=true

log "deploying SHA $sha with digest $digest"
docker compose --env-file "$env_file" -f "$compose_file" pull
docker compose --env-file "$env_file" -f "$compose_file" build caddy
docker compose --env-file "$env_file" -f "$compose_file" up -d --wait postgres
docker compose --env-file "$env_file" -f "$compose_file" --profile release run --rm db-bootstrap
docker compose --env-file "$env_file" -f "$compose_file" --profile release run --rm migrate
docker compose --env-file "$env_file" -f "$compose_file" up -d --wait --force-recreate app

asset_path=$(jq -r '."app.js"' "$deployment_dir/../ui/static/dist/manifest.json")
for path in /health/ready /login "$asset_path"; do
	# Caddy is private behind cloudflared. Validate the exact virtual host locally;
	# Cloudflare can reject a request sourced from the host itself, so the public
	# edge is checked by an external monitor rather than the release transaction.
	check_passed=false
	for attempt in $(seq 1 30); do
		if docker compose --env-file "$env_file" -f "$compose_file" exec -T caddy wget -q -O /dev/null --header="Host: $MYCFC_DOMAIN" "http://127.0.0.1$path"; then
			check_passed=true
			break
		fi
		sleep 2
	done
	if [ "$check_passed" != true ]; then
		log "release check failed for $path"
		exit 1
	fi
done

release_updated=false
trap - EXIT HUP INT TERM
log "deployed SHA $sha with digest $digest"
