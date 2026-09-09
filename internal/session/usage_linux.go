package session

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// On Linux the whole reading comes out of /proc, which is why nothing outside
// the standard library is needed for it.

// clockTick is the unit /proc reports process times in. It is a kernel build
// option, but has been 100 on every Linux this is likely to run on, and
// getconf is not something to shell out to on a timer.
const clockTick = 100

// pageSize is what /proc/<pid>/stat counts resident memory in.
var pageSize = uint64(os.Getpagesize())

// procParents returns the parent of every process this one can see.
func procParents() map[int]int {
	d, err := os.Open("/proc")
	if err != nil {
		return nil
	}
	defer d.Close()
	names, err := d.Readdirnames(-1)
	if err != nil {
		return nil
	}
	out := make(map[int]int, len(names))
	for _, name := range names {
		pid, err := strconv.Atoi(name)
		if err != nil {
			continue
		}
		fields, ok := readStat(pid)
		if !ok {
			continue
		}
		// Field 4 of /proc/<pid>/stat is the parent, counting the process id
		// as the first.
		ppid, err := strconv.Atoi(fields[3])
		if err != nil {
			continue
		}
		out[pid] = ppid
	}
	return out
}

// procMetrics returns what the operating system says about one process. ok is
// false once it is gone.
func procMetrics(pid int) (procMetric, bool) {
	fields, ok := readStat(pid)
	if !ok {
		return procMetric{}, false
	}
	// Fields 14 and 15 are the time spent in user and kernel code, 22 is when
	// the process started, counted from the same moment for every process on
	// the machine, and 24 is the resident set in pages.
	utime, err1 := strconv.ParseInt(fields[13], 10, 64)
	stime, err2 := strconv.ParseInt(fields[14], 10, 64)
	started, err3 := strconv.ParseUint(fields[21], 10, 64)
	pages, err4 := strconv.ParseInt(fields[23], 10, 64)
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
		return procMetric{}, false
	}
	m := procMetric{
		cpu:     time.Duration(utime+stime) * time.Second / clockTick,
		started: started,
	}
	if pages > 0 {
		m.rss = uint64(pages) * pageSize
	}
	return m, true
}

// readStat reads /proc/<pid>/stat and splits it into its fields.
//
// The second field is the executable name in brackets and may itself contain
// spaces and brackets, so the split starts after the last closing bracket
// rather than at the first space.
func readStat(pid int) ([]string, bool) {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return nil, false
	}
	return splitStat(string(raw))
}

func splitStat(line string) ([]string, bool) {
	open := strings.IndexByte(line, '(')
	shut := strings.LastIndexByte(line, ')')
	if open < 0 || shut < open {
		return nil, false
	}
	fields := make([]string, 0, 52)
	fields = append(fields, strings.TrimSpace(line[:open]), line[open+1:shut])
	fields = append(fields, strings.Fields(line[shut+1:])...)
	if len(fields) < 24 {
		return nil, false
	}
	return fields, true
}
