# Running any agent, not only Claude

> A historical design record from the multi-agent work; the code and README
> are current where they differ.

This is the contract for the work that makes Flockdeck orchestrate **any coding
agent** — any CLI, and any model API spoken to directly — with the model chosen
per pane, including choosing between Claude's own models.

It exists because that work is done by a dozen agents at once, each in its own
worktree, none of which can see the others. Everything two of them must agree
on is written down here. Read the sections your task names, and take the
wording here as decided: an improvement to the contract is worth less than
twelve branches that merge.

---

## 1. What is being built

Today a pane is `KindClaude` or `KindShell`, and every decision that follows —
what to run, how status is known, where the transcript is, whether resuming is
possible, how the pane briefing is delivered — is the `claude` CLI's answer,
written in line. Afterwards:

- A pane is a **shell** or an **agent**.
- An agent pane is an **`agent.Spec`** (which program, or which API) plus a
  **model id**.
- Every Claude-specific decision becomes a field on the Spec, so a second agent
  is a table entry and, for the user, a JSON file — not a Go branch.
- Two runners exist: `cli` runs an external command in the pane's PTY;
  `api` runs Flockdeck's own chat client, `flockdeck chat`, in the pane's PTY, talking
  straight to a model API. The second is what "native" means here: no wrapper
  CLI, no node, no Python — one binary.

Nothing about the terminal, the layout, the worktrees or the window changes.
The seam is where a pane is started and what Flockdeck believes about it after.

### Terminology, used consistently everywhere

| Word | Means |
| --- | --- |
| agent | a Spec: a program to run or an API to talk to |
| model | the model id an agent was asked for; may be empty ("whatever it is set to") |
| runner | `cli` or `api` |
| pane kind | `agent` or `shell` — no longer `claude` |
| catalog | the built-in Specs merged with the user's `agents.json` |

---

## 2. Who owns which files

Edit only the files your task owns. Where you need a change in a file another
task owns, code against the contract below and leave the other file alone —
the merge will supply it. If that leaves your branch not building, add the
smallest possible shim in a file **you** own and say so in your commit message.

| Task | Owns |
| --- | --- |
| 1 foundation | `internal/agent/**` |
| 2 session | `internal/session/{session,claude,launch,status,trust,stream}.go` + their tests |
| 3 workspace/store | `internal/workspace/{workspace,persist}.go`, `internal/store/store.go`, `cmd/statedump` |
| 4 server + UI | `internal/server/{control,agents,server}.go`, `internal/webui/assets/**` outside the fan-out dialog, `internal/webui/webui_test.go` |
| 5 fan-out | `internal/workspace/fanout.go`, `internal/server/fanout.go`, the fan-out dialog functions in `app.js` |
| 6 transcripts | `internal/session/{history,replies}.go`, `internal/session/transcript/**`, `internal/server/history.go` |
| 7 briefing | `internal/workspace/context.go`, `internal/hooks/hooks.go` |
| 8 chat runtime | `internal/chat/**`, `cli_chat.go` |
| 9 chat tools | `internal/chat/tool/**` |
| 10 credentials | `internal/creds/**`, `cli_keys.go`, the keys overlay in `app.js` |
| 11 CLI | `main.go`, `main_test.go` |
| 12 docs | `README.md`, `internal/help/pages/**`, `internal/help/keys.go` |

`design/multi-agent.md` is frozen. Nobody edits it.

Every task adds `internal/agent/spec.go` **verbatim from section 3** if it is
not already present. Identical additions merge without conflict; a reworded
copy does not. Copy it out of this file rather than retyping it.

---

## 3. The contract: `internal/agent/spec.go`

```go
// Package agent describes the coding agents Flockdeck can run in a pane.
//
// A pane is either a shell or an agent, and an agent is a Spec -- a program to
// run, or an API to talk to -- together with the model it was asked for. Every
// Claude-specific decision Flockdeck once made in line is a field here, so that a
// second agent is a table entry rather than another branch.
package agent

import "strings"

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
	switch strings.Trim(name, "{} ") {
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
func (t Tokens) Expand(s string) string {
	for _, name := range []string{"session", "model", "settings", "prompt", "cwd", "pane"} {
		s = strings.ReplaceAll(s, "{{"+name+"}}", t.Value(name))
	}
	return s
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
		if a.Value == "{{prompt}}" && !s.NoDashDash && strings.HasPrefix(v, "-") {
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
```

The Claude entry expressed in it, which must keep producing exactly the argv
`session.ClaudeArgs` produces today:

```go
Spec{
	ID: "claude", Name: "Claude Code", Runner: RunnerCLI, Exe: "claude",
	Args: []Arg{
		Group("session", "--session-id", "{{session}}"),
		Group("settings", "--settings", "{{settings}}"),
		Group("model", "--model", "{{model}}"),
		Lit("{{prompt}}"),
	},
	ResumeArgs: []Arg{
		Group("session", "--resume", "{{session}}"),
		Group("settings", "--settings", "{{settings}}"),
		Group("model", "--model", "{{model}}"),
	},
	Caps: Caps{Hooks: true, Resume: true, Transcript: true, Trust: true, Context: ContextHook},
	Models: []Model{
		{ID: "", Name: "Default", Note: "whatever the CLI is set to"},
		{ID: "opus", Name: "Opus", Note: "most capable"},
		{ID: "sonnet", Name: "Sonnet", Note: "the everyday one"},
		{ID: "haiku", Name: "Haiku", Note: "fastest"},
	},
	StripEnv: []string{"CLAUDECODE", "CLAUDE_CODE_CHILD_SESSION", "CLAUDE_CODE_ENTRYPOINT",
		"CLAUDE_CODE_SESSION_ID", "CLAUDE_CODE_SSE_PORT", "CLAUDE_CODE_DONT_INHERIT_ENV"},
	Install: "https://claude.com/claude-code",
}
```

---

## 4. The catalog and `agents.json`

The catalog is the built-in Specs, overlaid by the user's file at
`<state dir>/agents.json` (`store.Dir()`), matched by `id`:

```json
{
  "version": 1,
  "defaults": { "agent": "claude", "model": "" },
  "projects": { "C:\\code\\api": { "agent": "codex", "model": "gpt-5" } },
  "agents": [
    { "id": "claude", "defaultModel": "sonnet" },
    { "id": "local", "name": "Local llama", "runner": "api",
      "api": { "wire": "openai", "baseURL": "http://127.0.0.1:11434/v1" },
      "models": [{ "id": "qwen3-coder" }] }
  ]
}
```

Rules: a user entry with a known `id` is merged field by field over the
built-in (a set field wins, an absent one keeps the built-in's); an unknown
`id` is a new agent; `"hidden": true` removes one from the picker. A malformed
file is a notice in the interface and the built-ins carry on — never a failure
to start. Flockdeck's own settings writer is the only thing that writes it, and it
is read fresh whenever the picker is opened, so editing it by hand takes effect
without a restart.

**Built-in agents (v1).** `claude` as above. Then `codex`, `gemini`, `aider`,
`opencode` and `cursor-agent` as CLI runners, and `anthropic`, `openai`,
`google` and `openai-compatible` as API runners.

For any CLI other than `claude`, **verify the flags against the tool itself**
(`<exe> --help`) before writing the entry. Where the tool is not installed and
cannot be checked, write the entry with a comment saying so, give it `Caps{}` —
no hooks, no resume, no transcript — and cover it with a test that asserts only
that it parses and builds a plausible argv. Do not invent a resume flag or a
transcript path. An entry that is wrong is worse than an entry that is modest:
an agent with empty Caps still works perfectly well as a terminal with a
program in it, and its capabilities can be filled in later by anyone who has it
installed.

**Availability.** `Available(spec)` is: for a CLI, `exec.LookPath(Exe)`
succeeds; for an API, a key resolves (section 10) or the endpoint needs none (a
`baseURL` on loopback). Probed at startup and re-probed when the picker opens,
cached for a few seconds. Unavailable agents are shown greyed with their
`Install` line rather than hidden — somebody who has not installed Codex should
still learn that Flockdeck would run it.

---

## 5. Starting a pane

`workspace.startPane` becomes: resolve Spec and model → build tokens → for
`Caps.Hooks`, write the settings file as today → `BuildArgv` → for
`RunnerAPI`, prepend Flockdeck's own executable and `chat` → `session.Start`.

- Resume is attempted only when `Caps.Resume` **and** the agent's own
  transcript exists (section 6). Otherwise the pane starts fresh, exactly as a
  Claude pane with no transcript does today.
- The opening prompt goes in the argv (`{{prompt}}`) and is never typed into
  the terminal, because typing into a TUI means guessing when it is ready.
- Environment: `session.Env` keeps stripping the inherited-session markers, but
  the list is the union of `Spec.StripEnv` across the whole catalog, so a
  Claude marker is stripped for a Codex pane too — a pane is a clean top-level
  session whatever is running in it. Then `Spec.Env`, then the `FLOCKDECK_*` pane
  variables already there, plus `FLOCKDECK_AGENT` and `FLOCKDECK_MODEL`.

## 6. Status, transcripts and resume without hooks

**Status.** `Caps.Hooks` chooses between two paths that already exist in
`session`: hook events when it is true, the bell-and-quiet-timer fallback when
it is false. Sharpen that fallback with `Patterns`: a line matching
`Patterns.Waiting` in recent output means waiting on the user, one matching
`Patterns.Idle` means idle. Match against the last few hundred bytes with ANSI
sequences stripped, never against the whole ring, and let a hook event always
beat a pattern.

**Transcripts.** Reading what an agent said goes behind an interface rather
than behind `claudeHome()`:

```go
// Reader finds and reads one agent's stored conversations.
type Reader interface {
	Path(spec agent.Spec, sessionID string) string // "" when unknown
	Replies(spec agent.Spec, sessionID string, n int) []string
	Conversations(spec agent.Spec, cwd string) ([]Conversation, error)
}
```

Claude's existing implementation moves behind it unchanged. `flockdeck chat` gets
one that reads its own JSONL. An agent with `Caps.Transcript == false` gets the
null reader, and every caller already copes: a fan-out falls back to the
screen, the history overlay lists nothing for it, and resume is not attempted.

## 7. The pane briefing

`ContextHook` is today's behaviour, untouched. `ContextPrompt` renders the same
briefing and puts it in front of the opening prompt, fenced so the agent can
tell the two apart:

```
<flockdeck-context>
...the same text the hook returns...
</flockdeck-context>

<the task>
```

A pane with no task and `ContextPrompt` still gets the block, alone. After a
restart there is no new opening prompt, so a `ContextPrompt` agent is briefed
once per launch — which is honest, as long as the briefing says when it was
taken. Siblings are described with their agent and model (`codex · gpt-5`),
because which agent is in the next pane changes what is worth asking of it.

## 8. The native API agent

`flockdeck chat` is a subcommand run inside a pane's PTY. It is a real terminal
chat client, and it is what makes an API model a first-class agent rather than
something you shell out to.

```
flockdeck chat --agent openai --model gpt-5 --session <uuid> [--resume] [--] [task]
```

- Wire formats: `anthropic` (Messages, streaming), `openai` (Chat Completions,
  streaming — and with it every OpenAI-compatible endpoint), `gemini`
  (`streamGenerateContent`). Standard library only: no SDKs, no new modules.
- It reports its own lifecycle to `FLOCKDECK_API`/`FLOCKDECK_TOKEN` using the existing
  `/hook` endpoint and the existing event names (`SessionStart`,
  `UserPromptSubmit`, `PreToolUse`, `PostToolUse`, `Notification`, `Stop`,
  `SessionEnd`). Nothing in the workspace has to learn a second protocol, and an
  API pane gets accurate status for free. Its Spec therefore sets
  `Caps{Hooks: true, Resume: true, Transcript: true, Context: ContextHook}`.
- Transcript: JSONL at `<state dir>/chats/<session>.jsonl`, one object per
  entry, `{"type":"user"|"assistant"|"tool","ts":...,"text":...}`. `--resume`
  replays it into the request. This is what `RecentReplies` reads for a
  fan-out, so an API agent's plan can be fanned out like any other agent's.
- Rendering: streamed text wrapped to the terminal width, ANSI for headings,
  code and tool calls; Ctrl+C interrupts the turn rather than the process; the
  model, the token counts and the running cost sit on a status line.

**Tools** live behind one interface, so the loop and the tools can be built
apart:

```go
type Tool interface {
	Name() string
	Describe() Schema                     // JSON-schema shape, translated per wire
	Approval(args json.RawMessage) string // "" = no approval needed
	Run(ctx context.Context, args json.RawMessage) (string, error)
}
```

v1 tools: `read_file`, `write_file`, `edit_file`, `list_dir`, `glob`, `grep`,
`run_command`. Every one is confined to the pane's working directory; a path
that escapes it is refused, not approved. `write_file`, `edit_file` and
`run_command` ask first, in the terminal, and the ask is a `Notification`
event — so the pane turns amber and the user is told which pane wants them,
which is the whole point of Flockdeck. Approval is per call; "always for the rest
of this session" is offered for `run_command` prefixes only.

## 9. Persistence

`store.Pane` gains `agent` and `model`, and `kind` becomes `"agent"` or
`"shell"`. `store.Version` goes to 2. Reading a version 1 file, `kind:"claude"`
becomes `kind:"agent", agent:"claude"` with an empty model; it is migrated in
memory and written back as version 2, losing nothing and asking nobody. The
same two fields join the pane view the front end receives and the `spawn`
request.

## 10. Keys

`internal/creds` resolves an API key for a Spec: each name in `API.KeyEnv` from
the environment, then `<state dir>/keys.json` (0600,
`{"anthropic":"sk-...","openai":"..."}`), then nothing. A key reaches exactly
one place — the environment of the `flockdeck chat` process for the pane that needs
it — and is never logged, never in a snapshot, never in an error message. The
interface shows only "set" or "not set", offers "set…" and "clear", and never
reads one back. `flockdeck keys set <agent>` reads the key from stdin so it never
lands in shell history; `flockdeck keys list` prints which are set.

## 11. The interface

- Splitting or opening a tab with the default agent stays one keystroke and
  gains nothing. The picker is what opens when the split button is held, when
  the caret beside "New tab" is clicked, and from the palette.
- The picker: agents grouped as installed / not installed, each expanding to
  its models, the default marked, "Set as default for this project" at the
  foot. Keyboard-first, like every other overlay.
- The pane header names the agent and model beside the branch, in the same dim
  weight as the branch — `claude · sonnet`, `codex · gpt-5` — and shows nothing
  extra for a shell.
- New entries in `internal/help/keys.go` for "New agent tab (choose agent)…"
  and "Split right (choose agent)…". The palette, the shortcut tables and the
  README table all render from that list, and the front end must implement
  every id in it or `TestFrontEndImplementsEveryAction` fails.
- The fan-out dialog gains one agent/model control for the whole run, plus a
  per-line override on each row, so twelve tasks can be split between two
  agents deliberately.

## 12. House rules

- Go 1.27, `CGO_ENABLED=0`, standard library only. **No new module
  dependencies** — if a task seems to need one, it is the wrong shape.
- `make check` (vet + `go test ./...`) passes before you commit. Tests live
  beside what they test, and are table-driven where the existing ones are.
- Comments in this repository say *why*, in prose, in complete sentences. Match
  that voice; a comment restating the code is worse than none.
- Nothing regresses for somebody who only ever runs Claude: the same
  keystrokes, the same argv, the same statuses, the same resumed conversations,
  and a layout saved by the previous build restored without a word.
- Cross-platform: Windows, macOS and Linux. Paths through `filepath`, and
  nothing shells out to `sh`.
- Commit on your own branch, with a message in the repository's existing style
  (lower-case subject, `area: what changed and why`).
