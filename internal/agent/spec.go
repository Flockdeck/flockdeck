// Package agent describes the coding agents Flockdeck can run in a pane.
//
// A pane is either a shell or an agent, and an agent is a Spec -- a program to
// run, or an API to talk to -- together with the model it was asked for. Every
// Claude-specific decision Flockdeck once made in line is a field here, so that a
// second agent is a table entry rather than another branch.
package agent

import (
	"regexp"
	"strings"
)

// Runner is how a Spec is started.
type Runner string

const (
	// RunnerCLI runs an external command in the pane's pseudo-terminal.
	RunnerCLI Runner = "cli"
	// RunnerAPI runs Flockdeck's own chat client in the pane, talking directly to
	// a model API. The command is `flockdeck chat`; the Spec says which endpoint.
	RunnerAPI Runner = "api"
)

// ContextMode is how a pane's briefing -- which pane it is, who else is
// working -- reaches the agent running in it.
type ContextMode string

const (
	// ContextHook answers the agent's own session-start hook with the
	// briefing, which is the only way it survives a compaction.
	ContextHook ContextMode = "hook"
	// ContextPrompt puts the briefing in front of the opening prompt, for an
	// agent with no hooks to answer.
	ContextPrompt ContextMode = "prompt"
	// ContextNone leaves the agent unbriefed.
	ContextNone ContextMode = "none"
)

// Model is one model a Spec can be asked for.
type Model struct {
	ID   string `json:"id"`             // what the CLI or the API is given
	Name string `json:"name,omitempty"` // what the picker shows
	Note string `json:"note,omitempty"` // a few words on when to reach for it
}

// Caps says which of Flockdeck's facilities an agent supports. Everything Flockdeck
// does beyond drawing a terminal is gated on one of these, so an agent that
// supports none of it still works -- it is a terminal with a program in it.
type Caps struct {
	// Hooks reports lifecycle events to Flockdeck, so the pane's status is known
	// rather than inferred from what it prints.
	Hooks bool `json:"hooks,omitempty"`
	// Resume reattaches a conversation by id, which is what makes restoring a
	// layout more than cosmetic.
	Resume bool `json:"resume,omitempty"`
	// Transcript records what was said somewhere Flockdeck can read it: where a
	// fan-out finds a plan, and the history overlay finds a conversation.
	Transcript bool `json:"transcript,omitempty"`
	// Trust has a per-directory trust question Flockdeck can answer ahead of a
	// fan-out, so that twelve fresh worktrees do not each stop on it.
	Trust bool `json:"trust,omitempty"`
	// Context is how the pane briefing reaches the agent.
	Context ContextMode `json:"context,omitempty"`
}

// Arg is one entry in a Spec's argument list: a literal, or a group used only
// when a token has a value.
//
// The group is what keeps an option with its value: dropping "{{model}}" alone
// from ["--model", "{{model}}"] would leave the flag behind to swallow the
// next argument as its value.
type Arg struct {
	// Value is one argument, with tokens expanded. Empty when Args is set.
	Value string `json:"value,omitempty"`
	// If names a token; the group is used only when that token has a value.
	If string `json:"if,omitempty"`
	// Args is the group.
	Args []Arg `json:"args,omitempty"`
}

// APISpec describes an HTTP model API for the built-in chat client.
type APISpec struct {
	// Wire is the request shape: "anthropic", "openai" or "gemini". An
	// OpenAI-compatible endpoint -- Ollama, LM Studio, vLLM, a gateway -- is
	// "openai" with a BaseURL of its own.
	Wire string `json:"wire,omitempty"`
	// BaseURL is the endpoint root; empty means the vendor's own.
	BaseURL string `json:"baseURL,omitempty"`
	// KeyEnv names the environment variables a key may arrive in, tried in
	// order before Flockdeck's own key store.
	KeyEnv []string `json:"keyEnv,omitempty"`
}

// Patterns recognise, in what an agent prints, the two states its own
// lifecycle would have reported. They are used only when Caps.Hooks is false.
type Patterns struct {
	// Waiting matches a line that means the agent is blocked on the user: a
	// permission question, a confirmation, an editor prompt.
	Waiting []string `json:"waiting,omitempty"`
	// Idle matches a line that means it has finished and is at its prompt.
	Idle []string `json:"idle,omitempty"`
}

// Spec is one agent Flockdeck can run.
type Spec struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Runner Runner `json:"runner,omitempty"`
	// Exe is the command to look for on PATH, for a CLI runner.
	Exe string `json:"exe,omitempty"`
	// Args is the argument list; ResumeArgs replaces it when a conversation is
	// being reattached rather than started.
	Args       []Arg `json:"args,omitempty"`
	ResumeArgs []Arg `json:"resumeArgs,omitempty"`
	// Models are what the picker offers. DefaultModel is taken when nothing is
	// chosen; empty leaves the choice to the agent, which is how a CLI keeps
	// whatever it was configured with.
	Models       []Model `json:"models,omitempty"`
	DefaultModel string  `json:"defaultModel,omitempty"`
	// Env is added to the pane's environment; StripEnv is removed from it,
	// which is how the markers of the session Flockdeck was launched from are kept
	// from making every pane look like a nested child of it.
	Env      []string `json:"env,omitempty"`
	StripEnv []string `json:"stripEnv,omitempty"`
	API      APISpec  `json:"api,omitempty"`
	Caps     Caps     `json:"caps,omitempty"`
	Patterns Patterns `json:"patterns,omitempty"`
	// Install is what to tell somebody who picked an agent their machine does
	// not have.
	Install string `json:"install,omitempty"`
	// NoDashDash suppresses the "--" that otherwise guards an opening prompt
	// beginning with a dash.
	NoDashDash bool `json:"noDashDash,omitempty"`
	// Hidden keeps a built-in out of the picker without removing it.
	Hidden bool `json:"hidden,omitempty"`
}

// Tokens are the values an argument list may refer to. A token with no value
// removes the group it appears in.
type Tokens struct {
	Session  string // the pane id, which is also the conversation id
	Model    string
	Settings string // a generated settings file, for an agent that takes one
	Prompt   string // the opening task; empty when there is none
	Cwd      string
	Pane     string
}

// Value returns the token named by an `if`, or by "{{name}}".
func (t Tokens) Value(name string) string {
	switch strings.TrimSpace(strings.Trim(name, "{}")) {
	case "session":
		return t.Session
	case "model":
		return t.Model
	case "settings":
		return t.Settings
	case "prompt":
		return t.Prompt
	case "cwd":
		return t.Cwd
	case "pane":
		return t.Pane
	}
	return ""
}

// Expand replaces every {{token}} in s with its value.
//
// It is one pass, so a value is never read for tokens of its own. Replacing
// them one name after another did exactly that to the opening task, which
// comes before "cwd" and "pane": a task about a Handlebars template reached
// the agent with its "{{pane}}" swapped for the pane's id.
//
// Space inside the braces is allowed, as Value already allowed it in an `if`:
// "{{ model }}" is how anyone used to a template language writes it, and it
// went through untouched -- the CLI was handed a model called "{{ model }}",
// and a task written "{{ prompt }}" never reached the agent at all.
func (t Tokens) Expand(s string) string {
	return tokenPattern.ReplaceAllStringFunc(s, func(tok string) string {
		return t.Value(tok)
	})
}

// tokenPattern matches one token, space inside its braces or not.
var tokenPattern = regexp.MustCompile(`\{\{\s*(session|model|settings|prompt|cwd|pane)\s*\}\}`)

// onlyToken names the token s consists of, or "" when it is anything else.
func onlyToken(s string) string {
	if m := tokenPattern.FindStringSubmatch(s); m != nil && m[0] == s {
		return m[1]
	}
	return ""
}

// BuildArgv turns a Spec into the argv for one pane. The first element is the
// program; resolving it on PATH is the caller's job.
//
// An argument that is exactly "{{prompt}}" and expands to something beginning
// with a dash is preceded by "--", because the opening task is free to begin
// with one -- "-p is not what I meant, use ..." -- and without it the CLI reads
// the task as an option it does not have and the pane dies on the spot.
func BuildArgv(s Spec, resume bool, t Tokens) []string {
	args := s.Args
	if resume && len(s.ResumeArgs) > 0 {
		args = s.ResumeArgs
	}
	out := make([]string, 0, len(args)+2)
	// An API runner is Flockdeck's own chat client: the caller puts its binary and
	// the "chat" subcommand in front, because only the caller knows where the
	// running binary lives.
	if s.Runner != RunnerAPI && s.Exe != "" {
		out = append(out, s.Exe)
	}
	return appendArgs(out, args, s, t)
}

func appendArgs(out []string, args []Arg, s Spec, t Tokens) []string {
	for _, a := range args {
		if a.If != "" && t.Value(a.If) == "" {
			continue
		}
		if len(a.Args) > 0 {
			out = appendArgs(out, a.Args, s, t)
			continue
		}
		if a.Value == "" {
			continue
		}
		v := t.Expand(a.Value)
		if v == "" {
			continue
		}
		if onlyToken(a.Value) == "prompt" && !s.NoDashDash && strings.HasPrefix(v, "-") {
			out = append(out, "--")
		}
		out = append(out, v)
	}
	return out
}

// Lit is one literal argument, and Group a set of them used only when a token
// has a value. They are shorthand for writing a catalog by hand.
func Lit(v string) Arg { return Arg{Value: v} }

func Group(token string, values ...string) Arg {
	g := Arg{If: token}
	for _, v := range values {
		g.Args = append(g.Args, Arg{Value: v})
	}
	return g
}
