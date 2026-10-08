package main

import (
	"errors"
	"fmt"

	"github.com/jmwri/flockdeck/internal/server"
	"github.com/jmwri/flockdeck/internal/store"
)

// `flockdeck remote artifacts off` is the kill switch for viewing this
// machine's recordings and files from a paired device. It works with no window
// open: it switches every kind off in the preferences file, which the artifacts
// socket reads on every request, and then asks a running instance to close the
// sockets it has now.
//
// There is no `on`. What a device may view is widened at the desk, in the
// window, where the acknowledgement is shown.

func remoteArtifactsCmd(args []string, rio remoteIO) error {
	if len(args) == 0 || args[0] != "off" {
		if len(args) > 0 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help") {
			return remoteHelp("artifacts", rio)
		}
		fs := remoteFlags("artifacts")
		fs.SetOutput(rio.out)
		fs.Usage()
		return errors.New("say `flockdeck remote artifacts off`")
	}
	if err := parseRemote(remoteFlags("artifacts"), args[1:]); err != nil {
		return err
	}
	// Read first, and refuse if the file cannot be read: saving defaults with
	// one change over a file that exists would lose every other setting.
	p, err := store.ReadPrefs()
	if err != nil {
		return fmt.Errorf("the preferences could not be read, so nothing was changed: %w", err)
	}
	if p.RemoteArtifacts.AllOff() {
		if err := store.SavePrefs(p); err != nil {
			return fmt.Errorf("could not save the preferences: %w", err)
		}
		fmt.Fprintln(rio.out, "Remote artifacts switched off for every kind")
	} else {
		fmt.Fprintln(rio.out, "Remote artifacts were already off")
	}
	if rio.stopArtifacts == nil {
		return nil
	}
	ran, err := rio.stopArtifacts()
	switch {
	case err != nil:
		fmt.Fprintln(rio.out, fitted(fmt.Sprintf("the running flockdeck could not be told (%v); open artifact sockets close at the next request, or restart it", err)))
	case ran:
		fmt.Fprintln(rio.out, "The running flockdeck has closed its artifact sockets")
	}
	return nil
}

// stopRunningArtifacts tells the running instance, if there is one, to reread
// the preferences and close every artifacts socket.
func stopRunningArtifacts() (bool, error) {
	inst, base, err := runningInstance()
	if err != nil {
		return false, err
	}
	if inst == nil {
		return false, nil
	}
	if err := server.RequestArtifactsStop(base, inst.Token); err != nil {
		return true, errors.New(redactToken(err.Error(), inst.Token))
	}
	return true, nil
}
