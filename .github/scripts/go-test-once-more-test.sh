#!/usr/bin/env bash
# go-test-once-more-test.sh checks go-test-once-more.sh against small Go
# packages that fail in the ways that matter: a test that fails once, a test
# that always fails, a panic that hides the tests after it, and a test that
# fails only after another one has run.
#
#   bash .github/scripts/go-test-once-more-test.sh
#
# Each case runs the real script with the real go, in a scratch module, with
# GITHUB_EVENT_NAME set as a pull request or as a push to main would have it.
set -uo pipefail

script="$(cd "$(dirname "$0")" && pwd)/go-test-once-more.sh"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
failures=0

# scenario NAME: a module with one package, its test file read from stdin.
scenario() {
	mkdir -p "$work/$1"
	printf 'module scratch\n\ngo 1.21\n' > "$work/$1/go.mod"
	cat > "$work/$1/x_test.go"
}

# check NAME EVENT WANT_EXIT [go test flags]: runs the script in the scenario.
check() {
	local name=$1 event=$2 want=$3
	shift 3
	rm -f "$work/$name/mark"
	local got
	(cd "$work/$name" && GITHUB_EVENT_NAME="$event" SCRATCH_MARK="$work/$name/mark" GITHUB_STEP_SUMMARY=/dev/null \
		bash "$script" "$work/$name.log" -count=1 "$@" > "$work/$name.out" 2>&1)
	got=$?
	if [ "$got" -ne "$want" ]; then
		echo "FAIL: $name on $event: exit $got, want $want"
		sed 's/^/    /' "$work/$name.out"
		failures=$((failures + 1))
	else
		echo "ok:   $name on $event: exit $got"
	fi
}

scenario flaky <<'EOF'
package x

import (
	"os"
	"testing"
)

func TestFlaky(t *testing.T) {
	if _, err := os.Stat(os.Getenv("SCRATCH_MARK")); err != nil {
		os.WriteFile(os.Getenv("SCRATCH_MARK"), nil, 0o644)
		t.Fatal("fails the first time only")
	}
}
EOF

scenario always <<'EOF'
package x

import "testing"

func TestAlways(t *testing.T) { t.Fatal("always fails") }
EOF

# TestB panics once and passes after; TestC would fail, but only runs after the
# panic. A retry of TestB alone would never run TestC.
scenario panic <<'EOF'
package x

import (
	"os"
	"testing"
)

func TestA(t *testing.T) {}

func TestB(t *testing.T) {
	if _, err := os.Stat(os.Getenv("SCRATCH_MARK")); err != nil {
		os.WriteFile(os.Getenv("SCRATCH_MARK"), nil, 0o644)
		panic("fails the first time only")
	}
}

func TestC(t *testing.T) { t.Fatal("always fails, after the panic") }
EOF

# TestSecond fails only after TestFirst has run in the same binary.
scenario coupled <<'EOF'
package x

import "testing"

var left bool

func TestFirst(t *testing.T) { left = true }

func TestSecond(t *testing.T) {
	if left {
		t.Fatal("state left by an earlier test")
	}
}
EOF

# The caller names the tests to run; the retry must not replace that.
scenario callerrun <<'EOF'
package x

import (
	"os"
	"testing"
)

func TestX(t *testing.T) {
	if _, err := os.Stat(os.Getenv("SCRATCH_MARK")); err != nil {
		os.WriteFile(os.Getenv("SCRATCH_MARK"), nil, 0o644)
		t.Fatal("fails the first time only")
	}
}

func TestY(t *testing.T) { t.Fatal("not selected, never runs") }
EOF

# TestA fails once, then TestB ends the binary without a panic; TestC never runs.
scenario exitcrash <<'EOF'
package x

import (
	"os"
	"testing"
)

func TestA(t *testing.T) {
	if _, err := os.Stat(os.Getenv("SCRATCH_MARK")); err != nil {
		os.WriteFile(os.Getenv("SCRATCH_MARK"), nil, 0o644)
		t.Fatal("fails the first time only")
	}
}

func TestB(t *testing.T) {
	os.Exit(1)
}

func TestC(t *testing.T) { t.Fatal("always fails, after the binary ended") }
EOF

# The same, through log.Fatal.
scenario fatalcrash <<'EOF'
package x

import (
	"os"
	"log"
	"testing"
)

func TestA(t *testing.T) {
	if _, err := os.Stat(os.Getenv("SCRATCH_MARK")); err != nil {
		os.WriteFile(os.Getenv("SCRATCH_MARK"), nil, 0o644)
		t.Fatal("fails the first time only")
	}
}

func TestB(t *testing.T) {
	log.Fatal("ends the binary")
}

func TestC(t *testing.T) { t.Fatal("always fails, after the binary ended") }
EOF

# The same, with the process killed from outside the test (signal: killed).
scenario killcrash <<'EOF'
package x

import (
	"os"
	"testing"
)

func TestA(t *testing.T) {
	if _, err := os.Stat(os.Getenv("SCRATCH_MARK")); err != nil {
		os.WriteFile(os.Getenv("SCRATCH_MARK"), nil, 0o644)
		t.Fatal("fails the first time only")
	}
}

func TestB(t *testing.T) {
	p, _ := os.FindProcess(os.Getpid())
	p.Kill()
	select {}
}

func TestC(t *testing.T) { t.Fatal("always fails, after the binary ended") }
EOF

check flaky pull_request 0
check flaky push 0
check always pull_request 1
check always push 1
# A crash is never narrowed, on a pull request or elsewhere.
check panic pull_request 1
check panic push 1
# Only a pull request narrows: a push runs the whole package again.
check coupled pull_request 0
check coupled push 1
check callerrun pull_request 0 -run '^TestX$'
# A binary that ended without a panic is never narrowed either: the tests after
# the one that failed did not run, and TestC always fails.
check exitcrash pull_request 1
check fatalcrash pull_request 1
check killcrash pull_request 1

# The retry is recorded in the log, where the step that names failures reads it.
if ! grep -q 'once more' "$work/flaky.log"; then
	echo "FAIL: the retry left no trace in the log"
	failures=$((failures + 1))
fi

[ "$failures" -eq 0 ] || exit 1
echo "all cases passed"
