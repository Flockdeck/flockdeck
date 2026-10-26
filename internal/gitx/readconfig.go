package gitx

import (
	"context"
	"fmt"
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

// UnsafeReadConfig reports the first setting of dir's repository-local or worktree
// configuration that would run a program when git compares the working tree with
// the index, or nil when there is none. An error that is not an
// *UnsafeReadConfigError means the configuration could not be read, which callers
// must treat as not safe.
func UnsafeReadConfig(ctx context.Context, dir string) error {
	entries, err := configEntries(ctx, dir, unsafeReadKeys)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !allowedConfig(e[0], e[1]) {
			return &UnsafeReadConfigError{Key: e[0]}
		}
	}
	return nil
}
