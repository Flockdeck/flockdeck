#!/usr/bin/env bash
# test-gate.sh DIR OS RACE_OSES SHARDS decides whether the tests of one platform
# passed, from the result files its jobs left.
#
#   DIR        the folder the result files were downloaded into
#   OS         the platform, such as windows-latest
#   RACE_OSES  the platforms the race detector is run on in this run
#   SHARDS     how many shares the tests, and the race detector, are split into
#
# The tests are split across jobs, and the branch protection names one check
# per platform, so a job per platform collects what the others found. Each job
# writes DIR/KIND-OS-INDEX.txt with its status and the digest of the plan it
# ran and how many shares it was one of ("success 3f2a 4"): static-OS-0 for the
# checks that are not tests, then
# shard-OS-1 to shard-OS-N for the tests, and race-OS-1 to race-OS-N where the
# race detector runs. All must be there and succeeded, and the shares of a kind
# must have agreed on the plan: a share that worked out another split could
# leave a test unrun or run it twice. Each must also say it was one of SHARDS
# shares, so that a plan split four ways of which one was run is not passed
# because the gate was told one.
set -uo pipefail

dir="$1"
os="$2"
race_oses="$3"
shards="$4"
bad=0

if ! [[ $shards =~ ^[1-9][0-9]*$ ]]; then
	echo "::error::SHARDS must be a positive number, got '$shards'"
	exit 2
fi
if [ -z "$race_oses" ]; then
	echo "::error::RACE_OSES must name the platforms the race detector runs on, or none"
	exit 2
fi

problem() {
	echo "::error title=Tests on $os::$1"
	bad=1
}

# check KIND INDEX reads the result of one job and sets digest to its plan and
# count to the number of shares it says the plan has.
check() {
	local file="$dir/$1-$os-$2.txt"
	digest=""
	count=""
	if [ ! -f "$file" ]; then
		problem "$1 $2 left no result: it did not run, was cancelled, or its runner was lost"
		return
	fi
	local status
	read -r status digest count < "$file" || true
	if [ "$status" != success ]; then
		problem "$1 $2 ended with status ${status:-none}"
	fi
}

# shares_of KIND INDEX checks one share: its result, and that the plan it ran has
# the number of shares there should be.
shares_of() {
	check "$1" "$2"
	if [ -n "$digest" ] && [ "$count" != "$shards" ]; then
		problem "$1 $2 ran a plan of ${count:-an unknown number of} shares, not $shards"
	fi
}

# agree KIND checks the shares of that kind and compares their plans.
agree() {
	local kind=$1 first="" i
	for i in $(seq 1 "$shards"); do
		shares_of "$kind" "$i"
		if [ -z "$digest" ]; then
			problem "$kind $i reported no plan"
		elif [ -z "$first" ]; then
			first=$digest
		elif [ "$digest" != "$first" ]; then
			problem "$kind $i ran plan $digest, another share ran $first"
		fi
	done
}

check static 0
agree shard
for r in $race_oses; do
	if [ "$r" = "$os" ]; then
		agree race
	fi
done

if [ "$bad" -eq 0 ]; then
	echo "all jobs for $os passed, and the shares agreed on the plan"
fi
exit "$bad"
