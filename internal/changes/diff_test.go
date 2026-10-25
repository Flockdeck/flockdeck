package changes

import (
	"context"
	"strings"
	"testing"
)

func diffOf(t *testing.T, dir, file, against string) DiffHunks {
	t.Helper()
	d, ref := Diff(context.Background(), "p1", dir, DiffRequest{File: file, Against: against, Context: 0})
	if ref != nil {
		t.Fatalf("%s %s: refused: %+v", file, against, ref)
	}
	if d.Error != "" {
		t.Fatalf("%s %s: %s", file, against, d.Error)
	}
	return d
}

func changedLines(d DiffHunks) (add, del []string) {
	for _, h := range d.Hunks {
		for _, l := range h.Lines {
			switch l.T {
			case "add":
				add = append(add, l.Text)
			case "del":
				del = append(del, l.Text)
			}
		}
	}
	return add, del
}

// The three ways to ask for a diff cover different changes, and say whether the
// working tree is part of what was drawn.
func TestDiffAgainstTheWorktreeTheHeadAndTheMergeBase(t *testing.T) {
	dir := repo(t)
	runGit(t, dir, "checkout", "-q", "-b", "feat")
	write(t, dir, "a.txt", "one\ntwo committed\nthree\n")
	runGit(t, dir, "commit", "-q", "-am", "edit a")
	head := strings.TrimSpace(runGit(t, dir, "rev-parse", "HEAD"))
	write(t, dir, "a.txt", "one\ntwo committed\nthree uncommitted\n")

	mb := diffOf(t, dir, "a.txt", "merge-base")
	add, del := changedLines(mb)
	if len(add) != 2 || len(del) != 2 || !mb.Worktree || mb.HeadSha != head || mb.Against != "merge-base" {
		t.Errorf("merge-base: add %q del %q worktree %v head %s", add, del, mb.Worktree, mb.HeadSha)
	}
	hd := diffOf(t, dir, "a.txt", "head")
	add, del = changedLines(hd)
	if len(add) != 1 || add[0] != "two committed" || len(del) != 1 || hd.Worktree || hd.HeadSha != head {
		t.Errorf("head: add %q del %q worktree %v", add, del, hd.Worktree)
	}
	wt := diffOf(t, dir, "a.txt", "worktree")
	add, del = changedLines(wt)
	if len(add) != 1 || add[0] != "three uncommitted" || len(del) != 1 || del[0] != "three" || !wt.Worktree {
		t.Errorf("worktree: add %q del %q worktree %v", add, del, wt.Worktree)
	}
	if wt.TotalLines == nil || *wt.TotalLines != 2 {
		t.Errorf("total lines = %v, want 2", wt.TotalLines)
	}

	// A file with no uncommitted change is the head's own even against the merge base.
	write(t, dir, "a.txt", "one\ntwo committed\nthree\n")
	if d := diffOf(t, dir, "a.txt", "merge-base"); d.Worktree {
		t.Error("worktree is set for a file the working tree has not changed")
	}
}

func TestDiffOfAnUntrackedFileIsAllAdded(t *testing.T) {
	dir := repo(t)
	runGit(t, dir, "checkout", "-q", "-b", "feat")
	write(t, dir, "new.txt", "x\ny\n")
	d := diffOf(t, dir, "new.txt", "merge-base")
	add, del := changedLines(d)
	if len(add) != 2 || len(del) != 0 || !d.Worktree || d.Hunks[0].NewStart != 1 || d.Hunks[0].OldStart != 0 {
		t.Errorf("untracked: %+v", d)
	}
	if h := diffOf(t, dir, "new.txt", "head"); len(h.Hunks) != 0 || h.Worktree {
		t.Errorf("an untracked file in a committed diff: %+v", h)
	}
	write(t, dir, "bin.dat", "a\x00b")
	if b := diffOf(t, dir, "bin.dat", "merge-base"); !b.Binary || b.SizeAfter == nil || *b.SizeAfter != 3 || len(b.Hunks) != 0 {
		t.Errorf("binary: %+v", b)
	}
}

func TestDiffOfARenameNamesBothPathsAndMarksTheWords(t *testing.T) {
	dir := repo(t)
	runGit(t, dir, "checkout", "-q", "-b", "feat")
	runGit(t, dir, "mv", "old.txt", "renamed.txt")
	text := strings.Repeat("line of a file to rename\n", 19) + "line of a file to keep\n"
	write(t, dir, "renamed.txt", text)
	for _, name := range []string{"renamed.txt", "old.txt"} {
		d := diffOf(t, dir, name, "merge-base")
		if d.Path != "renamed.txt" || d.OldPath != "old.txt" {
			t.Fatalf("%s: path %q old %q", name, d.Path, d.OldPath)
		}
	}
	d := diffOf(t, dir, "renamed.txt", "merge-base")
	var del, add Line
	for _, l := range d.Hunks[0].Lines {
		switch l.T {
		case "del":
			del = l
		case "add":
			add = l
		}
	}
	if len(del.Intra) == 0 || len(add.Intra) == 0 {
		t.Fatalf("no word marks: del %+v add %+v", del, add)
	}
	r := []rune(add.Text)
	if got := string(r[add.Intra[0][0]:add.Intra[0][1]]); got != "keep" {
		t.Errorf("the marked word is %q, want keep", got)
	}
}

func TestDiffRefusesWhatItShouldNot(t *testing.T) {
	dir := repo(t)
	runGit(t, dir, "checkout", "-q", "-b", "feat")
	write(t, dir, "a.txt", "changed\n")
	for name, c := range map[string]struct {
		req  DiffRequest
		code string
	}{
		"absolute":     {DiffRequest{File: "/etc/passwd"}, "badRequest"},
		"drive":        {DiffRequest{File: "C:/x"}, "badRequest"},
		"parent":       {DiffRequest{File: "../a.txt"}, "badRequest"},
		"unclean":      {DiffRequest{File: "x/../a.txt"}, "badRequest"},
		"backslash up": {DiffRequest{File: "..\\a.txt"}, "badRequest"},
		"not a file":   {DiffRequest{File: "b.txt"}, "badRequest"},
		"bad against":  {DiffRequest{File: "a.txt", Against: "nope"}, "badRequest"},
		"bad context":  {DiffRequest{File: "a.txt", Context: 51}, "badRequest"},
	} {
		if c.req.Context == 0 && name != "bad context" {
			c.req.Context = DefaultContext
		}
		if _, ref := Diff(context.Background(), "p1", dir, c.req); ref == nil || ref.Code != c.code {
			t.Errorf("%s: %+v, want %s", name, ref, c.code)
		}
	}
	if _, ref := Diff(context.Background(), "p1", t.TempDir(), DiffRequest{File: "a.txt", Context: DefaultContext}); ref == nil || ref.Code != "noBranch" {
		t.Errorf("a folder with no repository: %+v, want noBranch", ref)
	}
}

func TestCleanPath(t *testing.T) {
	for p, ok := range map[string]bool{
		"a.txt": true, "dir/a.txt": true, "dir/sub/a b.txt": true, "": false, ".": false, "/a": false, "a/": false,
		"./a": false, "a//b": false, "a/../b": false, "..": false, "C:a": false, "C:\\a": false, "\\\\host\\share": false,
	} {
		if _, got := CleanPath(p); got != ok {
			t.Errorf("CleanPath(%q) = %v", p, got)
		}
	}
	if p, ok := CleanPath("dir\\a.txt"); !ok || p != "dir/a.txt" {
		t.Errorf("a path with backslashes: %q %v", p, ok)
	}
}

func TestADiffThatOnlyChangesLineEndingsHasNoHunks(t *testing.T) {
	dir := repo(t)
	runGit(t, dir, "checkout", "-q", "-b", "feat")
	write(t, dir, "a.txt", "one\r\ntwo\r\nthree\r\n")
	d := diffOf(t, dir, "a.txt", "merge-base")
	if !d.EolChange || len(d.Hunks) != 0 {
		t.Errorf("eol: %+v", d)
	}
}

func TestParseUnifiedCutsAtTheLimitAndKeepsTheCountsTrue(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("diff --git a/f b/f\n--- a/f\n+++ b/f\n@@ -0,0 +1,10 @@ func f()\n")
	for i := 0; i < 10; i++ {
		sb.WriteString("+line\n")
	}
	sb.WriteString("@@ -20,2 +30,2 @@\n-a\n+b\n")
	p := parseUnified(sb.String(), 4, 0)
	if !p.cut || p.total != 12 || p.keptLines != 4 || len(p.hunks) != 1 {
		t.Fatalf("parsed = %+v", p)
	}
	if h := p.hunks[0]; h.NewLines != 4 || h.OldLines != 0 || h.Header != "func f()" || len(h.Lines) != 4 {
		t.Errorf("cut hunk = %+v", h)
	}
	whole := parseUnified(sb.String(), 0, 0)
	if whole.cut || len(whole.hunks) != 2 || whole.hunks[1].OldStart != 20 || whole.hunks[1].Lines[1].NewNo != 30 {
		t.Errorf("whole = %+v", whole)
	}
}

func TestWordDiffMarksWhatChanged(t *testing.T) {
	oldLine, newLine := "if now.Sub(last) > window {", "if now.Sub(last) < window {"
	o, n := wordDiff(oldLine, newLine)
	if len(o) != 1 || len(n) != 1 {
		t.Fatalf("old %v new %v", o, n)
	}
	if got := string([]rune(oldLine)[o[0][0]:o[0][1]]); got != ">" {
		t.Errorf("old mark is %q", got)
	}
	if got := string([]rune(newLine)[n[0][0]:n[0][1]]); got != "<" {
		t.Errorf("new mark is %q", got)
	}
	// A character added is marked on the new side only.
	o, n = wordDiff("a > b", "a >= b")
	if len(o) != 0 || len(n) != 1 {
		t.Errorf("an added character: old %v new %v", o, n)
	}
	// Positions count code points, not bytes.
	o, _ = wordDiff("héllo wörld", "héllo wörlds")
	if len(o) != 1 || string([]rune("héllo wörld")[o[0][0]:o[0][1]]) != "wörld" {
		t.Errorf("code point marks: %v", o)
	}
}

func TestReadContextGivesTheLinesOfOneSide(t *testing.T) {
	dir := repo(t)
	runGit(t, dir, "checkout", "-q", "-b", "feat")
	var sb strings.Builder
	for i := 1; i <= 300; i++ {
		sb.WriteString("row\n")
	}
	write(t, dir, "big.txt", sb.String())
	runGit(t, dir, "add", "big.txt")
	runGit(t, dir, "commit", "-q", "-m", "big")
	write(t, dir, "big.txt", "first\n"+sb.String())

	c, ref := ReadContext(context.Background(), "p1", dir, "big.txt", "merge-base", "new", 2, 4)
	if ref != nil || len(c.Lines) != 3 || c.Lines[0].NewNo != 2 || c.Lines[0].Text != "row" || c.Eof || c.Lines[0].OldNo != 0 {
		t.Fatalf("new side: %+v %+v", c, ref)
	}
	c, _ = ReadContext(context.Background(), "p1", dir, "big.txt", "worktree", "old", 299, 400)
	if len(c.Lines) != 2 || !c.Eof || c.Lines[1].OldNo != 300 {
		t.Errorf("old side at the end: %+v", c)
	}
	c, _ = ReadContext(context.Background(), "p1", dir, "big.txt", "merge-base", "new", 1, 1000)
	if len(c.Lines) != maxContextLines || c.Eof {
		t.Errorf("a long ask: %d lines, eof %v, want %d", len(c.Lines), c.Eof, maxContextLines)
	}
	for name, args := range map[string][]any{
		"bad side":   {"big.txt", "merge-base", "left", 1, 2},
		"bad from":   {"big.txt", "merge-base", "new", 0, 2},
		"to < from":  {"big.txt", "merge-base", "new", 5, 2},
		"not a file": {"a.txt", "merge-base", "new", 1, 2},
		"escape":     {"../x", "merge-base", "new", 1, 2},
	} {
		_, ref := ReadContext(context.Background(), "p1", dir, args[0].(string), args[1].(string), args[2].(string), args[3].(int), args[4].(int))
		if ref == nil || ref.Code != "badRequest" {
			t.Errorf("%s: %+v, want badRequest", name, ref)
		}
	}
}
