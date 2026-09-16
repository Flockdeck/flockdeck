package agent

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestBuiltinCLIFlagsAreStillReal is the live half of the compatibility check
// described in flockdeck-planning/13-ci-agent-model-compatibility.md: for
// every built-in CLI-wrapped agent, it asks the installed binary's own --help
// whether the flags Flockdeck's spec assumes -- --session-id, --settings,
// --model, --resume, -i, --message and so on -- are still ones it has.
//
// It is a live check against whatever is actually on PATH, not a mock, which
// is why it is skipped rather than failed wherever that binary is not
// installed: every ordinary contributor's machine, and every ordinary PR.
// .github/workflows/agent-compat.yml installs each of these non-interactively
// and runs this same test on a schedule, and again on a pull_request that
// touches builtin.go, so a flag renamed upstream is caught there rather than
// by a pane dying on somebody's machine.
func TestBuiltinCLIFlagsAreStillReal(t *testing.T) {
	for _, spec := range normalizeAll(Builtins()) {
		if spec.Runner != RunnerCLI {
			continue
		}
		t.Run(spec.ID, func(t *testing.T) {
			path, err := exec.LookPath(spec.Exe)
			if err != nil {
				t.Skipf("%s is not installed here: %v", spec.Exe, err)
			}
			help := cliHelp(t, path)
			for _, flag := range flagsOf(spec) {
				if !strings.Contains(help, flag) {
					t.Errorf("%s --help does not mention %q, which agent %q's spec assumes; the flag may have been renamed upstream", spec.Exe, flag, spec.ID)
				}
			}
		})
	}
}

// cliHelp runs a CLI's own --help (falling back to -h, for the tools that
// only answer that one) and returns whatever it printed, stdout and stderr
// together -- several of these print their usage to stderr, and some exit
// non-zero doing it, which is not itself a failure here: the text is what is
// being checked, not the exit code.
func cliHelp(t *testing.T, path string) string {
	t.Helper()
	for _, flag := range []string{"--help", "-h"} {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		out, _ := exec.CommandContext(ctx, path, flag).CombinedOutput()
		cancel()
		if len(out) > 0 {
			return string(out)
		}
	}
	t.Fatalf("%s answered neither --help nor -h with anything to check", path)
	return ""
}

// flagsOf is every command-line flag a spec's own argument lists assume, for
// both the ordinary and the resume case, read off the real BuildArgv rather
// than reimplemented against Arg's shape a second time -- so this can never
// drift from what a pane is actually started with.
func flagsOf(spec Spec) []string {
	tokens := Tokens{
		Session:  "11111111-2222-3333-4444-555555555555",
		Model:    "some-model",
		Settings: "settings.json",
		Prompt:   "do the thing",
		Cwd:      "/work",
		Pane:     "pane-1",
	}
	seen := map[string]bool{}
	var flags []string
	note := func(argv []string) {
		for _, a := range argv {
			if strings.HasPrefix(a, "-") && !seen[a] {
				seen[a] = true
				flags = append(flags, a)
			}
		}
	}
	note(BuildArgv(spec, false, tokens))
	if len(spec.ResumeArgs) > 0 {
		note(BuildArgv(spec, true, tokens))
	}
	return flags
}
