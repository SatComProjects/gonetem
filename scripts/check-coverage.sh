#!/usr/bin/env bash
#
# Enforce the per-package coverage ratchet from a Go coverage profile.
#
# Usage: ./scripts/check-coverage.sh [coverage.out] [.coverage-thresholds]
#
# The profile is produced by:
#   go test -short -covermode=atomic -coverprofile=coverage.out <packages>
#
# Only internal packages are considered. Generated protobuf, cmd and testutil
# are ignored. Exits non-zero when any floor is missed.

set -euo pipefail
export LC_ALL=C

profile="${1:-coverage.out}"
thresholds="${2:-.coverage-thresholds}"

if [[ ! -f "$profile" ]]; then
	echo "coverage profile not found: $profile" >&2
	exit 2
fi
if [[ ! -f "$thresholds" ]]; then
	echo "coverage thresholds not found: $thresholds" >&2
	exit 2
fi

awk -v threshold_file="$thresholds" '
function trim(s) {
	gsub(/^[ \t]+|[ \t]+$/, "", s)
	return s
}
BEGIN {
	while ((getline line < threshold_file) > 0) {
		line = trim(line)
		if (line == "" || line ~ /^#/) {
			continue
		}
		split(line, fields, /[ \t]+/)
		key = fields[1]
		min[key] = fields[2] + 0
		if (!(key in seen)) {
			seen[key] = 1
			order[++count] = key
		}
	}
	close(threshold_file)
}
NR == 1 {
	next
}
{
	file = $1
	sub(/:.*/, "", file)
	if (file !~ /\/internal\//) {
		next
	}

	statements = $2 + 0
	hits = $3 + 0

	sub(/^.*\/internal\//, "", file)
	split(file, parts, "/")
	package = "internal/" parts[1]

	if (package == "internal/proto" || package == "internal/testutil") {
		next
	}

	total_statements[package] += statements
	total_hits[package] += (hits > 0 ? statements : 0)
	total_statements["total"] += statements
	total_hits["total"] += (hits > 0 ? statements : 0)
}
END {
	failed = 0
	for (i = 1; i <= count; i++) {
		key = order[i]
		if (!(key in total_statements)) {
			printf "%-24s no statements (min %5.1f%%)\n", key, min[key]
			failed = 1
			continue
		}
		percent = 100 * total_hits[key] / total_statements[key]
		status = (percent + 1e-9 >= min[key]) ? "ok" : "FAIL"
		printf "%-24s %6.2f%% (min %5.1f%%) %s\n", key, percent, min[key], status
		if (percent + 1e-9 < min[key]) {
			failed = 1
		}
	}
	exit failed
}
' "$profile"
