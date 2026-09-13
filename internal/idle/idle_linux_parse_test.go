package idle

import (
	"strconv"
	"testing"
	"time"
)

func TestParseGdbusIdle(t *testing.T) {
	ms, ok := parseGdbusIdle("(uint64 123456,)\n")
	if !ok || ms != 123456 {
		t.Errorf("got %v, %v", ms, ok)
	}
	if _, ok := parseGdbusIdle("some error message with no number"); ok {
		t.Error("parsed a number out of an answer with none")
	}
}

func TestParseXprintidle(t *testing.T) {
	ms, ok := parseXprintidle("42789\n")
	if !ok || ms != 42789 {
		t.Errorf("got %v, %v", ms, ok)
	}
	if _, ok := parseXprintidle("command not found\n"); ok {
		t.Error("parsed a number out of an answer with none")
	}
}

func TestParseLoginctl(t *testing.T) {
	now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)

	// Locked: away whatever the idle reading says.
	d, locked, ok := parseLoginctl("LockedHint=yes\nIdleHint=no\nIdleSinceHint=0\n", now)
	if !ok || !locked {
		t.Errorf("a locked session was not reported locked: %v %v", locked, ok)
	}
	if d != 0 {
		t.Errorf("a locked session carried an idle reading: %v", d)
	}

	// Idle for five minutes, unlocked.
	since := now.Add(-5 * time.Minute)
	usec := since.UnixMicro()
	out := "LockedHint=no\nIdleHint=yes\nIdleSinceHint=" + strconv.FormatInt(usec, 10) + "\n"
	d, locked, ok = parseLoginctl(out, now)
	if !ok || locked {
		t.Fatalf("an unlocked idle session: locked=%v ok=%v", locked, ok)
	}
	if d != 5*time.Minute {
		t.Errorf("idle since %v ago, got %v", 5*time.Minute, d)
	}

	// Not idle by logind's reckoning, and not locked: no answer, not "in
	// use". An SSH session, or a desktop that never tells logind, stays like
	// this for good, and taken for "in use" it would hold every push back.
	d, locked, ok = parseLoginctl("LockedHint=no\nIdleHint=no\nIdleSinceHint=0\n", now)
	if ok {
		t.Errorf("a session logind has not called idle was taken as an answer: d=%v locked=%v", d, locked)
	}

	if _, _, ok := parseLoginctl("Unit not found.\n", now); ok {
		t.Error("parsed usable fields out of an error message")
	}
}
