#!/bin/sh
set -eu

env_file=${MYCFC_ENV_FILE:-/etc/mycfc/mycfc.env}
token_file=${MYCFC_HETZNER_TOKEN_FILE:-/etc/mycfc/hetzner-read/token}
api_base=https://api.hetzner.cloud/v1
work_dir=$(mktemp -d /var/tmp/mycfc-hetzner-posture.XXXXXX)
trap 'rm -rf "$work_dir"' EXIT HUP INT TERM

read_setting() {
	setting_name=$1
	(
		set -a
		. "$env_file"
		set +a
		eval "printf '%s' \"\${$setting_name-}\""
	)
}

log_event() {
	printf '%s\n' "$1"
	logger -t mycfc-hetzner-posture -- "$1" 2>/dev/null || true
}

fail() {
	log_event "hetzner_backup_posture_failed reason=$1"
	exit 1
}

enabled=${HETZNER_BACKUP_POSTURE_ENABLED:-$(read_setting HETZNER_BACKUP_POSTURE_ENABLED)}
server_id=${HETZNER_SERVER_ID:-$(read_setting HETZNER_SERVER_ID)}
project_ref=${HETZNER_PROJECT_REF:-$(read_setting HETZNER_PROJECT_REF)}

if [ "${enabled:-false}" != true ]; then
	fail disabled
fi
case "$server_id" in
	''|*[!0-9]*) fail invalid_scope ;;
esac
if [ "$server_id" -eq 0 ] || ! printf '%s' "$project_ref" | grep -Eq '^[A-Za-z0-9][A-Za-z0-9_.-]{0,62}$'; then
	fail invalid_scope
fi

if [ ! -f "$token_file" ] || [ "$(stat -c '%u:%a' "$token_file" 2>/dev/null || true)" != "$(id -u):600" ]; then
	fail insecure_token
fi
token_size=$(wc -c <"$token_file" | tr -d ' ')
case "$token_size" in
	''|*[!0-9]*) fail invalid_token ;;
esac
if [ "$token_size" -eq 0 ] || [ "$token_size" -gt 4096 ] || [ "$(awk 'END { print NR }' "$token_file")" -ne 1 ]; then
	fail invalid_token
fi
token=$(tr -d '\n' <"$token_file")
if ! printf '%s' "$token" | LC_ALL=C grep -Eq '^[[:graph:]]+$'; then
	fail invalid_token
fi

api_get() {
	api_path=$1
	api_destination=$2
	if ! curl --fail --silent --max-time 30 \
		--header "Authorization: Bearer $token" \
		--header 'Accept: application/json' \
		--output "$api_destination" "$api_base$api_path"; then
		fail provider_unavailable
	fi
}

log_event hetzner_backup_posture_started

server_response=$work_dir/server.json
api_get "/servers/$server_id" "$server_response"
if ! jq -e --argjson server_id "$server_id" --arg project_ref "$project_ref" '
  .server.id == $server_id
  and (.server.backup_window | type == "string" and length > 0)
  and (.server.labels | type == "object")
  and .server.labels.project == $project_ref
' "$server_response" >/dev/null 2>&1; then
	fail server_posture_unverified
fi

inventory_once() {
	inventory_destination=$1
	inventory_raw=$2
	: >"$inventory_raw"
	for image_type in backup snapshot; do
		type_raw=$work_dir/raw-$image_type-$(basename "$inventory_destination").jsonl
		: >"$type_raw"
		page=1
		expected_total=
		pages=0
		while :; do
			pages=$((pages + 1))
			if [ "$pages" -gt 1000 ]; then
				fail pagination_invalid
			fi
			response=$work_dir/images-$image_type-$pages-$(basename "$inventory_destination").json
			api_get "/images?type=$image_type&sort=id&page=$page&per_page=50" "$response"
			if ! jq -e --argjson page "$page" --arg image_type "$image_type" '
          (.images | type == "array")
          and (.meta.pagination.page == $page)
          and (.meta.pagination.total_entries | type == "number" and . >= 0 and floor == .)
          and all(.images[];
            (.id | type == "number" and . > 0 and floor == .)
            and .type == $image_type
            and (.created | type == "string" and length > 0)
            and (.labels | type == "object")
          )
        ' "$response" >/dev/null 2>&1; then
				fail provider_response_invalid
			fi
			total=$(jq -r '.meta.pagination.total_entries' "$response")
			if [ -z "$expected_total" ]; then
				expected_total=$total
			elif [ "$expected_total" -ne "$total" ]; then
				fail inventory_changed
			fi
			jq -c '.images[]' "$response" >>"$type_raw"
			next=$(jq -r '.meta.pagination.next_page // ""' "$response")
			if [ -z "$next" ]; then
				break
			fi
			case "$next" in
				''|*[!0-9]*) fail pagination_invalid ;;
			esac
			if [ "$next" -le "$page" ] || [ "$next" -gt 1000 ]; then
				fail pagination_invalid
			fi
			page=$next
		done
		if [ "$(wc -l <"$type_raw" | tr -d ' ')" -ne "$expected_total" ]; then
			fail pagination_incomplete
		fi
		cat "$type_raw" >>"$inventory_raw"
	done
	if ! jq -s --argjson server_id "$server_id" '
      def epoch:
        sub("\\.[0-9]+(Z|\\+00:00)$"; "Z")
        | sub("\\+00:00$"; "Z")
        | fromdateiso8601;
      map(select(
        (.type == "backup" and .bound_to.id == $server_id)
        or (.type == "snapshot" and .created_from.id == $server_id)
      ))
      | map({
          id, type,
          created_epoch: (.created | epoch),
          bound_to: (.bound_to.id // null),
          created_from: (.created_from.id // null),
          owner_ref: .labels["mycfc-owner-ref"],
          reason_code: .labels["mycfc-reason-code"],
          declared_created_at: .labels["mycfc-created-at"],
          expires_at: .labels["mycfc-expires-at"]
        })
      | sort_by(.type, .id)
      | if ([.[].id] | unique | length) != length then error("duplicate") else . end
    ' "$inventory_raw" >"$inventory_destination" 2>/dev/null; then
		fail inventory_invalid
	fi
}

first=$work_dir/inventory-first.json
second=$work_dir/inventory-second.json
inventory_once "$first" "$work_dir/raw-first.jsonl"
inventory_once "$second" "$work_dir/raw-second.jsonl"
if ! cmp -s "$first" "$second"; then
	fail inventory_changed
fi

now_epoch=${MYCFC_POSTURE_NOW_EPOCH:-$(date -u +%s)}
case "$now_epoch" in
	''|*[!0-9]*) fail clock_invalid ;;
esac

if ! jq -e --argjson now "$now_epoch" '
  all(.[]; .created_epoch <= $now)
  and all(.[] | select(.type == "snapshot");
    (.owner_ref | type == "string" and test("^[A-Z0-9][A-Z0-9_-]{2,62}$"))
    and (.reason_code | type == "string" and test("^[A-Z][A-Z0-9_-]{2,62}$"))
    and (.declared_created_at | type == "string" and test("^[0-9]{10}$"))
    and (.expires_at | type == "string" and test("^[0-9]{10}$"))
    and (.declared_created_at | tonumber) == .created_epoch
    and (.expires_at | tonumber) > .created_epoch
    and (.expires_at | tonumber) <= (.created_epoch + 2592000)
    and (.expires_at | tonumber) > $now
  )
' "$first" >/dev/null 2>&1; then
	fail snapshot_metadata_invalid
fi

automatic_count=$(jq '[.[] | select(.type == "backup")] | length' "$first")
snapshot_count=$(jq '[.[] | select(.type == "snapshot")] | length' "$first")
if [ "$automatic_count" -gt 7 ]; then
	fail automatic_backup_limit_exceeded
fi

oldest_backup_epoch=$(jq --argjson now "$now_epoch" '[.[] | select(.type == "backup") | .created_epoch] | min // $now' "$first")
oldest_snapshot_epoch=$(jq --argjson now "$now_epoch" '[.[] | select(.type == "snapshot") | .created_epoch] | min // $now' "$first")
oldest_backup_age=$((now_epoch - oldest_backup_epoch))
oldest_snapshot_age=$((now_epoch - oldest_snapshot_epoch))

canonical=$work_dir/inventory-canonical.json
jq -cS --arg project_ref "$project_ref" --argjson server_id "$server_id" \
	'{contract:"mycfc/hetzner-backup-posture/v1",project_ref:$project_ref,server_id:$server_id,images:.}' \
	"$first" >"$canonical"
inventory_sha256=$(sha256sum "$canonical" | cut -d ' ' -f 1)

log_event "hetzner_backup_posture_succeeded automatic_backup_count=$automatic_count manual_snapshot_count=$snapshot_count oldest_backup_age_seconds=$oldest_backup_age oldest_snapshot_age_seconds=$oldest_snapshot_age inventory_sha256=$inventory_sha256"
