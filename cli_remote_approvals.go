package main

import (
	"errors"
	"fmt"
	"slices"

	"github.com/jmwri/flockdeck/internal/server"
	"github.com/jmwri/flockdeck/internal/store"
)

// `flockdeck remote approvals off` is the kill switch for answering an agent's
// permission requests from a paired device's phone. It works with no window
// open: it switches approval off for every device in the preferences file,
// which the host reads again before it accepts a decision, and then asks a
// running instance to do the same in memory and close the sockets it has now.
// `status` lists the devices it is on for.
//
// There is no `on`. Approval is turned on at the desk, in the window, for one
// device at a time, once that device's key is verified and the person has read
// what the device will be shown.

func remoteApprovalsCmd(args []string, rio remoteIO) error {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help") {
		return remoteHelp("approvals", rio)
	}
	if len(args) == 0 || (args[0] != "off" && args[0] != "status") {
		fs := remoteFlags("approvals")
		fs.SetOutput(rio.out)
		fs.Usage()
		if len(args) > 0 && args[0] == "on" {
			return errors.New("there is no `flockdeck remote approvals on`: a device is allowed to approve at the desk, in the window, once its key is verified")
		}
		return errors.New("say `flockdeck remote approvals off` or `flockdeck remote approvals status`")
	}
	if err := parseRemote(remoteFlags("approvals"), args[1:]); err != nil {
		return err
	}
	// Read first, and do not write if the file cannot be read: saving defaults
	// with one change over a file that exists would lose every other setting.
	p, readErr := store.ReadPrefs()
	if args[0] == "status" {
		if readErr != nil {
			return fmt.Errorf("the preferences could not be read: %w", readErr)
		}
		return approvalsStatus(p, rio)
	}
	// The running instance is told whether or not the file could be read or
	// written: its record is the one in force, and the switch must reach it.
	var fileErr error
	switch {
	case readErr != nil:
		fileErr = fmt.Errorf("the preferences could not be read, so the file was not changed: %w", readErr)
	default:
		next, was := p.WithoutApprovals()
		if len(was) > 0 || len(next.Devices) != len(p.Devices) {
			if err := store.SavePrefs(next); err != nil {
				fileErr = fmt.Errorf("could not save the preferences, so the file was not changed: %w", err)
			}
		}
		switch {
		case fileErr != nil:
		case len(was) > 0:
			fmt.Fprintf(rio.out, "Approval switched off for %d device%s\n", len(was), map[bool]string{true: "", false: "s"}[len(was) == 1])
		default:
			fmt.Fprintln(rio.out, "No device was allowed to approve")
		}
	}
	if rio.stopApprovals != nil {
		ran, err := rio.stopApprovals()
		switch {
		case err != nil:
			fmt.Fprintln(rio.out, fitted(fmt.Sprintf("the running flockdeck could not be told (%v). Its own record may still allow approval and its open sockets stay open until it is restarted", err)))
		case ran && fileErr != nil:
			fmt.Fprintln(rio.out, "The running flockdeck has switched approval off in memory and closed its sockets")
		case ran:
			fmt.Fprintln(rio.out, "The running flockdeck has switched approval off and closed its sockets")
		}
	}
	return fileErr
}

// approvalsStatus says which devices the preferences file lets approve at the
// wording in force. It is what the file says; the running instance also needs
// the device's key to be the one the grant was made for.
func approvalsStatus(p store.Prefs, rio remoteIO) error {
	var on []string
	for id, a := range p.Devices {
		if a.ApproveOn(server.ApproveAckVersion) {
			on = append(on, id)
		}
	}
	slices.Sort(on)
	if len(on) == 0 {
		fmt.Fprintln(rio.out, "No device is allowed to approve agent requests")
		return nil
	}
	fmt.Fprintf(rio.out, "Allowed to approve agent requests (%d):\n", len(on))
	for _, id := range on {
		fmt.Fprintf(rio.out, "  %s\n", id)
	}
	fmt.Fprintln(rio.out, "`flockdeck remote devices` lists the devices by name.")
	return nil
}

// stopRunningApprovals tells the running instance, if there is one, to switch
// approval off in memory and close every approvals socket.
func stopRunningApprovals() (bool, error) {
	inst, base, err := runningInstance()
	if err != nil {
		return false, err
	}
	if inst == nil {
		return false, nil
	}
	if err := server.RequestApprovalsStop(base, inst.Token); err != nil {
		return true, errors.New(redactToken(err.Error(), inst.Token))
	}
	return true, nil
}
