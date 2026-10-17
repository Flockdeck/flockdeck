// Package legacyenv reads the variables a pane carries under both the name in
// use now and the PERCH_ name they had before the program was renamed.
package legacyenv

import (
	"fmt"
	"io"
	"os"
	"sync"
)

// RemovalDate is when the PERCH_ names are due to be removed. It is also the
// date the warning and cli.md give, so it is kept in one place.
const RemovalDate = "17 April 2027"

var warned sync.Map // PERCH_ names already warned about in this process

// Get returns FLOCKDECK_<name>, or PERCH_<name> where that is not set, and
// never says anything. It is for the commands a pane's agent runs on every
// event, the hook and the status line, which are a new process each time and
// keep their stderr clean.
//
// A pane started by an earlier build is still running with only the old names
// in its environment, and a command inside it should not lose its pane because
// the binary on PATH was upgraded.
func Get(name string) string { return GetWarn(nil, name) }

// GetWarn is Get for a command a person runs. Where the old name is the one
// used it writes one line to w, once per name and process, saying which name
// replaces it and when the old one is due to be removed. A nil w warns nowhere.
func GetWarn(w io.Writer, name string) string {
	if v := os.Getenv("FLOCKDECK_" + name); v != "" {
		return v
	}
	v := os.Getenv("PERCH_" + name)
	if v != "" && w != nil {
		if _, seen := warned.LoadOrStore(name, true); !seen {
			fmt.Fprintf(w, "flockdeck: PERCH_%s is deprecated and will be removed after %s; set FLOCKDECK_%s instead\n", name, RemovalDate, name)
		}
	}
	return v
}
