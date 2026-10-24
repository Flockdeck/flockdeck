package depth

import (
	"os"
	"strconv"
	"strings"
)

func osLookup() Lookup {
	return func(pid int) (int, string, bool) {
		b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
		if err != nil {
			return 0, "", false
		}
		return parseStat(string(b))
	}
}

// parseStat reads "pid (name) state ppid ...". The name may hold spaces and
// brackets, so it ends at the last closing one.
func parseStat(s string) (int, string, bool) {
	open, end := strings.IndexByte(s, '('), strings.LastIndexByte(s, ')')
	if open < 0 || end < open {
		return 0, "", false
	}
	fields := strings.Fields(s[end+1:])
	if len(fields) < 2 {
		return 0, "", false
	}
	ppid, err := strconv.Atoi(fields[1])
	if err != nil {
		return 0, "", false
	}
	return ppid, s[open+1 : end], true
}
