#!/usr/bin/env bash
set -Eeuo pipefail

minimum=${GO_COVERAGE_MIN:-85.0}
report_dir=${COVERAGE_DIR:-artifacts/coverage}
raw_profile="$report_dir/unit.raw.out"
profile="$report_dir/unit.out"
text_report="$report_dir/unit.txt"
html_report="$report_dir/unit.html"
package_report="$report_dir/unit-packages.txt"
file_report="$report_dir/unit-files.txt"
changed_report="$report_dir/unit-changed-files.txt"
uncovered_report="$report_dir/unit-largest-uncovered.txt"
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

# Retain a machine- and human-readable file report alongside Go's standard
# function report. The filtered profile is the source of truth, so generated
# sqlc and templ output cannot enter any aggregate or per-file figure.
awk 'NR > 1 && $1 ~ /:/ {
  split($1, location, ":")
  file = location[1]
  total[file] += $2
  if ($3 > 0) covered[file] += $2
}
END {
  print "file\tcovered_statements\ttotal_statements\tcoverage_percent"
  for (file in total) {
    printf "%s\t%d\t%d\t%.1f%%\n", file, covered[file], total[file], 100 * covered[file] / total[file]
  }
}' "$profile" | sort >"$file_report"

awk -F '\t' 'NR > 1 {
  uncovered = $3 - $2
  printf "%d\t%s\t%s\n", uncovered, $4, $1
}' "$file_report" | sort -rn | head -20 >"$uncovered_report"

# CI supplies the tested comparison SHA for pull requests and pushes. Locally,
# callers can opt in with COVERAGE_CHANGED_BASE=<git-sha>. The report applies
# the 85% changed-code expectation to executable changed source lines, rather
# than treating an entire legacy file as changed. Tests and generated files are
# deliberately excluded. CI sets COVERAGE_REQUIRE_CHANGED_BASE so an absent or
# invalid comparison cannot silently bypass the expectation.
changed_failure=0
{
	echo -e "file\tcovered_changed_lines\texecutable_changed_lines\tcoverage_percent\tresult"
	if [[ -z "${COVERAGE_CHANGED_BASE:-}" ]]; then
		echo -e "comparison unavailable\t-\t-\t-\tnot evaluated (set COVERAGE_CHANGED_BASE)"
		if [[ "${COVERAGE_REQUIRE_CHANGED_BASE:-0}" == "1" ]]; then
			changed_failure=1
		fi
	elif ! git rev-parse --verify --quiet "$COVERAGE_CHANGED_BASE^{commit}" >/dev/null; then
		echo -e "comparison unavailable\t-\t-\t-\tfail (base commit is not present)"
		if [[ "${COVERAGE_REQUIRE_CHANGED_BASE:-0}" == "1" ]]; then
			changed_failure=1
		fi
	else
		changed_lines=$(git diff --unified=0 "$COVERAGE_CHANGED_BASE" -- '*.go' | awk '
			/^\+\+\+ b\// { file = substr($0, 7); next }
			/^@@ / {
				match($0, /\+[0-9]+(,[0-9]+)?/)
				range = substr($0, RSTART + 1, RLENGTH - 1)
				split(range, parts, ",")
				start = parts[1]
				count = (length(parts) > 1 ? parts[2] : 1)
				for (line = start; line < start + count; line++) print file "\t" line
			}')
		changed_lines=$(printf '%s\n' "$changed_lines" | grep -Ev '(^|/)(generated/|.*_templ\.go$|.*_test\.go$)' || true)
		if [[ -z "$changed_lines" ]]; then
			echo -e "no changed hand-written Go source lines\t-\t-\t-\tnot applicable"
		else
			changed_results=$(awk '
				NR == FNR { wanted[$1 SUBSEP $2] = 1; changed_files[$1] = 1; next }
				{
					split($1, location, ":")
					profile_file = location[1]
					split(location[2], span, ",")
					split(span[1], begin, "\\.")
					split(span[2], end, "\\.")
					for (file in changed_files) {
						if (profile_file ~ ("/" file "$")) {
							for (line = begin[1]; line <= end[1]; line++) {
								key = file SUBSEP line
								if (key in wanted) {
									if (!(key in executable)) { executable[key] = 1; source_file[key] = file; total[file]++ }
									if ($3 > 0) covered[key] = 1
								}
							}
						}
					}
				}
				END {
					for (key in covered) { covered_total[source_file[key]]++ }
					for (file in total) {
						percent = 100 * covered_total[file] / total[file]
						result = percent >= 85.0 ? "pass" : "fail (below 85% expectation)"
						printf "%s\t%d\t%d\t%.1f%%\t%s\n", file, covered_total[file] + 0, total[file], percent, result
					}
				}' <(printf '%s\n' "$changed_lines") "$profile" | sort)
			if [[ -z "$changed_results" ]]; then
				echo -e "no changed executable Go source lines\t-\t-\t-\tnot applicable"
			else
				printf '%s\n' "$changed_results"
				if grep -q $'\tfail ' <<<"$changed_results"; then
					changed_failure=1
				fi
			fi
		fi
	fi
} >"$changed_report"

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

if ((changed_failure)); then
  printf 'changed hand-written Go coverage is below the 85%% expectation; see %s\n' "$changed_report" >&2
  package_failure=1
fi

if [[ -n "${GITHUB_STEP_SUMMARY:-}" ]]; then
  {
    echo "## Unit coverage"
    echo
    echo "- Hand-written Go statements: **${total}%**"
    echo "- Required floor: **${minimum}%**"
    echo "- Per-package regression floors: **enforced**"
	echo '- Changed hand-written Go source lines: see `unit-changed-files.txt` (85% expectation; CI requires a valid comparison SHA).'
    echo '- Largest uncovered hand-written files: see `unit-largest-uncovered.txt`.'
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
