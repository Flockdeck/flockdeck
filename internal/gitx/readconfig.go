package gitx

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Reading a checkout is not harmless either. To say whether a tracked file changed,
// git compares it with the index, and when a clean or process filter is configured
// for it git runs that program on the file (the standard clean filter of Git LFS
// also runs each lfs.extension.<name>.clean). Status, diffs against the working
// tree, ls-files -m, update-index --refresh and add all do this, on every poll,
// with no click. A repository's own configuration is written by whoever works in
// the checkout, so the polls that compare the working tree are not made while it
// names such a program. Commands that read only trees run no clean filter, a smudge
// filter runs when files are written and not when they are read, and a text
// conversion or external diff runs only without --no-textconv and --no-ext-diff,
// which every diff here passes.

// unsafeReadKeys are the keys that make git run a program when it compares the
// working tree with the index, or that pull in more configuration.
const unsafeReadKeys = `^(filter\..*\.(clean|process)|include.*|lfs\..*)$`

// UnsafeReadConfigError says which setting of the repository's own configuration
// would run a program when the working tree is compared with the index.
type UnsafeReadConfigError struct{ Key string }

func (e *UnsafeReadConfigError) Error() string {
	return fmt.Sprintf("its config runs programs: %s", cleanKey(e.Key))
}

// ConfigUnreadableError says the repository's configuration could not be read, so it
// is not known whether it names a program. Callers refuse as they do for a setting.
type ConfigUnreadableError struct{ Err error }

func (e *ConfigUnreadableError) Error() string { return e.Err.Error() }
func (e *ConfigUnreadableError) Unwrap() error { return e.Err }

// UnsafeReadConfig reports the first setting of dir's repository-local or worktree
// configuration that would run a program when git compares the working tree with
// the index, or nil when there is none. An error that is not an
// *UnsafeReadConfigError is a *ConfigUnreadableError: the configuration could not be
// read, which callers must treat as not safe.
//
// The answer holds for readConfigTTL, so that the several git calls of one read pass
// ask once. It is a check at one moment, as the merge's is: a process control among
// cooperating agents, not protection against an agent that writes the config between
// the check and the read.
func UnsafeReadConfig(ctx context.Context, dir string) error {
	if recentReadConfig(dir) {
		return nil
	}
	entries, err := configEntries(ctx, dir, unsafeReadKeys)
	if err != nil {
		return &ConfigUnreadableError{Err: err} // not kept: a read that failed is asked again
	}
	for _, e := range entries {
		if !allowedConfig(e[0], e[1]) {
			return &UnsafeReadConfigError{Key: e[0]}
		}
	}
	keepReadConfig(dir)
	return nil
}

// readConfigTTL is how long a clean answer of UnsafeReadConfig is kept. A refusal is
// asked again each time, so that taking the setting away reads the checkout at once.
// Tests that change a configuration and ask again set it to zero.
var readConfigTTL = 2 * time.Second

var (
	readConfigMu   sync.Mutex
	readConfigSeen = map[string]time.Time{}
)

func recentReadConfig(dir string) bool {
	if readConfigTTL <= 0 {
		return false
	}
	readConfigMu.Lock()
	defer readConfigMu.Unlock()
	at, ok := readConfigSeen[dir]
	return ok && time.Since(at) <= readConfigTTL
}

func keepReadConfig(dir string) {
	if readConfigTTL <= 0 {
		return
	}
	readConfigMu.Lock()
	defer readConfigMu.Unlock()
	if len(readConfigSeen) > 256 {
		clear(readConfigSeen)
	}
	readConfigSeen[dir] = time.Now()
}
