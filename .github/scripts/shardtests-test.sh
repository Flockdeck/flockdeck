#!/usr/bin/env bash
# Tests that shardtests.go splits the suite the same way every time it is asked,
# which is what lets the jobs of a platform each run their own share without
# talking to one another. The tool itself refuses to run unless every package,
# and every test of a cut package, is in exactly one share, so a plan that is
# printed at all is a complete one.
set -uo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/../.." || exit 2

fail=0
check() {
	local what=$1
	shift
	if "$@"; then echo "ok: $what"; else echo "FAIL: $what"; fail=1; fi
}

plan() { go run .github/scripts/shardtests.go -shard "$1" -profile "$2" -plan 2>&1; }

for config in linux:4 windows:4 macos:2 race:4 race:2; do
	profile=${config%:*}
	of=${config#*:}
	{
		a=$(plan "1/$of" "$profile")
		b=$(plan "$of/$of" "$profile")
		c=$(plan "1/$of" "$profile")
		check "$profile in $of: the plan is the same for every share" test "$(echo "$a" | tail -1)" = "$(echo "$b" | tail -1)"
		check "$profile in $of: the plan is the same on every run" test "$a" = "$c"
		check "$profile in $of: the plan names a digest" bash -c '[[ "$(echo "$1" | tail -1)" =~ ^plan\ [0-9a-f]{12}$ ]]' _ "$a"
		check "$profile in $of: there is a share for each job" test "$(echo "$a" | grep -c '^share ')" -eq "$of"
	}
done

# Fuzz tests and examples run under a plain go test, so they must be in a share
# of every package that is cut. The plan is asked for the names of those
# packages; files with a build constraint are left out, since the listing is for
# this system.
for config in linux:4 race:4 macos:2; do
	profile=${config%:*}
	of=${config#*:}
	# In a file: the names are more than one argument can hold.
	namesfile=$(mktemp)
	go run .github/scripts/shardtests.go -shard "1/$of" -profile "$profile" -plan -names 2>&1 | grep '^names ' > "$namesfile"
	for pkg in $(awk '{print $3}' "$namesfile" | sort -u); do
		files=$(grep -L '^//go:build' "$pkg"/*_test.go 2> /dev/null)
		[ -n "$files" ] || continue
		# shellcheck disable=SC2086
		for fn in $(grep -hoE '^func (Fuzz|Example)[A-Za-z0-9_]*' $files | awk '{print $2}'); do
			check "$profile in $of: $pkg $fn is in a share" grep -qE "^names [0-9]+ $pkg (.* )?$fn( |$)" "$namesfile"
		done
	done
	rm -f "$namesfile"
done

# A share count that is not a share of the plan is refused.
check "a share past the last is refused" bash -c '! go run .github/scripts/shardtests.go -shard 5/4 -profile linux -plan > /dev/null 2>&1'

exit $fail
