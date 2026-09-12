package tool

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"regexp"
	"strings"
)

const (
	// grepDefaultResults is how many matching lines one search reports when
	// the model does not say. A search that returns more than this has usually
	// asked the wrong question, and the answer is a narrower pattern rather
	// than a longer list.
	grepDefaultResults = 200
	// grepMaxLine bounds one reported line, so that a minified bundle with a
	// single matching line does not arrive whole.
	grepMaxLine = 300
	// grepScanLine bounds how much of one line is searched: the first
	// megabyte, which is all of any line anybody wrote by hand.
	grepScanLine = 1 << 20
)

// grepTool searches file contents by regular expression.
type grepTool struct{ root *Root }

func (t *grepTool) Name() string { return "grep" }

func (t *grepTool) Describe() Schema {
	return Schema{
		Name: t.Name(),
		Description: "Search file contents by regular expression (Go/RE2 syntax) under the working " +
			"directory. Reports matches as path:line: text, in path order. Binary files and " +
			"version control stores are skipped.",
		Params: object(map[string]Property{
			"pattern":     {Type: "string", Description: "Regular expression to search for."},
			"path":        {Type: "string", Description: "Directory or file to search, relative to the working directory. Defaults to the working directory itself."},
			"glob":        {Type: "string", Description: "Only search files whose path matches this glob, for example **/*.go."},
			"ignore_case": {Type: "boolean", Description: "Match without regard to case."},
			"files_only":  {Type: "boolean", Description: "Report only the paths of matching files, not the matching lines."},
			"max_results": {Type: "integer", Description: "How many matches to report. Defaults to 200."},
		}, "pattern"),
	}
}

type grepArgs struct {
	Pattern    string `json:"pattern"`
	Path       string `json:"path"`
	Glob       string `json:"glob"`
	IgnoreCase bool   `json:"ignore_case"`
	FilesOnly  bool   `json:"files_only"`
	MaxResults int    `json:"max_results"`
}

// Approval is empty: searching is a read inside the pane's own directory, and
// a path outside it is refused rather than asked about.
func (t *grepTool) Approval(json.RawMessage) string { return "" }

func (t *grepTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var a grepArgs
	if err := decode(args, &a); err != nil {
		return "", err
	}
	if strings.TrimSpace(a.Pattern) == "" {
		return "", fmt.Errorf("pattern is required")
	}
	expr := a.Pattern
	if a.IgnoreCase {
		expr = "(?i)" + expr
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		return "", fmt.Errorf("pattern is not a valid regular expression: %w", err)
	}
	if a.Glob != "" {
		if err := validGlob(a.Glob); err != nil {
			return "", err
		}
	}
	base, err := t.root.Resolve(a.Path)
	if err != nil {
		return "", err
	}
	limit := a.MaxResults
	if limit <= 0 {
		limit = grepDefaultResults
	}
	if limit > maxListed {
		limit = maxListed
	}

	// A single file is searched directly, because a model that has just been
	// told a path by glob will pass it here rather than the directory above it.
	var out []string
	files := 0
	truncated := false
	search := func(abs, display string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		hits, err := matchFile(abs, re, a.FilesOnly, limit-len(out))
		if err != nil || len(hits) == 0 {
			return nil
		}
		files++
		if a.FilesOnly {
			out = append(out, display)
		} else {
			for _, h := range hits {
				out = append(out, display+":"+h)
			}
		}
		if len(out) >= limit {
			truncated = true
			return errStopWalk
		}
		return nil
	}

	if info, err := os.Stat(base); err == nil && !info.IsDir() {
		if err := search(base, t.root.Rel(base)); err != nil && !errors.Is(err, errStopWalk) {
			return "", err
		}
	} else {
		err = walkFiles(ctx, base, func(abs, rel string, _ fs.DirEntry) error {
			if a.Glob != "" && !matchGlob(a.Glob, rel) {
				return nil
			}
			return search(abs, joinRel(t.root.Rel(base), rel))
		})
		if err != nil {
			return "", err
		}
	}

	if len(out) == 0 {
		return fmt.Sprintf("No matches for %s.", a.Pattern), nil
	}
	var b strings.Builder
	for _, line := range out {
		fmt.Fprintf(&b, "%s\n", line)
	}
	if truncated {
		fmt.Fprintf(&b, "[stopped at %d matches; narrow the pattern]\n", limit)
	} else if !a.FilesOnly {
		fmt.Fprintf(&b, "\n%d matches in %d files.\n", len(out), files)
	}
	return b.String(), nil
}

// matchFile returns the matching lines of one file as "line: text", or a
// single empty entry when only the fact of a match is wanted.
//
// The file is read a line at a time rather than whole: a search runs over
// everything in the tree, and the largest file in a checkout should not decide
// how much memory the pane needs.
func matchFile(abs string, re *regexp.Regexp, filesOnly bool, room int) ([]string, error) {
	if room <= 0 {
		return nil, nil
	}
	f, err := os.Open(abs)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	r := bufio.NewReaderSize(f, 64<<10)
	if head, _ := r.Peek(8 << 10); looksBinary(head) {
		return nil, nil
	}
	var hits []string
	for n := 1; ; n++ {
		// Only so much of a line is searched. One line of a minified bundle or
		// a log can be as long as the file, and read whole it would be held in
		// memory whole, however large.
		text, _, err := nextLine(r, grepScanLine)
		if err != nil && (err != io.EOF || text == "") {
			return hits, nil
		}
		if re.MatchString(text) {
			if filesOnly {
				return []string{""}, nil
			}
			hits = append(hits, fmt.Sprintf("%d: %s", n, clip(text)))
			if len(hits) >= room {
				return hits, nil
			}
		}
		if err != nil {
			return hits, nil
		}
	}
}

// clip shortens a matching line to something a terminal can show on one row.
//
// It counts its way along rather than converting the whole line to runes,
// which for a line of a megabyte is four megabytes to keep three hundred.
func clip(s string) string {
	n := 0
	for i := range s {
		if n == grepMaxLine {
			return s[:i] + "..."
		}
		n++
	}
	return s
}
