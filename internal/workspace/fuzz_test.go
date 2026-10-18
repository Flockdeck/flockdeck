package workspace

import (
	"strings"
	"testing"
	"unicode/utf8"
)

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
