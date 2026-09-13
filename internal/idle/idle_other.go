//go:build !windows && !linux && !darwin

package idle

import "time"

// Since has nowhere to read this from on the rest. The caller falls back to
// input seen in Flockdeck's own windows on this machine.
func Since() (time.Duration, bool, bool) { return 0, false, false }
