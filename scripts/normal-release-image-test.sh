#!/bin/sh
# Actual candidate image/normal-contract check. No predecessor, DB or live API.
set -eu
repository_root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
for command in git docker jq sha256sum date mktemp; do
 command -v "$command" >/dev/null 2>&1 || { printf 'normal image gate requires %s\n' "$command" >&2; exit 1; }
done
git -C "$repository_root" diff --quiet --ignore-submodules --
git -C "$repository_root" diff --cached --quiet --ignore-submodules --
sha=$(git -C "$repository_root" rev-parse --verify 'HEAD^{commit}')
tree=$(git -C "$repository_root" rev-parse --verify 'HEAD^{tree}')
version=v0.0.0-ci
work_dir=$(mktemp -d)
suffix=$(basename "$work_dir" | tr -cd 'a-zA-Z0-9' | tr '[:upper:]' '[:lower:]')
image="mycfc-normal-release-$suffix"
image_created=false
cleanup() {
 if [ "$image_created" = true ]; then docker image rm "$image" >/dev/null 2>&1 || true; fi
 rm -rf "$work_dir"
}
trap cleanup EXIT HUP INT TERM
if docker image inspect "$image" >/dev/null 2>&1; then printf 'normal image name already exists\n' >&2; exit 1; fi
printf 'normal-image phase=build-candidate sha=%s version=%s\n' "$sha" "$version"
docker build --pull=false --build-arg "RELEASE_VERSION=$version" --build-arg "GIT_SHA=$sha" --tag "$image" "$repository_root"
image_created=true
digest=$(docker image inspect --format '{{.Id}}' "$image")
for command in normal-release-contract precision-capabilities schema-inventory schema-digest; do
 docker run --rm --network none "$image" "$command" >"$work_dir/$command.json"
done
schema=$(cat "$work_dir/schema-digest.json")
migrations=$(jq -c . "$work_dir/schema-inventory.json")
stamp=$(date -u +%Y%m%d%H%M%S)
published=$(date -u +%Y-%m-%dT%H:%M:%SZ)
publication="$work_dir/publication.json"
env RELEASE_VERSION="$version" GIT_SHA="$sha" GIT_TREE_SHA="$tree" \
 IMAGE_REPOSITORY=local.invalid/mycfc-ci IMAGE_DIGEST="$digest" \
 RELEASE_TAG="release-$version-$stamp-$sha" SCHEMA_MIGRATION_DIGEST="$schema" \
 MIGRATION_INVENTORY_JSON="$migrations" EXPECTED_GATES_JSON="$(jq -c . "$repository_root/deployment/release-gates.json")" \
 PUBLISHED_AT="$published" CI_RUN_ID="${GITHUB_RUN_ID:-1}" RELEASE_EVIDENCE_OUTPUT="$publication" \
 sh "$repository_root/scripts/release-evidence.sh" publication
verify_contract() {
 env NORMAL_RELEASE_CONTRACT="$1" NORMAL_RELEASE_CAPABILITIES="$work_dir/precision-capabilities.json" \
  RELEASE_PUBLICATION_MANIFEST="$publication" sh "$repository_root/scripts/release-evidence.sh" normal-contract
}
verify_contract "$work_dir/normal-release-contract.json"
jq '.candidate="0000000000000000000000000000000000000000"' "$work_dir/normal-release-contract.json" >"$work_dir/wrong-stamp.json"
if verify_contract "$work_dir/wrong-stamp.json" >"$work_dir/wrong-stamp.log" 2>&1; then
 printf 'normal image accepted wrong compiled stamp\n' >&2; exit 1
fi
printf 'normal-image result=passed candidate_sha=%s image_id=%s schema_digest=%s wrong_stamp=rejected\n' "$sha" "$digest" "$schema"
