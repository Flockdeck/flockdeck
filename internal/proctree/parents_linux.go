package proctree

import (
	"os"
	"strconv"
	"strings"
)

// Parents returns the parent of every process this one can see.
func Parents() map[int]int {
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
		fields, ok := Stat(pid)
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

// SessionMembers returns the processes in the session sid, other than sid
// itself.
//
// A session's id stays reserved while any process is in it, so a process
// listed here is one the session's leader or its descendants started, even if
// the leader has gone.
func SessionMembers(sid int) []int {
	names, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	var out []int
	for _, e := range names {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == sid {
			continue
		}
		fields, ok := Stat(pid)
		// Field 6 of /proc/<pid>/stat is the session.
		if !ok || fields[5] != strconv.Itoa(sid) {
			continue
		}
		out = append(out, pid)
	}
	return out
}

// Stat reads /proc/<pid>/stat and splits it into its fields.
//
// The second field is the executable name in brackets and may itself contain
// spaces and brackets, so the split starts after the last closing bracket
// rather than at the first space.
func Stat(pid int) ([]string, bool) {
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
