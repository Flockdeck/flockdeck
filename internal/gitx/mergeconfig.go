package gitx

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// A repository's own configuration (.git/config, and config.worktree of a linked
// worktree) is written by whoever works in the checkout, and an agent works there.
// A merge reads trees and writes files and commits, and several settings make git
// run a program of the repository's choosing while it does: a merge driver on a
// content conflict, a clean, smudge or process filter when a file is written, and a
// signing program when a commit is made. Git LFS's filter is let through, but not
// the settings that make it run a program of the repository's choosing while it
// downloads (any lfs.* key but plain data, a credential helper, an ask-pass, an ssh
// or proxy command). Hooks are turned off for every command (see configFor). These are refused before a merge is made or predicted.
//
// The person's own configuration, global and system, is not looked at: it is theirs.

// unsafeConfigKeys are the keys that name a program or pull in more configuration.
// It is a list of the keys known to do this, not a guarantee that no other does.
// The match is git's own (an extended regular expression over the lower-cased
// section and variable names).
const unsafeConfigKeys = `^(merge\..*\.driver|filter\..*\.(smudge|clean|process)|gpg\..*program|include.*|lfs\..*|credential\..*helper|core\.(sshcommand|gitproxy|askpass)|diff\..*\.(textconv|command)|diff\.external)$`

// lfsData are the keys of Git LFS that are only data, which Git LFS and `git lfs
// install --local` write. The filter is let through, so every other lfs.* key is
// refused: an extension, a custom transfer agent or anything added later runs a
// program, and an unknown key fails closed.
var lfsData = regexp.MustCompile(`^lfs\.(repositoryformatversion|url|pushurl|concurrenttransfers|fetchinclude|fetchexclude|locksverify|setlockablereadonly|activitytimeout|dialtimeout|keepalive|tlstimeout|[a-z][a-z0-9+.-]*://.*\.(access|locksverify))$`)

// UnsafeConfigError says which setting of the repository's own configuration would
// run a program during a merge.
type UnsafeConfigError struct{ Key string }

func (e *UnsafeConfigError) Error() string {
	return fmt.Sprintf("The repository's own configuration sets %s, which would run a program during the merge. Nothing was merged. Remove it from the repository's config, or merge by hand.", cleanKey(e.Key))
}

// cleanKey is a configuration key as it can be shown: the subsection of a key is
// whatever the repository's config says, so control, bidirectional and other format
// characters are removed and the length is limited.
func cleanKey(k string) string {
	return strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, clean(k, 200))
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
			if strings.HasPrefix(key, "filter.") && StandardLFSFilter(key, strings.TrimSpace(value)) {
				continue
			}
			if lfsData.MatchString(key) {
				continue
			}
			return &UnsafeConfigError{Key: key}
		}
	}
	return nil
}
