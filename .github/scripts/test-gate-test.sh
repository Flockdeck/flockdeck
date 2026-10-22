#!/usr/bin/env bash
# Tests test-gate.sh against result files made here.
set -uo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

fail=0
check() {
	local what=$1
	shift
	if "$@"; then echo "ok: $what"; else echo "FAIL: $what"; fail=1; fi
}

# all makes a full set of passing results for linux, with the race detector.
all() {
	rm -rf "$work/r"
	mkdir "$work/r"
	echo "success" > "$work/r/static-linux-0.txt"
	for i in 1 2 3 4; do
		echo "success abc123" > "$work/r/shard-linux-$i.txt"
		echo "success def456" > "$work/r/race-linux-$i.txt"
	done
}

gate() { bash "$here/test-gate.sh" "$work/r" "${1:-linux}" "${2:-linux}" 4 > "$work/out" 2>&1; }

all
gate
check "a full set passes" test $? -eq 0

all
rm "$work/r/shard-linux-3.txt"
gate
check "a share that left no result fails" test $? -ne 0

all
echo "failure abc123" > "$work/r/shard-linux-2.txt"
gate
check "a share that failed fails" test $? -ne 0

all
echo "success zzz999" > "$work/r/shard-linux-4.txt"
gate
check "a share that ran another plan fails" test $? -ne 0
check "and the output names it" grep -q "shard 4 ran plan zzz999" "$work/out"

all
echo "success" > "$work/r/shard-linux-1.txt"
gate
check "a share that reported no plan fails" test $? -ne 0

all
rm "$work/r/static-linux-0.txt"
gate
check "a missing static result fails" test $? -ne 0

all
rm "$work/r/race-linux-1.txt"
gate
check "a missing race result fails where the detector runs" test $? -ne 0

all
rm "$work/r/race-linux-1.txt"
gate linux "macos-latest"
check "a missing race result passes where the detector does not run" test $? -eq 0

all
echo "cancelled" > "$work/r/static-linux-0.txt"
gate
check "a cancelled static job fails" test $? -ne 0

# Another platform's results are not read.
all
for f in "$work/r"/*-linux-*; do mv "$f" "${f/-linux-/-windows-}"; done
gate linux
check "results of another platform do not count" test $? -ne 0

exit $fail
