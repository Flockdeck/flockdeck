package transcript

import (
	"fmt"
	"strconv"
	"strings"
)

// diffLineLimit bounds how large the two sides of an edit may be before a real
// line-by-line comparison is skipped in favour of the cheap "every line
// changed" rendering. The comparison is a classic LCS table, quadratic in the
// line counts, and an edit tool's before/after text is ordinarily a small
// fragment of a file -- something large enough to matter here is unusual
// enough that a coarse diff for it is a fair trade against ever blocking on
// one.
const diffLineLimit = 1500

// unifiedDiff renders the difference between before and after as a unified
// diff naming path on both sides, in the style of `diff -u`.
//
// Line numbers are relative to the fragment given, not the file it came from:
// an Edit or MultiEdit call is handed only the text around the change, never
// the whole file, so there is no file position to report truthfully.
func unifiedDiff(path, before, after string) string {
	a, b := splitLines(before), splitLines(after)
	var ops []diffOp
	if len(a)*len(b) > diffLineLimit*diffLineLimit || len(a) > diffLineLimit || len(b) > diffLineLimit {
		ops = coarseDiff(a, b)
	} else {
		ops = lcsDiff(a, b)
	}
	return renderUnified(path, ops)
}

// EditPair is one edit's old and new text, as MultiEdit's own input carries
// several of them for one file.
type EditPair struct {
	OldString string
	NewString string
}

// EditDiff, MultiEditDiff and WriteDiff let another package build the same
// inline diff a finished Edit, MultiEdit or Write tool row shows, straight
// from a call's own input -- in particular, a permission prompt built from a
// PreToolUse call before the tool has run at all, which has nothing else to
// diff against.
func EditDiff(path, oldString, newString string) string {
	return unifiedDiff(path, oldString, newString)
}

// MultiEditDiff renders every edit of a MultiEdit call as its own hunk,
// oldest first, the same order toolDiff already builds them in.
func MultiEditDiff(path string, edits []EditPair) string {
	var b strings.Builder
	for i, e := range edits {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(unifiedDiff(path, e.OldString, e.NewString))
	}
	return b.String()
}

// WriteDiff renders a Write call as a diff against nothing: every line of
// content arrives as added.
func WriteDiff(path, content string) string { return writeDiff(path, content) }

// writeDiff renders a Write call as a diff against nothing: every line of
// content arrives as added.
func writeDiff(path, content string) string {
	lines := splitLines(content)
	ops := make([]diffOp, len(lines))
	for i, l := range lines {
		ops[i] = diffOp{kind: opAdd, text: l}
	}
	return renderUnified(path, ops)
}

// splitLines splits text into lines without their line breaks. Trailing text
// with no line break after it is still a line; a wholly empty string is none.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	lines := strings.Split(s, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" && strings.HasSuffix(s, "\n") {
		lines = lines[:len(lines)-1]
	}
	return lines
}

type opKind int

const (
	opEqual opKind = iota
	opDel
	opAdd
)

type diffOp struct {
	kind opKind
	text string
}

// lcsDiff compares two line sequences by their longest common subsequence,
// the same technique `diff` itself uses, and returns the edit script as a run
// of equal, deleted and added lines.
func lcsDiff(a, b []string) []diffOp {
	n, m := len(a), len(b)
	// table[i][j] is the LCS length of a[i:] and b[j:].
	table := make([][]int32, n+1)
	for i := range table {
		table[i] = make([]int32, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				table[i][j] = table[i+1][j+1] + 1
			} else if table[i+1][j] >= table[i][j+1] {
				table[i][j] = table[i+1][j]
			} else {
				table[i][j] = table[i][j+1]
			}
		}
	}
	var ops []diffOp
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			ops = append(ops, diffOp{opEqual, a[i]})
			i++
			j++
		case table[i+1][j] >= table[i][j+1]:
			ops = append(ops, diffOp{opDel, a[i]})
			i++
		default:
			ops = append(ops, diffOp{opAdd, b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, diffOp{opDel, a[i]})
	}
	for ; j < m; j++ {
		ops = append(ops, diffOp{opAdd, b[j]})
	}
	return ops
}

// coarseDiff stands in for lcsDiff when the two sides are too large to
// compare line by line: every line of the old side is removed and every line
// of the new side added, which is honest about the size of the change even
// though it draws no line as unchanged that in fact was.
func coarseDiff(a, b []string) []diffOp {
	ops := make([]diffOp, 0, len(a)+len(b))
	for _, l := range a {
		ops = append(ops, diffOp{opDel, l})
	}
	for _, l := range b {
		ops = append(ops, diffOp{opAdd, l})
	}
	return ops
}

// diffContext is how many unchanged lines are kept around a change, the way
// `diff -u` does by default.
const diffContext = 3

// renderUnified turns an edit script into unified-diff text: a --- / +++
// header naming path on both sides, and one @@ hunk holding every change with
// up to diffContext lines of unchanged text around it.
//
// One hunk rather than several split apart by any long unchanged run in the
// middle: what is diffed here is an edit tool's own before/after fragment,
// not a whole file, so an unchanged run in the middle of it is already part
// of what the call touched and is worth showing whole rather than eliding.
func renderUnified(path string, ops []diffOp) string {
	// Trim the leading and trailing runs of unchanged lines down to
	// diffContext, keeping track of how many lines that drops from the start
	// of each side so the hunk header still names the right position.
	start := 0
	for start < len(ops) && ops[start].kind == opEqual {
		start++
	}
	end := len(ops)
	for end > start && ops[end-1].kind == opEqual {
		end--
	}
	if start == len(ops) {
		// Nothing changed at all.
		return ""
	}
	leadFrom := start - min(start, diffContext)
	trailTo := min(len(ops), end+diffContext)
	kept := ops[leadFrom:trailTo]

	aStart, bStart := 1, 1
	for _, op := range ops[:leadFrom] {
		if op.kind != opAdd {
			aStart++
		}
		if op.kind != opDel {
			bStart++
		}
	}

	var lines []string
	aCount, bCount := 0, 0
	for _, op := range kept {
		switch op.kind {
		case opEqual:
			lines = append(lines, " "+op.text)
			aCount++
			bCount++
		case opDel:
			lines = append(lines, "-"+op.text)
			aCount++
		case opAdd:
			lines = append(lines, "+"+op.text)
			bCount++
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "--- a/%s\n+++ b/%s\n", path, path)
	fmt.Fprintf(&b, "@@ -%s +%s @@\n", hunkRange(aStart, aCount), hunkRange(bStart, bCount))
	for _, l := range lines {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	return b.String()
}

func hunkRange(start, count int) string {
	if count == 1 {
		return strconv.Itoa(start)
	}
	if count == 0 {
		// diff -u reports an empty side one line before where it would begin.
		return strconv.Itoa(start-1) + ",0"
	}
	return strconv.Itoa(start) + "," + strconv.Itoa(count)
}
