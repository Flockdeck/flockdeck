package selfupdate

import (
	"regexp"
	"strconv"
	"strings"
)

// describeSuffix matches what `git describe --dirty` adds after a tag: the
// commits since it and the one built, and whether the tree had changes. The
// Makefile stamps a build that way, and read as a pre-release it would be
// ordered before the tag it came after, so a build three commits past v1.4.0
// was replaced by v1.4.0 on its next restart. It is a local build, which is
// not something to compare at all.
var describeSuffix = regexp.MustCompile(`(^|-)(\d+-g[0-9a-f]+(-dirty)?|dirty)$`)

// version is a release version, parsed far enough to be ordered against
// another one. Only what the release tags actually carry is understood:
// major.minor.patch with an optional pre-release suffix.
type version struct {
	major, minor, patch int
	pre                 string
}

// parseVersion reads a tag of the form v1.4.0 or v1.4.0-rc.1.
//
// A version that cannot be read is not an error to report to anyone, it is
// simply not something to compare: the caller treats it as "do not update".
// That is what keeps a developer's own build, stamped `dev` by the Makefile
// when there is no tag, from being replaced by whatever is on the release page.
func parseVersion(s string) (version, bool) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	if s == "" {
		return version{}, false
	}

	var v version
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		// Build metadata after '+' does not order a version, so it is dropped
		// rather than kept and then ignored.
		if s[i] == '-' {
			v.pre = s[i+1:]
			if j := strings.IndexByte(v.pre, '+'); j >= 0 {
				v.pre = v.pre[:j]
			}
		}
		s = s[:i]
	}

	if describeSuffix.MatchString(v.pre) {
		return version{}, false
	}

	parts := strings.Split(s, ".")
	if len(parts) == 0 || len(parts) > 3 {
		return version{}, false
	}
	nums := make([]int, 3)
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return version{}, false
		}
		nums[i] = n
	}
	v.major, v.minor, v.patch = nums[0], nums[1], nums[2]
	return v, true
}

// compare orders two versions the way semantic versioning does, returning -1,
// 0 or 1. A pre-release sorts before the release it leads to, so v1.4.0-rc.1
// is older than v1.4.0 and a release always wins over a candidate for it.
func compare(a, b version) int {
	for _, p := range [][2]int{{a.major, b.major}, {a.minor, b.minor}, {a.patch, b.patch}} {
		if p[0] != p[1] {
			if p[0] < p[1] {
				return -1
			}
			return 1
		}
	}
	switch {
	case a.pre == b.pre:
		return 0
	case a.pre == "":
		return 1
	case b.pre == "":
		return -1
	case a.pre < b.pre:
		return -1
	default:
		return 1
	}
}

// Newer reports whether candidate is a release worth moving to from current.
//
// It answers no when either side cannot be read, which is the case that keeps
// an untagged local build in place: there is no sense in which `dev` is behind
// v1.4.0, because it may well be ahead of it.
func Newer(candidate, current string) bool {
	c, ok := parseVersion(candidate)
	if !ok {
		return false
	}
	cur, ok := parseVersion(current)
	if !ok {
		return false
	}
	return compare(c, cur) > 0
}

// Parseable reports whether a version string is one that can be ordered
// against a release at all. An untagged local build, stamped `dev`, is not,
// which is a different thing from being up to date and worth saying so.
func Parseable(v string) bool {
	_, ok := parseVersion(v)
	return ok
}
