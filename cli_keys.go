package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/creds"
)

// `flockdeck keys` is how an API key gets into Flockdeck without going anywhere it
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
	// into, `flockdeck keys set` is a script, and a script's output should not
	// gain a line of instructions addressed to a person.
	if stdinIsTerminal() {
		k.prompt = os.Stderr
	}
	return keysCmd(args, k)
}

// `flockdeck keys` is dispatched by main, beside `hook`, `spawn` and `chat`.

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
			return fmt.Errorf("usage: flockdeck keys set <agent>, where <agent> is one of %s", strings.Join(keyAgentIDs(), ", "))
		}
		return keysSet(args[1], kio)
	case "clear", "rm":
		if len(args) != 2 {
			keysUsage(kio.out)
			return fmt.Errorf("usage: flockdeck keys clear <agent>, where <agent> is one of %s", strings.Join(keyAgentIDs(), ", "))
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
	fmt.Fprintf(out, "Usage: flockdeck keys <command>\n\n")
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
	// A key stored under a mistyped id is a key nothing will ever read, and
	// the only sign would be the agent still asking for one. It is kept all
	// the same -- the id may be an agent about to be added to agents.json --
	// but the ids that do take a key are named.
	ids := keyAgentIDs()
	if !slices.Contains(ids, agentID) {
		fmt.Fprintf(kio.out, "note: no agent called %s takes a key yet; the ones that do are %s\n", agentID, strings.Join(ids, ", "))
	}
	return nil
}

// keyAgentIDs are the ids of the agents that take a key, in catalog order.
func keyAgentIDs() []string {
	var ids []string
	for _, s := range creds.StatusAll(keysAgents()) {
		ids = append(ids, s.Agent)
	}
	return ids
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
// input without a newline is a key too — `printf %s "$k" | flockdeck keys set` —
// so an EOF with something before it is not an error.
func readKeyLine(r io.Reader) (string, error) {
	line, err := bufio.NewReader(r).ReadString('\n')
	// A byte-order mark is what PowerShell puts in front of piped input when
	// its output encoding is set to UTF-8, and `type` passes on the one a file
	// saved by Notepad starts with. It is not part of any key, and stored with
	// one the key is refused by the vendor for reasons nobody could see.
	line = strings.TrimSpace(strings.TrimPrefix(line, "\xef\xbb\xbf"))
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

// keysAgents is the list of agents that can want a key: the catalog, with the
// user's agents.json merged in, of which creds.StatusAll keeps the API runners.
//
// It is the catalog rather than a list of its own so that `keys list` says what
// a pane will actually find. A list kept here had drifted from it -- it named
// ANTHROPIC_AUTH_TOKEN, which nothing that starts a pane reads, and so reported
// a key as set for an agent that would then fail for want of one -- and it
// could not see an agent the user had added.
func keysAgents() []agent.Spec {
	return agent.Load().Specs
}
