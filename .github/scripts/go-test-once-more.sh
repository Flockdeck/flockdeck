#!/usr/bin/env bash
# go-test-once-more.sh runs every package's tests, and then runs the tests that
# failed once more, on their own, before calling the run a failure.
#
#   bash .github/scripts/go-test-once-more.sh LOG [go test flags...]
#
# A test that fails twice fails the run. One that passes the second time does
# not, but is named in a warning annotation and in the job summary, so that a
# test that depends on the runner's timing is seen and fixed rather than
# quietly passed. The output of both runs goes to LOG, where the step that
# names the failures reads it.
#
# Only the tests that failed are run again. Running the whole package again
# cost a second full run of it (over eight minutes for internal/server on
# Windows) for one flaky test, and the tests that passed have passed. A failed
# package with no failing test to name (it did not build, it timed out, or its
# test binary crashed) is run again whole, and so are all the failed packages
# if any one of them is like that.
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

# The top-level tests that failed, as an alternation for -run. A failed
# subtest shows its parent failing as well, so the parents are enough. A
# package's output comes whole and ends with its FAIL line, so the names seen
# since the last package line belong to the package that line closes. Empty if
# any failed package has no failing test to name.
names=$(tr -d '\r' < "$log" | awk '
	/^--- FAIL: / { seen = seen "|" $3; next }
	/^FAIL[ \t]+[^ \t]+/ {
		if (seen == "") { none = 1 }
		all = all seen
		seen = ""
		next
	}
	/^(ok|[?])[ \t]/ { seen = ""; next }
	END { if (!none && all != "") print substr(all, 2) }
')
again=()
what="the packages that failed: $(echo $failed)"
if [ -n "$names" ]; then
	again=(-run "^($names)\$")
	what="the tests that failed in $(echo $failed)"
fi

echo "::group::Running $what once more"
echo "===== once more: $(echo $failed)" >> "$log"
# shellcheck disable=SC2086 # one argument per package
go test "$@" "${again[@]}" $failed 2>&1 | tee -a "$log"
status=${PIPESTATUS[0]}
echo "::endgroup::"

if [ "$status" -eq 0 ]; then
	body=$(printf 'Failed, then passed when run again:\n%s\n\n%s\n' "$failed" "$first" | sed -e 's/%/%25/g' | awk 'BEGIN { ORS = "%0A" } { print }')
	echo "::warning title=Tests that passed only when run again on ${RUNNER_OS:-this runner}::$body"
	if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
		{
			echo "### Passed only when run again (${RUNNER_OS:-this runner})"
			echo '```'
			printf '%s\n\n%s\n' "$failed" "$first"
			echo '```'
		} >> "$GITHUB_STEP_SUMMARY"
	fi
fi
exit "$status"
