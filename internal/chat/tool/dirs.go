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

	var b strings.Builder
	fmt.Fprintf(&b, "%s:\n", t.root.Rel(abs))
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
	err = walkFiles(ctx, base, func(_, rel string, _ fs.DirEntry) error {
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

func matchSegments(pat, name []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			// A trailing ** matches whatever is left, including nothing.
			if len(pat) == 1 {
				return true
			}
			for i := 0; i <= len(name); i++ {
				if matchSegments(pat[1:], name[i:]) {
					return true
				}
			}
			return false
		}
		if len(name) == 0 {
			return false
		}
		ok, err := path.Match(pat[0], name[0])
		if err != nil || !ok {
			return false
		}
		pat, name = pat[1:], name[1:]
	}
	return len(name) == 0
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
