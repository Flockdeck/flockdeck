package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/jmwri/perch/internal/agent"
	"github.com/jmwri/perch/internal/creds"
)

// `perch keys` is how an API key gets into Perch without going anywhere it
// should not.
//
// `set` takes the key on standard input rather than as an argument, which is
// the whole reason the subcommand exists: an argument is in the process list
// while it runs and in the shell's history file long afterwards, and a key
// that has been in either has to be treated as spent. Nothing here prints a
// key back, `list` included — what is worth knowing is that there is one and
// where it came from, and the answer to "what is it" is the vendor's console.

// runKeys implements the `keys` subcommand. It is the entry point the
// top-level command dispatches to.
func runKeys(args []string) error {
	k := keysIO{in: os.Stdin, out: os.Stdout}
	// The prompt is only written when somebody is there to read it. Piped
	// into, `perch keys set` is a script, and a script's output should not
	// gain a line of instructions addressed to a person.
	if stdinIsTerminal() {
		k.prompt = os.Stderr
	}
	return keysCmd(args, k)
}

// `perch keys` is dispatched by main, beside `hook`, `spawn` and `chat`.

// keysIO is where the subcommand reads the key from and writes its output to,
// gathered so the command can be driven by a test without a terminal. A nil
// prompt means nobody is watching.
type keysIO struct {
	in     io.Reader
	out    io.Writer
	prompt io.Writer
}

func keysCmd(args []string, kio keysIO) error {
	if len(args) == 0 {
		keysUsage(kio.out)
		return nil
	}
	switch args[0] {
	case "list", "ls":
		return keysList(kio.out)
	case "set":
		if len(args) != 2 {
			keysUsage(kio.out)
			return errors.New("usage: perch keys set <agent>")
		}
		return keysSet(args[1], kio)
	case "clear", "rm":
		if len(args) != 2 {
			keysUsage(kio.out)
			return errors.New("usage: perch keys clear <agent>")
		}
		return keysClear(args[1], kio.out)
	case "-h", "--help", "help":
		keysUsage(kio.out)
		return nil
	}
	keysUsage(kio.out)
	return fmt.Errorf("unknown command %q", args[0])
}

func keysUsage(out io.Writer) {
	fmt.Fprintf(out, "Usage: perch keys <command>\n\n")
	fmt.Fprintf(out, "Commands:\n")
	fmt.Fprintf(out, "  list           show which agents have a key, and where it came from\n")
	fmt.Fprintf(out, "  set <agent>    read a key from standard input and store it\n")
	fmt.Fprintf(out, "  clear <agent>  forget a stored key\n\n")
	fmt.Fprintf(out, "The key is read from standard input so it stays out of your shell history.\n")
	fmt.Fprintf(out, "An agent whose key is already exported in the environment needs none of this.\n")
}

// keysList prints one line per agent that could want a key.
func keysList(out io.Writer) error {
	specs := keysAgents()
	statuses := creds.StatusAll(specs)

	// A key stored for an id no longer in the catalog still exists, and the
	// only way to be told it is there — and so to clear it — is to list it.
	known := make(map[string]bool, len(statuses))
	for _, s := range statuses {
		known[s.Agent] = true
	}
	stored, err := creds.Names()
	if err != nil {
		return err
	}
	for _, id := range stored {
		if !known[id] {
			statuses = append(statuses, creds.Status{Agent: id, Set: true, Source: creds.SourceStore})
		}
	}

	if len(statuses) == 0 {
		fmt.Fprintln(out, "no agents that use a key")
		return nil
	}
	width := 0
	for _, s := range statuses {
		if len(s.Agent) > width {
			width = len(s.Agent)
		}
	}
	for _, s := range statuses {
		fmt.Fprintf(out, "%-*s  %s\n", width, s.Agent, s.Describe())
	}
	return nil
}

// keysSet reads one key from standard input and stores it.
func keysSet(agentID string, kio keysIO) error {
	if kio.prompt != nil {
		// Said plainly, because the terminal echoes what is typed and this
		// package cannot turn that off without leaving the standard library.
		// Pasting into a terminal that is showing the paste is still better
		// than an argument, which the shell keeps.
		fmt.Fprintf(kio.prompt, "Paste the key for %s and press Enter (it will be visible): ", agentID)
	}
	key, err := readKeyLine(kio.in)
	if kio.prompt != nil {
		fmt.Fprintln(kio.prompt)
	}
	if err != nil {
		return err
	}
	if key == "" {
		return errors.New("nothing was given on standard input")
	}
	if err := creds.Set(agentID, key); err != nil {
		return err
	}
	// The confirmation names the agent and not the key, which is the rule
	// everywhere else here too.
	fmt.Fprintf(kio.out, "stored a key for %s\n", agentID)
	return nil
}

func keysClear(agentID string, out io.Writer) error {
	had, err := creds.Clear(agentID)
	if err != nil {
		return err
	}
	if !had {
		fmt.Fprintf(out, "no stored key for %s\n", agentID)
		return nil
	}
	fmt.Fprintf(out, "forgot the stored key for %s\n", agentID)
	return nil
}

// readKeyLine reads the first line of input and nothing after it.
//
// Only the first line is taken so that a key pasted with a trailing newline,
// or one piped in from a file that has more in it, comes out the same. End of
// input without a newline is a key too — `printf %s "$k" | perch keys set` —
// so an EOF with something before it is not an error.
func readKeyLine(r io.Reader) (string, error) {
	line, err := bufio.NewReader(r).ReadString('\n')
	line = strings.TrimSpace(line)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read the key: %w", err)
	}
	return line, nil
}

// stdinIsTerminal reports whether there is a person at the other end of
// standard input, as opposed to a pipe or a file.
func stdinIsTerminal() bool {
	fi, err := os.Stdin.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// keysAgents is the list of agents that can want a key.
//
// This is a shim. It belongs in the catalog, which another task owns and this
// branch may not touch; when the catalog arrives this whole function becomes a
// filter over it, and the entries below go with it. They are the built-in API
// runners of the design's section 4, with the environment variables each
// vendor's own tooling already uses, so that a key already exported for the
// vendor's CLI is found without being copied anywhere.
func keysAgents() []agent.Spec {
	return []agent.Spec{
		{ID: "anthropic", Name: "Anthropic API", Runner: agent.RunnerAPI,
			API: agent.APISpec{Wire: "anthropic", KeyEnv: []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN"}}},
		{ID: "openai", Name: "OpenAI API", Runner: agent.RunnerAPI,
			API: agent.APISpec{Wire: "openai", KeyEnv: []string{"OPENAI_API_KEY"}}},
		{ID: "google", Name: "Google Gemini API", Runner: agent.RunnerAPI,
			API: agent.APISpec{Wire: "gemini", KeyEnv: []string{"GEMINI_API_KEY", "GOOGLE_API_KEY"}}},
		{ID: "openai-compatible", Name: "OpenAI-compatible endpoint", Runner: agent.RunnerAPI,
			API: agent.APISpec{Wire: "openai", KeyEnv: []string{"OPENAI_API_KEY"}}},
	}
}
