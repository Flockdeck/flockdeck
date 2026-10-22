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
# On a pull request only the tests that failed are run again. Running the whole
# package again cost a second full run of it (over eight minutes for
# internal/server on Windows) for one flaky test, and the tests that passed
# have passed. The whole failed packages are run again, as before, when
#   - the run is not for a pull request (a push to main, a release, a manual
#     run), so that a failing package gets a full second pass before it counts
#     as green, which catches a test that only fails after another one;
#   - a failed package has no failing test to name (it did not build, or it
#     timed out), or it crashed (a panic, os.Exit, log.Fatal, a kill), which
#     ended the test binary and left the later tests unrun: a package whose
#     tests failed normally ends its output with a bare FAIL line, and one that
#     crashed does not (one such package makes all of them run whole);
#   - the caller passed its own -run, which the retry would replace.
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
# any failed package has no failing test to name, or crashed. A binary whose
# tests failed normally prints a bare FAIL line before go test adds the
# package's own FAIL line; one that died (a panic, os.Exit, log.Fatal, a kill, a
# timeout) does not. The tests after the one that failed never ran in a binary
# that died, and running only the failed one would leave them unrun.
names=$(tr -d '\r' < "$log" | awk '
	/^--- FAIL: / { seen = seen "|" $3; next }
	/^(panic:|fatal error:)/ { crashed = 1; next }
	$0 == "FAIL" { bare = 1; next }
	/^FAIL[ \t]+[^ \t]+/ {
		if (seen == "" || crashed || !bare) { none = 1 }
		all = all seen
		seen = ""
		crashed = 0
		bare = 0
		next
	}
	/^(ok|[?])[ \t]/ { seen = ""; crashed = 0; bare = 0; next }
	END { if (!none && all != "") print substr(all, 2) }
')

# Narrowing the second run is for pull requests. A test that fails only when
# the whole package runs (state an earlier test left behind) passes on its own,
# so a narrow retry would turn a real failure green. A push to main and a
# release run have no such shortcut: they run the whole failed package again,
# so a failing package gets a full second pass before it counts as green.
# A caller's own -run is left alone for the same reason: the retry would
# replace it.
narrow=0
if [ "${GITHUB_EVENT_NAME:-}" = pull_request ] && [ -n "$names" ]; then
	narrow=1
	for arg in "$@"; do
		case "$arg" in -run | -run=* | --run | --run=*) narrow=0 ;; esac
	done
fi
again=()
what="the packages that failed: $(echo $failed)"
if [ "$narrow" = 1 ]; then
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
