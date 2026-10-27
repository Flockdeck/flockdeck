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
// nothing beyond how to start the program: no hooks, no resume, no transcript,
// no trust -- and no output patterns. The one thing they do claim is a briefing
// in front of the opening task, which needs nothing from the tool. Such an agent still works
// perfectly well; it is a terminal with a program in it, and Flockdeck simply knows
// less about what is happening inside it. Anyone who has one of them installed
// should check its flags and fill its capabilities in.
//
// The model lists, and each model's tier, were read from each provider's own
// documentation on 2026-09-12: Claude Code's model-config page, Codex's models
// page and Gemini CLI's model page. A tier says where a model stands among
// the agent's own -- small, mid or top -- and a model mixed by design, such as Claude Code's
// opusplan, has none, so routing leaves it alone. The prices are not here but
// in internal/pricing, dated there.

// Builtins returns a fresh copy of the built-in specs, in the order the picker
// shows them: Claude first because it is the default, then the other
// command-line agents, then the ACP agents.
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
		opencodeACPSpec(),
		geminiACPSpec(),
		codexACPSpec(),
	}
}

// ClaudeStripEnv are the environment variables a running Claude Code session
// injects into its children. If a pane inherited them it would think it was a
// nested child session (and, among other things, stop saving its transcript),
// so they are stripped from every pane. It is the one list of them: the
// built-in claude Spec carries it, and session.EnvStripping removes it whatever
// an agents.json entry for claude says its stripEnv is.
func ClaudeStripEnv() []string {
	return []string{"CLAUDECODE", "CLAUDE_CODE_CHILD_SESSION", "CLAUDE_CODE_ENTRYPOINT",
		"CLAUDE_CODE_SESSION_ID", "CLAUDE_CODE_SSE_PORT", "CLAUDE_CODE_DONT_INHERIT_ENV"}
}

// claudeSpec is the agent Flockdeck ran before it could run any other, expressed
// in the same table as the rest. The argument lists are what
// a Claude pane has always been run with, which is the whole point: somebody
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
		Wake: claudeWake(),
		Models: []Model{
			{ID: "", Name: "Default", Note: "whatever the CLI is set to"},
			{ID: "opus", Name: "Opus", Note: "most capable", Tier: TierTop},
			{ID: "sonnet", Name: "Sonnet", Note: "the everyday one", Tier: TierMid},
			{ID: "haiku", Name: "Haiku", Note: "fastest", Tier: TierSmall},
			// Claude Code's own routing: Opus in plan mode, Sonnet otherwise.
			{ID: "opusplan", Name: "Opus plan", Note: "Opus to plan, Sonnet to carry it out"},
		},
		StripEnv: ClaudeStripEnv(),
		Install:  "https://claude.com/claude-code",
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
		Caps: Caps{Context: ContextPrompt},
		Models: append([]Model{
			{ID: "", Name: "Default", Note: "whatever the CLI is set to"},
		}, openAIModels()...),
		Install: "npm install -g @openai/codex",
	}
}

// openAIModels is OpenAI's line-up for the Codex CLI.
func openAIModels() []Model {
	return []Model{
		{ID: "gpt-6-astra", Name: "GPT-6 Astra", Note: "most capable, and the dearest", Tier: TierTop},
		{ID: "gpt-5.6-sol", Name: "GPT-5.6 Sol", Note: "for complex coding", Tier: TierTop},
		{ID: "gpt-5.6-terra", Name: "GPT-5.6 Terra", Note: "the everyday one", Tier: TierMid},
		{ID: "gpt-5.6-luna", Name: "GPT-5.6 Luna", Note: "fastest and cheapest", Tier: TierSmall},
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
		Caps: Caps{Context: ContextPrompt},
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
		// Briefed only when there is a task to carry the briefing: --message
		// answers once and exits (checked against aider 0.86.2, whose main()
		// returns straight after coder.run(with_message=...)), so a
		// bare-briefing message would start a pane that answers the briefing
		// and closes.
		// TextWake is an offer, not a claim about the tool: the model behind
		// aider has no way to run flockdeck, so it can be sent the text.
		Caps: Caps{Context: ContextTask, TextWake: true},
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
		Caps:    Caps{Context: ContextPrompt},
		Install: "https://opencode.ai",
	}
}

// homeEnv is what an agent that keeps its settings and logins in the user's
// home needs to find them: the home directory itself, on each platform, and the
// folders the XDG convention and Windows put under it. It is not a secret and it
// is not the whole environment; keys and tokens are never in it.
func homeEnv() []string {
	return []string{"HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME"}
}

// opencodeACPSpec is opencode in a structured pane: `opencode acp`, which ran a
// whole turn with no login in the spike, on its free model. It is a separate
// entry from the terminal one, so that choosing a pane's kind never changes
// what an existing layout means. It is listed only for somebody who has opted
// in to structured panes.
func opencodeACPSpec() Spec {
	return Spec{
		ID: "opencode-acp", Name: "opencode (structured)", Runner: RunnerACP,
		ACP:     ACPSpec{Adapter: "opencode", Auth: AuthNone, PassEnv: homeEnv(), MCP: true},
		Install: "https://opencode.ai",
	}
}

// geminiACPSpec is Gemini CLI over ACP, hidden until the task that checks it
// end to end has landed. Its session needs a Gemini login, made with its own
// tool.
func geminiACPSpec() Spec {
	return Spec{
		ID: "gemini-acp", Name: "Gemini CLI (structured)", Runner: RunnerACP, Hidden: true,
		ACP:     ACPSpec{Adapter: "gemini", Auth: AuthExternal, Login: "Run `gemini` once in a terminal and sign in.", PassEnv: homeEnv()},
		Install: "npm install -g @google/gemini-cli",
	}
}

// codexACPSpec is Codex through its ACP adapter, hidden until the task that
// checks it end to end has landed. Its session needs a Codex login.
func codexACPSpec() Spec {
	return Spec{
		ID: "codex-acp", Name: "Codex (structured)", Runner: RunnerACP, Hidden: true,
		ACP:     ACPSpec{Adapter: "codex", Auth: AuthExternal, Login: "Run `codex login` in a terminal.", PassEnv: homeEnv()},
		Install: "npm install --prefix <folder> @agentclientprotocol/codex-acp@2.2.2",
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
		Caps:    Caps{Context: ContextPrompt},
		Install: "https://cursor.com/cli",
	}
}
