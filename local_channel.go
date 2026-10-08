package main

import (
	"fmt"
	"os"
	"time"

	"github.com/jmwri/flockdeck/internal/channel"
	"github.com/jmwri/flockdeck/internal/server"
	"github.com/jmwri/flockdeck/internal/store"
)

// startChannel makes the local control channel beside the loopback server and
// returns what stops it. Nothing reads the channel's address yet: it is not
// recorded in instance.json. If no safe place for it can be made, the
// instance runs without one and says so on stderr and in error.log.
func startChannel(srv *server.Server, started time.Time) (stop func()) {
	say := func(format string, args ...any) {
		err := fmt.Errorf(format, args...)
		fmt.Fprintln(os.Stderr, "flockdeck:", err)
		logError(err)
	}
	say0 := func(msg string) { say("%s", msg) }
	dir, err := store.Dir()
	if err != nil {
		say("running without a local channel: %v", err)
		return func() {}
	}
	ch, err := channel.Start(channel.Config{
		StateDir: dir,
		PID:      os.Getpid(),
		Started:  started,
		Version:  version,
		Window:   srv.WindowURL,
		Logf:     say,
		// Nothing the user does depends on the channel yet, so its loss is
		// logged and not shown in a window.
		OnLost: say0,
	})
	if err != nil {
		say("running without a local channel: %v", err)
		return func() {}
	}
	return func() { _ = ch.Close() }
}
