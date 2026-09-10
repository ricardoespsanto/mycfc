#!/bin/sh
set -eu

root_dir=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
test_dir=$(mktemp -d)
trap 'rm -rf "$test_dir"' EXIT HUP INT TERM
mkdir -p "$test_dir/bin"

now_epoch=1800000000
created_one=$((now_epoch - 86400))
created_two=$((now_epoch - 172800))
expires_one=$((created_one + 2591000))
expires_two=$((created_two + 2591000))
created_one_iso=$(date -u -d "@$created_one" +%Y-%m-%dT%H:%M:%SZ)
created_two_iso=$(date -u -d "@$created_two" +%Y-%m-%dT%H:%M:%SZ)

cat >"$test_dir/env" <<'EOF'
HETZNER_BACKUP_POSTURE_ENABLED=true
HETZNER_SERVER_ID=156817862
HETZNER_PROJECT_REF=mycfc
EOF
printf '%s\n' 'ABCDEFGHIJKLMNOPQRSTUVWXYZabc._~+/=' >"$test_dir/token"
chmod 0600 "$test_dir/token"

cat >"$test_dir/server.json" <<'EOF'
{"server":{"id":156817862,"name":"secret-production-host","backup_window":"22-02","labels":{"project":"mycfc","environment":"production"}}}
EOF
cat >"$test_dir/server-disabled.json" <<'EOF'
{"server":{"id":156817862,"name":"secret-production-host","backup_window":null,"labels":{"project":"mycfc"}}}
EOF

cat >"$test_dir/page-1.json" <<EOF
{"images":[
 {"id":101,"type":"backup","name":"secret-backup-name","created":"$created_one_iso","created_from":{"id":156817862},"bound_to":{"id":156817862},"labels":{}},
 {"id":999,"type":"backup","name":"foreign-server-backup","created":"$created_one_iso","created_from":{"id":999999999},"bound_to":{"id":999999999},"labels":{}},
 {"id":201,"type":"snapshot","name":"member-name-must-not-leak","created":"$created_one_iso","created_from":{"id":156817862},"bound_to":null,"labels":{"mycfc-owner-ref":"OPERATOR_01","mycfc-reason-code":"RESTORE_DRILL","mycfc-created-at":"$created_one","mycfc-expires-at":"$expires_one"}}
],"meta":{"pagination":{"page":1,"per_page":3,"next_page":2,"last_page":2,"total_entries":6}}}
EOF
cat >"$test_dir/page-2.json" <<EOF
{"images":[
 {"id":102,"type":"backup","name":"another-secret-backup","created":"$created_two_iso","created_from":{"id":156817862},"bound_to":{"id":156817862},"labels":{}},
 {"id":202,"type":"snapshot","name":"another-member-name","created":"$created_two_iso","created_from":{"id":156817862},"bound_to":null,"labels":{"mycfc-owner-ref":"OPERATOR_02","mycfc-reason-code":"PRE_CHANGE","mycfc-created-at":"$created_two","mycfc-expires-at":"$expires_two"}},
 {"id":998,"type":"snapshot","name":"foreign-private-snapshot","created":"$created_two_iso","created_from":{"id":999999999},"bound_to":null,"labels":{}}
],"meta":{"pagination":{"page":2,"per_page":3,"next_page":null,"last_page":2,"total_entries":6}}}
EOF

jq -n --arg created "$created_one_iso" --argjson created_epoch "$created_one" --argjson expiry "$expires_one" '
 {images:[range(0;8) | {id:(300+.),type:"backup",name:"private",created:$created,created_from:{id:156817862},bound_to:{id:156817862},labels:{}}],
  meta:{pagination:{page:1,per_page:50,next_page:null,last_page:1,total_entries:8}}}
' >"$test_dir/too-many.json"

cat >"$test_dir/missing-metadata.json" <<EOF
{"images":[{"id":401,"type":"snapshot","name":"private","created":"$created_one_iso","created_from":{"id":156817862},"bound_to":null,"labels":{}}],"meta":{"pagination":{"page":1,"per_page":50,"next_page":null,"last_page":1,"total_entries":1}}}
EOF
too_late=$((created_one + 2592001))
cat >"$test_dir/too-long.json" <<EOF
{"images":[{"id":402,"type":"snapshot","name":"private","created":"$created_one_iso","created_from":{"id":156817862},"bound_to":null,"labels":{"mycfc-owner-ref":"OPERATOR_01","mycfc-reason-code":"PRE_CHANGE","mycfc-created-at":"$created_one","mycfc-expires-at":"$too_late"}}],"meta":{"pagination":{"page":1,"per_page":50,"next_page":null,"last_page":1,"total_entries":1}}}
EOF
overdue=$((now_epoch - 1))
cat >"$test_dir/overdue.json" <<EOF
{"images":[{"id":403,"type":"snapshot","name":"private","created":"$created_one_iso","created_from":{"id":156817862},"bound_to":null,"labels":{"mycfc-owner-ref":"OPERATOR_01","mycfc-reason-code":"PRE_CHANGE","mycfc-created-at":"$created_one","mycfc-expires-at":"$overdue"}}],"meta":{"pagination":{"page":1,"per_page":50,"next_page":null,"last_page":1,"total_entries":1}}}
EOF
cat >"$test_dir/changed.json" <<EOF
{"images":[{"id":405,"type":"backup","name":"changed-private-name","created":"$created_one_iso","created_from":{"id":156817862},"bound_to":{"id":156817862},"labels":{}}],"meta":{"pagination":{"page":1,"per_page":50,"next_page":null,"last_page":1,"total_entries":1}}}
EOF

cat >"$test_dir/bin/logger" <<'EOF'
#!/bin/sh
exit 0
EOF
chmod +x "$test_dir/bin/logger"

cat >"$test_dir/bin/curl" <<'EOF'
#!/bin/sh
set -eu
destination=
authorization=
url=
while [ "$#" -gt 0 ]; do
  case "$1" in
    --output) destination=$2; shift 2 ;;
    --header)
      case "$2" in Authorization:*) authorization=$2 ;; esac
      shift 2
      ;;
    --max-time) shift 2 ;;
    --fail|--silent) shift ;;
    *) url=$1; shift ;;
  esac
done
[ "$authorization" = 'Authorization: Bearer ABCDEFGHIJKLMNOPQRSTUVWXYZabc._~+/=' ] || exit 8
[ -n "$destination" ] || exit 9
printf '%s\n' "$url" >>"$FAKE_STATE_DIR/curl-urls"
case "$url" in
  */servers/156817862)
    if [ "${FAKE_MODE:-success}" = server_disabled ]; then
      cp "$FAKE_STATE_DIR/server-disabled.json" "$destination"
    else
      cp "$FAKE_STATE_DIR/server.json" "$destination"
    fi
    ;;
  */images*)
    page=$(printf '%s' "$url" | sed -n 's/.*[?&]page=\([0-9][0-9]*\).*/\1/p')
    image_type=$(printf '%s' "$url" | sed -n 's/.*[?&]type=\([^&]*\).*/\1/p')
    count_file="$FAKE_STATE_DIR/image-call-count"
    count=0
    [ ! -f "$count_file" ] || count=$(cat "$count_file")
    count=$((count + 1))
    printf '%s' "$count" >"$count_file"
    case "${FAKE_MODE:-success}" in
      success)
        jq --arg image_type "$image_type" --argjson page "$page" '
          .images |= map(select(.type == $image_type))
          | .meta.pagination.page = $page
          | .meta.pagination.per_page = 50
          | .meta.pagination.total_entries = 3
        ' "$FAKE_STATE_DIR/page-$page.json" >"$destination"
        ;;
      too_many)
        if [ "$image_type" = backup ]; then
          cp "$FAKE_STATE_DIR/too-many.json" "$destination"
        else
          jq -n --arg image_type "$image_type" '{images:[],meta:{pagination:{page:1,per_page:50,next_page:null,last_page:1,total_entries:0}}}' >"$destination"
        fi
        ;;
      missing_metadata|too_long|overdue)
        if [ "$image_type" = snapshot ]; then
          name=$(printf '%s' "$FAKE_MODE" | tr _ -)
          cp "$FAKE_STATE_DIR/$name.json" "$destination"
        else
          jq -n '{images:[],meta:{pagination:{page:1,per_page:50,next_page:null,last_page:1,total_entries:0}}}' >"$destination"
        fi
        ;;
      changed)
        if [ "$image_type" = backup ]; then
          backup_count_file="$FAKE_STATE_DIR/backup-call-count"
          backup_count=0
          [ ! -f "$backup_count_file" ] || backup_count=$(cat "$backup_count_file")
          backup_count=$((backup_count + 1))
          printf '%s' "$backup_count" >"$backup_count_file"
          if [ "$backup_count" -eq 1 ]; then
            cp "$FAKE_STATE_DIR/changed.json" "$destination"
          else
            sed 's/"id":405/"id":406/' "$FAKE_STATE_DIR/changed.json" >"$destination"
          fi
        else
          jq -n '{images:[],meta:{pagination:{page:1,per_page:50,next_page:null,last_page:1,total_entries:0}}}' >"$destination"
        fi
        ;;
      *) exit 10 ;;
    esac
    ;;
  *) exit 11 ;;
esac
EOF
chmod +x "$test_dir/bin/curl"

run_posture() {
	rm -f "$test_dir/image-call-count" "$test_dir/backup-call-count" "$test_dir/curl-urls"
	PATH="$test_dir/bin:$PATH" \
		FAKE_STATE_DIR="$test_dir" \
		FAKE_MODE="${FAKE_MODE:-success}" \
		MYCFC_ENV_FILE="$test_dir/env" \
		MYCFC_HETZNER_TOKEN_FILE="$test_dir/token" \
		MYCFC_POSTURE_NOW_EPOCH="$now_epoch" \
		sh "$root_dir/deployment/hetzner-backup-posture.sh"
}

output=$(run_posture)
printf '%s' "$output" | grep -Eq '^hetzner_backup_posture_started$
^hetzner_backup_posture_succeeded automatic_backup_count=2 manual_snapshot_count=2 oldest_backup_age_seconds=172800 oldest_snapshot_age_seconds=172800 inventory_sha256=[0-9a-f]{64}$'
[ "$(cat "$test_dir/image-call-count")" -eq 8 ]
for image_type in backup snapshot; do
	for page in 1 2; do
		image_query="https://api.hetzner.cloud/v1/images?type=$image_type&sort=id&page=$page&per_page=50"
		[ "$(grep -Fxc "$image_query" "$test_dir/curl-urls")" -eq 2 ]
	done
done
[ "$(grep -Fc '/images?' "$test_dir/curl-urls")" -eq 8 ]
if grep -Fq 'type=backup&type=snapshot' "$test_dir/curl-urls"; then
	printf '%s\n' 'posture check combined scalar image type filters' >&2
	exit 1
fi
for forbidden in 156817862 101 102 201 202 member-name OPERATOR RESTORE_DRILL PRE_CHANGE mycfc; do
	if printf '%s' "$output" | grep -q "$forbidden"; then
		printf '%s\n' 'posture output leaked a provider or accountability identifier' >&2
		exit 1
	fi
done

if HETZNER_BACKUP_POSTURE_ENABLED=false run_posture >"$test_dir/disabled-output" 2>&1; then
	printf '%s\n' 'posture check ran while disabled' >&2
	exit 1
fi
grep -q 'reason=disabled' "$test_dir/disabled-output"

chmod 0644 "$test_dir/token"
if run_posture >"$test_dir/token-output" 2>&1; then
	printf '%s\n' 'posture check accepted an insecure token file' >&2
	exit 1
fi
grep -q 'reason=insecure_token' "$test_dir/token-output"
chmod 0600 "$test_dir/token"

printf 'line-one\nline-two\n' >"$test_dir/token"
if run_posture >"$test_dir/multiline-token-output" 2>&1; then
	printf '%s\n' 'posture check accepted a multiline token' >&2
	exit 1
fi
grep -q 'reason=invalid_token' "$test_dir/multiline-token-output"
printf '%s\n' 'ABCDEFGHIJKLMNOPQRSTUVWXYZabc._~+/=' >"$test_dir/token"

for mode in too_many missing_metadata too_long overdue changed server_disabled; do
	if FAKE_MODE=$mode run_posture >"$test_dir/$mode-output" 2>&1; then
		printf '%s\n' "posture check accepted invalid fixture: $mode" >&2
		exit 1
	fi
	if grep -Eq 'private|member|OPERATOR|RESTORE_DRILL|PRE_CHANGE|156817862|401|402|403|405|406' "$test_dir/$mode-output"; then
		printf '%s\n' "posture failure leaked fixture data: $mode" >&2
		exit 1
	fi
done
grep -q 'reason=automatic_backup_limit_exceeded' "$test_dir/too_many-output"
grep -q 'reason=snapshot_metadata_invalid' "$test_dir/missing_metadata-output"
grep -q 'reason=snapshot_metadata_invalid' "$test_dir/too_long-output"
grep -q 'reason=snapshot_metadata_invalid' "$test_dir/overdue-output"
grep -q 'reason=inventory_changed' "$test_dir/changed-output"
grep -q 'reason=server_posture_unverified' "$test_dir/server_disabled-output"

printf '%s\n' 'Hetzner backup posture tests passed'
