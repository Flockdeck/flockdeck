package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/jmwri/flockdeck/internal/acp/claudeacp"
	"github.com/jmwri/flockdeck/internal/acp/codexacp"
)

// runACPAdapter is `flockdeck acp-adapter <agent>`: an ACP agent on stdin and
// stdout for an agent that has no ACP of its own. It is hidden, and it is
// started by Flockdeck's ACP client rather than by a person, so it is a
// subcommand for the same reason `hook` is: one artifact to ship.
//
// FLOCKDECK_CLAUDE_BIN and FLOCKDECK_CODEX_BIN name the executable of the
// agent when it is not on PATH. They are read here, never passed on: the
// adapters strip FLOCKDECK_ variables from the environment of the agent they
// start.
func runACPAdapter(args []string) error {
	if len(args) != 1 || (args[0] != "claude" && args[0] != "codex") {
		return fmt.Errorf("usage: flockdeck acp-adapter claude|codex")
	}
	ctx, stop := signal.NotifyContext(context.Background(), stopSignals()...)
	defer stop()
	if args[0] == "codex" {
		return codexacp.Run(ctx, os.Stdin, os.Stdout, codexacp.Options{
			CodexPath: os.Getenv("FLOCKDECK_CODEX_BIN"),
			Version:   version,
			Stderr:    os.Stderr,
		})
	}
	return claudeacp.Run(ctx, os.Stdin, os.Stdout, claudeacp.Options{
		ClaudePath: os.Getenv("FLOCKDECK_CLAUDE_BIN"),
		Version:    version,
		Stderr:     os.Stderr,
	})
}
