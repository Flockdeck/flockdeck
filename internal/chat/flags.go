package chat

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

// ErrHelpShown means the usage was asked for and printed: there is nothing
// wrong and nothing left to do.
var ErrHelpShown = errors.New("usage shown")

// ErrBadFlags means the command line was wrong. The flag set has already said
// which part of it, so this only points at where the rest is written down --
// repeating the complaint would bury it.
var ErrBadFlags = errors.New("run `perch chat -h` for the flags it takes")

// ParseArgs turns the arguments of `perch chat` into the options to run with.
//
// It lives here rather than beside main so that what the command accepts is
// tested where it is defined. Every flag has an environment variable behind it,
// because a pane's argv is built from an agent's Spec -- which can say
// `--wire openai` as a literal argument, or put the same thing in the pane's
// environment through Spec.Env -- and neither should be the only way.
func ParseArgs(args []string, out io.Writer) (Options, error) {
	var o Options
	var keyEnv string
	fs := flag.NewFlagSet("chat", flag.ContinueOnError)
	fs.SetOutput(out)
	fs.StringVar(&o.Agent, "agent", paneEnv("AGENT"), "the agent id this pane was started as")
	fs.StringVar(&o.Model, "model", paneEnv("MODEL"), "the model to ask for; empty leaves it to the endpoint")
	fs.StringVar(&o.Session, "session", "", "the conversation id, which is also the pane id")
	fs.BoolVar(&o.Resume, "resume", false, "carry on the conversation this session id already has")
	fs.StringVar(&o.Wire, "wire", paneEnv("WIRE"), "request shape: anthropic, openai or gemini")
	fs.StringVar(&o.BaseURL, "base-url", paneEnv("BASE_URL"), "endpoint root; empty means the vendor's own")
	fs.StringVar(&keyEnv, "key-env", paneEnv("KEY_ENV"), "comma-separated names an API key may arrive in")
	fs.IntVar(&o.MaxTokens, "max-tokens", 0, "ceiling on one answer, in tokens")
	fs.StringVar(&o.Cwd, "cwd", "", "the working directory; empty means this one")
	fs.Usage = func() { usage(fs, out) }

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return o, ErrHelpShown
		}
		return o, ErrBadFlags
	}

	for _, name := range strings.Split(keyEnv, ",") {
		if name = strings.TrimSpace(name); name != "" {
			o.KeyEnv = append(o.KeyEnv, name)
		}
	}
	// Everything left is the opening task. It arrives as separate arguments
	// when a shell split it, and as one when the argv was built from a Spec.
	o.Task = strings.TrimSpace(strings.Join(fs.Args(), " "))
	o.API = paneEnv("API")
	o.Token = paneEnv("TOKEN")
	// Colour is decided here rather than inside the client so that a caller
	// building Options itself -- a test, drawing to a buffer -- gets plain text
	// unless it asks for anything else.
	o.Colour = colourWanted()
	// A pane names itself, and a pane's id is its conversation id -- so a chat
	// started with no session named still resumes with the pane it belongs to.
	if o.Session == "" {
		o.Session = paneEnv("PANE")
	}
	return o, nil
}

func usage(fs *flag.FlagSet, out io.Writer) {
	fmt.Fprintf(out, "Usage: perch chat [flags] [--] [task]\n\n")
	fmt.Fprintf(out, "Holds a conversation with a model API in this terminal: no wrapper CLI,\n")
	fmt.Fprintf(out, "no node, no python. Run inside a Perch pane it reports its own status,\n")
	fmt.Fprintf(out, "records a transcript and can be resumed.\n\nFlags:\n")
	fs.PrintDefaults()
	fmt.Fprintf(out, "\nThe API key is read from the names given to -key-env, then from the\n")
	fmt.Fprintf(out, "conventional name for the wire, then from the keys Perch has been given.\n")
}

// paneEnv reads one of the variables a pane carries, accepting the name an
// earlier build used alongside the one in use now. A pane started by that build
// is still running with the old names in its environment, and a chat inside it
// should not lose its status reporting because the binary on PATH was upgraded.
func paneEnv(name string) string {
	if v := os.Getenv("PERCH_" + name); v != "" {
		return v
	}
	return os.Getenv("AGENT_WRAPPER_" + name)
}
