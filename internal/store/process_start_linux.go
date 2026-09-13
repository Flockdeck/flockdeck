//go:build linux

package store

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// clockTicks is USER_HZ, the unit /proc gives a process's start in. It is 100
// on every architecture Go builds for Linux, and asking sysconf for it would
// take cgo.
const clockTicks = 100

// processStarted reports when a process started, from /proc: its start in
// clock ticks since boot, and the boot time, to the second. ok is false when
// either cannot be read, as for a process that is not there.
func processStarted(pid int) (started time.Time, ok bool) {
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return time.Time{}, false
	}
	// The command name, the second field, is in brackets and may hold spaces
	// and brackets of its own, so the fields are counted from after the last
	// closing one. The state, the third field, is the first of them, and the
	// start time the twenty-second.
	i := bytes.LastIndexByte(stat, ')')
	if i < 0 {
		return time.Time{}, false
	}
	fields := strings.Fields(string(stat[i+1:]))
	if len(fields) <= 22-3 {
		return time.Time{}, false
	}
	ticks, err := strconv.ParseInt(fields[22-3], 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	boot, ok := bootTime()
	if !ok {
		return time.Time{}, false
	}
	return boot.Add(time.Duration(ticks) * (time.Second / clockTicks)), true
}

// bootTime is when the system started, from the btime line of /proc/stat.
func bootTime() (time.Time, bool) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return time.Time{}, false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if rest, found := strings.CutPrefix(line, "btime "); found {
			secs, err := strconv.ParseInt(strings.TrimSpace(rest), 10, 64)
			if err != nil {
				return time.Time{}, false
			}
			return time.Unix(secs, 0), true
		}
	}
	return time.Time{}, false
}
