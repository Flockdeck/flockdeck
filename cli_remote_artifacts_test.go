package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/store"
)

func runArtifacts(t *testing.T, stop func() (bool, error), args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := remoteCmd(append([]string{"artifacts"}, args...), remoteIO{out: &out, stopArtifacts: stop})
	return out.String(), err
}

func TestRemoteArtifactsOffSwitchesEveryKindOffAndTellsTheRunningInstance(t *testing.T) {
	isolateKeys(t)
	p := store.LoadPrefs()
	p.HelpSeen = true
	p.RemoteArtifacts.SetAck("recordings", store.ArtifactAck{Version: 1, At: time.Now()})
	p.RemoteArtifacts.SetKind("recordings", true)
	p.RemoteArtifacts.AddDevice("d1")
	if err := store.SavePrefs(p); err != nil {
		t.Fatal(err)
	}

	told := 0
	out, err := runArtifacts(t, func() (bool, error) { told++; return true, nil }, "off")
	if err != nil {
		t.Fatalf("artifacts off: %v\n%s", err, out)
	}
	got := store.LoadPrefs()
	if got.RemoteArtifacts.KindOn("recordings") {
		t.Error("recordings are still on")
	}
	if !got.HelpSeen || !got.RemoteArtifacts.HasDevice("d1") || !got.RemoteArtifacts.Acked("recordings", 1) {
		t.Errorf("off changed more than the kinds: %+v", got)
	}
	if told != 1 || !strings.Contains(out, "switched off") || !strings.Contains(out, "closed") {
		t.Errorf("told %d times, output:\n%s", told, out)
	}

	// Again: already off, but the running instance is still told, for sockets
	// that outlived an earlier attempt.
	out, err = runArtifacts(t, func() (bool, error) { told++; return false, nil }, "off")
	if err != nil || told != 2 || !strings.Contains(out, "already off") {
		t.Errorf("second run: err %v told %d\n%s", err, told, out)
	}
}

func TestRemoteArtifactsOffReportsAnInstanceItCouldNotTell(t *testing.T) {
	isolateKeys(t)
	out, err := runArtifacts(t, func() (bool, error) { return true, errors.New("connection refused") }, "off")
	if err != nil {
		t.Fatalf("a running instance that could not be told is not a failure of the switch: %v", err)
	}
	if !strings.Contains(out, "could not be told") {
		t.Errorf("output:\n%s", out)
	}
}

func TestRemoteArtifactsHasNoOn(t *testing.T) {
	isolateKeys(t)
	for _, args := range [][]string{nil, {"on"}, {"off", "extra"}} {
		if _, err := runArtifacts(t, nil, args...); err == nil {
			t.Errorf("artifacts %v was accepted", args)
		}
	}
	if store.LoadPrefs().RemoteArtifacts.KindOn("recordings") {
		t.Error("something switched a kind on")
	}
}
