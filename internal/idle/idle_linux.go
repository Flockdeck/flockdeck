//go:build linux

package idle

import (
	"os"
	"time"
)

// Since tries, in order, the first of these that answers:
//
//   - GNOME Mutter's own idle monitor, over its session D-Bus interface --
//     present on a stock GNOME desktop with nothing extra installed;
//   - xprintidle, an X11 tool some other desktops carry;
//   - logind, which does not read the idle time itself but exposes a
//     hint of it once the compositor has told it the session is idle, and
//     says whether the session is locked.
//
// The first that answers is taken whole, rather than mixing readings from
// two of them. logind answers only when it says the session is locked or
// idle; otherwise, as for an SSH session, there is no answer, and the push
// loop falls back to input seen in Flockdeck's own windows.
func Since() (time.Duration, bool, bool) {
	if d, locked, ok := gdbusIdle(); ok {
		return d, locked, ok
	}
	if d, locked, ok := xprintidleIdle(); ok {
		return d, locked, ok
	}
	return loginctlIdle()
}

// gdbusIdle asks Mutter's IdleMonitor how long it has been since it last saw
// keyboard or mouse input, in milliseconds. Mutter does not say whether the
// session is locked, so locked is always false here; a desktop that also
// wants that answered has logind to fall back to instead.
func gdbusIdle() (time.Duration, bool, bool) {
	out, err := runProbe("gdbus", "call", "--session",
		"--dest", "org.gnome.Mutter.IdleMonitor",
		"--object-path", "/org/gnome/Mutter/IdleMonitor/Core",
		"--method", "org.gnome.Mutter.IdleMonitor.GetIdletime")
	if err != nil {
		return 0, false, false
	}
	ms, ok := parseGdbusIdle(out)
	if !ok {
		return 0, false, false
	}
	return time.Duration(ms) * time.Millisecond, false, true
}

// xprintidleIdle asks xprintidle for the same, in milliseconds, when it is on
// PATH. Like gdbusIdle it says nothing of the lock screen.
func xprintidleIdle() (time.Duration, bool, bool) {
	out, err := runProbe("xprintidle")
	if err != nil {
		return 0, false, false
	}
	ms, ok := parseXprintidle(out)
	if !ok {
		return 0, false, false
	}
	return time.Duration(ms) * time.Millisecond, false, true
}

// loginctlIdle asks logind about the current session, named by
// XDG_SESSION_ID. It is the only one of the three that can say the session
// is locked, and the last one tried because its idle reading is the least
// direct: IdleHint only turns on after logind's own, usually longer, idle
// threshold, and never at all for a session nothing tells it about. Until it
// does, unless the session is locked, this is no answer; see parseLoginctl.
func loginctlIdle() (time.Duration, bool, bool) {
	session, ok := lookupSessionID()
	if !ok {
		return 0, false, false
	}
	out, err := runProbe("loginctl", "show-session", session,
		"-p", "LockedHint", "-p", "IdleHint", "-p", "IdleSinceHint")
	if err != nil {
		return 0, false, false
	}
	return parseLoginctl(out, time.Now())
}

// lookupSessionID says which session logind is asked about: the one this
// process is part of, which the desktop sets in its environment.
func lookupSessionID() (string, bool) {
	id := os.Getenv("XDG_SESSION_ID")
	return id, id != ""
}
