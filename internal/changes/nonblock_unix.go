//go:build !windows

package changes

import "syscall"

// openNoBlock is added to the flags a working file is opened with, so that a
// file swapped for a FIFO after it was looked at is not waited on.
const openNoBlock = syscall.O_NONBLOCK
