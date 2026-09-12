package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
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
		k.hide = hideInput
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
	// hide stops the terminal showing what is typed, returning what puts it
	// back, or nil where it cannot. It is nil where there is no terminal.
	hide func() func()
}

func keysCmd(args []string, kio keysIO) error {
	if len(args) == 0 {
		// What somebody typing the bare command wants to know is which agents
		// have a key; the listing answers it, and the usage after it says how
		// to change what it shows.
		if err := keysList(kio.out); err != nil {
			return err
		}
		fmt.Fprintln(kio.out)
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
	// A key pasted into a terminal that shows it is on the screen, in the
	// scrollback, and in anything shared of either, so the terminal is asked
	// not to show it. Where it cannot be, that is said plainly: pasting into a
	// terminal that shows the paste is still better than an argument, which
	// the shell keeps.
	var restore func()
	if kio.hide != nil {
		restore = kio.hide()
	}
	if restore != nil {
		// Ctrl+C at the prompt must not leave the terminal hiding everything
		// typed into it afterwards.
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt)
		done := make(chan struct{})
		go func() {
			select {
			case <-sig:
				restore()
				os.Exit(130)
			case <-done:
			}
		}()
		defer func() {
			signal.Stop(sig)
			close(done)
		}()
	}
	if kio.prompt != nil {
		shown := "it will be visible"
		if restore != nil {
			shown = "it will not be shown"
		}
		fmt.Fprintf(kio.prompt, "Paste the key for %s and press Enter (%s): ", agentID, shown)
	}
	key, err := readKeyLine(kio.in)
	if restore != nil {
		restore()
	}
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
	// everywhere else here too. It says when the key takes effect, because a
	// pane already running still holds the key it started with.
	fmt.Fprintf(kio.out, "stored a key for %s\n", agentID)
	fmt.Fprintf(kio.out, "new panes use it; one already running picks it up when its old key is refused, or when it is restarted\n")
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
		// The usual reason to clear a key is that the agent is using one it
		// should not, and where that one comes from the environment, clearing
		// the store changes nothing; saying where it does come from does.
		for _, s := range keysAgents() {
			if st := creds.StatusOf(s); s.ID == agentID && st.Source == creds.SourceEnv {
				fmt.Fprintf(out, "its key comes from %s in the environment, which this cannot clear\n", st.Env)
			}
		}
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
//
// Git Bash's window hands a program a pipe even when a person is typing into
// it, and taken for a script's pipe it got no prompt: `flockdeck keys set`
// there sat waiting with nothing on the screen to say what for.
func stdinIsTerminal() bool {
	fi, err := os.Stdin.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0 || isMsysTerminal(os.Stdin)
}

// isMsysPtyName reports whether a pipe's name is one mintty or another Cygwin
// or MSYS2 terminal gives the pty behind it: \msys-<id>-pty<n>-from-master,
// or -to-master, or the same with cygwin.
func isMsysPtyName(name string) bool {
	if !strings.HasPrefix(name, `\msys-`) && !strings.HasPrefix(name, `\cygwin-`) {
		return false
	}
	return strings.Contains(name, "-pty") &&
		(strings.HasSuffix(name, "-from-master") || strings.HasSuffix(name, "-to-master"))
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
