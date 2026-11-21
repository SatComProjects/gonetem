#!/usr/bin/env bash
#
# Run govulncheck and fail on any reachable vulnerability that is not in the
# reviewed exception list.
#
# Usage: ./scripts/check-vulnerabilities.sh [.govulncheck-allow]
#
# govulncheck exits 0 when clean, 3 when it finds reachable vulnerabilities,
# and another status for scanner/infrastructure failures. Only exit 3 is
# filtered against the allowlist; scanner failures always fail the gate.

set -euo pipefail
export LC_ALL=C

allow_file="${1:-.govulncheck-allow}"
log="$(mktemp)"
trap 'rm -f "$log"' EXIT

set +e
govulncheck ./... >"$log" 2>&1
status=$?
set -e

if [[ "$status" -eq 0 ]]; then
	echo "govulncheck: no reachable vulnerabilities"
	exit 0
fi

if [[ "$status" -ne 3 ]]; then
	echo "govulncheck failed with exit $status:" >&2
	cat "$log" >&2
	exit "$status"
fi

mapfile -t found < <(
	grep -E '^Vulnerability #' "$log" |
		grep -oE 'GO-[0-9]{4}-[0-9]+' |
		sort -u
)
if [[ "${#found[@]}" -eq 0 ]]; then
	echo "govulncheck reported vulnerabilities but no IDs were found:" >&2
	cat "$log" >&2
	exit 1
fi

unknown=()
for id in "${found[@]}"; do
	if ! grep -qxF "$id" "$allow_file"; then
		unknown+=("$id")
	fi
done

if [[ "${#unknown[@]}" -gt 0 ]]; then
	echo "Unreviewed reachable vulnerabilities: ${unknown[*]}" >&2
	grep -A10 '^Vulnerability #' "$log" >&2
	exit 1
fi

echo "govulncheck: only reviewed exceptions remain"
for id in "${found[@]}"; do
	echo "  - $id"
done
