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
func startChannel(srv *server.Server) (stop func()) {
	say := func(format string, args ...any) {
		err := fmt.Errorf(format, args...)
		fmt.Fprintln(os.Stderr, "flockdeck:", err)
		logError(err)
	}
	dir, err := store.Dir()
	if err != nil {
		say("running without a local channel: %v", err)
		return func() {}
	}
	ch, err := channel.Start(channel.Config{
		StateDir: dir,
		PID:      os.Getpid(),
		Started:  time.Now(),
		Version:  version,
		Window:   srv.WindowURL,
		Logf:     say,
		OnLost:   func(msg string) { srv.NotifyWindows(msg, true) },
	})
	if err != nil {
		say("running without a local channel: %v", err)
		return func() {}
	}
	return func() { _ = ch.Close() }
}
