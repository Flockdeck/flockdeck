// Package legacyenv reads the variables a pane carries under both the name in
// use now and the PERCH_ name they had before the program was renamed.
package legacyenv

import (
	"log"
	"os"
	"sync"
)

// RemovalDate is when the PERCH_ names stop being read. It is also the date
// the warning and cli.md give, so it is kept in one place.
const RemovalDate = "17 April 2027"

var warned sync.Map // PERCH_ names already warned about in this process

// Get returns FLOCKDECK_<name>, or PERCH_<name> where that is not set. A pane
// started by an earlier build is still running with only the old names in its
// environment, and a command inside it should not lose its pane because the
// binary on PATH was upgraded.
//
// Using the old name logs one line per name and process saying which name
// replaces it and when the old one stops working.
func Get(name string) string {
	if v := os.Getenv("FLOCKDECK_" + name); v != "" {
		return v
	}
	v := os.Getenv("PERCH_" + name)
	if v != "" {
		if _, seen := warned.LoadOrStore(name, true); !seen {
			log.Printf("flockdeck: PERCH_%s is deprecated and stops being read after %s; set FLOCKDECK_%s instead", name, RemovalDate, name)
		}
	}
	return v
}
