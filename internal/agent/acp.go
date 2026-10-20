package agent

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/jmwri/flockdeck/internal/acp/manifest"
)

// envNameRe is a variable name an entry may pass to a structured agent.
var envNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// reservedEnvPrefix is the prefix of Flockdeck's own variables, which carry its
// tokens and addresses. No entry can pass one on.
const reservedEnvPrefix = "FLOCKDECK_"

// ACPPassEnv returns the variables of environ (name=value, as os.Environ gives
// them) that the entry lists in ACP.PassEnv, and nothing else. A structured
// agent gets these on top of acp.MinimalEnv, so the parent's keys and tokens
// are never handed on by accident.
func (s Spec) ACPPassEnv(environ []string) []string {
	var out []string
	for _, kv := range environ {
		name, _, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		for _, want := range s.ACP.PassEnv {
			if strings.EqualFold(name, want) {
				out = append(out, kv)
				break
			}
		}
	}
	return out
}

// ACPAdapter returns the manifest entry a structured Spec starts.
func (s Spec) ACPAdapter() (manifest.Adapter, bool) {
	if s.Runner != RunnerACP {
		return manifest.Adapter{}, false
	}
	return manifest.Default().Lookup(s.ACP.Adapter)
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
	var keep []string
	for _, name := range s.ACP.PassEnv {
		switch {
		case !envNameRe.MatchString(name):
			problems = append(problems, fmt.Sprintf("agent %q: %q is not a variable name; it is not passed", s.ID, name))
		case strings.HasPrefix(strings.ToUpper(name), reservedEnvPrefix):
			problems = append(problems, fmt.Sprintf("agent %q: %s variables are never passed to an agent; %q is dropped", s.ID, strings.TrimSuffix(reservedEnvPrefix, "_"), name))
		default:
			keep = append(keep, name)
		}
	}
	s.ACP.PassEnv = keep
	return problems, true
}
