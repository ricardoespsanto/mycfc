#!/bin/sh
set -eu

fail() {
	printf 'release verification rejected: %s\n' "$1" >&2
	exit 1
}

for name in AWS_REGION CLOUDWATCH_LOG_GROUP RELEASE_VERSION GIT_SHA IMAGE_REPOSITORY IMAGE_DIGEST RELEASE_TAG SCHEMA_MIGRATION_DIGEST PUBLICATION_MANIFEST_SHA256 EXPECTED_GATES_JSON RELEASE_EVIDENCE_START_TIME_MS RELEASE_EVIDENCE_OUTPUT; do
	eval "value=\${$name-}"
	[ -n "$value" ] || fail "$name is required"
done
printf '%s' "$RELEASE_VERSION" | grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?$' || fail 'invalid version'
printf '%s' "$GIT_SHA" | grep -Eq '^[0-9a-f]{40}$' || fail 'invalid SHA'
printf '%s' "$IMAGE_REPOSITORY" | grep -Eq '^[A-Za-z0-9._/-]+$' || fail 'invalid image repository'
printf '%s' "$IMAGE_DIGEST" | grep -Eq '^sha256:[0-9a-f]{64}$' || fail 'invalid image digest'
printf '%s' "$SCHEMA_MIGRATION_DIGEST" | grep -Eq '^[0-9a-f]{64}$' || fail 'invalid schema digest'
printf '%s' "$PUBLICATION_MANIFEST_SHA256" | grep -Eq '^[0-9a-f]{64}$' || fail 'invalid manifest digest'
printf '%s' "$EXPECTED_GATES_JSON" | jq -e 'type == "object" and (keys | sort) == ["guardian_intake","privacy_worker"] and all(.[]; type == "boolean")' >/dev/null || fail 'invalid expected gates'
case "$RELEASE_TAG" in release-??????????????-"$GIT_SHA") ;; *) fail 'release tag does not bind exact SHA' ;; esac
printf '%s' "$CLOUDWATCH_LOG_GROUP" | grep -Eq '^/[A-Za-z0-9._/-]+$' || fail 'invalid log group'

attempts=${RELEASE_EVIDENCE_ATTEMPTS:-40}
interval=${RELEASE_EVIDENCE_INTERVAL_SECONDS:-15}
max_age=${RELEASE_EVIDENCE_MAX_AGE_SECONDS:-900}
case "$attempts:$interval:$max_age:$RELEASE_EVIDENCE_START_TIME_MS" in *[!0-9:]*) fail 'poll and freshness settings must be numeric' ;; esac
[ "$attempts" -gt 0 ] && [ "$interval" -ge 0 ] && [ "$max_age" -gt 0 ] || fail 'poll and freshness settings are invalid'

expected="event=deployment_receipt result=succeeded version=$RELEASE_VERSION sha=$GIT_SHA digest=$IMAGE_DIGEST schema_migration_digest=$SCHEMA_MIGRATION_DIGEST manifest_sha256=$PUBLICATION_MANIFEST_SHA256"
work_file=$(mktemp)
trap 'rm -f "$work_file"' EXIT HUP INT TERM
script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)

for attempt in $(seq 1 "$attempts"); do
	aws logs filter-log-events \
		--region "$AWS_REGION" \
		--log-group-name "$CLOUDWATCH_LOG_GROUP" \
		--start-time "$RELEASE_EVIDENCE_START_TIME_MS" \
		--filter-pattern '"event=deployment_receipt"' \
		--query 'events[].{timestamp:timestamp,message:message}' --output json >"$work_file"
	jq -e 'type == "array" and all(.[]; (.timestamp | type == "number") and (.message | type == "string"))' "$work_file" >/dev/null || fail 'CloudWatch returned malformed evidence'
	if jq -r '.[].message' "$work_file" | grep -Eqi 'password=|secret=|token=|postgres(ql)?://[^[:space:]]+@'; then
		fail 'credential-shaped content appeared in deployment evidence'
	fi
	match=$(jq -r --arg expected "$expected" --argjson start "$RELEASE_EVIDENCE_START_TIME_MS" '
		map(. as $event | ($event.message | split("\n")[]) | {timestamp:$event.timestamp,message:.}) |
		map(select(.timestamp >= $start and (.message | startswith($expected + " ")))) |
		sort_by(.timestamp) | last // empty | select(type == "object") | [.timestamp,.message] | @tsv
	' "$work_file")
	if [ -n "$match" ]; then
		message=$(printf '%s\n' "$match" | cut -f2-)
		if ! printf '%s\n' "$message" | grep -Eq '^event=deployment_receipt result=succeeded version=v[0-9A-Za-z.-]+ sha=[0-9a-f]{40} digest=sha256:[0-9a-f]{64} schema_migration_digest=[0-9a-f]{64} manifest_sha256=[0-9a-f]{64} slot=(blue|green) failure_phase=none traffic_switched=(true|false) rollback_performed=false guardian_intake_active=(true|false) privacy_worker_active=(true|false) privacy_worker_activation_required=(true|false) started_at=[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z finished_at=[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z receipt_sha256=[0-9a-f]{64}$'; then
			fail 'deployment receipt contains fields outside the allowlist'
		fi
		field() { printf '%s\n' "$message" | tr ' ' '\n' | sed -n "s/^$1=//p"; }
		slot=$(field slot)
		switched=$(field traffic_switched)
		guardian=$(field guardian_intake_active)
		privacy=$(field privacy_worker_active)
		activation_required=$(field privacy_worker_activation_required)
		started=$(field started_at)
		finished=$(field finished_at)
		receipt_sha=$(field receipt_sha256)
		finished_epoch=$(date -u -d "$finished" +%s) || fail 'receipt finish time is invalid'
		now_epoch=$(date -u +%s)
		[ "$finished_epoch" -le "$((now_epoch + 30))" ] || fail 'receipt is dated in the future'
		[ "$((now_epoch - finished_epoch))" -le "$max_age" ] || fail 'receipt is older than the allowed verification window'
		[ "$((finished_epoch * 1000))" -ge "$RELEASE_EVIDENCE_START_TIME_MS" ] || fail 'receipt predates this publication attempt'
		expected_guardian=$(printf '%s' "$EXPECTED_GATES_JSON" | jq -r .guardian_intake)
		expected_privacy=$(printf '%s' "$EXPECTED_GATES_JSON" | jq -r .privacy_worker)
		[ "$guardian" = "$expected_guardian" ] || fail 'guardian intake state does not match the signed release policy'
		if [ "$privacy" != "$expected_privacy" ]; then
			[ "$expected_privacy:$privacy:$activation_required" = true:false:true ] || fail 'privacy worker state does not match the signed release policy'
		else
			[ "$activation_required" = false ] || fail 'privacy worker cannot require activation while matching policy'
		fi
		env RELEASE_VERSION="$RELEASE_VERSION" GIT_SHA="$GIT_SHA" IMAGE_REPOSITORY="$IMAGE_REPOSITORY" \
			IMAGE_DIGEST="$IMAGE_DIGEST" RELEASE_TAG="$RELEASE_TAG" SCHEMA_MIGRATION_DIGEST="$SCHEMA_MIGRATION_DIGEST" \
			PUBLICATION_MANIFEST_SHA256="$PUBLICATION_MANIFEST_SHA256" RELEASE_RESULT=succeeded RELEASE_SLOT="$slot" \
			TRAFFIC_SWITCHED="$switched" ROLLBACK_PERFORMED=false RELEASE_STARTED_AT="$started" RELEASE_FINISHED_AT="$finished" \
			GUARDIAN_INTAKE_ACTIVE="$guardian" PRIVACY_WORKER_ACTIVE="$privacy" PRIVACY_WORKER_ACTIVATION_REQUIRED="$activation_required" \
			RELEASE_EVIDENCE_OUTPUT="$RELEASE_EVIDENCE_OUTPUT" sh "$script_dir/release-evidence.sh" receipt
		[ "$(sha256sum "$RELEASE_EVIDENCE_OUTPUT" | awk '{print $1}')" = "$receipt_sha" ] || fail 'CloudWatch receipt hash does not match its canonical contents'
		printf '%s\n' "$message"
		exit 0
	fi
	[ "$attempt" -eq "$attempts" ] || sleep "$interval"
done

fail 'no fresh matching successful deployment receipt was observed'
