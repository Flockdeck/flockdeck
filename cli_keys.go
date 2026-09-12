package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/chat"
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
	case "endpoint", "url":
		if len(args) != 2 && len(args) != 3 {
			keysUsage(kio.out)
			return fmt.Errorf("usage: flockdeck keys endpoint <agent> [<url> | default], where <agent> is one of %s", strings.Join(keyAgentIDs(), ", "))
		}
		if len(args) == 2 {
			return keysShowEndpoint(args[1], kio.out)
		}
		return keysSetEndpoint(args[1], args[2], kio.out)
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
	fmt.Fprintf(out, "  clear <agent>  forget a stored key\n")
	fmt.Fprintf(out, "  endpoint <agent> [<url> | default]\n")
	fmt.Fprintf(out, "                 show the address an agent talks to, or change it: a local\n")
	fmt.Fprintf(out, "                 model server, a gateway; default goes back to the vendor's\n\n")
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
	// Where an agent talks to somewhere other than its vendor, that is said
	// beside its key: the two are what decide whether a pane can answer, and
	// this listing is where somebody checking either one looks.
	endpoints := map[string]string{}
	for _, s := range specs {
		endpoints[s.ID] = s.API.BaseURL
	}
	for _, s := range statuses {
		line := s.Describe()
		if u := endpoints[s.Agent]; u != "" {
			if s.NotNeeded && !s.Set {
				// Already said to talk to a model on this machine; the
				// address says which, rather than saying it again.
				line += ", at " + redactURL(u)
			} else {
				line += "; talks to " + redactURL(u)
			}
		}
		fmt.Fprintf(out, "%-*s  %s\n", width, s.Agent, line)
	}
	return nil
}

// keysSet reads one key from standard input and stores it.
func keysSet(agentID string, kio keysIO) error {
	// The id is checked before the key is asked for: a key stored under a
	// mistyped id is one nothing will ever read, leaving the agent still
	// asking for one, and a key pasted only to be refused is pasted twice.
	// An agent that is being added goes into agents.json first.
	if ids := keyAgentIDs(); !slices.Contains(ids, agentID) {
		return fmt.Errorf("no agent called %s takes a key: the ones that do are %s (an agent of your own goes in agents.json first)",
			agentID, strings.Join(ids, ", "))
	}
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
	// A key in the environment is used before a stored one, so one there
	// makes the key just stored one that nothing reads: somebody replacing a
	// refused key this way would go on having it refused.
	if spec, err := keyAgentSpec(agentID); err == nil {
		if st := creds.StatusOf(spec); st.Source == creds.SourceEnv {
			fmt.Fprintf(kio.out, "but %s is set in this environment, and is used before the stored key by anything started from it;\n", st.Env)
			fmt.Fprintf(kio.out, "unset %s for the stored key to be used\n", st.Env)
			return nil
		}
	}
	// A key the API has just refused is not one new panes should be told
	// they will use: the refusal says what to do instead.
	if kio.prompt != nil && checkKey(agentID, key, kio.out) {
		return nil
	}
	fmt.Fprintf(kio.out, "new panes use it; one already running picks it up when its old key is refused, or when it is restarted\n")
	return nil
}

// checkKey asks the agent's endpoint whether it takes the key just stored,
// where somebody is at the terminal to be told. A key pasted with a character
// missing, or the wrong one of two, was otherwise found out at the first prompt
// of a pane. The key is stored whatever the answer: an endpoint that cannot be
// reached now says nothing about the key. A script is not sent anywhere.
//
// It reports whether the key was refused.
func checkKey(agentID, key string, out io.Writer) (refused bool) {
	spec, err := keyAgentSpec(agentID)
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	switch err := chat.CheckKey(ctx, spec.API.Wire, spec.API.BaseURL, key); {
	case err == nil:
		fmt.Fprintf(out, "checked: the API accepted it\n")
	case errors.Is(err, chat.ErrKeyRefused):
		fmt.Fprintf(out, "but %v: check it was copied whole, and run this again to replace it\n", err)
		return true
	case errors.Is(err, chat.ErrUnreachable):
		where := "the vendor's endpoint"
		if spec.API.BaseURL != "" {
			where = redactURL(spec.API.BaseURL)
		}
		fmt.Fprintf(out, "could not reach %s to check it; it is stored, and a pane will say if it is refused\n", where)
	default:
		fmt.Fprintf(out, "could not check it just now: %v\n", err)
	}
	return false
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

// keysShowEndpoint says which address an agent talks to.
func keysShowEndpoint(agentID string, out io.Writer) error {
	spec, err := keyAgentSpec(agentID)
	if err != nil {
		return err
	}
	if spec.API.BaseURL == "" {
		fmt.Fprintf(out, "%s talks to the vendor's own endpoint\n", agentID)
		return nil
	}
	fmt.Fprintf(out, "%s talks to %s\n", agentID, redactURL(spec.API.BaseURL))
	return nil
}

// keysSetEndpoint changes the address an agent talks to, in agents.json.
//
// Pointing an agent at a local model server or a gateway is one address, and
// it was the one setting of an API agent that could only be changed by opening
// agents.json and knowing that it goes under api.baseURL in an entry with the
// agent's id. Only that field is touched: whatever else the entry and the file
// hold is written back as it was.
func keysSetEndpoint(agentID, address string, out io.Writer) error {
	if _, err := keyAgentSpec(agentID); err != nil {
		return err
	}
	builtin := false
	for _, s := range agent.Builtins() {
		builtin = builtin || s.ID == agentID
	}
	if address == "default" {
		if !builtin {
			// An agent of the user's own is the address it was given; without
			// one it would be talking to whichever vendor its wire names.
			return fmt.Errorf("%s is an agent of your own, and has no default address to go back to; give it another one instead", agentID)
		}
		address = ""
	} else {
		u, err := url.Parse(address)
		switch {
		case err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "":
			return fmt.Errorf("give the address as http://host:port/... or https://..., for example http://127.0.0.1:11434/v1")
		case u.User != nil:
			// Said without repeating the address, which has a secret in it.
			return fmt.Errorf("the address has a name or password in it; store the key with `flockdeck keys set %s` and give the address without it", agentID)
		}
	}
	path, err := agent.ConfigPath()
	if err != nil {
		return err
	}
	if err := setAgentEndpoint(filepath.Dir(path), agentID, address); err != nil {
		return err
	}
	if address == "" {
		fmt.Fprintf(out, "%s talks to the vendor's own endpoint again\n", agentID)
	} else {
		fmt.Fprintf(out, "%s now talks to %s\n", agentID, address)
	}
	fmt.Fprintf(out, "new panes use it; one already running keeps the address it started with until it is restarted\n")
	return nil
}

// setAgentEndpoint writes an agent's api.baseURL into agents.json in dir, or
// takes it out where address is "". The agent's last entry is the one changed,
// since a later entry is merged over an earlier one; an agent with no entry
// gets one holding the address alone, which is merged over the built-in.
func setAgentEndpoint(dir, agentID, address string) error {
	f, err := agent.ReadConfig(dir)
	if err != nil {
		// A file that cannot be read is not written over: it holds the only
		// copy of the user's own agents.
		return err
	}
	var entry map[string]json.RawMessage
	at := -1
	for i, raw := range f.Agents {
		var head struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(raw, &head) == nil && head.ID == agentID {
			entry, at = nil, i
			if err := json.Unmarshal(raw, &entry); err != nil {
				return fmt.Errorf("%s: the entry for %s: %w", agent.ConfigName, agentID, err)
			}
		}
	}
	if entry == nil {
		id, _ := json.Marshal(agentID)
		entry = map[string]json.RawMessage{"id": id}
	}
	api := map[string]json.RawMessage{}
	if raw, ok := entry["api"]; ok {
		if err := json.Unmarshal(raw, &api); err != nil {
			return fmt.Errorf("%s: the api of %s: %w", agent.ConfigName, agentID, err)
		}
	}
	if address == "" {
		delete(api, "baseURL")
	} else {
		api["baseURL"], _ = json.Marshal(address)
	}
	if len(api) == 0 {
		delete(entry, "api")
	} else {
		entry["api"], _ = json.Marshal(api)
	}
	raw, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	if at >= 0 {
		f.Agents[at] = raw
	} else {
		f.Agents = append(f.Agents, raw)
	}
	return agent.WriteConfig(dir, f)
}

// keyAgentSpec is the catalog's entry for an agent that takes a key, or an
// error naming the ones there are.
func keyAgentSpec(agentID string) (agent.Spec, error) {
	ids := keyAgentIDs()
	if slices.Contains(ids, agentID) {
		for _, s := range keysAgents() {
			if s.ID == agentID {
				return s, nil
			}
		}
	}
	return agent.Spec{}, fmt.Errorf("no agent called %s talks to an API: the ones that do are %s", agentID, strings.Join(ids, ", "))
}

// redactURL is an address as it can be shown: without a password in it, where
// one was written into agents.json by hand.
func redactURL(s string) string {
	if u, err := url.Parse(s); err == nil {
		return u.Redacted()
	}
	return s
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
