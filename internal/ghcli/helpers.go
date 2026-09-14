package ghcli

import (
	"strconv"
	"strings"
)

// prNumberFromURL picks the number off the end of a PR or issue URL, as gh
// prints after `pr create` and `issue create` --
// "https://github.com/owner/repo/pull/123". 0 means it could not be read,
// which ViewPR/ViewIssue then fail on with gh's own "could not resolve"
// rather than silently viewing the wrong item.
func prNumberFromURL(url string) int {
	url = strings.TrimSpace(url)
	i := strings.LastIndex(url, "/")
	if i < 0 {
		return 0
	}
	n, err := strconv.Atoi(url[i+1:])
	if err != nil {
		return 0
	}
	return n
}

// isNoneFound reports whether err is gh saying there was nothing to find --
// "no pull requests found for branch ..." -- rather than a real failure, so a
// caller like CurrentPR can answer "no PR here" instead of surfacing an
// error for the ordinary case of a branch with none.
func isNoneFound(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no pull requests found") || strings.Contains(msg, "no issues found")
}
