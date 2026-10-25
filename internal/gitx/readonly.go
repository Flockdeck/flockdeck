package gitx

import "context"

// ReadOnly runs a git command that only reads the repository, with the same
// protections as every other command here (no fsmonitor command, no hooks), and
// returns its standard output. Cancelling ctx kills the process. The caller must
// pass only a command that does not write: nothing here checks.
func ReadOnly(ctx context.Context, dir string, args ...string) (string, error) {
	return runUntil(ctx, dir, args...)
}
