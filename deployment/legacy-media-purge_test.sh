#!/bin/sh
set -eu

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
test_dir=$(mktemp -d)
trap 'rm -rf "$test_dir"' EXIT HUP INT TERM
fake_bin="$test_dir/bin"
mkdir -p "$fake_bin" "$test_dir/config" "$test_dir/evidence" "$test_dir/runtime"

cat >"$fake_bin/id" <<'EOF'
#!/bin/sh
printf '0\n'
EOF
cat >"$fake_bin/stat" <<'EOF'
#!/bin/sh
case "$*" in
  *%u:%g:%a*) printf '0:65532:440\n' ;;
  *evidence*) printf '0:700\n' ;;
  *) printf '0:600\n' ;;
esac
EOF
cat >"$fake_bin/chown" <<'EOF'
#!/bin/sh
exit 0
EOF
cat >"$fake_bin/ln" <<'EOF'
#!/bin/sh
if [ "${TEST_LATE_DESTINATION:-false}" = true ]; then
  : >"$2"
fi
exec /usr/bin/ln "$@"
EOF
cat >"$fake_bin/logger" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >>"$TEST_EVENT_LOG"
EOF
cat >"$fake_bin/flock" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >>"$TEST_FLOCK_LOG"
[ "${TEST_LOCK_BUSY:-false}" != true ]
EOF
cat >"$fake_bin/systemctl" <<'EOF'
#!/bin/sh
case "$*" in
  *"${TEST_ACTIVE_UNIT:-not-a-real-unit}"*) exit 0 ;;
  *) exit 1 ;;
esac
EOF
cat >"$fake_bin/aws" <<'EOF'
#!/bin/sh
printf '%s|%s|%s\n' "${AWS_PROFILE:-}" "${AWS_SHARED_CREDENTIALS_FILE:-}" "$*" >>"$TEST_AWS_LOG"
case "$*" in
  *'sts get-caller-identity'*) printf '%s\n' "${TEST_ACTUAL_PRINCIPAL_ARN:-$TEST_EXPECTED_PRINCIPAL_ARN}" ;;
  *) printf 'temporary-password\n' ;;
esac
EOF
cat >"$fake_bin/gh" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >>"$TEST_GH_LOG"
[ "${TEST_ATTESTATION_INVALID:-false}" != true ]
EOF
cat >"$fake_bin/docker" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >>"$TEST_DOCKER_LOG"
case "$1" in
  login) cat >/dev/null ;;
  pull) ;;
  inspect)
    case "$*" in
      *"${TEST_ACTIVE_CONTAINER:-not-a-real-container}"*) printf 'true\n' ;;
      *) printf 'false\n' ;;
    esac
    ;;
  image) printf '%s\n' "$TEST_EXPECTED_SHA" ;;
  run)
    if [ "${TEST_EVIDENCE_VARIANT:-}" = extra-field ]; then
      printf '%s\n' '{"evidence_version":"mycfc/legacy-media-purge-evidence/v1","mode":"DRY_RUN","evidence_key_id":"test-key","raw_key":"must-not-pass","prefixes":[],"versions":0,"delete_markers":0,"deleted_versions":0,"deleted_markers":0,"list_calls":0,"stable_empty_scans":0,"inventory_digest":"dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"}'
      exit 0
    fi
    if [ "${TEST_EVIDENCE_VARIANT:-}" = wrong-key ]; then
      printf '%s\n' '{"evidence_version":"mycfc/legacy-media-purge-evidence/v1","mode":"DRY_RUN","evidence_key_id":"wrong-key","prefixes":[{"prefix":"profiles/","versions":0,"delete_markers":0,"deleted_versions":0,"deleted_markers":0,"list_calls":1,"stable_empty_scans":0,"inventory_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},{"prefix":"repairs/","versions":0,"delete_markers":0,"deleted_versions":0,"deleted_markers":0,"list_calls":1,"stable_empty_scans":0,"inventory_digest":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},{"prefix":"equipment/","versions":0,"delete_markers":0,"deleted_versions":0,"deleted_markers":0,"list_calls":1,"stable_empty_scans":0,"inventory_digest":"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"}],"versions":0,"delete_markers":0,"deleted_versions":0,"deleted_markers":0,"list_calls":3,"stable_empty_scans":0,"inventory_digest":"dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"}'
      exit 0
    fi
    if [ "${TEST_EVIDENCE_VARIANT:-}" = fractional ]; then
      printf '%s\n' '{"evidence_version":"mycfc/legacy-media-purge-evidence/v1","mode":"DRY_RUN","evidence_key_id":"test-key","prefixes":[{"prefix":"profiles/","versions":0.5,"delete_markers":0,"deleted_versions":0,"deleted_markers":0,"list_calls":1,"stable_empty_scans":0,"inventory_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},{"prefix":"repairs/","versions":0,"delete_markers":0,"deleted_versions":0,"deleted_markers":0,"list_calls":1,"stable_empty_scans":0,"inventory_digest":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},{"prefix":"equipment/","versions":0,"delete_markers":0,"deleted_versions":0,"deleted_markers":0,"list_calls":1,"stable_empty_scans":0,"inventory_digest":"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"}],"versions":0.5,"delete_markers":0,"deleted_versions":0,"deleted_markers":0,"list_calls":3,"stable_empty_scans":0,"inventory_digest":"dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"}'
      exit 0
    fi
    if [ "${TEST_EVIDENCE_VARIANT:-}" = inconsistent ]; then
      printf '%s\n' '{"evidence_version":"mycfc/legacy-media-purge-evidence/v1","mode":"DRY_RUN","evidence_key_id":"test-key","prefixes":[{"prefix":"profiles/","versions":1,"delete_markers":0,"deleted_versions":0,"deleted_markers":0,"list_calls":1,"stable_empty_scans":0,"inventory_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},{"prefix":"repairs/","versions":0,"delete_markers":0,"deleted_versions":0,"deleted_markers":0,"list_calls":1,"stable_empty_scans":0,"inventory_digest":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},{"prefix":"equipment/","versions":0,"delete_markers":0,"deleted_versions":0,"deleted_markers":0,"list_calls":1,"stable_empty_scans":0,"inventory_digest":"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"}],"versions":2,"delete_markers":0,"deleted_versions":0,"deleted_markers":0,"list_calls":3,"stable_empty_scans":0,"inventory_digest":"dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"}'
      exit 0
    fi
    if [ "${TEST_EVIDENCE_VARIANT:-}" = oversized ]; then
      printf '%s\n' '{"evidence_version":"mycfc/legacy-media-purge-evidence/v1","mode":"DRY_RUN","evidence_key_id":"test-key","prefixes":[{"prefix":"profiles/","versions":250001,"delete_markers":0,"deleted_versions":0,"deleted_markers":0,"list_calls":1,"stable_empty_scans":0,"inventory_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},{"prefix":"repairs/","versions":0,"delete_markers":0,"deleted_versions":0,"deleted_markers":0,"list_calls":1,"stable_empty_scans":0,"inventory_digest":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},{"prefix":"equipment/","versions":0,"delete_markers":0,"deleted_versions":0,"deleted_markers":0,"list_calls":1,"stable_empty_scans":0,"inventory_digest":"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"}],"versions":250001,"delete_markers":0,"deleted_versions":0,"deleted_markers":0,"list_calls":3,"stable_empty_scans":0,"inventory_digest":"dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"}'
      exit 0
    fi
    case " $* " in
      *' --execute '*)
        digest=${TEST_EXECUTION_DIGEST:-dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd}
        cat <<JSON
{"evidence_version":"mycfc/legacy-media-purge-evidence/v1","mode":"EXECUTE","evidence_key_id":"test-key","prefixes":[{"prefix":"profiles/","versions":1,"delete_markers":0,"deleted_versions":1,"deleted_markers":0,"list_calls":3,"stable_empty_scans":2,"inventory_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},{"prefix":"repairs/","versions":2,"delete_markers":1,"deleted_versions":2,"deleted_markers":1,"list_calls":3,"stable_empty_scans":2,"inventory_digest":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},{"prefix":"equipment/","versions":1,"delete_markers":1,"deleted_versions":1,"deleted_markers":1,"list_calls":3,"stable_empty_scans":2,"inventory_digest":"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"}],"versions":4,"delete_markers":2,"deleted_versions":4,"deleted_markers":2,"list_calls":9,"stable_empty_scans":6,"inventory_digest":"$digest"}
JSON
        ;;
      *)
        cat <<JSON
{"evidence_version":"mycfc/legacy-media-purge-evidence/v1","mode":"DRY_RUN","evidence_key_id":"test-key","prefixes":[{"prefix":"profiles/","versions":1,"delete_markers":0,"deleted_versions":0,"deleted_markers":0,"list_calls":1,"stable_empty_scans":0,"inventory_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},{"prefix":"repairs/","versions":2,"delete_markers":1,"deleted_versions":0,"deleted_markers":0,"list_calls":1,"stable_empty_scans":0,"inventory_digest":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},{"prefix":"equipment/","versions":1,"delete_markers":1,"deleted_versions":0,"deleted_markers":0,"list_calls":1,"stable_empty_scans":0,"inventory_digest":"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"}],"versions":4,"delete_markers":2,"deleted_versions":0,"deleted_markers":0,"list_calls":3,"stable_empty_scans":0,"inventory_digest":"dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"}
JSON
        ;;
    esac
    ;;
  *) exit 1 ;;
esac
EOF
chmod +x "$fake_bin"/*

: >"$test_dir/config/aws-credentials"
: >"$test_dir/config/evidence.key"
: >"$test_dir/config/release-credentials"
cat >"$test_dir/config/environment" <<'EOF'
AWS_REGION=eu-west-1
S3_BUCKET_NAME=mycfc-production-test-eu-west-1-repairs
ECR_REPOSITORY_URL=registry.example/mycfc-production
LEGACY_MEDIA_PURGE_EVIDENCE_KEY_ID=test-key
LEGACY_MEDIA_PURGE_EXPECTED_SHA=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
LEGACY_MEDIA_PURGE_EXPECTED_PRINCIPAL_ARN=arn:aws:iam::123456789012:user/mycfc-production-legacy-media-purge
EOF
: >"$test_dir/docker.log"
: >"$test_dir/aws.log"
: >"$test_dir/events.log"
: >"$test_dir/flock.log"
: >"$test_dir/gh.log"

run_purge() {
	env \
		PATH="$fake_bin:$PATH" \
		TEST_DOCKER_LOG="$test_dir/docker.log" \
		TEST_AWS_LOG="$test_dir/aws.log" \
		TEST_EVENT_LOG="$test_dir/events.log" \
		TEST_FLOCK_LOG="$test_dir/flock.log" \
		TEST_GH_LOG="$test_dir/gh.log" \
		TEST_EXPECTED_SHA=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa \
		TEST_EXPECTED_PRINCIPAL_ARN=arn:aws:iam::123456789012:user/mycfc-production-legacy-media-purge \
		MYCFC_LEGACY_MEDIA_PURGE_CONFIG_FILE="$test_dir/config/environment" \
		MYCFC_LEGACY_MEDIA_PURGE_CREDENTIALS_FILE="$test_dir/config/aws-credentials" \
		MYCFC_LEGACY_MEDIA_PURGE_EVIDENCE_KEY_FILE="$test_dir/config/evidence.key" \
		MYCFC_RELEASE_AWS_CREDENTIALS_FILE="$test_dir/config/release-credentials" \
		MYCFC_RUNTIME_DIR="$test_dir/runtime" \
		MYCFC_LEGACY_MEDIA_PURGE_EVIDENCE_DIR="$test_dir/evidence" \
		sh "$script_dir/legacy-media-purge.sh" "$@"
}

image='registry.example/mycfc-production@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'
run_purge inventory "$image" "$test_dir/evidence/inventory-one.json"
jq -e '.mode == "DRY_RUN" and .versions == 4 and .delete_markers == 2' "$test_dir/evidence/inventory-one.json" >/dev/null
grep -q 'event=legacy_media_purge_succeeded mode=DRY_RUN versions=4 delete_markers=2 deleted_versions=0 deleted_markers=0' "$test_dir/events.log"
grep -q '^mycfc-release|.*/release-credentials|ecr get-login-password' "$test_dir/aws.log"
grep -q -- '--read-only --user 65532:65532 --cap-drop ALL --security-opt no-new-privileges --pids-limit 64 --memory 256m' "$test_dir/docker.log"
grep -q -- '-e AWS_EC2_METADATA_DISABLED=true' "$test_dir/docker.log"
grep -q -- '-v .*/aws-credentials:/run/legacy-media-purge/aws-credentials:ro' "$test_dir/docker.log"
grep -q -- 'attestation verify oci://registry.example/mycfc-production@sha256:' "$test_dir/gh.log"
grep -q '^mycfc-legacy-media-purge|.*/aws-credentials|sts get-caller-identity' "$test_dir/aws.log"
grep -q '^-n 9$' "$test_dir/flock.log"

run_purge execute "$image" dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd "$test_dir/evidence/execution.json"
jq -e '.mode == "EXECUTE" and .deleted_versions == 4 and .deleted_markers == 2 and (.prefixes | all(.stable_empty_scans == 2))' "$test_dir/evidence/execution.json" >/dev/null
grep -q -- '--execute --inventory-digest dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd --confirm DELETE-ALL-LEGACY-MEDIA-VERSIONS' "$test_dir/docker.log"

if run_purge inventory "$image" "$test_dir/evidence/inventory-one.json"; then
	printf '%s\n' 'existing evidence file was overwritten' >&2
	exit 1
fi

if run_purge inventory "$image" "$test_dir/evidence/../escaped.json"; then
	printf '%s\n' 'traversal evidence path was accepted' >&2
	exit 1
fi
mkdir "$test_dir/nested"
ln -s "$test_dir/nested" "$test_dir/evidence/link"
if run_purge inventory "$image" "$test_dir/evidence/link/escaped.json"; then
	printf '%s\n' 'symlinked evidence parent was accepted' >&2
	exit 1
fi
for active_unit in mycfc-pull-release.timer mycfc-pull-release.service mycfc-privacy-worker.service; do
	if TEST_ACTIVE_UNIT=$active_unit run_purge inventory "$image" "$test_dir/evidence/active-unit.json"; then
		printf '%s\n' "active maintenance unit was accepted: $active_unit" >&2
		exit 1
	fi
done
for active_container in mycfc-production-app-1 mycfc-production-app-blue-1 mycfc-production-app-green-1 mycfc-production-privacy-worker-1; do
	if TEST_ACTIVE_CONTAINER=$active_container run_purge inventory "$image" "$test_dir/evidence/active-container.json"; then
		printf '%s\n' "active media writer was accepted: $active_container" >&2
		exit 1
	fi
done
for variant in extra-field wrong-key fractional inconsistent oversized; do
	if TEST_EVIDENCE_VARIANT=$variant run_purge inventory "$image" "$test_dir/evidence/$variant.json"; then
		printf '%s\n' "malformed evidence was accepted: $variant" >&2
		exit 1
	fi
done
if TEST_ATTESTATION_INVALID=true run_purge inventory "$image" "$test_dir/evidence/untrusted.json"; then
	printf '%s\n' 'image without trusted provenance was accepted' >&2
	exit 1
fi
if TEST_ACTUAL_PRINCIPAL_ARN=arn:aws:iam::123456789012:user/wrong \
	run_purge inventory "$image" "$test_dir/evidence/wrong-principal.json"; then
	printf '%s\n' 'unexpected purge principal was accepted' >&2
	exit 1
fi
if TEST_LOCK_BUSY=true run_purge inventory "$image" "$test_dir/evidence/busy-lock.json"; then
	printf '%s\n' 'busy release lock was accepted' >&2
	exit 1
fi
if TEST_EXECUTION_DIGEST=eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee \
	run_purge execute "$image" dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd "$test_dir/evidence/wrong-execute-digest.json"; then
	printf '%s\n' 'execution evidence was not bound to the approved digest' >&2
	exit 1
fi
if TEST_LATE_DESTINATION=true run_purge inventory "$image" "$test_dir/evidence/late-destination.json"; then
	printf '%s\n' 'late-created evidence destination was overwritten' >&2
	exit 1
fi

printf '%s\n' 'legacy media purge deployment tests passed'
