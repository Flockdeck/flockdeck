package agent

import (
	"fmt"
	"regexp"
	"runtime"
	"strings"
	"unicode"

	"github.com/jmwri/flockdeck/internal/acp/manifest"
)

// envNameRe is a variable name an entry may pass to a structured agent.
var envNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// reservedEnvPrefixes are the prefixes of Flockdeck's own variables, which carry
// its tokens and addresses and the pane's identity (the same values are set
// under both). No entry can pass one on, whatever the case of its name.
var reservedEnvPrefixes = []string{"FLOCKDECK_", "PERCH_"}

func reservedEnv(name string) bool {
	up := strings.ToUpper(name)
	for _, p := range reservedEnvPrefixes {
		if strings.HasPrefix(up, p) {
			return true
		}
	}
	return false
}

// minimalEnvNames are the variables acp.MinimalEnv already gives every agent,
// so passing them again would put them in the environment twice. A test keeps
// the list equal to MinimalEnv's.
var minimalEnvNames = []string{"PATH", "SYSTEMROOT", "SystemRoot", "SYSTEMDRIVE", "COMSPEC", "PATHEXT", "TEMP", "TMP", "TMPDIR", "LANG", "LC_ALL"}

// maxPassEnv is the most names an entry may list.
const maxPassEnv = 64

// maxLogin is the longest login advice kept.
const maxLogin = 300

// sameEnvName compares two variable names the way the platform does: Windows
// ignores case, and Linux and macOS do not.
func sameEnvName(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// cleanLine removes every control character and every Unicode format character
// (bidi overrides, zero-width marks) from s, so that advice an entry carries
// cannot move the cursor, ring a bell or reorder the text where it is shown, and
// cuts it at n runes.
func cleanLine(s string, n int) string {
	s = strings.Map(func(r rune) rune {
		if r == ' ' || !unicode.IsControl(r) && !unicode.Is(unicode.Cf, r) {
			return r
		}
		return -1
	}, strings.ToValidUTF8(s, ""))
	if r := []rune(s); len(r) > n {
		s = string(r[:n])
	}
	return strings.TrimSpace(s)
}

// ACPPassEnv returns the variables of environ (name=value, as os.Environ gives
// them) that the entry lists in ACP.PassEnv, and nothing else. A structured
// agent gets these on top of acp.MinimalEnv, so the parent's keys and tokens
// are never handed on by accident.
//
// This is the function that decides what leaves the process, so it enforces the
// rules itself and does not rely on the catalog having checked the entry: a name
// with a reserved prefix is never passed, a name MinimalEnv already gives is
// skipped, and at most maxPassEnv names are read.
func (s Spec) ACPPassEnv(environ []string) []string {
	want := s.ACP.PassEnv
	if len(want) > maxPassEnv {
		want = want[:maxPassEnv]
	}
	var out []string
	for _, kv := range environ {
		name, _, ok := strings.Cut(kv, "=")
		if !ok || reservedEnv(name) || inMinimalEnv(name) {
			continue
		}
		for _, w := range want {
			if !reservedEnv(w) && sameEnvName(name, w) {
				out = append(out, kv)
				break
			}
		}
	}
	return out
}

func inMinimalEnv(name string) bool {
	for _, m := range minimalEnvNames {
		if sameEnvName(name, m) {
			return true
		}
	}
	return false
}

// ACPAdapter returns the manifest entry a structured Spec starts.
func (s Spec) ACPAdapter() (manifest.Adapter, bool) {
	if s.Runner != RunnerACP {
		return manifest.Adapter{}, false
	}
	return manifest.Default().Lookup(s.ACP.Adapter)
}

// ACPLaunchEnv is the environment the manifest says Flockdeck sets for the
// adapter this Spec starts, as name=value. It is added after ACPPassEnv and wins
// over it: a name the user's environment also passes is set to this value.
func (s Spec) ACPLaunchEnv() []string {
	a, ok := s.ACPAdapter()
	if !ok {
		return nil
	}
	return a.LaunchEnvList()
}

// ACPPermissionProbe is the check the manifest says to run when this Spec's
// agent starts, if it names one.
func (s Spec) ACPPermissionProbe() (manifest.PermissionProbe, bool) {
	a, ok := s.ACPAdapter()
	if !ok || a.PermissionProbe == nil {
		return manifest.PermissionProbe{}, false
	}
	return *a.PermissionProbe, true
}

// RunsWithoutAsking says the manifest records that the agent this Spec starts
// runs commands and edits without sending a permission request.
func (s Spec) RunsWithoutAsking() bool {
	a, ok := s.ACPAdapter()
	return ok && a.RunsWithoutAsking
}

// checkACP reports what is wrong with a structured entry's "acp" block. An
// entry that is not structured has nothing to check. ok is false when the
// entry cannot be used at all, which is when it names no adapter the manifest
// has; a bad environment name is dropped with a notice and the rest stays.
func checkACP(s *Spec) (problems []string, ok bool) {
	if s.Runner != RunnerACP {
		return nil, true
	}
	if _, found := manifest.Default().Lookup(s.ACP.Adapter); !found {
		if s.ACP.Adapter == "" {
			return []string{fmt.Sprintf("agent %q: a structured agent needs \"acp\": {\"adapter\": ...}, naming an entry of the adapter manifest", s.ID)}, false
		}
		return []string{fmt.Sprintf("agent %q: %q is not an adapter this build knows (it knows %s)", s.ID, s.ACP.Adapter, strings.Join(manifest.Default().IDs(), ", "))}, false
	}
	switch strings.ToLower(strings.TrimSpace(s.ACP.Auth)) {
	case "", AuthExternal:
		s.ACP.Auth = AuthExternal
	case AuthNone:
		s.ACP.Auth = AuthNone
	default:
		problems = append(problems, fmt.Sprintf("agent %q: acp auth %q is neither %q nor %q; %q is used", s.ID, s.ACP.Auth, AuthNone, AuthExternal, AuthExternal))
		s.ACP.Auth = AuthExternal
	}
	if login := cleanLine(s.ACP.Login, maxLogin); login != s.ACP.Login {
		problems = append(problems, fmt.Sprintf("agent %q: acp login was cleaned of control characters or cut at %d characters", s.ID, maxLogin))
		s.ACP.Login = login
	}
	var keep []string
	for _, name := range s.ACP.PassEnv {
		switch {
		case !envNameRe.MatchString(name):
			problems = append(problems, fmt.Sprintf("agent %q: %q is not a variable name; it is not passed", s.ID, cleanLine(name, 40)))
		case reservedEnv(name):
			problems = append(problems, fmt.Sprintf("agent %q: variables starting FLOCKDECK_ or PERCH_ are never passed to an agent; %q is dropped", s.ID, name))
		case len(keep) >= maxPassEnv:
			problems = append(problems, fmt.Sprintf("agent %q: passEnv takes at most %d names; %q is dropped", s.ID, maxPassEnv, name))
		default:
			keep = append(keep, name)
		}
	}
	s.ACP.PassEnv = keep
	return problems, true
}
