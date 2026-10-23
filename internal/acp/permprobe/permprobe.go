// Package permprobe reads which permissions an agent will apply to its shell and
// edit tools, from the agent's own account of its resolved rules, and builds the
// settings Flockdeck launches it with so that those tools ask.
//
// It exists for opencode, which reads OPENCODE_CONFIG_CONTENT as the last of its
// settings sources and prints the rules it resolved with `opencode debug agent
// build`. A rule has a permission (a tool name, or * for all), a pattern and an
// action; the last rule that matches is the one that applies. The user's and the
// project's settings are among those rules, and so are an agent's own permission
// settings in the project, which come after any launch setting and so win over it.
package permprobe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
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

// ParseRules reads the output of the agent's `debug agent` command: the agent
// as JSON, perhaps after some lines of text.
func ParseRules(out []byte) ([]Rule, error) {
	i := bytes.IndexByte(out, '{')
	if i < 0 {
		return nil, errors.New("no agent in the output")
	}
	var agent struct {
		Permission []Rule `json:"permission"`
	}
	dec := json.NewDecoder(bytes.NewReader(out[i:]))
	if err := dec.Decode(&agent); err != nil {
		return nil, err
	}
	if len(agent.Permission) == 0 {
		return nil, errors.New("the agent has no permission rules")
	}
	if len(agent.Permission) > 20*maxRules {
		return nil, errors.New("too many permission rules")
	}
	return agent.Permission, nil
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
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir = dir
	cmd.Env = env
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
	// Inline is the settings text to launch with, or "" for the plain setting.
	Inline string
	// Unasked says a tool still runs some command unasked with Inline set, or
	// that this could not be found out.
	Unasked bool
	// Note says in a few words what happened, for a log or a test.
	Note string
}

// Check reads the rules the agent resolves without any launch setting, builds the
// settings that keep the denials and ask where it would allow, and reads the
// rules again with those set to see whether a tool still allows. launch is the
// environment without the setting, setting is the variable name, and fallback its
// plain value, used when the rules cannot be read or turned into settings.
func Check(ctx context.Context, run Runner, path string, args []string, dir string, launch []string, setting, fallback string, tools ...string) Result {
	with := func(value string) []string {
		out := append([]string(nil), launch...)
		return append(out, setting+"="+value)
	}
	res := Result{Inline: fallback, Unasked: true}
	out, err := run(ctx, path, args, dir, launch)
	var rules []Rule
	if err == nil {
		rules, err = ParseRules(out)
	}
	if err != nil {
		res.Note = "the rules could not be read: " + err.Error()
		return verify(ctx, run, path, args, dir, with(fallback), res, tools)
	}
	inline, ok := Inline(rules, tools...)
	if !ok {
		res.Note = "the rules could not be kept: using the plain setting"
		return verify(ctx, run, path, args, dir, with(fallback), res, tools)
	}
	res.Inline = inline
	return verify(ctx, run, path, args, dir, with(inline), res, tools)
}

func verify(ctx context.Context, run Runner, path string, args []string, dir string, env []string, res Result, tools []string) Result {
	out, err := run(ctx, path, args, dir, env)
	var rules []Rule
	if err == nil {
		rules, err = ParseRules(out)
	}
	if err != nil {
		if res.Note == "" {
			res.Note = "the result could not be checked: " + err.Error()
		}
		return res
	}
	res.Unasked = AnyUnasked(rules, tools...)
	if res.Note == "" {
		res.Note = "checked"
	}
	return res
}
