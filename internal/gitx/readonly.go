package gitx

import (
	"bytes"
	"context"
	"errors"
)

// ReadOnly runs a git command that only reads the repository, with the same
// protections as every other command here (no fsmonitor command, no hooks), and
// returns its standard output. Cancelling ctx kills the process. The caller must
// pass only a command that does not write: nothing here checks.
func ReadOnly(ctx context.Context, dir string, args ...string) (string, error) {
	return runUntil(ctx, dir, args...)
}

// ErrTooLarge is what ReadOnlyCapped fails with when git wrote more than it was
// allowed to keep.
var ErrTooLarge = errors.New("git wrote more than the limit")

// cappedBuffer keeps what git writes up to max bytes. Past that its Write fails,
// which stops the copy from git's pipe and so ends git on its next write.
type cappedBuffer struct {
	buf  bytes.Buffer
	max  int
	over bool
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	if c.buf.Len()+len(p) > c.max {
		c.over = true
		return 0, ErrTooLarge
	}
	return c.buf.Write(p)
}

func (c *cappedBuffer) String() string { return c.buf.String() }

// ReadOnlyCapped is ReadOnly for a command whose output a repository can make
// arbitrarily large (a diff, a listing of files): it is never held past max
// bytes, and ErrTooLarge is returned when it would be.
func ReadOnlyCapped(ctx context.Context, dir string, max int, args ...string) (string, error) {
	out := &cappedBuffer{max: max}
	_, err := runTo(ctx, commandTimeout, dir, nil, out, args...)
	if out.over {
		return "", ErrTooLarge
	}
	if err != nil {
		return "", err
	}
	return out.buf.String(), nil
}
