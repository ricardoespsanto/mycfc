#!/bin/sh
set -eu

kind=${1:-}
output=${2:-}
case "$kind" in merge|release|infrastructure|credential|purge|activation) ;; *) printf 'approval packet kind must be merge, release, infrastructure, credential, purge, or activation\n' >&2; exit 2 ;; esac
[ -n "$output" ] || { printf 'approval packet output path is required\n' >&2; exit 2; }

single_line() {
	value=$1
	newline='
'
	case "$value" in *"$newline"*) return 1 ;; esac
	printf '%s' "$value" | LC_ALL=C grep -q '[[:cntrl:]]' && return 1
	printf '%s' "$value" | grep -Eqi 'password=|secret=|token=|postgres(ql)?://[^[:space:]]+@' && return 1
	[ "${#value}" -le 500 ]
}

version=${RELEASE_VERSION:-not-applicable}
sha=${GIT_SHA:-not-applicable}
summary=${APPROVAL_SUMMARY:-No summary supplied.}
identity=${APPROVAL_IDENTITY:-No immutable identity supplied.}
prerequisites=${APPROVAL_PREREQUISITES:-No prerequisites recorded.}
impact=${APPROVAL_USER_IMPACT:-No user impact recorded.}
irreversible=${APPROVAL_IRREVERSIBLE_EFFECTS:-None recorded.}
evidence=${APPROVAL_EVIDENCE:-Not yet recorded.}
missing=${APPROVAL_MISSING_EVIDENCE:-None recorded.}
rollback=${APPROVAL_ROLLBACK:-Stop and use the documented compensating rollback.}
scope=${APPROVAL_SCOPE:-No live activation is included.}
for value in "$version" "$sha" "$summary" "$identity" "$prerequisites" "$impact" "$irreversible" "$evidence" "$missing" "$rollback" "$scope"; do
	single_line "$value" || { printf 'approval packet rejected unsafe or multiline input\n' >&2; exit 1; }
done
case "$sha" in
	not-applicable) ;;
	*)
		printf '%s' "$sha" | grep -Eq '^[0-9a-f]{40}$' || {
			printf 'approval packet rejected invalid GIT_SHA\n' >&2
			exit 1
		}
		;;
esac

temporary=$(mktemp "${output}.tmp.XXXXXX")
trap 'rm -f "$temporary"' EXIT HUP INT TERM
{
	printf '# %s approval\n\n' "$(printf '%s' "$kind" | awk '{print toupper(substr($0,1,1)) substr($0,2)}')"
	printf '**Decision requested:** Approve only the %s gate described below.\n\n' "$kind"
	printf -- '- Version: `%s`\n' "$version"
	printf -- '- Commit: `%s`\n' "$sha"
	printf -- '- Exact identity: %s\n' "$identity"
	printf -- '- Change: %s\n' "$summary"
	printf -- '- Prerequisites: %s\n' "$prerequisites"
	printf -- '- User impact: %s\n' "$impact"
	printf -- '- Irreversible effects: %s\n' "$irreversible"
	printf -- '- Evidence checked: %s\n' "$evidence"
	printf -- '- Missing evidence: %s\n' "$missing"
	printf -- '- Rollback: %s\n' "$rollback"
	printf -- '- Explicit boundary: %s\n\n' "$scope"
	printf 'Approval of this packet does not approve merge, another infrastructure change, deployment, data deletion, or feature activation unless that exact action is named above.\n'
} >"$temporary"
chmod 0644 "$temporary"
mv "$temporary" "$output"
trap - EXIT HUP INT TERM
