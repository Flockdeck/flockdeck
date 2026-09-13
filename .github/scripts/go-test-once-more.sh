#!/usr/bin/env bash
# go-test-once-more.sh runs every package's tests, and then runs any package
# that failed once more, on its own, before calling the run a failure.
#
#   bash .github/scripts/go-test-once-more.sh LOG [go test flags...]
#
# A package that fails twice fails the run. One that passes the second time
# does not, but is named in a warning annotation with the tests that failed
# the first time, so that a test that depends on the runner's timing is seen
# and fixed rather than quietly passed. The output of both runs goes to LOG,
# where the step that names the failures reads it.
#
# Six release candidates of v0.3.4 in a row were each stopped by a different
# test that had passed everywhere else and passed again when run on its own.
set -uo pipefail

log="$1"
shift

go test "$@" ./... 2>&1 | tee "$log"
status=${PIPESTATUS[0]}
[ "$status" -eq 0 ] && exit 0

# go test ends each failed package with "FAIL<tab>path<tab>time", or with a
# bracketed reason for one that did not build. The bare "FAIL" line it ends
# the whole run with names nothing, and is left out.
failed=$(tr -d '\r' < "$log" | grep -E '^FAIL[[:space:]]+[^[:space:]]+' | awk '{print $2}' | sort -u)
if [ -z "$failed" ]; then
	exit "$status"
fi
first=$(tr -d '\r' < "$log" | grep -E -- '--- FAIL' | sed 's/^[[:space:]]*//' | sort -u)

echo "::group::Running the packages that failed once more: $(echo $failed)"
echo "===== once more: $(echo $failed)" >> "$log"
# shellcheck disable=SC2086 # one argument per package
go test "$@" $failed 2>&1 | tee -a "$log"
status=${PIPESTATUS[0]}
echo "::endgroup::"

if [ "$status" -eq 0 ]; then
	body=$(printf 'Failed, then passed when run again:\n%s\n\n%s\n' "$failed" "$first" | sed -e 's/%/%25/g' | awk 'BEGIN { ORS = "%0A" } { print }')
	echo "::warning title=Tests that passed only when run again on ${RUNNER_OS:-this runner}::$body"
fi
exit "$status"
