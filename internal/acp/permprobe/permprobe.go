// Package permprobe reads which permissions an agent will apply to its shell and
// edit tools, from the agent's own account of its resolved rules, and builds the
// settings Flockdeck launches it with so that those tools ask.
//
// It exists for opencode, which reads OPENCODE_CONFIG_CONTENT as the last of its
// settings sources and prints the rules it resolved with `opencode agent list`.
// A rule has a permission (a tool name, or * for all), a pattern and an action;
// the last rule that matches is the one that applies. The user's and the
// project's settings are among those rules. So are the permissions an agent is
// given in the project's configuration (`agent.NAME.permission`, or a file under
// `.opencode/agent`), which come after any launch setting and so win over it. The
// built-in agents' own defaults come before it, so the launch setting wins over
// those: the `plan` agent's `edit: deny` is followed by the launch setting's
// `edit: ask`. The checks here read the listing and do not assume either order.
package permprobe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// Rule is one resolved permission rule.
type Rule struct {
	Permission string `json:"permission"`
	Action     string `json:"action"`
	Pattern    string `json:"pattern"`
}

// Limits. A rule list or a value past them is not used: the caller falls back to
// the plain setting that makes the tools ask.
const (
	maxOutput = 4 << 20
	maxRules  = 400
	// MaxInline is the longest settings text built, in bytes. Environment values
	// have limits of their own, and a longer list is not worth passing on.
	MaxInline = 12 << 10
	// Timeout bounds one run of the probe.
	Timeout = 30 * time.Second
)

// execTimeout is Timeout; a test shortens it.
var execTimeout = Timeout

// Agent is one agent and the rules it resolved.
type Agent struct {
	Name, Mode string
	Rules      []Rule
}

var agentHeader = regexp.MustCompile(`^(\S+) \((\w+)\)$`)

const maxAgents = 64

// ParseAgents reads the output of the agent program's list of agents: for each
// agent a line "name (mode)" at the start of a line, then its rules as a JSON
// array. Lines before the first agent are ignored. It fails when there is no
// agent, an agent's rules are not an array of rules, or there are too many.
func ParseAgents(out []byte) ([]Agent, error) {
	text := strings.ReplaceAll(string(out), "\r", "")
	var agents []Agent
	var body []string
	flush := func() error {
		if len(agents) == 0 {
			return nil
		}
		a := &agents[len(agents)-1]
		if err := json.Unmarshal([]byte(strings.Join(body, "\n")), &a.Rules); err != nil {
			return fmt.Errorf("the rules of %q: %w", a.Name, err)
		}
		if len(a.Rules) > 20*maxRules {
			return fmt.Errorf("the rules of %q are too many", a.Name)
		}
		return nil
	}
	for _, line := range strings.Split(text, "\n") {
		if m := agentHeader.FindStringSubmatch(line); m != nil {
			if err := flush(); err != nil {
				return nil, err
			}
			if len(agents) >= maxAgents {
				return nil, errors.New("too many agents")
			}
			agents = append(agents, Agent{Name: m[1], Mode: m[2]})
			body = body[:0]
			continue
		}
		if len(agents) > 0 {
			body = append(body, line)
		}
	}
	if err := flush(); err != nil {
		return nil, err
	}
	if len(agents) == 0 {
		return nil, errors.New("no agent in the output")
	}
	return agents, nil
}

// For is the rules that apply to one tool, in order: those naming the tool and
// those naming every tool.
func For(rules []Rule, tool string) []Rule {
	var out []Rule
	for _, r := range rules {
		if r.Permission == tool || r.Permission == "*" {
			out = append(out, r)
		}
	}
	return out
}

// Unasked says the tool can run some command without asking: the last rule for
// every command allows it, or a rule for some commands allows them after the last
// rule for every command, or there is no rule for every command at all. A tool
// that is denied, or asks, is not unasked.
func Unasked(rules []Rule, tool string) bool {
	base, specific := "", false
	for _, r := range For(rules, tool) {
		if r.Pattern == "*" {
			base, specific = r.Action, false
		} else if r.Action == "allow" {
			specific = true
		}
	}
	return base != "ask" && base != "deny" || specific
}

// AnyUnasked says some tool in tools is unasked.
func AnyUnasked(rules []Rule, tools ...string) bool {
	for _, t := range tools {
		if Unasked(rules, t) {
			return true
		}
	}
	return false
}

type entry struct{ pattern, action string }

// Inline builds the settings text that makes each tool ask where it would have
// allowed, and keeps what denies and what already asks. The rules for a tool
// are kept in their order (the last match applies), an allow becomes an ask, and
// a pattern that comes again drops its earlier rule, which could never apply. It
// reports false when the rules are too many or the text too long, or a rule is
// not one it understands; the caller then uses the plain setting.
func Inline(rules []Rule, tools ...string) (string, bool) {
	var b strings.Builder
	b.WriteString(`{"permission":{`)
	for i, tool := range tools {
		var list []entry
		for _, r := range For(rules, tool) {
			act := r.Action
			switch act {
			case "allow":
				act = "ask"
			case "ask", "deny":
			default:
				return "", false
			}
			if r.Pattern == "" {
				return "", false
			}
			kept := list[:0]
			for _, e := range list {
				if e.pattern != r.Pattern {
					kept = append(kept, e)
				}
			}
			list = append(kept, entry{r.Pattern, act})
		}
		if len(list) > maxRules {
			return "", false
		}
		// A list with no rule for every command asks for the rest.
		if !has(list, "*") {
			list = append([]entry{{"*", "ask"}}, list...)
		}
		if i > 0 {
			b.WriteByte(',')
		}
		name, _ := json.Marshal(tool)
		b.Write(name)
		b.WriteByte(':')
		if len(list) == 1 && list[0].pattern == "*" {
			v, _ := json.Marshal(list[0].action)
			b.Write(v)
			continue
		}
		b.WriteByte('{')
		for j, e := range list {
			if j > 0 {
				b.WriteByte(',')
			}
			p, _ := json.Marshal(e.pattern)
			a, _ := json.Marshal(e.action)
			b.Write(p)
			b.WriteByte(':')
			b.Write(a)
		}
		b.WriteByte('}')
	}
	b.WriteString(`}}`)
	if b.Len() > MaxInline {
		return "", false
	}
	return b.String(), true
}

func has(list []entry, pattern string) bool {
	for _, e := range list {
		if e.pattern == pattern {
			return true
		}
	}
	return false
}

// Runner runs the probe: the program with args, in dir, with env as its whole
// environment, and returns what it printed. exec is the real one.
type Runner func(ctx context.Context, path string, args []string, dir string, env []string) ([]byte, error)

// Exec runs the program and returns its standard output, at most a few megabytes
// of it, within Timeout.
func Exec(ctx context.Context, path string, args []string, dir string, env []string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, execTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir = dir
	cmd.Env = env
	// A program that is killed but whose child holds the pipe open does not keep
	// the probe waiting.
	cmd.WaitDelay = 2 * time.Second
	var out limited
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s: %w", args, err)
	}
	if out.over {
		return nil, errors.New("the output is too long")
	}
	return out.buf.Bytes(), nil
}

type limited struct {
	buf  bytes.Buffer
	over bool
}

func (l *limited) Write(p []byte) (int, error) {
	if l.buf.Len()+len(p) > maxOutput {
		l.over = true
		return len(p), nil
	}
	return l.buf.Write(p)
}

// Result is what a launch learned.
type Result struct {
	// Inline is the settings text to launch with, or the plain setting.
	Inline string
	// Unasked says a tool still runs some command unasked with Inline set, in
	// some agent, or that this could not be found out.
	Unasked bool
	// Agents names the agents in which a tool is unasked, when that was found.
	Agents []string
	// Note says in a few words what happened, for a log or a test.
	Note string
}

// Check reads the agents and the rules the program resolves without any launch
// setting, builds the settings from the rules of the baseline agent (the one the
// user's and the project's own permissions reach, without an agent's own) so that
// the tools ask and the denials stay, and reads the agents again with those set
// to see whether any agent, the session's default or one a task starts, still
// allows a tool. launch is the environment without the setting, setting is the
// variable name, and fallback its plain value, used when the rules cannot be read
// or turned into settings. Every agent counts, not only the one a session starts
// in: a project can name a default agent and can give a subagent its own rules,
// and this does not try to work out which one a session will use.
func Check(ctx context.Context, run Runner, path string, args []string, dir string, launch []string, setting, fallback, baseline string, tools ...string) Result {
	with := func(value string) []string {
		out := append([]string(nil), launch...)
		return append(out, setting+"="+value)
	}
	res := Result{Inline: fallback, Unasked: true}
	agents, err := listAgents(ctx, run, path, args, dir, launch)
	var base *Agent
	if err == nil {
		for i := range agents {
			if agents[i].Name == baseline {
				base = &agents[i]
			}
		}
		if base == nil {
			err = fmt.Errorf("no agent called %q", baseline)
		}
	}
	if err != nil {
		res.Note = "the rules could not be read: " + err.Error()
		return verify(ctx, run, path, args, dir, with(fallback), res, tools)
	}
	inline, ok := Inline(base.Rules, tools...)
	if !ok {
		res.Note = "the rules could not be kept: using the plain setting"
		return verify(ctx, run, path, args, dir, with(fallback), res, tools)
	}
	res.Inline = inline
	return verify(ctx, run, path, args, dir, with(inline), res, tools)
}

func listAgents(ctx context.Context, run Runner, path string, args []string, dir string, env []string) ([]Agent, error) {
	out, err := run(ctx, path, args, dir, env)
	if err != nil {
		return nil, err
	}
	return ParseAgents(out)
}

func verify(ctx context.Context, run Runner, path string, args []string, dir string, env []string, res Result, tools []string) Result {
	agents, err := listAgents(ctx, run, path, args, dir, env)
	if err != nil {
		if res.Note == "" {
			res.Note = "the result could not be checked: " + err.Error()
		}
		return res
	}
	res.Unasked = false
	for _, a := range agents {
		if AnyUnasked(a.Rules, tools...) {
			res.Unasked = true
			res.Agents = append(res.Agents, a.Name)
		}
	}
	if res.Note == "" {
		res.Note = "checked"
		if res.Unasked {
			res.Note += ": a tool can run unasked in " + strings.Join(res.Agents, ", ")
		}
	}
	return res
}
