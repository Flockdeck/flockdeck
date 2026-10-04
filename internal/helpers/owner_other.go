//go:build !windows && !linux

package helpers

const ownerCheckSupported = false

// platformListenerOwner cannot say who owns a socket here (macOS has no
// interface for it short of running lsof, whose output is not something to
// trust a decision to), so the helper is shown as "owner not verified".
func platformListenerOwner(port int, group []int) (ownerResult, string) {
	return ownerUnknown, "this platform cannot say who owns a port"
}

func groupMembers(pgid int) ([]int, bool) { return nil, false }
