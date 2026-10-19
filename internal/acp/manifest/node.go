package manifest

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
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
		return fail(NodeBroken, "", clipText(err.Error(), 200))
	}
	have, err := ParseVersion(out)
	if err != nil {
		return fail(NodeBroken, "", clipText(err.Error(), 200))
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
