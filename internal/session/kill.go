package session

import (
	"github.com/jmwri/flockdeck/internal/proctree"
)

// endTree ends the pane's process and everything it started, and waits for the
// process to be gone, within closeGrace. How is proctree's: a job object on
// Windows, the pane's session elsewhere.
func (s *Session) endTree() { s.tree.End(s.cmd.Process, s.reaped, closeGrace) }

// KillTree ends pid and everything the process table, or its session, shows
// descended from it. It is exported for a startup sweep to call directly, once
// it has already confirmed pid still names the same process it was recorded
// under (see store.ProcessAlive and store.ProcessStartedAt); nothing here
// repeats that check. See internal/workspace's reaper, and proctree.KillTree.
func KillTree(pid int) { proctree.KillTree(pid) }
