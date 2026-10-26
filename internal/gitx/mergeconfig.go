package gitx

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// A repository's own configuration (.git/config, and config.worktree of a linked
// worktree) is written by whoever works in the checkout, and an agent works there.
// A merge reads trees and writes files and commits, and several settings make git
// run a program of the repository's choosing while it does: a merge driver on a
// content conflict, a clean, smudge or process filter when a file is written, and a
// signing program when a commit is made. Hooks are turned off for every command
// (see configFor). These are refused before a merge is made or predicted.
//
// The person's own configuration, global and system, is not looked at: it is theirs.

// unsafeConfigKeys are the keys that name a program or pull in more configuration.
// The match is git's own (an extended regular expression over the lower-cased
// section and variable names).
const unsafeConfigKeys = `^(merge\..*\.driver|filter\..*\.(smudge|clean|process)|gpg\..*program|include.*)$`

// lfsFilter is the one program a filter may name: Git LFS's own commands, as it
// installs them. Anything else in the value, a second command after a separator
// included, is not this.
var lfsFilter = regexp.MustCompile(`^git-lfs (clean|smudge|filter-process)( --skip)?( -- %f)?$`)

// UnsafeConfigError says which setting of the repository's own configuration would
// run a program during a merge.
type UnsafeConfigError struct{ Key string }

func (e *UnsafeConfigError) Error() string {
	return fmt.Sprintf("The repository's own configuration sets %s, which would run a program during the merge. Nothing was merged. Remove it from the repository's config, or merge by hand.", e.Key)
}

// UnsafeConfig reports the first setting of dir's repository-local or worktree
// configuration that would run a program during a merge, or nil when there is none.
// An error that is not an *UnsafeConfigError means the configuration could not be
// read, which callers must treat as not safe.
func UnsafeConfig(ctx context.Context, dir string) error {
	for _, scope := range []string{"--local", "--worktree"} {
		out, _, err := runCapture(ctx, commandTimeout, dir, "config", scope, "-z", "--get-regexp", unsafeConfigKeys)
		if err != nil {
			if exitCode(err) == 1 {
				continue // no such key
			}
			return fmt.Errorf("the repository's configuration could not be read: %w", err)
		}
		for _, entry := range strings.Split(out, "\x00") {
			key, value, _ := strings.Cut(entry, "\n")
			if key == "" {
				continue
			}
			if strings.HasPrefix(key, "filter.") && lfsFilter.MatchString(strings.TrimSpace(value)) {
				continue
			}
			return &UnsafeConfigError{Key: key}
		}
	}
	return nil
}
