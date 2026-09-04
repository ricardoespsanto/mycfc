#!/usr/bin/env bash
set -Eeuo pipefail

workers=${1:-}
runs=${2:-20}

if [[ $workers != 2 && $workers != 3 && $workers != 4 ]]; then
	printf '%s\n' 'usage: scripts/e2e-worker-trial.sh <2|3|4> [positive-run-count]' >&2
	exit 2
fi
if [[ ! $runs =~ ^[1-9][0-9]*$ ]]; then
	printf '%s\n' 'run count must be a positive integer' >&2
	exit 2
fi

root_dir=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
if [[ ${E2E_TRIAL_ALLOW_REMOTE_DOCKER:-false} != true ]]; then
	case "${DOCKER_HOST:-}" in ''|unix://*) ;; *) printf '%s\n' 'worker trials require a local Docker daemon' >&2; exit 2 ;; esac
	docker_endpoint=$(docker context inspect --format '{{.Endpoints.docker.Host}}' 2>/dev/null || true)
	case "$docker_endpoint" in unix://*) ;; *) printf '%s\n' 'worker trials require a local Docker context' >&2; exit 2 ;; esac
fi
if [[ ${E2E_TRIAL_ALLOW_DIRTY:-false} != true ]] && [[ -n $(git -C "$root_dir" status --porcelain --untracked-files=normal) ]]; then
	printf '%s\n' 'worker evidence requires a clean checkout; commit the tested source first' >&2
	exit 2
fi
trial_stamp=$(date -u +%Y%m%dT%H%M%SZ)
output_dir=${E2E_TRIAL_OUTPUT_DIR:-$root_dir/artifacts/e2e-worker-trials/$trial_stamp-workers-$workers}
mkdir -p "$output_dir"
summary_file="$output_dir/summary.tsv"
git_sha=$(git -C "$root_dir" rev-parse HEAD)
inventory_hash=$(cd "$root_dir" && git ls-files -z 'e2e/*.spec.mjs' playwright.config.mjs package-lock.json | xargs -0 sha256sum | sha256sum | awk '{print $1}')
runner=${RUNNER_OS:-$(uname -s)}-${RUNNER_ARCH:-$(uname -m)}-${ImageOS:-local}
printf 'run\tgit_sha\trunner\tinventory_hash\tworkers\tstarted_at\tjob_duration_seconds\tbrowser_duration_ms\tdiscovered\tpassed\tunexpected\tflaky\tskipped\tretries\tresult\tfailure_class\tlog\tjson\n' >"$summary_file"
active_compose_project=
log_file=/dev/null
cleanup_trial_project() {
	if [[ -n $active_compose_project ]]; then
		(cd "$root_dir" && COMPOSE_PROJECT_NAME="$active_compose_project" docker compose -f compose.yaml -f compose.e2e-ci.yaml --profile e2e down -v --remove-orphans) >>"$log_file" 2>&1 || true
		active_compose_project=
	fi
}
trap cleanup_trial_project EXIT
trap 'cleanup_trial_project; exit 130' HUP INT TERM

for ((run_number = 1; run_number <= runs; run_number++)); do
	started_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)
	started_epoch=$(date +%s)
	log_file="$output_dir/run-$run_number.log"
	json_file="$output_dir/run-$run_number.json"
	compose_project="mycfc-e2e-trial-$workers-$$-$run_number"
	active_compose_project=$compose_project
	current_report="$root_dir/artifacts/e2e-worker-trials/.current-$compose_project.json"
	report_in_workspace="/workspace/artifacts/e2e-worker-trials/.current-$compose_project.json"
	result=success
	failure_class=none
	set +e
	(cd "$root_dir" && CI=true COMPOSE_PROJECT_NAME="$compose_project" PLAYWRIGHT_WORKERS="$workers" E2E_JSON_OUTPUT="$report_in_workspace" make test-e2e-ci) 2>&1 | tee "$log_file"
	test_status=${PIPESTATUS[0]}
	set -e
	if (( test_status != 0 )); then
		result=failure
		failure_class=infrastructure-or-test
	fi
	if [[ -f $current_report ]]; then
		cp "$current_report" "$json_file"
		rm -f "$current_report"
		if summary_values=$(node "$root_dir/scripts/summarize-playwright-json.mjs" "$json_file"); then
			read -r browser_duration_ms discovered passed unexpected flaky skipped retries <<<"$summary_values"
		else
			browser_duration_ms=unknown
			discovered=unknown
			passed=unknown
			unexpected=unknown
			flaky=unknown
			skipped=unknown
			retries=unknown
			result=failure
			failure_class=invalid-playwright-report
		fi
		if [[ $result == success && ( $discovered != 51 || $passed != 20 || $unexpected != 0 || $flaky != 0 || $skipped != 31 || $retries != 0 ) ]]; then
			result=failure
			failure_class=test-inventory-or-outcome
		fi
	else
		browser_duration_ms=unknown
		discovered=unknown
		passed=unknown
		unexpected=unknown
		flaky=unknown
		skipped=unknown
		retries=unknown
		if [[ $result == success ]]; then
			result=failure
			failure_class=missing-playwright-report
		fi
	fi
	if [[ $result != success && -d $root_dir/test-results ]]; then
		cp -R "$root_dir/test-results" "$output_dir/run-$run_number-test-results"
	fi
	cleanup_trial_project
	duration_seconds=$(($(date +%s) - started_epoch))
	printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' \
		"$run_number" "$git_sha" "$runner" "$inventory_hash" "$workers" "$started_at" "$duration_seconds" \
		"$browser_duration_ms" "$discovered" "$passed" "$unexpected" "$flaky" "$skipped" "$retries" \
		"$result" "$failure_class" "$log_file" "$json_file" >>"$summary_file"
	if [[ $result != success ]]; then
		printf 'worker trial failed on run %s; see %s\n' "$run_number" "$log_file" >&2
		exit 1
	fi
done

trap - EXIT HUP INT TERM

printf 'worker trial evidence: %s\n' "$summary_file"
