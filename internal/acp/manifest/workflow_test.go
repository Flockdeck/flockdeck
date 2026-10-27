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
	if strings.Contains(strings.ToLower(w), "claude") {
		t.Error("the canary mentions Claude; there is no Claude adapter")
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
	if n := strings.Count(w, "timeout-minutes:"); n != 2 {
		t.Errorf("%d jobs have a time limit, want 2", n)
	}
	_, hs, _ := strings.Cut(w, "\n  handshake:")
	if strings.Contains(hs, "cache: true") || !strings.Contains(hs, "cache: false") {
		t.Error("the handshake job uses the Go build cache")
	}
	if !strings.Contains(w, "not a plain version") {
		t.Error("the version from the registry is not checked")
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
