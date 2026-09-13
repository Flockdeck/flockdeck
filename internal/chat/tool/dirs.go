package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// maxListed bounds how many paths one call reports. A model given ten thousand
// file names has learned nothing it could not have learned from a hundred and
// a narrower pattern, and the rest is context it cannot spend twice.
const maxListed = 1000

// errStopWalk ends a walk that has found everything it was going to report.
// filepath.WalkDir has no other way to stop early, and a walk of a large
// checkout is worth stopping.
var errStopWalk = errors.New("walk complete")

// listDir shows one directory, nearest first, because a model orienting itself
// in an unfamiliar project asks what is here before it asks what is in it.
type listDir struct{ root *Root }

func (t *listDir) Name() string { return "list_dir" }

func (t *listDir) Describe() Schema {
	return Schema{
		Name: t.Name(),
		Description: "List the entries of one directory in the working directory. Directories are " +
			"shown with a trailing slash. Lists only that directory, not the tree below it; use " +
			"glob for that.",
		Params: object(map[string]Property{
			"path": {Type: "string", Description: "Directory to list, relative to the working directory. Defaults to the working directory itself."},
		}),
	}
}

type listArgs struct {
	Path string `json:"path"`
}

// Approval is empty: listing is a read inside the pane's own directory, and a
// path outside it is refused rather than asked about.
func (t *listDir) Approval(json.RawMessage) string { return "" }

func (t *listDir) Run(_ context.Context, args json.RawMessage) (string, error) {
	var a listArgs
	if err := decode(args, &a); err != nil {
		return "", err
	}
	abs, err := t.root.Resolve(a.Path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", t.root.explain(err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is a file; use read_file", t.root.Rel(abs))
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return "", t.root.explain(err)
	}
	if len(entries) == 0 {
		return fmt.Sprintf("%s is empty.", t.root.Rel(abs)), nil
	}

	// Directories come first because they are where the model goes next, and
	// each group is in name order because os.ReadDir's order is the file
	// system's and says nothing.
	var dirs, files []string
	for _, e := range entries {
		// A link that leads out of the pane is refused by every tool, and
		// shown as the file it looks like, the model reaches for it and is
		// refused; it is said to be what it is.
		if e.Type()&(fs.ModeSymlink|fs.ModeIrregular) != 0 {
			if _, err := t.root.Resolve(filepath.Join(abs, e.Name())); errors.Is(err, ErrOutsideRoot) {
				files = append(files, e.Name()+"  (a link leading outside the working directory)")
				continue
			}
			// A link to a directory is not a directory to the entry, which
			// describes the link: listed as a file of a few bytes, it was
			// read as one and the model told it was a directory. What it
			// leads to is what it is -- a package linked in by pnpm or a
			// workspace, a junction on Windows.
			if info, err := os.Stat(filepath.Join(abs, e.Name())); err == nil && info.IsDir() {
				dirs = append(dirs, e.Name()+"/")
				continue
			}
		}
		if e.IsDir() {
			dirs = append(dirs, e.Name()+"/")
			continue
		}
		size := ""
		if info, err := e.Info(); err == nil {
			size = "  " + humanBytes(info.Size())
		}
		files = append(files, e.Name()+size)
	}

	// The first line names the directory and says what is in it. It is the
	// line drawn under the call in the pane, where ".:" -- the root by its
	// relative name -- told the user nothing at all.
	name := t.root.Rel(abs) + "/"
	if name == "./" {
		name = "the working directory"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %d %s, %d %s\n", name, len(dirs), plural(len(dirs), "directory", "directories"),
		len(files), plural(len(files), "file", "files"))
	shown := 0
	for _, line := range append(sortedStrings(dirs), sortedStrings(files)...) {
		if shown == maxListed {
			fmt.Fprintf(&b, "[%d more entries]\n", len(entries)-shown)
			break
		}
		fmt.Fprintf(&b, "%s\n", line)
		shown++
	}
	return b.String(), nil
}

// globTool finds files by name pattern.
type globTool struct{ root *Root }

func (t *globTool) Name() string { return "glob" }

func (t *globTool) Describe() Schema {
	return Schema{
		Name: t.Name(),
		Description: "Find files by name pattern, anywhere under the working directory. Patterns " +
			"use forward slashes; * matches within one path segment, ? matches one character, " +
			"and ** matches any number of segments, so **/*_test.go finds every test file. " +
			"Results are relative paths in alphabetical order.",
		Params: object(map[string]Property{
			"pattern": {Type: "string", Description: "The pattern to match, for example **/*.go or internal/**/*_test.go."},
			"path":    {Type: "string", Description: "Directory to search under, relative to the working directory. Defaults to the working directory itself."},
		}, "pattern"),
	}
}

type globArgs struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path"`
}

// Approval is empty: matching names is a read inside the pane's own directory,
// and a path outside it is refused rather than asked about.
func (t *globTool) Approval(json.RawMessage) string { return "" }

func (t *globTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var a globArgs
	if err := decode(args, &a); err != nil {
		return "", err
	}
	if strings.TrimSpace(a.Pattern) == "" {
		return "", fmt.Errorf("pattern is required")
	}
	if err := validGlob(a.Pattern); err != nil {
		return "", err
	}
	base, err := t.root.Resolve(a.Path)
	if err != nil {
		return "", err
	}
	var found []string
	truncated := false
	start, prefix := walkStart(base, a.Pattern)
	err = walkFiles(ctx, start, func(_, rel string, _ fs.DirEntry) error {
		rel = joinRel(prefix, rel)
		if !matchGlob(a.Pattern, rel) {
			return nil
		}
		found = append(found, rel)
		if len(found) == maxListed {
			truncated = true
			return errStopWalk
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if len(found) == 0 {
		return fmt.Sprintf("No files match %s.", a.Pattern), nil
	}
	var b strings.Builder
	for _, rel := range sortedStrings(found) {
		fmt.Fprintf(&b, "%s\n", joinRel(t.root.Rel(base), rel))
	}
	if truncated {
		fmt.Fprintf(&b, "[stopped at %d matches; narrow the pattern]\n", maxListed)
	}
	return b.String(), nil
}

// matchGlob matches a slash-separated path against a pattern in which **
// spans any number of segments, including none: **/*.go is what a model writes
// when it means every Go file, and it would be a poor answer that left out the
// ones in the top directory. filepath.Match has no ** of its own, so the
// pattern is matched a segment at a time with ** allowed to consume as many as
// it needs.
func matchGlob(pattern, name string) bool {
	pattern = strings.TrimPrefix(filepath.ToSlash(pattern), "./")
	name = strings.TrimPrefix(filepath.ToSlash(name), "./")
	return matchSegments(strings.Split(pattern, "/"), strings.Split(name, "/"))
}

// validGlob refuses a pattern that cannot match anything because it is not a
// pattern at all -- an unclosed [, a stray escape. Matched as it stands it
// matches nothing and says nothing, and the model told "no files match" goes
// looking for files that are there under a pattern it wrote wrongly.
func validGlob(pattern string) error {
	for _, seg := range strings.Split(filepath.ToSlash(pattern), "/") {
		if seg == "**" {
			continue
		}
		// Match checks the whole of a pattern before it says no.
		if _, err := path.Match(seg, ""); err != nil {
			return fmt.Errorf("%q is not a valid glob (%v): check its [ and ] and backslashes", pattern, err)
		}
	}
	return nil
}

// matchSegments matches a pattern against a name a segment at a time.
//
// Each ** tries every number of segments it could take, and without a record
// of what has failed a pattern with many of them tried every way of sharing
// the name out between them: forty against a path thirty deep never finished,
// on one file, where Ctrl+C cannot reach. What is left to match after a ** is
// only ever some later part of the pattern against some later part of the
// name, so a combination that failed once is remembered and not tried again,
// which bounds the work by the two lengths rather than by how many ** there are.
func matchSegments(pat, name []string) bool {
	var failed []bool // by pattern index and name index, once a ** is met
	var match func(p, n int) bool
	match = func(p, n int) bool {
		for p < len(pat) {
			if pat[p] == "**" {
				// A trailing ** matches whatever is left, including nothing.
				if p == len(pat)-1 {
					return true
				}
				if failed == nil {
					failed = make([]bool, len(pat)*(len(name)+1))
				}
				key := p*(len(name)+1) + n
				if failed[key] {
					return false
				}
				for i := n; i <= len(name); i++ {
					if match(p+1, i) {
						return true
					}
				}
				failed[key] = true
				return false
			}
			if n == len(name) {
				return false
			}
			ok, err := path.Match(pat[p], name[n])
			if err != nil || !ok {
				return false
			}
			p, n = p+1, n+1
		}
		return n == len(name)
	}
	return match(0, 0)
}

// walkStart is where a walk for pattern under base begins, and the part of the
// path it stepped over: the directories the pattern names before its first
// wildcard -- src/app of src/app/**/*.ts -- where they are there, and base
// where they are not.
//
// A walk from base visits every file under it to match each against the
// pattern, and the patterns a model writes begin with a directory more often
// than not: src/**/*.go in a tree with a node_modules walked all ten thousand
// files of it to find fifty. Only a directory of exactly the name the pattern
// gives is stepped into, which is all a walk from base would have matched: not
// a link, which a walk never follows, not a name in another case, and not a
// version control store, which a walk steps over.
func walkStart(base, pattern string) (start, prefix string) {
	parts := strings.Split(strings.TrimPrefix(filepath.ToSlash(pattern), "./"), "/")
	start = base
	var named []string
	for _, p := range parts[:len(parts)-1] {
		if p == "" || p == "." || p == ".." || strings.ContainsAny(p, `*?[\`) || skipDir(p) || !isDirEntry(start, p) {
			break
		}
		start = filepath.Join(start, p)
		named = append(named, p)
	}
	return start, strings.Join(named, "/")
}

// isDirEntry reports whether dir holds a directory called exactly name.
func isDirEntry(dir, name string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.Name() == name {
			return e.IsDir()
		}
	}
	return false
}

// joinRel puts a path relative to a search base back together as a path
// relative to the root, so that every path a tool reports means the same thing
// wherever the search started.
func joinRel(base, rel string) string {
	if base == "." || base == "" {
		return rel
	}
	return base + "/" + rel
}

// walkFiles visits every file under root, giving fn the absolute path and the
// path relative to root with forward slashes.
//
// It never descends into a version control store and never follows a symbolic
// link. The links are the important half: confinement here is by path, and a
// link inside the tree is free to point outside it, so a walk that followed
// one would hand back a file the tools are not allowed to open.
func walkFiles(ctx context.Context, root string, fn func(abs, rel string, d fs.DirEntry) error) error {
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		// A walk of a large tree is the one read long enough for somebody to
		// give up on, and Ctrl+C has to reach it wherever it has got to.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err != nil {
			// A directory that cannot be read is skipped rather than ending
			// the search: one unreadable corner should not cost the model the
			// answer that was in the rest of the tree.
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if p != root && skipDir(d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		// Nor is a pipe, a socket or a device handed on: a named pipe in the
		// tree, opened by grep to be searched, waits for a writer forever.
		// Only those: on Windows a file behind a reparse point of another
		// kind -- a OneDrive placeholder, a deduplicated file -- is reported
		// as irregular, and reads like any other.
		if notAFile(d.Type()) {
			return nil
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return nil
		}
		return fn(p, filepath.ToSlash(rel), d)
	})
	if errors.Is(err, errStopWalk) {
		return nil
	}
	return err
}

// skipDir names the directories a search steps over. They hold a version
// control system's own storage: thousands of files that are nobody's source
// and that would drown any honest answer.
func skipDir(name string) bool {
	switch name {
	case ".git", ".hg", ".svn":
		return true
	}
	return false
}
