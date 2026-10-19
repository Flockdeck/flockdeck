package manifest

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
	"unicode/utf8"
)

// Problems a NodeRequiredError reports.
const (
	NodeMissing = "missing"
	NodeTooOld  = "too old"
	NodeBroken  = "unusable"
)

// NodeRequiredError says an adapter needs Node and it is not usable. The
// caller shows it as a needs-you state called "Node required" with Fix as the
// text; nothing is installed for the user.
type NodeRequiredError struct {
	Adapter string // the adapter's id
	Name    string // the adapter's display name
	Problem string // NodeMissing, NodeTooOld or NodeBroken
	Found   string // the version found, for NodeTooOld
	Need    string // the oldest version that works
	Detail  string // what went wrong, for NodeBroken
	Fix     string // what the person can do about it
}

func (e *NodeRequiredError) Error() string {
	switch e.Problem {
	case NodeTooOld:
		return fmt.Sprintf("%s needs Node.js %s or newer; found %s", e.Name, e.Need, e.Found)
	case NodeBroken:
		return fmt.Sprintf("%s needs Node.js %s or newer; node did not run: %s", e.Name, e.Need, e.Detail)
	}
	return fmt.Sprintf("%s needs Node.js %s or newer; node was not found on PATH", e.Name, e.Need)
}

// Probe is how the checks look at the machine; tests replace it.
type Probe struct {
	// LookPath finds a program on PATH, like exec.LookPath.
	LookPath func(name string) (string, error)
	// Output runs a program given by its full path and returns what it printed.
	Output func(ctx context.Context, path string, args ...string) (string, error)
}

// SystemProbe looks at the real machine. Programs are run by full path, for a
// few seconds, with only PATH in the environment.
func SystemProbe() Probe {
	return Probe{
		LookPath: exec.LookPath,
		Output: func(ctx context.Context, path string, args ...string) (string, error) {
			ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, path, args...)
			cmd.Env = minimalEnv()
			out, err := cmd.Output()
			return string(out), err
		},
	}
}

// CheckNode returns nil if the adapter needs no Node, or Node is there and new
// enough, and a *NodeRequiredError if not.
func CheckNode(ctx context.Context, a Adapter, p Probe) error {
	if !a.NeedsNode {
		return nil
	}
	fail := func(problem, found, detail string) error {
		return &NodeRequiredError{Adapter: a.ID, Name: a.Name, Problem: problem, Found: found, Need: a.MinNode, Detail: detail, Fix: nodeFix(a.MinNode)}
	}
	path, err := p.LookPath("node")
	if err != nil || path == "" {
		return fail(NodeMissing, "", "")
	}
	out, err := p.Output(ctx, path, "--version")
	if err != nil {
		return fail(NodeBroken, "", safeText(err.Error(), 200))
	}
	have, err := ParseVersion(out)
	if err != nil {
		return fail(NodeBroken, "", safeText(err.Error(), 200))
	}
	need, _ := ParseVersion(a.MinNode)
	if Compare(have, need) < 0 {
		return fail(NodeTooOld, have.String(), "")
	}
	return nil
}

func nodeFix(min string) string {
	major := min
	if i := strings.Index(min, "."); i > 0 {
		major = min[:i]
	}
	return "Install Node.js " + major + " or newer from https://nodejs.org (Flockdeck does not include it), then restart Flockdeck."
}

// minimalEnv is what a probe or an adapter check runs with: PATH, and on
// Windows the few variables a program cannot start without.
func minimalEnv() []string {
	return minimalEnvFrom(osEnviron())
}

// maxProbeOutput is all of a probe's output that is kept. `node --version`
// prints a few bytes; a program that prints more is not node.
const maxProbeOutput = 4 << 10

// capBuffer keeps the first maxProbeOutput bytes written to it and drops the
// rest, so a program on PATH named node cannot fill memory.
type capBuffer struct{ b []byte }

func (c *capBuffer) Write(p []byte) (int, error) {
	if room := maxProbeOutput - len(c.b); room > 0 {
		if len(p) < room {
			room = len(p)
		}
		c.b = append(c.b, p[:room]...)
	}
	return len(p), nil
}

func (c *capBuffer) String() string { return string(c.b) }

// safeText is s for showing to a person: control characters (an escape
// sequence from a hostile program) become spaces, and it is cut to n bytes at
// a character boundary.
func safeText(s string, n int) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) || r == utf8.RuneError {
			return ' '
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		s = s[:n]
		for len(s) > 0 && !utf8.ValidString(s) {
			s = s[:len(s)-1]
		}
		s += "..."
	}
	return s
}
