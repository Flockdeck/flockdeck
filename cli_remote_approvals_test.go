package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/server"
	"github.com/jmwri/flockdeck/internal/store"
)

func runApprovals(t *testing.T, stop func() (bool, error), args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := remoteCmd(append([]string{"approvals"}, args...), remoteIO{out: &out, stopApprovals: stop})
	return out.String(), err
}

func approvalPrefs(t *testing.T) {
	t.Helper()
	p := store.LoadPrefs()
	p.SetUIValue(store.UIHelpSeen, true)
	now := time.Now().UTC()
	p, _ = p.WithAccess("d1", store.DeviceAccess{Role: store.RoleFull, Approve: true, ApproveV: server.ApproveAckVersion, ApproveKey: "k", ApproveAt: now})
	p, _ = p.WithAccess("d2", store.DeviceAccess{Role: store.RoleViewer, WatchPanes: true, Approve: true, ApproveV: server.ApproveAckVersion, ApproveKey: "k", ApproveAt: now})
	if err := store.SavePrefs(p); err != nil {
		t.Fatal(err)
	}
}

func TestRemoteApprovalsOffSwitchesEveryDeviceOffAndTellsTheRunningInstance(t *testing.T) {
	isolateKeys(t)
	approvalPrefs(t)
	told := 0
	out, err := runApprovals(t, func() (bool, error) { told++; return true, nil }, "off")
	if err != nil {
		t.Fatalf("approvals off: %v\n%s", err, out)
	}
	got := store.LoadPrefs()
	for _, id := range []string{"d1", "d2"} {
		if got.AccessFor(id).Approve {
			t.Errorf("%s may still approve", id)
		}
	}
	if a := got.AccessFor("d2"); a.EffectiveRole() != store.RoleViewer || !a.WatchPanes {
		t.Errorf("d2 = %+v, want its role kept", a)
	}
	if string(got.UI[store.UIHelpSeen]) != "true" {
		t.Error("off changed more than approval")
	}
	if told != 1 || !strings.Contains(out, "switched off for 2 devices") || !strings.Contains(out, "closed") {
		t.Errorf("told %d times, output:\n%s", told, out)
	}
	// Again: nothing on, but the running instance is still told, for sockets
	// and records that outlived an earlier attempt.
	out, err = runApprovals(t, func() (bool, error) { told++; return false, nil }, "off")
	if err != nil || told != 2 || !strings.Contains(out, "No device") {
		t.Errorf("second run: err %v told %d\n%s", err, told, out)
	}
}

func TestRemoteApprovalsOffReportsAnInstanceItCouldNotTell(t *testing.T) {
	isolateKeys(t)
	approvalPrefs(t)
	out, err := runApprovals(t, func() (bool, error) { return true, errors.New("connection refused") }, "off")
	if err != nil {
		t.Fatalf("a running instance that could not be told is not a failure of the switch: %v", err)
	}
	if !strings.Contains(out, "could not be told") || !strings.Contains(out, "stay open until it is restarted") {
		t.Errorf("output:\n%s", out)
	}
	if store.LoadPrefs().AccessFor("d1").Approve {
		t.Error("the file was not changed")
	}
}

func TestRemoteApprovalsHasNoOn(t *testing.T) {
	isolateKeys(t)
	approvalPrefs(t)
	for _, args := range [][]string{nil, {"on"}, {"off", "extra"}, {"status", "extra"}} {
		if _, err := runApprovals(t, nil, args...); err == nil {
			t.Errorf("approvals %v was accepted", args)
		}
	}
	_, err := runApprovals(t, nil, "on")
	if err == nil || !strings.Contains(err.Error(), "at the desk") {
		t.Errorf("approvals on: %v", err)
	}
	if !store.LoadPrefs().AccessFor("d1").Approve {
		t.Error("a refused command changed the file")
	}
}

func TestRemoteApprovalsStatusListsTheDevicesAndChangesNothing(t *testing.T) {
	isolateKeys(t)
	out, err := runApprovals(t, func() (bool, error) { t.Error("status told the instance"); return false, nil }, "status")
	if err != nil || !strings.Contains(out, "No device is allowed") {
		t.Errorf("empty: %v\n%s", err, out)
	}
	approvalPrefs(t)
	out, err = runApprovals(t, nil, "status")
	if err != nil || !strings.Contains(out, "(2)") || !strings.Contains(out, "d1") || !strings.Contains(out, "d2") {
		t.Errorf("status: %v\n%s", err, out)
	}
	if !store.LoadPrefs().AccessFor("d1").Approve {
		t.Error("status switched approval off")
	}
}

func TestRemoteApprovalsIsInTheHelpAndTheCommandList(t *testing.T) {
	isolateKeys(t)
	var out bytes.Buffer
	if err := remoteCmd([]string{"help", "approvals"}, remoteIO{out: &out}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "flockdeck remote approvals off|status") || !strings.Contains(out.String(), "there is no `on` here") {
		t.Errorf("help:\n%s", out.String())
	}
	out.Reset()
	remoteUsage(&out)
	if !strings.Contains(out.String(), "approvals off") {
		t.Errorf("usage does not list approvals:\n%s", out.String())
	}
}

func TestRemoteApprovalsStatusListsOnlyDevicesAtTheCurrentWording(t *testing.T) {
	isolateKeys(t)
	approvalPrefs(t)
	p := store.LoadPrefs()
	p, _ = p.WithAccess("old", store.DeviceAccess{Role: store.RoleFull, Approve: true, ApproveV: server.ApproveAckVersion - 1, ApproveKey: "k", ApproveAt: time.Now().UTC()})
	if err := store.SavePrefs(p); err != nil {
		t.Fatal(err)
	}
	out, err := runApprovals(t, nil, "status")
	if err != nil || !strings.Contains(out, "(2)") || strings.Contains(out, "old") {
		t.Errorf("status: %v\n%s", err, out)
	}
}

func TestRemoteApprovalsOffStillTellsTheInstanceWhenThePrefsCannotBeRead(t *testing.T) {
	isolateKeys(t)
	dir, err := store.Dir()
	if err != nil {
		t.Fatal(err)
	}
	// A directory where the file should be: the read fails and the file is not
	// absent or damaged.
	if err := os.MkdirAll(filepath.Join(dir, "prefs.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	told := 0
	out, err := runApprovals(t, func() (bool, error) { told++; return true, nil }, "off")
	if err == nil || !strings.Contains(err.Error(), "could not be read") {
		t.Errorf("err = %v, want the read failure", err)
	}
	if told != 1 || !strings.Contains(out, "switched approval off in memory") {
		t.Errorf("told %d times, output:\n%s", told, out)
	}
}
