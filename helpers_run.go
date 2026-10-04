package main

import (
	"fmt"
	"os"
	"time"

	"github.com/jmwri/flockdeck/internal/helpers"
	"github.com/jmwri/flockdeck/internal/server"
	"github.com/jmwri/flockdeck/internal/store"
)

// startHelpers gives the server the supervisor and installer for helper apps,
// after ending any helper a crashed earlier run left behind and clearing the
// staging folders an interrupted install left. It returns a function that
// begins stopping every helper, for the shutdown to call first, and that
// itself returns a function waiting for them to have stopped. They stop
// together while the layout is saved, so the shutdown is not the sum of the
// two.
//
// A state directory that cannot be read means no helper apps this run; that
// is said and nothing else is affected.
func startHelpers(srv *server.Server) (begin func() (wait func())) {
	noop := func() func() { return func() {} }
	st, err := helpers.DefaultStore()
	if err != nil {
		fmt.Fprintln(os.Stderr, "flockdeck: helper apps are off:", err)
		return noop
	}
	sup := helpers.NewSupervisor(helpers.Config{
		Store:  st,
		Notify: func(helpers.Status) { srv.Wake() },
		// A record naming a live process may be another Flockdeck's helper
		// (-solo starts a second instance on purpose), so nothing is reaped
		// while one is running.
		OtherInstance: func() bool {
			inst, err := store.LoadInstance()
			return err == nil && inst != nil && inst.PID != os.Getpid() && inst.StillRunning()
		},
	})
	sup.ReapStale()
	for _, e := range helpers.Catalogue() {
		st.SweepStaging(e.ID, time.Now())
	}
	srv.SetHelpers(sup, helpers.NewInstaller(helpers.Options{Store: st, Busy: sup.Active}))
	return func() func() {
		done := make(chan struct{})
		go func() {
			defer close(done)
			sup.StopAll(shutdownGrace)
		}()
		return func() {
			select {
			case <-done:
			case <-time.After(shutdownGrace + 10*time.Second):
				fmt.Fprintln(os.Stderr, "flockdeck: a helper app was still stopping at exit")
			}
		}
	}
}
