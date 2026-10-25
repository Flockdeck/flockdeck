//go:build windows

package changes

// openNoBlock is added to the flags a working file is opened with. Windows has no
// pipe in a checkout to block on.
const openNoBlock = 0
