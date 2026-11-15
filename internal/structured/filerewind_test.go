package structured

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

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
	sendAndSettle(t, r, "plain a", 1)
	put(t, dir, "a.txt", "v2\n")
	sendAndSettle(t, r, "plain b", 2)
	put(t, dir, "a.txt", "v3\n")
	put(t, dir, "made.txt", "made\n")
	if err := os.Remove(filepath.Join(dir, "keep.txt")); err != nil {
		t.Fatal(err)
	}
	s := sendAndSettle(t, r, "plain c", 3)
	return r, s, dir
}

func paths(cs []filerewind.Change) string {
	var out []string
	for _, c := range cs {
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
	var list []string
	for _, c := range plan {
		list = append(list, c.Path)
	}
	text, out, err := r.RewindFiles(ids[1], r.RewindToken(), list)
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
	var list []string
	for _, c := range plan {
		list = append(list, c.Path)
	}
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
	list = append(list, "late.txt")
	text, out, err := r.RewindFiles(ids[1], r.RewindToken(), list)
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
		s = sendAndSettle(t, r, txt, i+1)
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
	sendAndSettle(t, r, "plain a", 1)
	s := sendAndSettle(t, r, "plain b", 2)
	if len(s.FilePoints) != 0 || s.FilesWhy != "" {
		t.Errorf("points = %v, why = %q", s.FilePoints, s.FilesWhy)
	}
	if refs := gitCmd(t, dir, "for-each-ref", "refs/flockdeck/"); refs != "" {
		t.Errorf("refs were written: %q", refs)
	}
}
