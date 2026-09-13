package workspace

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// FuzzExtractTasks runs the plan extractor over whatever it is given.
//
// Its input is a pane's screen: bytes a terminal drew, cut to a window, with
// escape sequences, half-written UTF-8 and redrawn rows in it. The extractor
// reads that with hand-written indexing — leading markers, checkbox brackets,
// numbers and their separators, and a run of decorative glyphs — and every one
// of those is a place a slice can be taken past its end. A panic there is not
// a bad suggestion in the fan-out dialog; it is the goroutine serving the
// window going down.
func FuzzExtractTasks(f *testing.F) {
	f.Add("- Add a health endpoint to the HTTP server")
	f.Add("Here is the plan:\n1. Do the thing\n2) Do the other\n(3) And this\n")
	f.Add("- [ ] unticked\n- [x] ticked\n- [-] dropped\n")
	f.Add("Task 1: split the router\nStep 2 — add a timeout\n")
	f.Add("- ✅ ticked task with a glyph\n- \U0001F527 another one\n")
	f.Add("```\n- inside a code block\n```\n- outside it\n")
	f.Add("· Perambulating… (26s · ↓ 1.2k tokens · esc to interrupt)\n")
	f.Add("- 計画を三つに分割する\n- 設定を読み込む\n")
	f.Add("\r- redrawn\r- row\r\n")
	f.Add("(")
	f.Add("1")
	f.Add("- [")

	f.Fuzz(func(t *testing.T, screen string) {
		tasks := ExtractTasks(screen)
		if len(tasks) > maxTasks {
			t.Fatalf("extracted %d tasks, more than the cap of %d", len(tasks), maxTasks)
		}
		for _, task := range tasks {
			if n := utf8.RuneCountInString(task); n < minTaskRunes || len(task) > maxTaskBytes {
				t.Fatalf("task %q is %d runes and %d bytes, outside the %d runes to %d bytes a task may be",
					task, n, len(task), minTaskRunes, maxTaskBytes)
			}
			if strings.ContainsAny(task, "\n\r") {
				t.Fatalf("task %q spans more than one line", task)
			}
			// A branch is derived from every task a fan-out starts, and git
			// will not take a ref that is not valid UTF-8.
			if branch := BranchNameFor(task); !utf8.ValidString(branch) {
				t.Fatalf("task %q derived the branch %q, which is not valid UTF-8", task, branch)
			}
		}
	})
}

// FuzzBranchNameFor derives a branch from arbitrary text.
//
// What comes out is handed to `git worktree add -b` and turned into a
// directory name, so it has to be a ref git will take: valid UTF-8, never
// empty, and without the double dashes or trailing dash that reads as a name
// the truncation cut badly.
func FuzzBranchNameFor(f *testing.F) {
	f.Add("Add a health endpoint")
	f.Add("Добавить проверку состояния приложения и его окружения")
	f.Add("設定ファイルを読み込んでから検証する")
	f.Add(strings.Repeat("é", 60))
	f.Add("!!! ??? ...")
	f.Add("")

	f.Fuzz(func(t *testing.T, task string) {
		got := BranchNameFor(task)
		if !strings.HasPrefix(got, "agent/") {
			t.Fatalf("BranchNameFor(%q) = %q, which is not under agent/", task, got)
		}
		name := strings.TrimPrefix(got, "agent/")
		switch {
		case name == "":
			t.Fatalf("BranchNameFor(%q) named no branch at all", task)
		case !utf8.ValidString(got):
			t.Fatalf("BranchNameFor(%q) = %q, which is not valid UTF-8", task, got)
		case strings.HasPrefix(name, "-"), strings.HasSuffix(name, "-"):
			t.Fatalf("BranchNameFor(%q) = %q, which opens or closes on a dash", task, got)
		case strings.Contains(name, "--"):
			t.Fatalf("BranchNameFor(%q) = %q, which has an empty word in it", task, got)
		case strings.HasSuffix(name, ".lock"), strings.HasPrefix(name, "."):
			t.Fatalf("BranchNameFor(%q) = %q, which git will not take as a ref", task, got)
		}
	})
}
