package session

import (
	"os"
	"strconv"
	"time"

	"github.com/jmwri/flockdeck/internal/proctree"
)

// On Linux the whole reading comes out of /proc, which is why nothing outside
// the standard library is needed for it.

// clockTick is the unit /proc reports process times in. It is a kernel build
// option, but has been 100 on every Linux this is likely to run on, and
// getconf is not something to shell out to on a timer.
const clockTick = 100

// pageSize is what /proc/<pid>/stat counts resident memory in.
var pageSize = uint64(os.Getpagesize())

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

// readStat is proctree.Stat, where /proc/<pid>/stat is read and split.
func readStat(pid int) ([]string, bool) { return proctree.Stat(pid) }

func procParents() map[int]int { return proctree.Parents() }
