package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/jmwri/flockdeck/internal/acp/claudeacp"
)

// runACPAdapter is `flockdeck acp-adapter <agent>`: an ACP agent on stdin and
// stdout for an agent that has no ACP of its own. It is hidden, and it is
// started by Flockdeck's ACP client rather than by a person, so it is a
// subcommand for the same reason `hook` is: one artifact to ship.
//
// FLOCKDECK_CLAUDE_BIN names the claude executable when it is not on PATH. It
// is read here, never passed on: the adapter strips FLOCKDECK_ variables from
// the environment of the agent it starts.
func runACPAdapter(args []string) error {
	if len(args) != 1 || args[0] != "claude" {
		return fmt.Errorf("usage: flockdeck acp-adapter claude")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return claudeacp.Run(ctx, os.Stdin, os.Stdout, claudeacp.Options{
		ClaudePath: os.Getenv("FLOCKDECK_CLAUDE_BIN"),
		Version:    version,
		Stderr:     os.Stderr,
	})
}
