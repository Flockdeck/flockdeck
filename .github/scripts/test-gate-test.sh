#!/usr/bin/env bash
# Tests test-gate.sh against result files made here, with the platform names and
# share counts the workflow uses (four shares on Linux and Windows, two on macOS).
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

# all OS N makes a full set of passing results for OS with N shares, with the
# race detector.
all() {
	rm -rf "$work/r"
	mkdir "$work/r"
	echo "success" > "$work/r/static-$1-0.txt"
	local i
	for i in $(seq 1 "$2"); do
		echo "success abc123 $2" > "$work/r/shard-$1-$i.txt"
		echo "success def456 $2" > "$work/r/race-$1-$i.txt"
	done
}

# gate OS RACE_OSES SHARDS
gate() { bash "$here/test-gate.sh" "$work/r" "$1" "$2" "$3" > "$work/out" 2>&1; }
passes() { gate "$@"; test $? -eq 0; }
fails() { gate "$@"; test $? -ne 0; }

L=ubuntu-latest
M=macos-latest
W=windows-latest

all $L 4
check "four shares on Linux pass" passes $L "$L $M" 4
all $M 2
check "two shares on macOS pass" passes $M "$L $M" 2
all $W 4
check "Windows passes without the race detector" passes $W "$L $M" 4

all $L 4
rm "$work/r/shard-$L-3.txt"
check "a share that left no result fails" fails $L "$L" 4

all $L 4
echo "failure abc123 4" > "$work/r/shard-$L-2.txt"
check "a share that failed fails" fails $L "$L" 4

all $L 4
echo "success zzz999 4" > "$work/r/shard-$L-4.txt"
check "a share that ran another plan fails" fails $L "$L" 4
check "and the output names it" grep -q "shard 4 ran plan zzz999" "$work/out"

all $L 4
echo "success" > "$work/r/shard-$L-1.txt"
check "a share that reported no plan fails" fails $L "$L" 4

all $L 4
rm "$work/r/static-$L-0.txt"
check "a missing static result fails" fails $L "$L" 4

all $L 4
rm "$work/r/race-$L-1.txt"
check "a missing race result fails where the detector runs" fails $L "$L" 4

all $M 2
rm "$work/r/race-$M-1.txt"
check "a missing race result passes where the detector does not run" passes $M "$L" 2

all $L 4
echo "cancelled" > "$work/r/static-$L-0.txt"
check "a cancelled static job fails" fails $L "$L" 4

all $L 4
echo "failure def456 4" > "$work/r/race-$L-2.txt"
check "a race share that failed fails" fails $L "$L" 4

all $L 4
echo "success zzz999 4" > "$work/r/race-$L-3.txt"
check "a race share that ran another plan fails" fails $L "$L" 4
check "and the output names it" grep -q "race 3 ran plan zzz999" "$work/out"

# Another platform's results are not read.
all $L 4
for f in "$work/r"/*-$L-*; do mv "$f" "${f/-$L-/-$W-}"; done
check "results of another platform do not count" fails $L "$L" 4

# The number of shares comes from the shares, not only from the gate's own count.
all $L 4
check "a plan of four shares, gate told one, fails" fails $L "$L" 1
check "and the output says why" grep -q "ran a plan of 4 shares, not 1" "$work/out"

all $M 2
check "a plan of two shares, gate told four, fails" fails $M "$L $M" 4

all $L 4
for k in shard race; do for i in 1 2 3 4; do echo "success abc123" > "$work/r/$k-$L-$i.txt"; done; done
check "results with no share count fail" fails $L "$L" 4

# A count that is not a positive number is refused, whatever the results say.
all $L 4
for bad in 0 "" abc -1 1.5 " 4"; do
	check "a share count of '$bad' is refused" fails $L "$L" "$bad"
done
check "and the output says so" grep -q "SHARDS must be a positive number" "$work/out"

# An empty list of platforms with the race detector is refused; none is allowed.
all $L 4
check "an empty race list is refused" fails $L "" 4
all $L 4
rm "$work/r"/race-*
check "none as the race list passes without race results" passes $L none 4

exit $fail
