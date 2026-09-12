package route

import (
	"path"
	"regexp"
	"strings"
)

// filePattern finds what reads as a path in a task: something with a slash in
// it, or a name with an extension.
var filePattern = regexp.MustCompile(`(?:[\w.-]+[/\\])+[\w.-]+|[\w-]+\.[A-Za-z]\w*`)

// filesIn is the paths a task names, with forward slashes.
func filesIn(task string) []string {
	var out []string
	for _, f := range filePattern.FindAllString(task, -1) {
		f = strings.TrimRight(strings.ReplaceAll(f, `\`, "/"), ".")
		f = strings.TrimPrefix(f, "./")
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

// anyFileMatches reports whether any of the paths matches any of the globs.
func anyFileMatches(globs, files []string) bool {
	for _, g := range globs {
		for _, f := range files {
			if MatchGlob(g, f) {
				return true
			}
		}
	}
	return false
}

// MatchGlob matches a path against a glob: path.Match for each part, with
// "**" standing for any number of directories. A glob with no slash in it is
// matched against the path's last part, so "*.sql" finds db/0003.sql as well
// as 0003.sql. Case is not compared, since the same path is written both ways
// on Windows and macOS.
func MatchGlob(glob, name string) bool {
	glob, name = strings.ToLower(glob), strings.ToLower(name)
	if !strings.Contains(glob, "/") {
		name = path.Base(name)
	}
	return matchParts(strings.Split(glob, "/"), strings.Split(name, "/"))
}

func matchParts(glob, name []string) bool {
	for len(glob) > 0 {
		if glob[0] == "**" {
			for i := 0; i <= len(name); i++ {
				if matchParts(glob[1:], name[i:]) {
					return true
				}
			}
			return false
		}
		if len(name) == 0 {
			return false
		}
		if ok, _ := path.Match(glob[0], name[0]); !ok {
			return false
		}
		glob, name = glob[1:], name[1:]
	}
	return len(name) == 0
}
