#!/usr/bin/env bash
set -Eeuo pipefail

url=${1:?usage: wait-for-http.sh URL [attempts]}
attempts=${2:-60}

for ((attempt = 1; attempt <= attempts; attempt++)); do
  if curl --fail --silent --show-error --max-time 2 "$url" >/dev/null; then
    exit 0
  fi
  sleep 1
done

echo "timed out waiting for $url" >&2
exit 1
