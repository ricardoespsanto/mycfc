#!/bin/sh
set -eu

env_file=/etc/mycfc/mycfc.env
deployment_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)

if [ "$(id -u)" -ne 0 ]; then
	printf '%s\n' 'Run this script as root so it can verify the protected environment file.' >&2
	exit 1
fi

if [ ! -f "$env_file" ]; then
	printf '%s\n' "Missing $env_file. See $deployment_dir/README.md." >&2
	exit 1
fi

if [ "$(stat -c '%u:%a' "$env_file")" != '0:600' ]; then
	printf '%s\n' "$env_file must be owned by root and have mode 0600." >&2
	exit 1
fi

docker compose --env-file "$env_file" -f "$deployment_dir/compose.yaml" config -q
docker compose --env-file "$env_file" -f "$deployment_dir/compose.yaml" pull
docker compose --env-file "$env_file" -f "$deployment_dir/compose.yaml" up -d --wait
