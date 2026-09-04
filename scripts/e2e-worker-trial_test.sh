#!/usr/bin/env bash
set -Eeuo pipefail

root_dir=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
trial="$root_dir/scripts/e2e-worker-trial.sh"

for invalid_workers in '' 0 1 5 many; do
	if "$trial" "$invalid_workers" 1 >/dev/null 2>&1; then
		printf 'accepted invalid worker count: %s\n' "$invalid_workers" >&2
		exit 1
	fi
done
if "$trial" 2 0 >/dev/null 2>&1; then
	printf '%s\n' 'accepted invalid run count' >&2
	exit 1
fi

for workers in 2 3 4; do
	(
		cd "$root_dir"
		CI=true PLAYWRIGHT_WORKERS="$workers" node --input-type=module -e \
			'import config from "./playwright.config.mjs"; if (config.workers !== Number(process.env.PLAYWRIGHT_WORKERS)) process.exit(1)'
	)
done

if (cd "$root_dir" && CI=true PLAYWRIGHT_WORKERS=invalid node -e 'import("./playwright.config.mjs")') >/dev/null 2>&1; then
	printf '%s\n' 'accepted invalid PLAYWRIGHT_WORKERS' >&2
	exit 1
fi

work_dir=$(mktemp -d)
trap 'rm -rf "$work_dir"' EXIT HUP INT TERM
cat >"$work_dir/.env" <<'EOF'
COMPOSE_PROJECT_NAME=wrong-project
DOCKER_HOST=tcp://remote.example:2376
DOCKER_CONTEXT=remote
COMPOSE_FILE=wrong.yaml
COMPOSE_PROFILES=wrong-profile
COMPOSE_ENV_FILES=wrong.env
EOF
preserved_environment=$(
	cd "$work_dir"
	CI=true E2E_BOOTSTRAP_VALIDATE_ENV_ONLY=true \
		COMPOSE_PROJECT_NAME=expected-project DOCKER_HOST=unix:///var/run/docker.sock DOCKER_CONTEXT=default \
		COMPOSE_FILE=compose.yaml COMPOSE_PROFILES=e2e COMPOSE_ENV_FILES=.env \
		"$root_dir/scripts/e2e-ci-bootstrap.sh"
)
printf '%s\n' "$preserved_environment" | grep -qx 'COMPOSE_PROJECT_NAME=expected-project'
printf '%s\n' "$preserved_environment" | grep -qx 'DOCKER_HOST=unix:///var/run/docker.sock'
printf '%s\n' "$preserved_environment" | grep -qx 'DOCKER_CONTEXT=default'
printf '%s\n' "$preserved_environment" | grep -qx 'COMPOSE_FILE=compose.yaml'
printf '%s\n' "$preserved_environment" | grep -qx 'COMPOSE_PROFILES=e2e'
printf '%s\n' "$preserved_environment" | grep -qx 'COMPOSE_ENV_FILES=.env'

fake_bin="$work_dir/bin"
mkdir -p "$fake_bin"
cat >"$fake_bin/docker" <<'EOF'
#!/usr/bin/env bash
if [[ $1 == context ]]; then
	printf 'unix:///var/run/docker.sock\n'
	exit 0
fi
printf '%s\n' "$*" >>"$TEST_TRIAL_DOCKER_LOG"
EOF
cat >"$fake_bin/make" <<'EOF'
#!/usr/bin/env bash
report=${E2E_JSON_OUTPUT#/workspace/}
case "$TEST_TRIAL_MODE" in
	missing) exit 0 ;;
	nonzero) exit 1 ;;
	malformed)
		mkdir -p "$(dirname "$report")"
		printf '{}\n' >"$report"
		;;
	success|mismatch)
		mkdir -p "$(dirname "$report")"
		expected=20
		skipped=31
		if [[ $TEST_TRIAL_MODE == mismatch ]]; then expected=19; fi
		printf '{"config":{"projects":[{"retries":0}]},"stats":{"duration":1000,"expected":%s,"unexpected":0,"flaky":0,"skipped":%s}}\n' "$expected" "$skipped" >"$report"
		;;
	*) exit 2 ;;
esac
EOF
chmod +x "$fake_bin/docker" "$fake_bin/make"

run_trial_case() {
	mode=$1
	expected_class=$2
	case_output="$work_dir/$mode"
	docker_log="$work_dir/$mode-docker.log"
	if (
		cd "$root_dir"
		env PATH="$fake_bin:$PATH" E2E_TRIAL_ALLOW_DIRTY=true TEST_TRIAL_MODE="$mode" \
			TEST_TRIAL_DOCKER_LOG="$docker_log" E2E_TRIAL_OUTPUT_DIR="$case_output" "$trial" 2 1
	) >/dev/null 2>&1; then
		printf 'worker trial mode %s unexpectedly succeeded\n' "$mode" >&2
		exit 1
	fi
	awk -F '\t' -v expected="$expected_class" 'NR == 2 && $16 == expected { found = 1 } END { exit !found }' "$case_output/summary.tsv"
	grep -q 'down -v --remove-orphans' "$docker_log"
}

run_trial_case missing missing-playwright-report
run_trial_case malformed invalid-playwright-report
run_trial_case nonzero infrastructure-or-test
run_trial_case mismatch test-inventory-or-outcome

success_output="$work_dir/success"
success_docker_log="$work_dir/success-docker.log"
(
	cd "$root_dir"
	env PATH="$fake_bin:$PATH" E2E_TRIAL_ALLOW_DIRTY=true TEST_TRIAL_MODE=success \
		TEST_TRIAL_DOCKER_LOG="$success_docker_log" E2E_TRIAL_OUTPUT_DIR="$success_output" "$trial" 2 1
) >/dev/null
awk -F '\t' 'NR == 2 && $9 == 51 && $10 == 20 && $13 == 31 && $15 == "success" { found = 1 } END { exit !found }' "$success_output/summary.tsv"
grep -q 'down -v --remove-orphans' "$success_docker_log"

printf '%s\n' 'E2E worker-trial harness tests passed'
