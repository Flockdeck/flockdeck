package tool

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"
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
		if err := regularFile(t.root, base, info); err != nil {
			return "", err
		}
		if err := search(base, t.root.Rel(base)); err != nil && !errors.Is(err, errStopWalk) {
			return "", err
		}
	} else {
		start, prefix := base, ""
		if a.Glob != "" {
			start, prefix = walkStart(base, a.Glob)
		}
		err = walkFiles(ctx, start, func(abs, rel string, _ fs.DirEntry) error {
			rel = joinRel(prefix, rel)
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
		fmt.Fprintf(&b, "\n%d %s in %d %s.\n", len(out), plural(len(out), "match", "matches"),
			files, plural(files, "file", "files"))
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

	r, _ := utf16Text(bufio.NewReaderSize(f, 64<<10))
	if head, _ := r.Peek(8 << 10); looksBinary(head) {
		return nil, nil
	}
	var hits []string
	var spare []byte
	for n := 1; ; n++ {
		// Only so much of a line is searched. One line of a minified bundle or
		// a log can be as long as the file, and read whole it would be held in
		// memory whole, however large.
		var line []byte
		line, spare, err = lineBytes(r, grepScanLine, spare)
		if err != nil && (err != io.EOF || len(line) == 0) {
			return hits, nil
		}
		// Bytes that are not UTF-8 are left out before matching, as they
		// always were, so that a pattern matches across them. It is rare, and
		// only such a line pays for a copy.
		if !utf8.Valid(line) {
			line = []byte(strings.ToValidUTF8(string(line), ""))
		}
		if re.Match(line) {
			if filesOnly {
				return []string{""}, nil
			}
			hits = append(hits, fmt.Sprintf("%d: %s", n, clip(string(line))))
			if len(hits) >= room {
				return hits, nil
			}
		}
		if err != nil {
			return hits, nil
		}
	}
}

// lineBytes reads one line without its ending, keeping at most max bytes of
// it, for a caller that is done with each line before it asks for the next.
//
// A search reads every line of every file and matches nearly none of them, and
// made into a string each line cost two allocations, which were most of what a
// search of a large tree did: a hundred files of five thousand lines took a
// million allocations and 71 MB. A line that fits in the reader's buffer is
// handed back in place; a longer one is gathered into spare, which the caller
// passes back to be used again. What comes back is good until the next call.
func lineBytes(r *bufio.Reader, max int, spare []byte) (line, next []byte, err error) {
	chunk, err := r.ReadSlice('\n')
	if err != bufio.ErrBufferFull {
		return bytes.TrimRight(chunk, "\r\n"), spare, err
	}
	kept := append(spare[:0], chunk[:min(len(chunk), max)]...)
	for err == bufio.ErrBufferFull {
		chunk, err = r.ReadSlice('\n')
		if err != bufio.ErrBufferFull {
			chunk = bytes.TrimRight(chunk, "\r\n")
		}
		if room := max - len(kept); room > 0 {
			kept = append(kept, chunk[:min(room, len(chunk))]...)
		}
	}
	// A CRLF split across two reads leaves its carriage return here.
	kept = bytes.TrimRight(kept, "\r")
	return kept, kept, err
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
