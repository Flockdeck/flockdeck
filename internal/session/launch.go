package session

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/jmwri/flockdeck/internal/agent"
)

// Launch is a pane about to be started: which agent to run in it, which model
// it was asked for, and everything about the pane that the agent's argument
// list and environment are written in terms of.
//
// It exists so that the knowledge of how a pane is started lives on the Spec
// rather than in the caller. Every decision Flockdeck once made in line for the
// `claude` CLI -- which program, which flags, whether a settings file is
// written and handed over, what is taken out of the environment -- is read off
// the Spec here, so that a second agent is a table entry rather than another
// branch at the point a pane is launched.
type Launch struct {
	Spec  agent.Spec
	Model string
	// ID is the pane id, which doubles as the conversation id: pinning the two
	// together is what lets a later run reattach to exactly the conversation
	// this pane had rather than to whichever one the agent saw last.
	ID   string
	Name string
	Cwd  string
	// Prompt is the task the pane opens with. It goes into the argv and is
	// never typed into the terminal, because typing into a running interface
	// means guessing when it is ready to accept it.
	//
	// It is passed through even when resuming. Whether a resumed pane is given
	// a task again is the Spec's own answer, written in whether ResumeArgs
	// mentions the prompt, rather than something decided here for every agent.
	Prompt string
	// Resume asks for the conversation to be reattached rather than started
	// afresh. Whether that is possible -- the agent supports it, and its
	// transcript is there to be resumed -- is the caller's question to answer
	// before it gets here, because only the caller can read the transcript.
	Resume bool
	// SelfExe is Flockdeck's own binary: the hook command an agent calls back on,
	// and the program itself for an API runner, whose pane runs `flockdeck chat`.
	SelfExe string
	// SettingsDir is where the generated settings file is written, for an agent
	// whose arguments ask to be handed one.
	SettingsDir string
	// Endpoint is where a pane reports its lifecycle back to Flockdeck. The
	// secret it reports with is not written into the settings: it is
	// FLOCKDECK_TOKEN, in Env, which the hooks inherit.
	Endpoint string
	// StripEnv is what to take out of the inherited environment. The caller
	// passes the union across the whole catalog rather than this Spec's own
	// list, so that a pane is a clean top-level session whatever is running in
	// it: the markers of the Claude session Flockdeck was launched from have to go
	// from a Codex pane too. The markers Flockdeck knew before agents had Specs
	// are stripped whatever it holds.
	StripEnv []string
	// Env is added to the pane's environment last and wins over everything
	// before it: these are the FLOCKDECK_* variables telling the pane what to call
	// back on, which nothing in a catalog entry may shadow. The agent and model
	// are added alongside them without being asked for.
	Env        []string
	Cols, Rows int
}

// Config resolves a Launch into the configuration Start takes, writing the
// agent's settings file along the way if its arguments ask for one.
func (l Launch) Config() (Config, error) {
	argv, err := l.argv()
	if err != nil {
		return Config{}, err
	}
	return Config{
		ID:   l.ID,
		Kind: KindAgent,
		Spec: l.Spec,
		Name: l.Name,
		Cwd:  l.Cwd,
		Argv: argv,
		Env:  EnvStripping(l.StripEnv, layerEnv(l.Spec.Env, l.paneVars(), l.Env)...),
		Cols: l.Cols,
		Rows: l.Rows,
	}, nil
}

// argv builds the command line for the pane, which is where the settings file
// is written: the path to it is one of the arguments, so there is no point in
// writing one for an agent whose arguments never ask for it.
func (l Launch) argv() ([]string, error) {
	settings, err := Settings(l.Spec, l.SettingsDir, l.ID, l.SelfExe, l.Endpoint)
	if err != nil {
		return nil, err
	}

	argv := agent.BuildArgv(l.Spec, l.Resume, agent.Tokens{
		Session:  l.ID,
		Model:    l.model(),
		Settings: settings,
		Prompt:   l.Prompt,
		Cwd:      l.Cwd,
		Pane:     l.ID,
	})

	// An API agent is Flockdeck's own chat client: BuildArgv deliberately leaves
	// the program off, because only something holding the running binary's
	// path knows where to find it again.
	if l.Spec.Runner == agent.RunnerAPI {
		if l.SelfExe == "" {
			return nil, fmt.Errorf("agent %s talks to an API, which needs Flockdeck's own binary to run", l.Spec.ID)
		}
		argv = append([]string{ChatExe(l.SelfExe), "chat"}, argv...)
	}
	if len(argv) == 0 {
		return nil, fmt.Errorf("agent %s has no command to run", l.Spec.ID)
	}
	return argv, nil
}

// chatTwin is the console build of Flockdeck that a Windows release ships
// beside flockdeck.exe, for API agents' panes to run.
const chatTwin = "flockdeck-chat.exe"

// ChatExe returns the program an API agent's pane runs: Flockdeck's own
// binary, or on Windows the console build shipped beside it.
//
// A release for Windows is linked as a GUI program (-H=windowsgui), so that
// starting it opens no console window, and Windows attaches no console to a
// GUI program even inside a pseudo-console: `flockdeck.exe chat` in a pane
// printed nothing and read nothing. A build without the twin -- a plain `go
// build` makes a console program -- runs itself as before.
func ChatExe(selfExe string) string { return chatExeFor(runtime.GOOS, selfExe) }

// chatExeFor is ChatExe for a given platform.
func chatExeFor(goos, selfExe string) string {
	if goos != "windows" || selfExe == "" {
		return selfExe
	}
	twin := filepath.Join(filepath.Dir(selfExe), chatTwin)
	if fi, err := os.Stat(twin); err == nil && fi.Mode().IsRegular() {
		return twin
	}
	return selfExe
}

// model is the model the pane runs on: the one it was asked for, or the Spec's
// own default. Both may be empty, which is how a CLI keeps whatever it was
// configured with rather than being told.
func (l Launch) model() string {
	if l.Model != "" {
		return l.Model
	}
	return l.Spec.DefaultModel
}

// paneVars name the agent and the model in the pane's own environment, so that
// whatever is running in it -- a shell prompt, a script, an agent spawning a
// helper of its own with `flockdeck spawn` -- can say which agent it is without
// having to ask Flockdeck.
func (l Launch) paneVars() []string {
	return []string{"FLOCKDECK_AGENT=" + l.Spec.ID, "FLOCKDECK_MODEL=" + l.model()}
}

// Look resolves the program an agent runs, or explains that it is not
// installed. The explanation carries the Spec's own Install line, because the
// answer to "codex is not on PATH" is wherever Codex comes from, and only the
// catalog knows that.
//
// An API agent has nothing to find: it runs Flockdeck's own binary, which is
// already running.
func Look(spec agent.Spec) (string, error) {
	if spec.Runner == agent.RunnerAPI {
		return "", nil
	}
	if spec.Exe == "" {
		return "", fmt.Errorf("agent %s names no command to run", spec.ID)
	}
	exe, err := exec.LookPath(spec.Exe)
	if err != nil {
		name := spec.Name
		if name == "" {
			name = spec.ID
		}
		if spec.Install != "" {
			return "", fmt.Errorf("the `%s` CLI was not found on PATH; install %s first (%s): %w",
				spec.Exe, name, spec.Install, err)
		}
		return "", fmt.Errorf("the `%s` CLI was not found on PATH; install %s first: %w", spec.Exe, name, err)
	}
	return exe, nil
}

// Settings writes the settings file an agent is handed so that it reports its
// lifecycle to Flockdeck, and returns its path.
//
// An agent that reports no lifecycle gets none. Neither does one that reports
// its own without being handed a file to say where -- `flockdeck chat` is told in
// its environment -- because the path is only ever used as an argument, and
// writing one anyway would leave a file in the state directory for every pane
// ever opened that nothing would read.
func Settings(spec agent.Spec, dir, sessionID, selfExe, endpoint string) (string, error) {
	if !spec.Caps.Hooks || endpoint == "" || !wantsSettings(spec) {
		return "", nil
	}
	return WriteHookSettingsFor(spec.Exe, dir, sessionID, selfExe, endpoint)
}

// wantsSettings reports whether an agent's arguments ever refer to a settings
// file.
func wantsSettings(spec agent.Spec) bool {
	return mentionsToken(spec.Args, "settings") || mentionsToken(spec.ResumeArgs, "settings")
}

// mentionsToken reports whether an argument list refers to a token, either by
// expanding it or by being a group conditional on it.
func mentionsToken(args []agent.Arg, name string) bool {
	for _, a := range args {
		if a.If == name || strings.Contains(a.Value, "{{"+name+"}}") {
			return true
		}
		if mentionsToken(a.Args, name) {
			return true
		}
	}
	return false
}

// StripEnvUnion collects what to take out of a pane's environment across a
// whole catalog of agents.
//
// The union rather than one Spec's list, because the markers being removed are
// those of the session Flockdeck itself was launched from, and that session's agent
// has nothing to do with the one about to run in the pane: a pane opened from
// inside Claude Code must not tell Codex it is a nested Claude session either.
func StripEnvUnion(specs []agent.Spec) []string {
	var out []string
	for _, s := range specs {
		for _, name := range s.StripEnv {
			if name != "" && !slices.Contains(out, name) {
				out = append(out, name)
			}
		}
	}
	// Sorted so that the same catalog always produces the same list, whatever
	// order the entries were merged in.
	slices.Sort(out)
	return out
}

// defaultStripEnv are the environment variables a running Claude Code session
// injects into its children. If we passed them through, every pane we spawn
// would think it was a nested child session (and, among other things, stop
// saving its transcript). They are stripped so each pane is a clean top-level
// session, regardless of whether Flockdeck itself was launched from Claude.
//
// They are stripped whatever else is: a caller with a catalog to hand passes
// the union of StripEnv across it on top of them.
var defaultStripEnv = []string{
	"CLAUDECODE",
	"CLAUDE_CODE_CHILD_SESSION",
	"CLAUDE_CODE_ENTRYPOINT",
	"CLAUDE_CODE_SESSION_ID",
	"CLAUDE_CODE_SSE_PORT",
	"CLAUDE_CODE_DONT_INHERIT_ENV",
}

// terminalEnv is what a pane is told about the terminal it runs in, and what
// it inherits about another terminal that has to go, on a given platform.
//
// A pane's terminal is the one Flockdeck draws, whatever Flockdeck itself was
// started in. Started from a desktop launcher on macOS or Linux it has no TERM
// to pass on, and every pane was a dumb terminal: Claude Code 2.1.269, read
// from its executable, picks no colour at all with neither TERM nor COLORTERM
// set, and vim and clear refuse to run. Started from another terminal, every
// pane was told it was that one: Claude Code picks its colours, notifications
// and key handling from TERM_PROGRAM and LC_TERMINAL, and with TMUX set it runs
// `tmux display-message -t $TMUX_PANE` and `tmux show-environment -g` against
// the user's own tmux server, about a pane that is not this one. What is
// stripped is what it reads to tell which terminal it is in.
//
// Windows is left as it is. Nothing there reads TERM the way terminfo does --
// Claude Code takes its colours from the Windows version -- and Claude Code
// reads WT_SESSION and TERM_PROGRAM there to decide that the console
// understands escape sequences, which a ConPTY pane does too.
func terminalEnv(goos string) (strip, set []string) {
	if goos == "windows" {
		return nil, nil
	}
	strip = []string{
		"TERM_PROGRAM", "TERM_PROGRAM_VERSION", "LC_TERMINAL", "LC_TERMINAL_VERSION",
		"TMUX", "TMUX_PANE", "STY",
		"KONSOLE_VERSION", "GNOME_TERMINAL_SERVICE", "XTERM_VERSION",
	}
	return strip, []string{"TERM=xterm-256color", "COLORTERM=truecolor"}
}

// Env builds the environment for a pane: Flockdeck's own environment minus the
// markers of the session it was launched from, plus extra KEY=VALUE entries.
func Env(extra ...string) []string { return EnvStripping(nil, extra...) }

// EnvStripping is Env with more variables to remove named explicitly, which is
// how a pane's environment comes to be driven by Spec.StripEnv rather than by
// what Flockdeck happened to know about Claude.
//
// The built-in markers go whatever the list says. What a caller has is the
// catalog's union, and an agents.json entry for claude that gives a stripEnv of
// its own replaces the built-in one rather than adding to it: handed that union
// alone, every pane opened from inside Claude Code would believe it was a
// nested child session again.
//
// An extra entry replaces an inherited one of the same name rather than
// joining it. A duplicated name in an environment block is resolved by the
// first copy, on Windows and on Unix alike, so appending alone would leave the
// stale value in force -- which is how a pane opened from inside another
// instance would tell its agent it was the pane that spawned it.
func EnvStripping(strip []string, extra ...string) []string {
	return envFrom(runtime.GOOS, os.Environ(), strip, extra)
}

// envFrom is EnvStripping for a given platform and inherited environment.
func envFrom(goos string, base, strip, extra []string) []string {
	termStrip, termSet := terminalEnv(goos)
	// What a pane is told about its terminal is the least of what it is given:
	// the catalog and the caller can still say otherwise.
	if len(termSet) > 0 {
		extra = layerEnv(termSet, extra)
	}

	drop := make(map[string]bool, len(defaultStripEnv)+len(termStrip)+len(strip)+len(extra))
	for _, name := range append(append(slices.Clip(defaultStripEnv), termStrip...), strip...) {
		drop[envKey(name)] = true
	}
	for _, kv := range extra {
		if name, _, ok := strings.Cut(kv, "="); ok {
			drop[envKey(name)] = true
		}
	}

	out := make([]string, 0, len(base)+len(extra))
	for _, kv := range base {
		name, _, ok := strings.Cut(kv, "=")
		if ok && drop[envKey(name)] {
			continue
		}
		out = append(out, kv)
	}
	return append(out, extra...)
}

// layerEnv joins the environment a pane is given, in order of increasing
// authority: an entry one layer sets is dropped if a later one sets it again.
//
// A duplicated name in an environment block is resolved by its first copy, on
// Windows and on Unix alike, so without this a catalog entry -- a file the user
// may edit -- could shadow the FLOCKDECK_* variables a pane reports its lifecycle
// on simply by naming one of them.
func layerEnv(layers ...[]string) []string {
	taken := map[string]bool{}
	var out []string
	// Walked from the last layer backwards, because the authoritative copy of a
	// name is the last one written and it is the earlier ones that have to go.
	for i := len(layers) - 1; i >= 0; i-- {
		layer := layers[i]
		for j := len(layer) - 1; j >= 0; j-- {
			name, _, ok := strings.Cut(layer[j], "=")
			if ok {
				if taken[envKey(name)] {
					continue
				}
				taken[envKey(name)] = true
			}
			out = append(out, layer[j])
		}
	}
	slices.Reverse(out)
	return out
}

// envKey normalises an environment variable name for comparison: Windows
// matches them without regard to case, everywhere else they are exact.
func envKey(name string) string {
	if runtime.GOOS == "windows" {
		return strings.ToUpper(name)
	}
	return name
}

// ShellArgs returns the argv for a plain shell pane on this platform.
func ShellArgs() []string { return shellArgsFor(runtime.GOOS, os.Getenv, exec.LookPath) }

// shellArgsFor is ShellArgs for a given platform, environment and PATH.
//
// A shell the environment names is only taken if it can be run. SHELL outlives
// the shell it names -- one uninstalled since the login began, a path carried
// over from another machine's dotfiles -- and a shell pane started on it did
// not start at all, saying only that the program was not found, when the
// fallback that stands in for SHELL being unset was there to be used.
func shellArgsFor(goos string, getenv func(string) string, look func(string) (string, error)) []string {
	runnable := func(p string) bool {
		_, err := look(p)
		return err == nil
	}
	if goos == "windows" {
		if ps, err := look("pwsh"); err == nil {
			return []string{ps, "-NoLogo"}
		}
		if comspec := getenv("COMSPEC"); comspec != "" && runnable(comspec) {
			return []string{comspec}
		}
		return []string{"powershell", "-NoLogo"}
	}
	if sh := getenv("SHELL"); sh != "" && runnable(sh) {
		return []string{sh, "-l"}
	}
	return []string{"/bin/sh"}
}
