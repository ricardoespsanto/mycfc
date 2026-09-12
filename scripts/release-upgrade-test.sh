#!/bin/sh
set -eu

predecessor_ref=${1:-origin/main}
repository_root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)

for command in git docker curl tar mktemp sed grep; do
	command -v "$command" >/dev/null 2>&1 || {
		printf 'release upgrade gate requires %s\n' "$command" >&2
		exit 1
	}
done

git -C "$repository_root" diff --quiet --ignore-submodules -- || {
	printf 'release upgrade gate requires a clean tracked worktree\n' >&2
	exit 1
}
git -C "$repository_root" diff --cached --quiet --ignore-submodules -- || {
	printf 'release upgrade gate requires a clean index\n' >&2
	exit 1
}

predecessor_sha=$(git -C "$repository_root" rev-parse --verify "${predecessor_ref}^{commit}")
candidate_sha=$(git -C "$repository_root" rev-parse --verify 'HEAD^{commit}')
if [ "$predecessor_sha" = "$candidate_sha" ]; then
	printf 'release upgrade gate requires a predecessor distinct from the candidate\n' >&2
	exit 1
fi

work_dir=$(mktemp -d)
suffix=$(basename "$work_dir" | tr -cd 'a-zA-Z0-9' | tail -c 18 | tr '[:upper:]' '[:lower:]')
prefix="mycfc-release-upgrade-$suffix"
network="$prefix"
postgres_container="$prefix-postgres"
candidate_container="$prefix-candidate"
predecessor_image="$prefix-predecessor"
candidate_image="$prefix-candidate"
network_created=false
postgres_started=false
candidate_started=false
predecessor_built=false
candidate_built=false

cleanup() {
	if [ "$candidate_started" = true ]; then docker rm -f "$candidate_container" >/dev/null 2>&1 || true; fi
	if [ "$postgres_started" = true ]; then docker rm -f "$postgres_container" >/dev/null 2>&1 || true; fi
	if [ "$network_created" = true ]; then docker network rm "$network" >/dev/null 2>&1 || true; fi
	if [ "$predecessor_built" = true ]; then docker image rm "$predecessor_image" >/dev/null 2>&1 || true; fi
	if [ "$candidate_built" = true ]; then docker image rm "$candidate_image" >/dev/null 2>&1 || true; fi
	rm -rf "$work_dir"
}
trap cleanup EXIT HUP INT TERM

printf 'release-upgrade phase=archive-predecessor sha=%s\n' "$predecessor_sha"
mkdir "$work_dir/predecessor"
git -C "$repository_root" archive "$predecessor_sha" | tar -x -C "$work_dir/predecessor"

printf 'release-upgrade phase=build-predecessor\n'
docker build --pull=false --tag "$predecessor_image" "$work_dir/predecessor"
predecessor_built=true
printf 'release-upgrade phase=build-candidate sha=%s\n' "$candidate_sha"
docker build --pull=false --tag "$candidate_image" "$repository_root"
candidate_built=true

docker network create "$network" >/dev/null
network_created=true
docker run -d --name "$postgres_container" --network "$network" \
	-e POSTGRES_DB=mycfc -e POSTGRES_USER=postgres -e POSTGRES_PASSWORD=release-parity-only \
	postgres:16.9-alpine3.21 >/dev/null
postgres_started=true

ready=false
for _ in $(seq 1 30); do
	if docker exec "$postgres_container" pg_isready -U postgres -d mycfc >/dev/null 2>&1; then
		ready=true
		break
	fi
	sleep 1
done
[ "$ready" = true ] || { printf 'predecessor database did not become ready\n' >&2; exit 1; }

admin_url="postgres://postgres:release-parity-only@$postgres_container:5432/mycfc?sslmode=disable"
migration_url="postgres://mycfc_migration:release-parity-migration@$postgres_container:5432/mycfc?sslmode=disable"
app_url="postgres://mycfc_app:release-parity-app@$postgres_container:5432/mycfc?sslmode=disable"
guardian_url="postgres://mycfc_guardian_release_bind:release-parity-guardian@$postgres_container:5432/mycfc?sslmode=disable"

run_database_command() {
	image=$1
	database_url=$2
	shift 2
	docker run --rm --network "$network" \
		-e APP_ENV=test -e DATABASE_URL="$database_url" \
		-e APP_DB_USER=mycfc_app -e APP_DB_PASSWORD=release-parity-app \
		-e MIGRATION_DB_USER=mycfc_migration -e MIGRATION_DB_PASSWORD=release-parity-migration \
		"$image" "$@"
}

printf 'release-upgrade phase=predecessor-bootstrap\n'
run_database_command "$predecessor_image" "$admin_url" bootstrap-db
printf 'release-upgrade phase=predecessor-migrate\n'
run_database_command "$predecessor_image" "$migration_url" migrate

# Production pre-stages this narrowly privileged identity independently of the
# candidate. Prove that the predecessor contract can establish it before any
# candidate migration or hardening begins.
printf 'release-upgrade phase=guardian-provision\n'
docker run --rm --network "$network" -e APP_ENV=test -e DATABASE_URL="$admin_url" \
	-e GUARDIAN_RELEASE_BIND_DATABASE_URL="$guardian_url" \
	"$predecessor_image" provision-guardian-release-bind

# Match the production candidate order exactly: bootstrap, migrate, harden and
# bind the immutable guardian image before the candidate can start.
printf 'release-upgrade phase=candidate-bootstrap\n'
run_database_command "$candidate_image" "$admin_url" bootstrap-db
printf 'release-upgrade phase=candidate-migrate\n'
run_database_command "$candidate_image" "$migration_url" migrate
printf 'release-upgrade phase=candidate-harden\n'
run_database_command "$candidate_image" "$admin_url" harden-db
runtime_digest=sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
printf 'release-upgrade phase=guardian-bind\n'
docker run --rm --network "$network" \
	-e GUARDIAN_RELEASE_BIND_DATABASE_URL="$guardian_url" \
	-e GUARDIAN_RELEASE_BIND_EXPECTED_DATABASE=mycfc \
	-e GUARDIAN_RUNTIME_IMAGE_DIGEST="$runtime_digest" \
	"$candidate_image" bind-guardian-release

printf 'release-upgrade phase=candidate-start\n'
docker run -d --name "$candidate_container" --network "$network" -p 127.0.0.1::8080 \
	--env-file "$repository_root/.env.example" \
	-e APP_ENV=test -e APP_VERSION=v0.0.0-parity -e GIT_SHA="$candidate_sha" \
	-e GUARDIAN_RUNTIME_IMAGE_DIGEST="$runtime_digest" \
	-e BASE_URL=http://localhost -e DATABASE_URL="$app_url" \
	-e S3_ENDPOINT=http://object-store.invalid:9000 \
	"$candidate_image" serve >/dev/null
candidate_started=true
candidate_port=$(docker port "$candidate_container" 8080/tcp | sed -n '1s/.*://p')
case "$candidate_port" in ''|*[!0-9]*) printf 'candidate port could not be resolved\n' >&2; exit 1 ;; esac

ready=false
for _ in $(seq 1 30); do
	if curl -fsS -o /dev/null "http://127.0.0.1:$candidate_port/health/ready"; then
		ready=true
		break
	fi
	sleep 1
done
if [ "$ready" != true ]; then
	docker logs "$candidate_container" >&2 || true
	printf 'candidate failed its readiness check\n' >&2
	exit 1
fi
for path in /health/live /health/ready; do
	curl -fsS -o /dev/null "http://127.0.0.1:$candidate_port$path"
done
login_html=$(curl -fsS "http://127.0.0.1:$candidate_port/login")
asset_path=$(printf '%s\n' "$login_html" | sed -n 's#.*src="\(/assets/app-[0-9a-f]\{12\}\.js\)".*#\1#p')
[ -n "$asset_path" ] || { printf 'candidate login did not reference a fingerprinted JavaScript asset\n' >&2; exit 1; }
curl -fsS -o /dev/null "http://127.0.0.1:$candidate_port$asset_path"

printf 'release-upgrade result=passed predecessor_sha=%s candidate_sha=%s schema_digest=%s\n' \
	"$predecessor_sha" "$candidate_sha" "$(docker run --rm "$candidate_image" schema-digest)"
