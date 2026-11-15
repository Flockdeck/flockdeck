package structured

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/acp/acptest"
	"github.com/jmwri/flockdeck/internal/filerewind"
)

func gitCmd(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t.invalid", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func put(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func got(dir, rel string) string {
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		return "<missing>"
	}
	return strings.ReplaceAll(string(b), "\r\n", "\n")
}

// repoRuntime starts a pane whose folder is a git repository holding a.txt,
// with file rewind on, and sends three prompts, changing files between them.
// a.txt is v1, v2 and v3 when the prompts are sent. After the second prompt
// the agent adds made.txt and deletes keep.txt.
func repoRuntime(t *testing.T) (*Runtime, Snapshot, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	o := fakeOptions(t, acptest.WithRewind())
	o.FileRewind = true
	dir := o.Cwd
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
	gitCmd(t, dir, "init", "-q")
	put(t, dir, "a.txt", "v0\n")
	put(t, dir, "keep.txt", "keep\n")
	gitCmd(t, dir, "add", "-A")
	gitCmd(t, dir, "commit", "-q", "-m", "first")
	r := begin(t, o)
	until(t, r, "ready", idle)
	put(t, dir, "a.txt", "v1\n")
	typedAndSettle(t, r, "plain a", 1)
	put(t, dir, "a.txt", "v2\n")
	typedAndSettle(t, r, "plain b", 2)
	put(t, dir, "a.txt", "v3\n")
	put(t, dir, "made.txt", "made\n")
	if err := os.Remove(filepath.Join(dir, "keep.txt")); err != nil {
		t.Fatal(err)
	}
	s := typedAndSettle(t, r, "plain c", 3)
	return r, s, dir
}

// typedAndSettle sends a prompt as the person's box does.
func typedAndSettle(t *testing.T, r *Runtime, text string, want int) Snapshot {
	t.Helper()
	if err := r.SendTyped(text, nil); err != nil {
		t.Fatal(err)
	}
	return until(t, r, "turn of "+text, func(s Snapshot) bool { return idle(s) && len(prompts(s)) == want })
}

func confirmAll(l filerewind.Listing) []filerewind.Confirm {
	out := []filerewind.Confirm{}
	for _, c := range l.Changes {
		out = append(out, filerewind.Confirm{Path: c.Path, ID: c.ID})
	}
	return out
}

func paths(l filerewind.Listing) string {
	var out []string
	for _, c := range l.Changes {
		out = append(out, string(c.Action)+":"+c.Path)
	}
	return strings.Join(out, ",")
}

func TestFilesComeBackWithTheConversation(t *testing.T) {
	r, s, dir := repoRuntime(t)
	ids := promptIDs(s)
	if pts, why := r.FilePoints(); len(pts) != 2 || pts[0] != ids[1] || pts[1] != ids[2] || why != "" {
		t.Fatalf("file points = %v (%q), prompts = %v", pts, why, ids)
	}
	if len(s.FilePoints) != 2 {
		t.Errorf("snapshot file points = %v", s.FilePoints)
	}
	// Going back to before prompt b: the files are as they were when b was sent.
	plan, err := r.FilePlan(ids[1], r.RewindToken())
	if err != nil {
		t.Fatal(err)
	}
	// Prompt b was sent with a.txt at v2 and no made.txt; keep.txt was still there.
	if want := "overwrite:a.txt,recreate:keep.txt,remove:made.txt"; paths(plan) != want {
		t.Fatalf("plan = %s, want %s", paths(plan), want)
	}
	if got(dir, "a.txt") != "v3\n" {
		t.Fatal("the plan changed a file")
	}
	text, out, err := r.RewindFiles(ids[1], r.RewindToken(), confirmAll(plan))
	if err != nil || out.Err != nil {
		t.Fatalf("rewind: %v, files: %v", err, out.Err)
	}
	if text != "plain b" {
		t.Errorf("text = %q", text)
	}
	if out.Result.Written != 2 || out.Result.Removed != 1 || out.Result.Undo == "" || len(out.Result.Failed) != 0 {
		t.Errorf("result = %+v", out.Result)
	}
	if got(dir, "a.txt") != "v2\n" || got(dir, "keep.txt") != "keep\n" || got(dir, "made.txt") != "<missing>" {
		t.Errorf("files: a=%q keep=%q made=%q", got(dir, "a.txt"), got(dir, "keep.txt"), got(dir, "made.txt"))
	}
	if n := len(prompts(r.Snapshot())); n != 1 {
		t.Errorf("prompts = %d", n)
	}
	// What the rewind removed is still in git under the undo ref.
	if b := gitCmd(t, dir, "show", out.Result.Undo+":a.txt"); strings.ReplaceAll(b, "\r\n", "\n") != "v3\n" {
		t.Errorf("undo ref a.txt = %q", b)
	}
	// The checkpoints of the prompts that went are dropped, the one before stays.
	refs := gitCmd(t, dir, "for-each-ref", "--format=%(refname)", filerewind.RefPrefix)
	if n := strings.Count(refs, "\n"); n != 1 {
		t.Errorf("checkpoint refs after the rewind: %q", refs)
	}
	_ = r.Close()
	if refs := gitCmd(t, dir, "for-each-ref", "--format=%(refname)", filerewind.RefPrefix); refs != "" {
		t.Errorf("checkpoint refs after Close: %q", refs)
	}
	if undo := gitCmd(t, dir, "for-each-ref", "--format=%(refname)", filerewind.UndoPrefix); !strings.Contains(undo, out.Result.Undo) {
		t.Errorf("the undo ref went with the pane: %q", undo)
	}
}

func TestAListThatWentOutOfDateChangesNothing(t *testing.T) {
	r, s, dir := repoRuntime(t)
	ids := promptIDs(s)
	plan, err := r.FilePlan(ids[1], r.RewindToken())
	if err != nil {
		t.Fatal(err)
	}
	list := confirmAll(plan)
	// A file changes after the list was shown.
	put(t, dir, "late.txt", "late\n")
	_, _, err = r.RewindFiles(ids[1], r.RewindToken(), list)
	var fe *FilesError
	if !errors.As(err, &fe) || !errors.Is(err, ErrFilesChanged) {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(paths(fe.Now), "remove:late.txt") {
		t.Errorf("the new list = %s", paths(fe.Now))
	}
	if n := len(prompts(r.Snapshot())); n != 3 {
		t.Errorf("the conversation went back with a stale list: %d prompts", n)
	}
	if got(dir, "a.txt") != "v3\n" || got(dir, "late.txt") != "late\n" {
		t.Error("a file was touched")
	}
	// And the pane can still be gone back in the usual way, and then again with the list.
	now, err := r.FilePlan(ids[1], r.RewindToken())
	if err != nil {
		t.Fatal(err)
	}
	text, out, err := r.RewindFiles(ids[1], r.RewindToken(), confirmAll(now))
	if err != nil || out.Err != nil || text != "plain b" {
		t.Fatalf("second try: %q %v %v", text, err, out.Err)
	}
	if got(dir, "late.txt") != "<missing>" || got(dir, "a.txt") != "v2\n" {
		t.Error("the confirmed files were not put back")
	}
}

func TestAPaneOutsideARepoOffersNoFiles(t *testing.T) {
	o := fakeOptions(t, acptest.WithRewind())
	o.FileRewind = true
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(o.Cwd))
	r := begin(t, o)
	until(t, r, "ready", idle)
	var s Snapshot
	for i, txt := range []string{"plain a", "plain b"} {
		s = typedAndSettle(t, r, txt, i+1)
	}
	if len(s.FilePoints) != 0 || s.FilesWhy != FilesWhyNotRepo {
		t.Fatalf("points = %v, why = %q", s.FilePoints, s.FilesWhy)
	}
	id := promptIDs(s)[1]
	if _, err := r.FilePlan(id, r.RewindToken()); !errors.Is(err, ErrNoFiles) {
		t.Errorf("plan err = %v", err)
	}
	if _, _, err := r.RewindFiles(id, r.RewindToken(), nil); !errors.Is(err, ErrNoFiles) {
		t.Errorf("rewind err = %v", err)
	}
	if n := len(prompts(r.Snapshot())); n != 2 {
		t.Errorf("a refused rewind changed the conversation: %d prompts", n)
	}
	// The conversation alone still goes back.
	if _, err := r.Rewind(id, r.RewindToken()); err != nil {
		t.Errorf("conversation rewind: %v", err)
	}
}

func TestFileRewindIsOffUnlessAsked(t *testing.T) {
	o := fakeOptions(t, acptest.WithRewind())
	dir := o.Cwd
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
	gitCmd(t, dir, "init", "-q")
	put(t, dir, "a.txt", "v0\n")
	gitCmd(t, dir, "add", "-A")
	gitCmd(t, dir, "commit", "-q", "-m", "first")
	r := begin(t, o)
	until(t, r, "ready", idle)
	typedAndSettle(t, r, "plain a", 1)
	s := typedAndSettle(t, r, "plain b", 2)
	if len(s.FilePoints) != 0 || s.FilesWhy != "" {
		t.Errorf("points = %v, why = %q", s.FilePoints, s.FilesWhy)
	}
	if refs := gitCmd(t, dir, "for-each-ref", "refs/flockdeck/"); refs != "" {
		t.Errorf("refs were written: %q", refs)
	}
}

// Review item 9: only prompts the person typed are checkpointed.
func TestOnlyTypedPromptsAreCheckpointed(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	o := fakeOptions(t, acptest.WithRewind())
	o.FileRewind = true
	dir := o.Cwd
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
	gitCmd(t, dir, "init", "-q")
	put(t, dir, "a.txt", "v0\n")
	gitCmd(t, dir, "add", "-A")
	gitCmd(t, dir, "commit", "-q", "-m", "first")
	r := begin(t, o)
	until(t, r, "ready", idle)
	typedAndSettle(t, r, "plain a", 1)
	sendAndSettle(t, r, "plain b", 2) // sent by another agent or tool: not typed
	s := typedAndSettle(t, r, "plain c", 3)
	ids := promptIDs(s)
	if len(s.FilePoints) != 1 || s.FilePoints[0] != ids[2] {
		t.Errorf("file points = %v, prompts = %v", s.FilePoints, ids)
	}
	if n := strings.Count(gitCmd(t, dir, "for-each-ref", filerewind.RefPrefix), "\n"); n != 2 {
		t.Errorf("checkpoint refs = %d, want 2", n)
	}
}

// Review item 3: a restore that did not finish keeps the checkpoint.
func TestACheckpointIsKeptWhenTheFilesWereNotAllPutBack(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("a held-open file blocks a delete on Windows")
	}
	r, s, dir := repoRuntime(t)
	ids := promptIDs(s)
	plan, err := r.FilePlan(ids[1], r.RewindToken())
	if err != nil {
		t.Fatal(err)
	}
	held, err := os.Open(filepath.Join(dir, "made.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	_, out, err := r.RewindFiles(ids[1], r.RewindToken(), confirmAll(plan))
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Result.Failed) == 0 || out.Result.Kept == "" {
		t.Fatalf("result = %+v", out.Result)
	}
	refs := gitCmd(t, dir, "for-each-ref", "--format=%(refname)", filerewind.UndoPrefix)
	if !strings.Contains(refs, out.Result.Kept) {
		t.Errorf("the kept ref is not there: %q", refs)
	}
	// What the kept ref holds is the files at the prompt.
	if got := strings.ReplaceAll(gitCmd(t, dir, "show", out.Result.Kept+":a.txt"), "\r\n", "\n"); got != "v2\n" {
		t.Errorf("kept a.txt = %q", got)
	}
	// The checkpoints of the prompts that went are not left behind, apart from the kept one.
	if left := gitCmd(t, dir, "for-each-ref", "--format=%(refname)", filerewind.RefPrefix); strings.Count(left, "\n") != 1 {
		t.Errorf("checkpoint refs: %q", left)
	}
}

// repoOnly makes a runtime for a git folder holding a.txt, with file rewind on.
func repoOnly(t *testing.T) (*Runtime, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	o := fakeOptions(t, acptest.WithRewind())
	o.FileRewind = true
	dir := o.Cwd
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
	gitCmd(t, dir, "init", "-q")
	put(t, dir, "a.txt", "v0\n")
	gitCmd(t, dir, "add", "-A")
	gitCmd(t, dir, "commit", "-q", "-m", "first")
	r := begin(t, o)
	until(t, r, "ready", idle)
	return r, dir
}

// Review item 2: after a slow save the next prompts skip it, and say so.
func TestASlowSaveSkipsTheNextPrompts(t *testing.T) {
	saved := slowSave
	slowSave = time.Nanosecond
	defer func() { slowSave = saved }()
	r, _ := repoOnly(t)
	s := typedAndSettle(t, r, "p1", 1)
	if s.FilesWhy != "" {
		t.Fatalf("why after a good save = %q", s.FilesWhy)
	}
	for i := 2; i <= 1+skipPrompts; i++ {
		s = typedAndSettle(t, r, fmt.Sprintf("p%d", i), i)
		if !strings.Contains(s.FilesWhy, "skips it for the next") {
			t.Fatalf("prompt %d: why = %q", i, s.FilesWhy)
		}
	}
	if len(s.FilePoints) != 0 {
		t.Errorf("file points while skipping = %v", s.FilePoints)
	}
	n := 2 + skipPrompts
	s = typedAndSettle(t, r, fmt.Sprintf("p%d", n), n)
	ids := promptIDs(s)
	if len(s.FilePoints) != 1 || s.FilePoints[0] != ids[n-1] {
		t.Errorf("after the skip, file points = %v, prompts = %v", s.FilePoints, ids)
	}
}

// Review items 2 and 9: a failed save names the file, and the next prompts skip it.
func TestAFailedSaveNamesTheFileAndBacksOff(t *testing.T) {
	r, dir := repoOnly(t)
	put(t, dir, ".gitattributes", "*.u16 working-tree-encoding=UTF-16\n")
	gitCmd(t, dir, "add", "-A")
	gitCmd(t, dir, "commit", "-q", "-m", "attrs")
	put(t, dir, "docs/bad.u16", "plain bytes, no byte order mark\n")
	s := typedAndSettle(t, r, "p1", 1)
	if !strings.Contains(s.FilesWhy, "docs/bad.u16") || !strings.Contains(s.FilesWhy, "working-tree encoding") {
		t.Fatalf("why = %q", s.FilesWhy)
	}
	s = typedAndSettle(t, r, "p2", 2)
	if !strings.Contains(s.FilesWhy, "skips it for the next") || !strings.Contains(s.FilesWhy, "docs/bad.u16") {
		t.Errorf("why while skipping = %q", s.FilesWhy)
	}
	if refs := gitCmd(t, dir, "for-each-ref", filerewind.RefPrefix); refs != "" {
		t.Errorf("refs = %q", refs)
	}
}

// Review items 1 and 2: a file over the limit is named, and is not a reason to skip prompts.
func TestAFileOverTheLimitIsNamedAndNotBackedOffFrom(t *testing.T) {
	r, dir := repoOnly(t)
	big := make([]byte, 33<<20)
	if err := os.WriteFile(filepath.Join(dir, "dump.bin"), big, 0o644); err != nil {
		t.Fatal(err)
	}
	s := typedAndSettle(t, r, "p1", 1)
	if !strings.Contains(s.FilesWhy, "dump.bin is 33 MB") || !strings.Contains(s.FilesWhy, "32 MB") {
		t.Fatalf("why = %q", s.FilesWhy)
	}
	if err := os.Remove(filepath.Join(dir, "dump.bin")); err != nil {
		t.Fatal(err)
	}
	s = typedAndSettle(t, r, "p2", 2)
	if s.FilesWhy != "" {
		t.Errorf("the next prompt was skipped or failed: %q", s.FilesWhy)
	}
}

func TestWhyTextForTheNewErrors(t *testing.T) {
	cases := map[string]error{
		"over the limit of 3000":    &filerewind.TooBigError{Kind: filerewind.TooManyFiles, Files: 3500},
		"x/y.bin is 40 MB":          &filerewind.TooBigError{Kind: filerewind.FileTooLarge, Path: "x/y.bin", Size: 40 << 20},
		"more than 128 MB":          &filerewind.TooBigError{Kind: filerewind.TotalTooLarge, Path: "z.bin", Size: 129 << 20},
		"Comparing the saved files": filerewind.ErrSlowNow,
	}
	for want, err := range cases {
		if got := whyText(err); !strings.Contains(got, want) {
			t.Errorf("%v: %q lacks %q", err, got, want)
		}
	}
	if got := whyText(filerewind.ErrSlowNow); strings.Contains(got, "were not saved") {
		t.Errorf("a slow comparison says the files were not saved: %q", got)
	}
	if got := whyFile("a\nb\x00" + strings.Repeat("x", 200)); strings.ContainsAny(got, "\n\x00") || len([]rune(got)) > 100 {
		t.Errorf("whyFile = %q", got)
	}
}

// Review item 8: when the checkpoint cannot be moved to a kept ref it stays, and the answer names it.
func TestACheckpointThatCannotBeMovedIsKeptWhereItIs(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("a held-open file blocks a delete on Windows")
	}
	r, s, dir := repoRuntime(t)
	ids := promptIDs(s)
	plan, err := r.FilePlan(ids[1], r.RewindToken())
	if err != nil {
		t.Fatal(err)
	}
	// A ref where the kept ref's folder would go makes the move fail.
	head := strings.TrimSpace(gitCmd(t, dir, "rev-parse", "HEAD"))
	gitCmd(t, dir, "update-ref", filerewind.UndoPrefix+"kept/"+r.ckptRun, head)
	held, err := os.Open(filepath.Join(dir, "made.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	_, out, err := r.RewindFiles(ids[1], r.RewindToken(), confirmAll(plan))
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Result.Failed) == 0 || !strings.HasPrefix(out.Result.Kept, filerewind.RefPrefix) {
		t.Fatalf("result = %+v", out.Result)
	}
	refs := gitCmd(t, dir, "for-each-ref", "--format=%(refname)", filerewind.RefPrefix)
	if !strings.Contains(refs, out.Result.Kept) {
		t.Errorf("the checkpoint ref is gone: %q, wanted %s", refs, out.Result.Kept)
	}
	if got := strings.ReplaceAll(gitCmd(t, dir, "show", out.Result.Kept+":a.txt"), "\r\n", "\n"); got != "v2\n" {
		t.Errorf("kept a.txt = %q", got)
	}
}
