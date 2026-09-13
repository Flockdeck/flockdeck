package idle

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// gdbusIdleRe picks the millisecond figure out of gdbus's answer, which reads
// like `(uint64 123456,)` -- the type name comes first, so it is matched and
// skipped rather than mistaken for part of the number.
var gdbusIdleRe = regexp.MustCompile(`uint64\s+(\d+)`)

// parseGdbusIdle is gdbusIdle's parser, kept apart from the command it reads
// so it can be tested without gdbus.
func parseGdbusIdle(output string) (uint64, bool) {
	m := gdbusIdleRe.FindStringSubmatch(output)
	if m == nil {
		return 0, false
	}
	ms, err := strconv.ParseUint(m[1], 10, 64)
	return ms, err == nil
}

// parseXprintidle is xprintidleIdle's parser: xprintidle prints one plain
// number of milliseconds and a trailing newline, nothing else.
func parseXprintidle(output string) (uint64, bool) {
	ms, err := strconv.ParseUint(strings.TrimSpace(output), 10, 64)
	return ms, err == nil
}

// parseLoginctl is loginctlIdle's parser. loginctl's show-session, asked for
// specific properties, answers with them each on their own line:
//
//	LockedHint=no
//	IdleHint=yes
//	IdleSinceHint=1699999999999999
//
// IdleSinceHint is a wall-clock reading in microseconds since the epoch, of
// when the idle hint last turned on; now is passed in rather than read here
// so a test can fix it. A session with IdleHint not yet on is not proven idle
// -- logind's own idle threshold has simply not passed -- so 0 is reported
// for it rather than guessing.
func parseLoginctl(output string, now time.Time) (time.Duration, bool, bool) {
	fields := map[string]string{}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		k, v, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		fields[k] = v
	}
	lockedHint, hasLocked := fields["LockedHint"]
	idleHint, hasIdle := fields["IdleHint"]
	if !hasLocked && !hasIdle {
		return 0, false, false
	}
	locked := lockedHint == "yes"
	if idleHint != "yes" {
		return 0, locked, true
	}
	usec, err := strconv.ParseInt(fields["IdleSinceHint"], 10, 64)
	if err != nil || usec == 0 {
		return 0, locked, true
	}
	since := time.Unix(usec/1e6, (usec%1e6)*1000)
	d := now.Sub(since)
	if d < 0 {
		d = 0
	}
	return d, locked, true
}
