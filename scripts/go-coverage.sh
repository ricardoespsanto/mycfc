#!/usr/bin/env bash
set -Eeuo pipefail

minimum=${GO_COVERAGE_MIN:-50.0}
report_dir=${COVERAGE_DIR:-artifacts/coverage}
raw_profile="$report_dir/unit.raw.out"
profile="$report_dir/unit.out"
text_report="$report_dir/unit.txt"
html_report="$report_dir/unit.html"
package_report="$report_dir/unit-packages.txt"
package_floors=scripts/go-coverage-floors.txt

mkdir -p "$report_dir"
go test -covermode=atomic -coverprofile="$raw_profile" ./internal/... ./cmd/... ./ui/... | tee "$package_report"

# sqlc and templ output are generated from reviewed SQL/templates. Counting
# their wrappers as hand-written unit-testable code obscures the useful
# application coverage signal; their behavior is exercised by integration,
# rendering, and E2E gates.
awk 'NR == 1 || ($0 !~ /\/internal\/db\/generated\// && $0 !~ /_templ\.go:/)' "$raw_profile" >"$profile"
rm "$raw_profile"

go tool cover -func="$profile" >"$text_report"
go tool cover -html="$profile" -o "$html_report"
total=$(awk '/^total:/ { gsub(/%/, "", $3); print $3 }' "$text_report")
tail -n 1 "$text_report"

package_failure=0
while read -r package package_minimum; do
  [[ -z "$package" || "$package" == \#* ]] && continue
  package_line=$(awk -v package="$package" '$2 == package && /coverage:/ { line=$0 } END { print line }' "$package_report")
  package_total=$(sed -n 's/.*coverage: \([0-9.]*\)% of statements.*/\1/p' <<<"$package_line")
  if [[ -z "$package_total" ]]; then
    printf 'coverage result missing for %s\n' "$package" >&2
    package_failure=1
    continue
  fi
  if ! awk -v total="$package_total" -v minimum="$package_minimum" 'BEGIN { exit !((total + 0) >= (minimum + 0)) }'; then
    printf '%s coverage %.1f%% is below its %.1f%% floor\n' "$package" "$package_total" "$package_minimum" >&2
    package_failure=1
  fi
done <"$package_floors"

if [[ -n "${GITHUB_STEP_SUMMARY:-}" ]]; then
  {
    echo "## Unit coverage"
    echo
    echo "- Hand-written Go statements: **${total}%**"
    echo "- Required floor: **${minimum}%**"
    echo "- Per-package regression floors: **enforced**"
    echo "- Generated sqlc wrappers are excluded; integration coverage remains a separate CI gate."
  } >>"$GITHUB_STEP_SUMMARY"
fi

awk -v total="$total" -v minimum="$minimum" 'BEGIN {
  if ((total + 0) < (minimum + 0)) {
    printf "unit coverage %.1f%% is below the %.1f%% floor\n", total, minimum > "/dev/stderr"
    exit 1
  }
}'
exit "$package_failure"
