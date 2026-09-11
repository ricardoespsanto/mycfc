#!/bin/sh
set -eu

repository_root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
image="mycfc-legacy-media-purge-test:$(date +%s)-$$"
container=
cleanup() {
	if [ -n "$container" ]; then
		docker rm "$container" >/dev/null 2>&1 || true
	fi
	docker image rm "$image" >/dev/null 2>&1 || true
}
trap cleanup EXIT HUP INT TERM

docker build --platform linux/amd64 --file "$repository_root/Dockerfile.legacy-media-purge" --tag "$image" "$repository_root" >/dev/null

docker image inspect "$image" | jq -e '
  length == 1 and
  .[0].Config.User == "nonroot:nonroot" and
  .[0].Config.WorkingDir == "/app" and
  .[0].Config.Entrypoint == ["/app/legacy-media-purge"] and
  (.[0].Config.Cmd == null or .[0].Config.Cmd == [])
' >/dev/null

container=$(docker create "$image")
app_entries=$(docker export "$container" | tar -tf - | awk '/^app\/?/ {print}' | sort)
expected=$(printf '%s\n' app/ app/legacy-media-purge | sort)
[ "$app_entries" = "$expected" ]

printf '%s\n' 'legacy media purge image tests passed'
