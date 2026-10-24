package depth

import (
	"os"
	"strconv"
	"strings"
)

func osLookup() Lookup {
	return func(pid int) (Proc, bool) {
		b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
		if err != nil {
			return Proc{}, false
		}
		return parseStat(string(b))
	}
}

// parseStat reads "pid (name) state ppid ... starttime ...". The name may hold
// spaces and brackets, so it ends at the last closing one. starttime is the 22nd
// field, in clock ticks after boot.
func parseStat(s string) (Proc, bool) {
	open, end := strings.IndexByte(s, '('), strings.LastIndexByte(s, ')')
	if open < 0 || end < open {
		return Proc{}, false
	}
	fields := strings.Fields(s[end+1:])
	if len(fields) < 2 {
		return Proc{}, false
	}
	ppid, err := strconv.Atoi(fields[1])
	if err != nil {
		return Proc{}, false
	}
	p := Proc{Parent: ppid, Name: s[open+1 : end]}
	if len(fields) > 19 {
		if t, err := strconv.ParseInt(fields[19], 10, 64); err == nil {
			p.Start = t
		}
	}
	return p, true
}
