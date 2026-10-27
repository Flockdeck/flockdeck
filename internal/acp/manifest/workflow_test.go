package manifest

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

const canaryWorkflow = "../../../.github/workflows/acp-canary.yml"

func workflowText(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(canaryWorkflow)
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(b), "\r\n", "\n")
}

// The canary reports; it does not gate. It must not run on a pull request or a
// push, and must not be given a secret.
func TestCanaryOnlyRunsOnASchedule(t *testing.T) {
	w := workflowText(t)
	on, _, _ := strings.Cut(strings.TrimPrefix(w[strings.Index(w, "\non:\n"):], "\non:\n"), "\npermissions:")
	for _, bad := range []string{"pull_request", "push:", "pull_request_target", "workflow_run"} {
		if strings.Contains(on, bad) {
			t.Errorf("the canary triggers on %s", bad)
		}
	}
	if !strings.Contains(on, "schedule:") || !strings.Contains(on, "workflow_dispatch:") {
		t.Errorf("triggers = %q", on)
	}
	if !strings.Contains(w, "permissions:\n  contents: read") {
		t.Error("the workflow does not ask for read-only permission")
	}
	if strings.Contains(w, "secrets.") || strings.Contains(w, "GITHUB_TOKEN") {
		t.Error("the canary references a secret or a token")
	}
	// The npm adapters are installed by the handshake job, which has nothing
	// to do with Claude Code; Flockdeck's own adapter for it has a job of its own.
	hs, _, _ := strings.Cut(w[strings.Index(w, "\n  handshake:"):], "\n  claude:")
	if strings.Contains(strings.ToLower(hs), "claude") {
		t.Error("the handshake job mentions Claude; Flockdeck's own adapter has the claude job")
	}
}

// The matrix names every adapter of the manifest, and no other.
func TestCanaryMatrixIsTheManifest(t *testing.T) {
	w := workflowText(t)
	m := regexp.MustCompile(`adapter: \[([^\]]*)\]`).FindStringSubmatch(w)
	if m == nil {
		t.Fatal("no adapter matrix")
	}
	var got []string
	for _, id := range strings.Split(m[1], ",") {
		got = append(got, strings.TrimSpace(id))
	}
	if strings.Join(sortedCopy(got), ",") != strings.Join(Default().IDs(), ",") {
		t.Errorf("the matrix is %v, the manifest has %v", got, Default().IDs())
	}
	if !strings.Contains(w, "channel: [pinned, latest]") {
		t.Error("the matrix does not try both channels")
	}
	for _, a := range Default().Visible() {
		if a.Package == "" {
			t.Errorf("%s has no npm package, so the canary cannot install it", a.ID)
		}
	}
}

// The handshake job's isolation is the part to read in the log; keep it there.
func TestCanaryShowsItsIsolation(t *testing.T) {
	w := workflowText(t)
	for _, want := range []string{
		"mktemp -d \"${RUNNER_TEMP}/acp-canary.XXXXXX\"",
		"echo \"isolation folder:",
		"echo \"runner home (must not change):",
		"env -i PATH=\"$PATH\" HOME=\"$ISO/home\" npm_config_cache=\"$ISO/npm-cache\"",
		"npm install --prefix \"$ISO/adapter\" --ignore-scripts",
		"Show whether the runner home changed",
		"-tags acp_canary",
	} {
		if !strings.Contains(w, want) {
			t.Errorf("the workflow lacks %q", want)
		}
	}
}

// Every action is pinned to a commit, every job has a time limit, and the job
// that runs registry code has no build cache to poison.
func TestCanaryPinsAndLimits(t *testing.T) {
	w := workflowText(t)
	for _, m := range regexp.MustCompile(`uses: (\S+)`).FindAllStringSubmatch(w, -1) {
		if !regexp.MustCompile(`@[0-9a-f]{40}$`).MatchString(m[1]) {
			t.Errorf("%s is not pinned to a commit", m[1])
		}
	}
	if n := strings.Count(w, "timeout-minutes:"); n != 3 {
		t.Errorf("%d jobs have a time limit, want 3", n)
	}
	_, hs, _ := strings.Cut(w, "\n  handshake:")
	if strings.Contains(hs, "cache: true") || !strings.Contains(hs, "cache: false") {
		t.Error("the handshake job uses the Go build cache")
	}
	if !strings.Contains(w, "not a plain version") {
		t.Error("the version from the registry is not checked")
	}
}

// The claude job runs code from the npm registry like the handshake job does,
// so it has the same limits: no build cache, no install scripts, claude
// installed outside the runner's home from a clean environment, and a version
// that is checked. It runs the real-agent test with the switch that keeps the
// test harness from putting a stub where claude is.
func TestCanaryClaudeJob(t *testing.T) {
	w := workflowText(t)
	_, job, ok := strings.Cut(w, "\n  claude:")
	if !ok {
		t.Fatal("the workflow has no claude job")
	}
	if strings.Contains(job, "cache: true") || !strings.Contains(job, "cache: false") {
		t.Error("the claude job uses the Go build cache")
	}
	for _, want := range []string{
		"mktemp -d \"${RUNNER_TEMP}/acp-canary.XXXXXX\"",
		"@anthropic-ai/claude-code",
		"env -i PATH=\"$PATH\" HOME=\"$ISO/home\" npm_config_cache=\"$ISO/npm-cache\"",
		"npm install --prefix \"$ISO/claude\" --ignore-scripts",
		"not a plain version",
		"select(.id == \"claude\") | .testedWith",
		"go build -o \"$ISO/bin/flockdeck\" .",
		"-run TestCanaryInitialize",
		"FLOCKDECK_CANARY_ADAPTER: claude",
		"-run TestCanaryClaudeSession",
		"FLOCKDECK_LIVE_AGENT_TESTS: \"1\"",
		"Show whether the runner home changed",
	} {
		if !strings.Contains(job, want) {
			t.Errorf("the claude job lacks %q", want)
		}
	}
	if strings.Contains(job, "FLOCKDECK_CANARY_LOGIN") {
		t.Error("the claude job asks for the login mode, which needs the real home")
	}
	a, ok := Default().Lookup("claude")
	if !ok || a.Package != "" {
		t.Errorf("claude = %+v, %v", a, ok)
	}
}

func sortedCopy(s []string) []string {
	out := append([]string(nil), s...)
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}
