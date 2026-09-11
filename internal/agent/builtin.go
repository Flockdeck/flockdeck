package agent

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
			{ID: "opus", Name: "Opus", Note: "most capable"},
			{ID: "sonnet", Name: "Sonnet", Note: "the everyday one"},
			{ID: "haiku", Name: "Haiku", Note: "fastest"},
		},
		StripEnv: []string{"CLAUDECODE", "CLAUDE_CODE_CHILD_SESSION", "CLAUDE_CODE_ENTRYPOINT",
			"CLAUDE_CODE_SESSION_ID", "CLAUDE_CODE_SSE_PORT", "CLAUDE_CODE_DONT_INHERIT_ENV"},
		Install: "https://claude.com/claude-code",
	}
}

// codexSpec describes OpenAI's Codex CLI. It was not installed where this was
// written, so it takes only a model and an opening task -- the two things every
// one of these tools accepts -- and claims no capabilities.
func codexSpec() Spec {
	return Spec{
		ID: "codex", Name: "Codex", Runner: RunnerCLI, Exe: "codex",
		Args: []Arg{
			Group("model", "--model", "{{model}}"),
			Lit("{{prompt}}"),
		},
		Models: []Model{
			{ID: "", Name: "Default", Note: "whatever the CLI is set to"},
			{ID: "gpt-5", Name: "GPT-5"},
			{ID: "gpt-5-mini", Name: "GPT-5 mini", Note: "cheaper and quicker"},
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
		Models: []Model{
			{ID: "", Name: "Default", Note: "whatever the CLI is set to"},
			{ID: "gemini-2.5-pro", Name: "Gemini 2.5 Pro"},
			{ID: "gemini-2.5-flash", Name: "Gemini 2.5 Flash", Note: "cheaper and quicker"},
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

// chatArgs is the argument list of `flockdeck chat` for one agent id. The binary
// and the "chat" subcommand are not here: BuildArgv leaves an API runner's
// program to the caller, because only the running process knows where its own
// executable lives.
//
// The agent id is written in as a literal rather than a token because the
// tokens are the pane's, not the catalog's, and an agent defined only in the
// user's agents.json must still be able to say which entry the chat client
// should read its endpoint from.
func chatArgs(id string, resume bool) []Arg {
	args := []Arg{
		Lit("--agent"), Lit(id),
		Group("model", "--model", "{{model}}"),
		Group("session", "--session", "{{session}}"),
	}
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
		Args:       chatArgs("anthropic", false),
		ResumeArgs: chatArgs("anthropic", true),
		API: APISpec{
			Wire:   "anthropic",
			KeyEnv: []string{"ANTHROPIC_API_KEY"},
		},
		Models: []Model{
			{ID: "claude-opus-5", Name: "Opus 5", Note: "most capable"},
			{ID: "claude-sonnet-5", Name: "Sonnet 5", Note: "the everyday one"},
			{ID: "claude-haiku-4-5-20251001", Name: "Haiku 4.5", Note: "fastest"},
		},
		DefaultModel: "claude-sonnet-5",
		Caps:         chatCaps(),
		Install:      "set a key with `flockdeck keys set anthropic`",
	}
}

func openAIAPISpec() Spec {
	return Spec{
		ID: "openai", Name: "OpenAI API", Runner: RunnerAPI,
		Args:       chatArgs("openai", false),
		ResumeArgs: chatArgs("openai", true),
		API: APISpec{
			Wire:   "openai",
			KeyEnv: []string{"OPENAI_API_KEY"},
		},
		Models: []Model{
			{ID: "gpt-5", Name: "GPT-5"},
			{ID: "gpt-5-mini", Name: "GPT-5 mini", Note: "cheaper and quicker"},
		},
		DefaultModel: "gpt-5",
		Caps:         chatCaps(),
		Install:      "set a key with `flockdeck keys set openai`",
	}
}

func googleAPISpec() Spec {
	return Spec{
		ID: "google", Name: "Gemini API", Runner: RunnerAPI,
		Args:       chatArgs("google", false),
		ResumeArgs: chatArgs("google", true),
		API: APISpec{
			Wire: "gemini",
			// Google's own tools read either name, and somebody who has one
			// exported should not have to export the other for Flockdeck.
			KeyEnv: []string{"GEMINI_API_KEY", "GOOGLE_API_KEY"},
		},
		Models: []Model{
			{ID: "gemini-2.5-pro", Name: "Gemini 2.5 Pro"},
			{ID: "gemini-2.5-flash", Name: "Gemini 2.5 Flash", Note: "cheaper and quicker"},
		},
		DefaultModel: "gemini-2.5-pro",
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
		ID: "openai-compatible", Name: "OpenAI-compatible endpoint", Runner: RunnerAPI,
		Args:       chatArgs("openai-compatible", false),
		ResumeArgs: chatArgs("openai-compatible", true),
		API:        APISpec{Wire: "openai"},
		Caps:       chatCaps(),
		Install:    `add a "baseURL" for it to agents.json`,
	}
}
