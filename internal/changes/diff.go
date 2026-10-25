package changes

import (
	"context"
	"errors"
	"log"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jmwri/flockdeck/internal/gitx"
)

const (
	// maxDiffLines and maxDiffBytes cut a diff that is too big to send.
	maxDiffLines = 5000
	maxDiffBytes = 1 << 20
	// maxContextLines is the most lines diffContext returns at once.
	maxContextLines = 200
	// maxContextFile is the largest file diffContext reads.
	maxContextFile = 8 << 20
	// maxIntraTokens bounds the word diff of one pair of lines.
	maxIntraTokens = 400
	// maxIntraCells bounds the word diffs of one diff: all the tables together.
	maxIntraCells = 4 << 20
	// wholeFile is the context that shows a whole file.
	wholeFile = 100_000_000
)

// Refusal is why a command was not carried out, as the code and words of the
// changesResult that tells the window.
type Refusal struct {
	Code string
	Msg  string
}

func (r *Refusal) Error() string { return r.Msg }

func badRequest(msg string) *Refusal { return &Refusal{Code: "badRequest", Msg: msg} }

// Line is one line of a hunk.
type Line struct {
	T     string   `json:"t"`
	Text  string   `json:"text"`
	OldNo int      `json:"oldNo,omitempty"`
	NewNo int      `json:"newNo,omitempty"`
	Intra [][2]int `json:"intra,omitempty"`
}

// Hunk is one hunk of a diff.
type Hunk struct {
	OldStart int    `json:"oldStart"`
	OldLines int    `json:"oldLines"`
	NewStart int    `json:"newStart"`
	NewLines int    `json:"newLines"`
	Header   string `json:"header"`
	Lines    []Line `json:"lines"`
}

// Truncated says how much of a cut diff was kept.
type Truncated struct {
	Lines int `json:"lines"`
	Bytes int `json:"bytes"`
}

// DiffHunks is the diffHunks message, less its type.
type DiffHunks struct {
	Req        string     `json:"req,omitempty"`
	Pane       string     `json:"pane"`
	Path       string     `json:"path"`
	OldPath    string     `json:"oldPath,omitempty"`
	Against    string     `json:"against,omitempty"`
	HeadSha    string     `json:"headSha,omitempty"`
	Worktree   bool       `json:"worktree,omitempty"`
	Binary     bool       `json:"binary,omitempty"`
	SizeBefore *int64     `json:"sizeBefore,omitempty"`
	SizeAfter  *int64     `json:"sizeAfter,omitempty"`
	EolChange  bool       `json:"eolChange,omitempty"`
	Hunks      []Hunk     `json:"hunks"`
	TotalLines *int       `json:"totalLines,omitempty"`
	Truncated  *Truncated `json:"truncated,omitempty"`
	Error      string     `json:"error,omitempty"`
}

// DiffContext is the diffContext message, less its type.
type DiffContext struct {
	Req   string `json:"req,omitempty"`
	Pane  string `json:"pane"`
	Path  string `json:"path"`
	Lines []Line `json:"lines"`
	Eof   bool   `json:"eof"`
	Error string `json:"error,omitempty"`
}

// DiffRequest is what diffHunks asks for. Context is -1 for the whole file, and
// DefaultContext asks for the usual three lines.
type DiffRequest struct {
	File             string
	Against          string
	Context          int
	IgnoreWhitespace bool
}

// DefaultContext is a Context that was not asked for.
const DefaultContext = -2

// CleanPath checks a path a window sent and returns it in the form git lists it:
// relative to the repository root, with forward slashes, nothing absolute, no
// drive and no element that leaves the checkout. The caller still checks that it
// is one of the branch's files.
func CleanPath(p string) (string, bool) {
	if p == "" || len(p) > 4096 || strings.ContainsRune(p, 0) {
		return "", false
	}
	if runtime.GOOS == "windows" {
		p = strings.ReplaceAll(p, "\\", "/")
	}
	if strings.HasPrefix(p, "/") || filepath.VolumeName(p) != "" || (len(p) > 1 && p[1] == ':') {
		return "", false
	}
	for _, el := range strings.Split(p, "/") {
		if el == ".." {
			return "", false
		}
	}
	c := path.Clean(p)
	if c == "." || c != p {
		return "", false
	}
	return c, true
}

// locate finds a file of the branch by either of its paths.
func locate(b Branch, file string) (File, bool) {
	for _, f := range b.AllFiles() {
		if f.Path == file || (f.OldPath != "" && f.OldPath == file) {
			return f, true
		}
	}
	return File{}, false
}

// checkedFile reads the checkout and finds the file in it, or says why not.
func checkedFile(ctx context.Context, pane, cwd, file string) (Branch, File, *Refusal) {
	clean, ok := CleanPath(file)
	if !ok {
		return Branch{}, File{}, badRequest("file is not a path inside the checkout")
	}
	b := cachedBranch(ctx, pane, cwd)
	if b.Error != "" {
		return b, File{}, &Refusal{Code: "noBranch", Msg: b.Error}
	}
	f, ok := locate(b, clean)
	if !ok {
		return b, File{}, badRequest("file is not one of the branch's files")
	}
	return b, f, nil
}

// Diff makes the diffHunks answer for one file of one pane's branch.
func Diff(ctx context.Context, pane, cwd string, req DiffRequest) (DiffHunks, *Refusal) {
	against := req.Against
	if against == "" {
		against = "merge-base"
	}
	if against != "worktree" && against != "head" && against != "merge-base" {
		return DiffHunks{}, badRequest("against is worktree, head or merge-base")
	}
	n := req.Context
	if n == DefaultContext {
		n = 3
	}
	if n < -1 || n > 50 {
		return DiffHunks{}, badRequest("context is -1 or 0 to 50")
	}
	b, f, ref := checkedFile(ctx, pane, cwd, req.File)
	if ref != nil {
		return DiffHunks{}, ref
	}
	out := DiffHunks{Pane: pane, Path: f.Path, OldPath: f.OldPath, Against: against, HeadSha: b.HeadSha, Hunks: []Hunk{}}
	fail := func(msg string, err error) (DiffHunks, *Refusal) {
		if err != nil {
			// What git said can hold paths of this machine; it goes to the log and not to the window.
			log.Printf("changes: the diff of %s in pane %s failed: %v", f.Path, pane, firstLine(err.Error()))
		}
		return DiffHunks{Pane: pane, Path: f.Path, Hunks: []Hunk{}, Error: msg}, nil
	}

	paths := []string{f.Path}
	if f.OldPath != "" {
		paths = append(paths, f.OldPath)
	}
	untracked := false
	if f.Status == "A" && against != "head" {
		if o, err := git(ctx, cwd, "ls-files", "--others", "--exclude-standard", "-z", "--", f.Path); err == nil && strings.TrimSpace(strings.ReplaceAll(o, "\x00", "")) != "" {
			untracked = true
		}
	}
	if against != "head" && !parentsArePlain(cwd, f.Path) {
		// git would read the working tree through a link, out of the checkout.
		return DiffHunks{}, badRequest("file is not a path inside the checkout")
	}
	if against != "head" {
		// The new side is the working tree. It is the head's own only when this
		// file has no uncommitted change.
		out.Worktree = untracked
		if !untracked {
			if o, err := git(ctx, cwd, append([]string{"diff", "--name-only", "-z", "--no-ext-diff", "--end-of-options", b.HeadSha, "--"}, paths...)...); err == nil && strings.ReplaceAll(o, "\x00", "") != "" {
				out.Worktree = true
			}
		}
	}

	if f.Binary {
		out.Binary, out.SizeBefore, out.SizeAfter = true, f.SizeBefore, f.SizeAfter
		if against == "head" {
			// The sizes of the file are for the merge base against the working tree; a
			// committed diff has the sizes of the two commits.
			old := f.Path
			if f.OldPath != "" {
				old = f.OldPath
			}
			out.SizeBefore = blobSize(ctx, cwd, b.BaseSha, old)
			out.SizeAfter = blobSize(ctx, cwd, b.HeadSha, f.Path)
		}
		zero := 0
		out.TotalLines = &zero
		return out, nil
	}

	var text string
	switch {
	case untracked:
		var binary bool
		var size int64
		text, binary, size = syntheticAdd(cwd, f.Path)
		if binary {
			out.Binary = true
			out.SizeAfter = &size
			zero := 0
			out.TotalLines = &zero
			return out, nil
		}
	case against == "head" && f.Status == "A" && !tracked(ctx, cwd, f.Path):
		// Added in the working tree only: nothing is committed.
	default:
		flags := []string{"-M", "-U" + strconv.Itoa(contextArg(n)), "--no-color", "--no-ext-diff", "--no-textconv"}
		if req.IgnoreWhitespace {
			flags = append(flags, "-w")
		}
		var revs []string
		switch against {
		case "worktree":
			revs = []string{b.HeadSha}
		case "head":
			revs = []string{b.BaseSha, b.HeadSha}
		default:
			revs = []string{b.BaseSha}
		}
		build := func(extra ...string) []string {
			a := append([]string{"diff"}, flags...)
			a = append(a, extra...)
			a = append(a, "--end-of-options")
			a = append(a, revs...)
			a = append(a, "--")
			return append(a, paths...)
		}
		o, err := git(ctx, cwd, build()...)
		if errors.Is(err, gitx.ErrTooLarge) {
			return fail("the diff is too large to read", nil)
		}
		if err != nil {
			return fail("could not read the diff", err)
		}
		text = o
		if strings.TrimSpace(text) != "" && !req.IgnoreWhitespace && strings.Contains(text, "\r") {
			// The same diff, with a carriage return before a line end not counted: when
			// that leaves nothing, the file changed only in its line endings.
			if o2, err := git(ctx, cwd, build("--ignore-cr-at-eol")...); err == nil && len(parseUnified(o2, 0, 0).hunks) == 0 && len(parseUnified(text, 0, 0).hunks) > 0 {
				out.EolChange = true
				zero := 0
				out.TotalLines = &zero
				return out, nil
			}
		}
	}

	p := parseUnified(text, maxDiffLines, maxDiffBytes)
	out.Hunks = p.hunks
	if out.Hunks == nil {
		out.Hunks = []Hunk{}
	}
	total := p.total
	out.TotalLines = &total
	if p.cut {
		out.Truncated = &Truncated{Lines: p.keptLines, Bytes: p.keptBytes}
	}
	markIntra(ctx, out.Hunks)
	return out, nil
}

// tracked says whether git has the path in its index.
func tracked(ctx context.Context, cwd, p string) bool {
	o, err := git(ctx, cwd, "ls-files", "-z", "--", p)
	return err == nil && strings.ReplaceAll(o, "\x00", "") != ""
}

func contextArg(n int) int {
	if n < 0 {
		return wholeFile
	}
	return n
}

// syntheticAdd is the diff of a file git does not know yet: all of it added.
func syntheticAdd(cwd, rel string) (text string, binary bool, size int64) {
	data, size, ok := readWorking(cwd, rel)
	if !ok {
		return "", false, size
	}
	head := data
	if len(head) > 8000 {
		head = head[:8000]
	}
	for _, c := range head {
		if c == 0 {
			return "", true, size
		}
	}
	lines := splitLines(string(data))
	if len(lines) == 0 {
		return "", false, size
	}
	var sb strings.Builder
	sb.WriteString("@@ -0,0 +1," + strconv.Itoa(len(lines)) + " @@\n")
	for _, l := range lines {
		sb.WriteString("+" + l + "\n")
	}
	return sb.String(), false, size
}

// splitLines splits text on line ends and drops the carriage return before each.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	for i, l := range lines {
		lines[i] = strings.TrimSuffix(l, "\r")
	}
	return lines
}

var hunkHeader = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@ ?(.*)$`)

type parsed struct {
	hunks                []Hunk
	total                int
	cut                  bool
	keptLines, keptBytes int
}

// parseUnified reads the hunks of a unified diff. It keeps at most maxLines lines
// and maxBytes bytes of text (0 for no limit), counting the rest in total. A hunk
// that is cut has its counts made to match the lines kept.
func parseUnified(diff string, maxLines, maxBytes int) parsed {
	var p parsed
	var cur *Hunk
	oldNo, newNo := 0, 0
	closeHunk := func() {
		if cur == nil {
			return
		}
		if p.cut {
			o, n := 0, 0
			for _, l := range cur.Lines {
				if l.T != "add" {
					o++
				}
				if l.T != "del" {
					n++
				}
			}
			cur.OldLines, cur.NewLines = o, n
		}
		if len(cur.Lines) > 0 {
			p.hunks = append(p.hunks, *cur)
		}
		cur = nil
	}
	inHunk := false
	for _, raw := range strings.Split(diff, "\n") {
		if m := hunkHeader.FindStringSubmatch(raw); m != nil {
			closeHunk()
			inHunk = true
			atoi := func(s string, def int) int {
				if s == "" {
					return def
				}
				n, _ := strconv.Atoi(s)
				return n
			}
			h := Hunk{OldStart: atoi(m[1], 0), OldLines: atoi(m[2], 1), NewStart: atoi(m[3], 0), NewLines: atoi(m[4], 1), Header: m[5], Lines: []Line{}}
			oldNo, newNo = h.OldStart, h.NewStart
			if !p.cut {
				cur = &h
			}
			continue
		}
		if !inHunk || raw == "" || raw[0] == '\\' {
			continue
		}
		var l Line
		switch raw[0] {
		case ' ':
			l = Line{T: "ctx", Text: strings.TrimSuffix(raw[1:], "\r"), OldNo: oldNo, NewNo: newNo}
			oldNo++
			newNo++
		case '+':
			l = Line{T: "add", Text: strings.TrimSuffix(raw[1:], "\r"), NewNo: newNo}
			newNo++
		case '-':
			l = Line{T: "del", Text: strings.TrimSuffix(raw[1:], "\r"), OldNo: oldNo}
			oldNo++
		default:
			continue
		}
		p.total++
		if p.cut || cur == nil {
			continue
		}
		if (maxLines > 0 && p.keptLines >= maxLines) || (maxBytes > 0 && p.keptBytes+len(l.Text) > maxBytes) {
			p.cut = true
			continue
		}
		cur.Lines = append(cur.Lines, l)
		p.keptLines++
		p.keptBytes += len(l.Text)
	}
	closeHunk()
	return p
}

// markIntra gives the paired deleted and added lines of each hunk their
// word-level changes: the i-th line of a run of deleted lines is paired with the
// i-th of the added lines that follow it.
//
// The work is bounded: it waits for a git slot, as the other work of a read does,
// stops when ctx ends, and pairs lines only while the diff as a whole has used
// fewer than maxIntraCells table cells.
func markIntra(ctx context.Context, hunks []Hunk) {
	select {
	case gitSlots <- struct{}{}:
		defer func() { <-gitSlots }()
	case <-ctx.Done():
		return
	}
	spent := 0
	for hi := range hunks {
		ls := hunks[hi].Lines
		for i := 0; i < len(ls); {
			if ls[i].T != "del" {
				i++
				continue
			}
			d := i
			for i < len(ls) && ls[i].T == "del" {
				i++
			}
			a := i
			for i < len(ls) && ls[i].T == "add" {
				i++
			}
			dels, adds := a-d, i-a
			for k := 0; k < dels && k < adds; k++ {
				if ctx.Err() != nil {
					return
				}
				o, n := wordDiffBudget(ls[d+k].Text, ls[a+k].Text, &spent)
				if len(o) > 0 && len(n) > 0 {
					ls[d+k].Intra, ls[a+k].Intra = o, n
				}
			}
		}
	}
}

// tokens splits a line into runs of letters and digits, runs of spaces, and single
// other characters.
func tokens(s string) []string {
	var out []string
	var cur []rune
	kind := 0
	flush := func() {
		if len(cur) > 0 {
			out = append(out, string(cur))
			cur = cur[:0]
		}
	}
	for _, r := range s {
		k := 3
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_':
			k = 1
		case unicode.IsSpace(r):
			k = 2
		}
		if k == 3 || k != kind {
			flush()
		}
		kind = k
		cur = append(cur, r)
	}
	flush()
	return out
}

// wordDiff gives the ranges, in code points, of the tokens of old and new that
// are not in the longest run of tokens they share.
func wordDiff(oldText, newText string) (o, n [][2]int) {
	spent := 0
	return wordDiffBudget(oldText, newText, &spent)
}

// wordDiffBudget is wordDiff that adds the cells of its table to spent, and gives
// nothing once the diff has used maxIntraCells.
func wordDiffBudget(oldText, newText string, spent *int) (o, n [][2]int) {
	a, b := tokens(oldText), tokens(newText)
	if len(a) == 0 || len(b) == 0 || len(a) > maxIntraTokens || len(b) > maxIntraTokens {
		return nil, nil
	}
	cells := (len(a) + 1) * (len(b) + 1)
	if *spent+cells > maxIntraCells {
		return nil, nil
	}
	*spent += cells
	lcs := make([][]int, len(a)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	sameA, sameB := make([]bool, len(a)), make([]bool, len(b))
	for i, j := 0, 0; i < len(a) && j < len(b); {
		switch {
		case a[i] == b[j]:
			sameA[i], sameB[j] = true, true
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			i++
		default:
			j++
		}
	}
	return ranges(a, sameA), ranges(b, sameB)
}

// ranges merges the tokens that are not shared into [start, end) pairs.
func ranges(toks []string, same []bool) [][2]int {
	var out [][2]int
	pos := 0
	for i, t := range toks {
		w := utf8.RuneCountInString(t)
		if !same[i] {
			if k := len(out); k > 0 && out[k-1][1] == pos {
				out[k-1][1] = pos + w
			} else {
				out = append(out, [2]int{pos, pos + w})
			}
		}
		pos += w
	}
	return out
}

// ReadContext makes the diffContext answer: the unchanged lines of one side of a
// file from line from to line to, at most maxContextLines.
func ReadContext(ctx context.Context, pane, cwd, file, against, side string, from, to int) (DiffContext, *Refusal) {
	if against == "" {
		against = "merge-base"
	}
	if against != "worktree" && against != "head" && against != "merge-base" {
		return DiffContext{}, badRequest("against is worktree, head or merge-base")
	}
	if side != "old" && side != "new" {
		return DiffContext{}, badRequest("side is old or new")
	}
	if from < 1 || to < from {
		return DiffContext{}, badRequest("from and to are line numbers, from 1")
	}
	if to-from+1 > maxContextLines {
		to = from + maxContextLines - 1
	}
	b, f, ref := checkedFile(ctx, pane, cwd, file)
	if ref != nil {
		return DiffContext{}, ref
	}
	out := DiffContext{Pane: pane, Path: f.Path, Lines: []Line{}}
	var content, problem string
	var ok bool
	switch {
	case side == "new" && against != "head":
		content, problem, ok = workingText(cwd, f.Path)
	case side == "new":
		content, problem, ok = blobText(ctx, cwd, b.HeadSha, f.Path)
	default:
		rev := b.BaseSha
		if against == "worktree" {
			rev = b.HeadSha
		}
		old := f.Path
		if f.OldPath != "" {
			old = f.OldPath
		}
		content, problem, ok = blobText(ctx, cwd, rev, old)
	}
	if problem != "" {
		out.Error = problem
		return out, nil
	}
	if !ok {
		out.Eof = true
		return out, nil
	}
	lines := splitLines(content)
	for n := from; n <= to && n <= len(lines); n++ {
		l := Line{T: "ctx", Text: lines[n-1]}
		if side == "old" {
			l.OldNo = n
		} else {
			l.NewNo = n
		}
		out.Lines = append(out.Lines, l)
	}
	out.Eof = to >= len(lines)
	return out, nil
}

// workingText reads a file of the checkout as the diff sees it: a link is its
// target text and is never followed (see readWorking). found is false when there
// is no such file or it is not a regular one, and problem says why a file that is
// there was not read.
func workingText(cwd, rel string) (content, problem string, found bool) {
	data, size, ok := readWorking(cwd, rel)
	if !ok {
		if size > maxContextFile {
			return "", "the file is too large to read", true
		}
		return "", "", false
	}
	return string(data), "", true
}

// blobText reads a file as it was at a commit.
func blobText(ctx context.Context, cwd, rev, rel string) (content, problem string, found bool) {
	out, err := git(ctx, cwd, "show", "--no-textconv", "--end-of-options", rev+":"+rel)
	switch {
	case errors.Is(err, gitx.ErrTooLarge):
		return "", "the file is too large to read", true
	case ctx.Err() != nil:
		return "", "reading the file took too long", true
	case err != nil:
		return "", "", false
	case len(out) > maxContextFile:
		return "", "the file is too large to read", true
	}
	return out, "", true
}

// blobSize is the size of a file at a commit, or nil when it is not there.
func blobSize(ctx context.Context, cwd, rev, rel string) *int64 {
	o, err := git(ctx, cwd, "cat-file", "-s", "--end-of-options", rev+":"+rel)
	if err != nil {
		return nil
	}
	n, err := strconv.ParseInt(strings.TrimSpace(o), 10, 64)
	if err != nil {
		return nil
	}
	return &n
}
