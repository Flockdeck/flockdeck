package main

import (
	"context"
	"errors"
	"os"

	"github.com/jmwri/perch/internal/chat"
)

// runChat implements the `chat` subcommand: Perch's own chat client, run inside
// a pane's pseudo-terminal and talking straight to a model API.
//
// It is a subcommand rather than a second binary for the same reason `hook` and
// `spawn` are: there is one artifact to ship, and a pane that can run Perch can
// run everything Perch does. The pane's callback address, its token and its id
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

// chatTools are what the model in a chat pane can do besides talk.
//
// The loop already handles everything around a tool -- asking for one, the
// approval, the lifecycle events that turn the pane amber, feeding the answer
// back -- so the tools themselves are the only thing missing, and they are
// built separately behind chat.Tool. Until they land the pane is a conversation
// and nothing more, which is a perfectly good thing for a pane to be.
func chatTools(cwd string) []chat.Tool { return nil }
