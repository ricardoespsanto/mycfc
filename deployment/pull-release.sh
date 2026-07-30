#!/bin/sh
set -eu

env_file=/etc/mycfc/mycfc.env
deployment_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
compose_file="$deployment_dir/compose.yaml"
lock_file=/run/mycfc-pull-release.lock
backup_file=
release_updated=false

log() {
	logger -t mycfc-pull-release -- "$*"
}

rollback() {
	status=$?
	if [ "$release_updated" = true ]; then
		log "deployment failed; restoring the previous release"
		mv "$backup_file" "$env_file"
		docker compose --env-file "$env_file" -f "$compose_file" up -d --wait || log "rollback compose update failed"
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
digest=$(aws ecr describe-images --region "$AWS_REGION" --repository-name "${ECR_REPOSITORY_URL#*/}" --image-ids imageTag=production --query 'imageDetails[0].imageDigest' --output text)
case "$digest" in
	sha256:*) ;;
	*) log "ECR production tag has no image digest"; exit 1 ;;
esac

image="$ECR_REPOSITORY_URL@$digest"
if [ "${MYCFC_IMAGE:-}" = "$image" ]; then
	log "release $digest is already deployed"
	exit 0
fi

sha=$(aws ecr describe-images --region "$AWS_REGION" --repository-name "${ECR_REPOSITORY_URL#*/}" --image-ids imageDigest="$digest" --query 'imageDetails[0].imageTags[?starts_with(@, `git-`)] | [0]' --output text)
case "$sha" in
	git-????????????????????????????????????????) sha=${sha#git-} ;;
	*) log "release $digest has no valid git SHA tag"; exit 1 ;;
esac

# The systemd unit makes home directories inaccessible, so keep the temporary
# ECR credential helper state under its writable runtime directory.
export DOCKER_CONFIG=/run/mycfc-pull-release-docker
mkdir -p "$DOCKER_CONFIG"
aws ecr get-login-password --region "$AWS_REGION" | docker login --username AWS --password-stdin "$registry"
backup_file=$(mktemp "${env_file}.previous.XXXXXX")
cp "$env_file" "$backup_file"
trap rollback EXIT HUP INT TERM

next_file=$(mktemp "${env_file}.next.XXXXXX")
cp "$env_file" "$next_file"
sed -i "s|^MYCFC_IMAGE=.*|MYCFC_IMAGE=$image|; s|^APP_VERSION=.*|APP_VERSION=$sha|; s|^GIT_SHA=.*|GIT_SHA=$sha|" "$next_file"
chmod 600 "$next_file"
chown root:root "$next_file"
mv "$next_file" "$env_file"
release_updated=true

log "deploying SHA $sha with digest $digest"
docker compose --env-file "$env_file" -f "$compose_file" pull
docker compose --env-file "$env_file" -f "$compose_file" up -d --wait

asset_path=$(jq -r '."app.js"' "$deployment_dir/../ui/static/dist/manifest.json")
for path in /health/ready /login "$asset_path"; do
	status=$(curl --silent --show-error --output /dev/null --write-out '%{http_code}' --resolve "$MYCFC_DOMAIN:443:127.0.0.1" --max-time 10 "https://$MYCFC_DOMAIN$path")
	if [ "$status" != 200 ]; then
		log "release check failed for $path with HTTP $status"
		exit 1
	fi
done

release_updated=false
rm -f "$backup_file"
trap - EXIT HUP INT TERM
log "deployed SHA $sha with digest $digest"
