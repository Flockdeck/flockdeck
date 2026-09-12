package agent

import "strings"

// The built-in catalog: the agents Flockdeck knows about before the user has said
// anything. Everything here can be corrected, extended or hidden by the user's
// own agents.json, so nothing in this file has to be right forever -- but an
// entry that is wrong is worse than an entry that is modest, because a wrong
// flag kills the pane on the spot and a missing one only means the agent starts
// with its own defaults.
//
// Only the `claude` entry below was checked against the tool itself on the
// machine it was written on. The other command-line agents were not installed
// there and could not be asked for their `--help`, so each of them claims
// nothing beyond how to start the program: empty Caps -- no hooks, no resume,
// no transcript, no trust -- and no output patterns. Such an agent still works
// perfectly well; it is a terminal with a program in it, and Flockdeck simply knows
// less about what is happening inside it. Anyone who has one of them installed
// should check its flags and fill its capabilities in.
//
// The model lists, and each model's tier, were read from each provider's own
// documentation on 2026-09-12: Claude Code's model-config page, Codex's models
// page, Gemini CLI's model page, and the Claude, OpenAI and Gemini API model
// and pricing pages. A tier says where a model stands among the agent's own --
// small, mid or top -- and a model mixed by design, such as Claude Code's
// opusplan, has none, so routing leaves it alone. The prices are not here but
// in internal/pricing, dated there.

// Builtins returns a fresh copy of the built-in specs, in the order the picker
// shows them: Claude first because it is the default, then the other
// command-line agents, then the API runners that need no CLI at all.
//
// The copy is fresh on every call because the catalog merges the user's file
// over these entries in place, and a shared slice would carry one run's
// agents.json into the next read of the built-ins.
func Builtins() []Spec {
	return []Spec{
		claudeSpec(),
		codexSpec(),
		geminiSpec(),
		aiderSpec(),
		opencodeSpec(),
		cursorAgentSpec(),
		anthropicAPISpec(),
		openAIAPISpec(),
		googleAPISpec(),
		openAICompatibleSpec(),
	}
}

// claudeSpec is the agent Flockdeck ran before it could run any other, expressed
// in the same table as the rest. The argument lists are what
// session.ClaudeArgs has always produced, which is the whole point: somebody
// who only ever runs Claude sees the same argv as before.
func claudeSpec() Spec {
	return Spec{
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
			{ID: "opus", Name: "Opus", Note: "most capable", Tier: TierTop},
			{ID: "sonnet", Name: "Sonnet", Note: "the everyday one", Tier: TierMid},
			{ID: "haiku", Name: "Haiku", Note: "fastest", Tier: TierSmall},
			// Claude Code's own routing: Opus in plan mode, Sonnet otherwise.
			{ID: "opusplan", Name: "Opus plan", Note: "Opus to plan, Sonnet to carry it out"},
		},
		StripEnv: []string{"CLAUDECODE", "CLAUDE_CODE_CHILD_SESSION", "CLAUDE_CODE_ENTRYPOINT",
			"CLAUDE_CODE_SESSION_ID", "CLAUDE_CODE_SSE_PORT", "CLAUDE_CODE_DONT_INHERIT_ENV"},
		Install: "https://claude.com/claude-code",
	}
}

// codexSpec describes OpenAI's Codex CLI. It was not installed where this was
// written, so it takes only a model and an opening task -- the two things every
// one of these tools accepts -- and claims no capabilities. Its models are the
// ones Codex's own documentation recommends for it.
func codexSpec() Spec {
	return Spec{
		ID: "codex", Name: "Codex", Runner: RunnerCLI, Exe: "codex",
		Args: []Arg{
			Group("model", "--model", "{{model}}"),
			Lit("{{prompt}}"),
		},
		Models: []Model{
			{ID: "", Name: "Default", Note: "whatever the CLI is set to"},
			{ID: "gpt-6-astra", Name: "GPT-6 Astra", Note: "most capable, and the dearest", Tier: TierTop},
			{ID: "gpt-5.6-sol", Name: "GPT-5.6 Sol", Note: "for complex coding", Tier: TierTop},
			{ID: "gpt-5.6-terra", Name: "GPT-5.6 Terra", Note: "the everyday one", Tier: TierMid},
			{ID: "gpt-5.6-luna", Name: "GPT-5.6 Luna", Note: "fastest and cheapest", Tier: TierSmall},
		},
		Install: "npm install -g @openai/codex",
	}
}

// geminiSpec describes Google's Gemini CLI. Unverified, as above: the opening
// task goes through -i rather than as a bare argument because that is the flag
// the tool documents for starting an interactive session with something to do,
// and a pane that exits after one answer would be no use here.
func geminiSpec() Spec {
	return Spec{
		ID: "gemini", Name: "Gemini CLI", Runner: RunnerCLI, Exe: "gemini",
		// The task is the value of -i, so the "--" guard BuildArgv puts before
		// a positional task beginning with a dash would land between the flag
		// and its value and mean nothing to the tool.
		NoDashDash: true,
		Args: []Arg{
			Group("model", "--model", "{{model}}"),
			Group("prompt", "-i", "{{prompt}}"),
		},
		// Only the models the CLI's own page names and the Gemini API still
		// serves as stable. Its page also names Gemini 3 previews, which the
		// API has since shut down; a model that is not there kills the pane.
		// Flash is Google's middle tier, below Pro and above Flash-Lite.
		Models: []Model{
			{ID: "", Name: "Default", Note: "whatever the CLI is set to"},
			// Gemini CLI's own routing between a Pro and a Flash model.
			{ID: "auto", Name: "Auto", Note: "Gemini CLI chooses Pro or Flash itself"},
			{ID: "gemini-2.5-pro", Name: "Gemini 2.5 Pro", Tier: TierTop},
			{ID: "gemini-2.5-flash", Name: "Gemini 2.5 Flash", Note: "cheaper and quicker", Tier: TierMid},
		},
		Install: "npm install -g @google/gemini-cli",
	}
}

// aiderSpec describes aider. Unverified, and the one entry here whose opening
// task is worth a warning: aider's --message runs a single message and then
// leaves, so a pane started with a task may end when the task does. Without a
// machine to check it on, saying that plainly is better than guessing at a flag
// that keeps the session open.
func aiderSpec() Spec {
	return Spec{
		ID: "aider", Name: "Aider", Runner: RunnerCLI, Exe: "aider",
		NoDashDash: true,
		Args: []Arg{
			Group("model", "--model", "{{model}}"),
			Group("prompt", "--message", "{{prompt}}"),
		},
		Models: []Model{
			{ID: "", Name: "Default", Note: "whatever the CLI is set to"},
		},
		Install: "python -m pip install aider-install && aider-install",
	}
}

// opencodeSpec describes opencode. Unverified. Its model ids are qualified by
// provider ("anthropic/claude-sonnet-4-5"), so the list is left empty rather
// than half-guessed: an empty list means the agent keeps whatever it was
// configured with, which is always safe.
func opencodeSpec() Spec {
	return Spec{
		ID: "opencode", Name: "opencode", Runner: RunnerCLI, Exe: "opencode",
		NoDashDash: true,
		Args: []Arg{
			Group("model", "--model", "{{model}}"),
			Group("prompt", "--prompt", "{{prompt}}"),
		},
		Install: "https://opencode.ai",
	}
}

// cursorAgentSpec describes Cursor's command-line agent. Unverified.
func cursorAgentSpec() Spec {
	return Spec{
		ID: "cursor-agent", Name: "Cursor Agent", Runner: RunnerCLI, Exe: "cursor-agent",
		Args: []Arg{
			Group("model", "--model", "{{model}}"),
			Lit("{{prompt}}"),
		},
		Install: "https://cursor.com/cli",
	}
}

// The API runners below need no CLI at all: each one starts `flockdeck chat`,
// Flockdeck's own terminal chat client, which talks straight to the endpoint. They
// therefore all share the capabilities of that client -- it reports its own
// lifecycle through the same hook endpoint every Claude pane uses, keeps its
// own transcript and can replay it -- rather than each claiming something
// different.

// chatCaps are what `flockdeck chat` supports whatever endpoint it is pointed at.
// It has no per-directory trust question of its own, which is the one thing it
// cannot do that Claude can.
func chatCaps() Caps {
	return Caps{Hooks: true, Resume: true, Transcript: true, Context: ContextHook}
}

// chatSwitch is how `flockdeck chat` changes model mid-conversation. Its
// /model changes the session's model and nothing else: the client keeps no
// settings of its own to save one in.
const chatSwitch = "/model {{model}}"

// chatArgs is the argument list of `flockdeck chat` for one agent id. The binary
// and the "chat" subcommand are not here: BuildArgv leaves an API runner's
// program to the caller, because only the running process knows where its own
// executable lives.
//
// The agent id is written in as a literal rather than a token because the
// tokens are the pane's, not the catalog's, and an agent defined only in the
// user's agents.json must still be able to say which entry it is.
//
// The endpoint goes on as literals too: the wire, the base URL and the names
// a key may arrive in. The chat client reads nothing from the catalog, so
// without them it took the defaults it falls back on -- the Anthropic wire at
// Anthropic's address -- for every API agent: an OpenAI or Gemini pane, or a
// local model, sent its request, and whichever key was stored under its id,
// to api.anthropic.com. normalize builds these from the entry as merged, so
// a baseURL written over a built-in reaches the command line.
func chatArgs(id string, api APISpec, resume bool) []Arg {
	args := []Arg{Lit("--agent"), Lit(id)}
	if api.Wire != "" {
		args = append(args, Lit("--wire"), Lit(api.Wire))
	}
	if api.BaseURL != "" {
		args = append(args, Lit("--base-url"), Lit(api.BaseURL))
	}
	if len(api.KeyEnv) > 0 {
		args = append(args, Lit("--key-env"), Lit(strings.Join(api.KeyEnv, ",")))
	}
	args = append(args,
		Group("model", "--model", "{{model}}"),
		Group("session", "--session", "{{session}}"),
	)
	if resume {
		return append(args, Lit("--resume"))
	}
	return append(args, Lit("{{prompt}}"))
}

// anthropicAPISpec talks to the Messages API directly, which is how Flockdeck runs
// a Claude model without the Claude CLI in the way.
func anthropicAPISpec() Spec {
	return Spec{
		ID: "anthropic", Name: "Claude API", Runner: RunnerAPI,
		API: APISpec{
			Wire:   "anthropic",
			KeyEnv: []string{"ANTHROPIC_API_KEY"},
		},
		Models: []Model{
			{ID: "claude-opus-5", Name: "Opus 5", Note: "most capable", Tier: TierTop},
			{ID: "claude-sonnet-5", Name: "Sonnet 5", Note: "the everyday one", Tier: TierMid},
			{ID: "claude-haiku-4-5-20251001", Name: "Haiku 4.5", Note: "fastest", Tier: TierSmall},
		},
		DefaultModel: "claude-sonnet-5",
		Switch:       chatSwitch,
		Caps:         chatCaps(),
		Install:      "set a key with `flockdeck keys set anthropic`",
	}
}

func openAIAPISpec() Spec {
	return Spec{
		ID: "openai", Name: "OpenAI API", Runner: RunnerAPI,
		API: APISpec{
			Wire:   "openai",
			KeyEnv: []string{"OPENAI_API_KEY"},
		},
		// The default is the middle of the line-up, as Sonnet is for Claude.
		Models: []Model{
			{ID: "gpt-6-astra", Name: "GPT-6 Astra", Note: "most capable, and the dearest", Tier: TierTop},
			{ID: "gpt-5.6-sol", Name: "GPT-5.6 Sol", Note: "for complex work", Tier: TierTop},
			{ID: "gpt-5.6-terra", Name: "GPT-5.6 Terra", Note: "the everyday one", Tier: TierMid},
			{ID: "gpt-5.6-luna", Name: "GPT-5.6 Luna", Note: "fastest and cheapest", Tier: TierSmall},
		},
		DefaultModel: "gpt-5.6-terra",
		Switch:       chatSwitch,
		Caps:         chatCaps(),
		Install:      "set a key with `flockdeck keys set openai`",
	}
}

func googleAPISpec() Spec {
	return Spec{
		ID: "google", Name: "Gemini API", Runner: RunnerAPI,
		API: APISpec{
			Wire: "gemini",
			// Google's own tools read either name, and somebody who has one
			// exported should not have to export the other for Flockdeck.
			KeyEnv: []string{"GEMINI_API_KEY", "GOOGLE_API_KEY"},
		},
		// Google's current Pro is a preview, and previews are shut down when
		// they are replaced -- Gemini 3 Pro's was -- so the default is the
		// current Flash, which is stable.
		Models: []Model{
			{ID: "gemini-3.1-pro-preview", Name: "Gemini 3.1 Pro", Note: "most capable; a preview", Tier: TierTop},
			{ID: "gemini-3.8-flash", Name: "Gemini 3.8 Flash", Note: "the everyday one", Tier: TierMid},
			{ID: "gemini-3.1-flash-lite", Name: "Gemini 3.1 Flash-Lite", Note: "fastest and cheapest", Tier: TierSmall},
		},
		DefaultModel: "gemini-3.8-flash",
		Switch:       chatSwitch,
		Caps:         chatCaps(),
		Install:      "set a key with `flockdeck keys set google`",
	}
}

// openAICompatibleSpec is the entry for everything that speaks the OpenAI wire
// format at an address of its own: Ollama, LM Studio, vLLM, a company gateway.
//
// It ships with no endpoint and no models on purpose. There is no address that
// would be right for everyone, and one guessed here would show as an installed
// agent that fails the moment it is picked. Empty, it shows as unavailable with
// the line below, which says exactly what to do about it.
func openAICompatibleSpec() Spec {
	return Spec{
		ID: OpenAICompatibleID, Name: "OpenAI-compatible endpoint", Runner: RunnerAPI,
		API:    APISpec{Wire: "openai"},
		Switch: chatSwitch,
		Caps:   chatCaps(),
		// The picker is named first because it is where this line is read, and
		// it takes the address there and then. The command is for `flockdeck
		// agents`, which prints the same line in a terminal.
		Install: "give it an address: pick it here and type one, such as http://127.0.0.1:11434/v1, or run `flockdeck keys endpoint openai-compatible <address>`",
	}
}
