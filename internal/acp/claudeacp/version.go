package claudeacp

import (
	"context"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/jmwri/flockdeck/internal/acp/manifest"
	"github.com/jmwri/flockdeck/internal/sysproc"
)

// The adapter drives claude's stream-json control protocol, which claude does not
// document and can change in any release. The adapter is tested against one
// version, the claude entry's testedWith in the adapter manifest, and treats the
// whole major.minor series of that version as tested. Any other series still
// runs; the pane is told once, with a fixed notice, that it was not tested.
// docs/acp-claude-adapter.md says how the pin is moved.

// versionTimeout bounds `claude --version`.
const versionTimeout = 10 * time.Second

// versionCap bounds what is read from it.
const versionCap = 4 << 10

// testedSeries is the major.minor of the manifest's testedWith for claude, such
// as "2.1". Empty when the manifest has none.
func testedSeries() string {
	a, ok := manifest.Default().Lookup("claude")
	if !ok {
		return ""
	}
	return series(a.TestedWith)
}

// series is the "major.minor" of the first version in s, or "".
func series(s string) string {
	v, err := manifest.ParseVersion(s)
	if err != nil {
		return ""
	}
	return strconv.Itoa(v.Major) + "." + strconv.Itoa(v.Minor)
}

// checkVersion runs `claude --version` and reports whether its series is the
// tested one. A claude that does not answer, or answers with no version, is
// untested: the notice says so and the session starts anyway, since a claude
// that cannot run is found out by the handshake.
func (s *server) checkVersion() (tested bool, version string) {
	want := testedSeries()
	ctx, cancel := context.WithTimeout(context.Background(), versionTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, s.opts.ClaudePath, "--version")
	cmd.Env = s.opts.Env
	sysproc.NoWindow(cmd)
	out, err := cmd.StdoutPipe()
	if err != nil {
		s.logf("claude --version: %v", err)
		return false, ""
	}
	if err := cmd.Start(); err != nil {
		s.logf("claude --version: %v", err)
		return false, ""
	}
	b, _ := io.ReadAll(io.LimitReader(out, versionCap))
	_, _ = io.Copy(io.Discard, out)
	if err := cmd.Wait(); err != nil {
		s.logf("claude --version: %v", err)
		return false, ""
	}
	text := strings.TrimSpace(string(b))
	got := series(text)
	if got == "" || want == "" || got != want {
		s.logf("claude version %q is outside the tested series %q", clip(text, 80), want)
		return false, clip(text, 80)
	}
	return true, clip(text, 80)
}
