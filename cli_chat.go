package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/jmwri/flockdeck/internal/chat"
	"github.com/jmwri/flockdeck/internal/chat/tool"
)

// runChat implements the `chat` subcommand: Flockdeck's own chat client, run inside
// a pane's pseudo-terminal and talking straight to a model API.
//
// It is a subcommand rather than a second binary for the same reason `hook` and
// `spawn` are: there is one artifact to ship, and a pane that can run Flockdeck can
// run everything Flockdeck does. The pane's callback address, its token and its id
// come from the environment it was started with, so a chat in a pane reports
// its own status without being told how.
func runChat(args []string) error {
	opts, err := chat.ParseArgs(args, os.Stderr)
	if errors.Is(err, chat.ErrHelpShown) {
		return nil
	}
	if err != nil {
		return err
	}
	if opts.Cwd == "" {
		opts.Cwd, _ = os.Getwd()
	}
	opts.Tools = chatTools(opts.Cwd)
	return chat.Run(context.Background(), opts)
}

// chatTools are what the model in a chat pane can do besides talk: read and
// write files, look around the directory, and run a command, each of them
// confined to the pane's own working directory.
//
// A pane whose directory cannot be resolved gets no tools rather than no pane.
// The confinement every tool depends on is that resolved directory, so a tool
// set built without one would be a tool set confined to nothing; a conversation
// with no tools is a perfectly good thing for a pane to be, and it says so.
func chatTools(cwd string) []chat.Tool {
	set, err := tool.New(cwd)
	if err != nil {
		fmt.Fprintln(os.Stderr, "flockdeck chat: no tools in this pane:", err)
		return nil
	}
	tools := set.Tools()
	out := make([]chat.Tool, 0, len(tools))
	for _, t := range tools {
		out = append(out, chatTool{t})
	}
	return out
}

// chatTool presents one of the pane's tools to the chat loop.
//
// The two halves were built to the same description and describe a tool
// slightly differently: the tools carry a name and a typed parameter object,
// the loop wants a description and the raw JSON Schema to hand whichever wire
// format it is talking. Translating here keeps that difference out of both.
type chatTool struct{ t tool.Tool }

func (c chatTool) Name() string { return c.t.Name() }

func (c chatTool) Describe() chat.Schema {
	s := c.t.Describe()
	params, err := json.Marshal(s.Params)
	if err != nil {
		// A schema that will not encode would be sent as an empty object,
		// which reads to the model as a tool taking no arguments at all.
		// Describing it as taking nothing it can name is the safer lie.
		params = nil
	}
	return chat.Schema{Description: s.Description, Params: params}
}

func (c chatTool) Approval(args json.RawMessage) string { return c.t.Approval(args) }

func (c chatTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	return c.t.Run(ctx, args)
}

// AlwaysKey lets a tool that can be approved in bulk say so. Only run_command
// offers it, as the command prefix -- "go test", "npm run" -- that the user
// would be agreeing to for the rest of the session.
func (c chatTool) AlwaysKey(args json.RawMessage) string {
	p, ok := c.t.(interface {
		Prefix(json.RawMessage) string
	})
	if !ok {
		return ""
	}
	return p.Prefix(args)
}
