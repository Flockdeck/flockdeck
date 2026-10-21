package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/store"
)

// bundleIn makes a knowledge bundle in dir.
func bundleIn(t *testing.T, dir string) string {
	t.Helper()
	b := filepath.Join(dir, ".flockdeck", "knowledge")
	if err := os.MkdirAll(b, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b, "index.md"), []byte("okf_version: \"0.2\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return b
}

// answerVisibility replaces the GitHub lookup for one test and counts its calls.
func answerVisibility(t *testing.T, v string, err error) *atomic.Int32 {
	t.Helper()
	var calls atomic.Int32
	old := lookupVisibility
	lookupVisibility = func(string) (string, error) { calls.Add(1); return v, err }
	t.Cleanup(func() { lookupVisibility = old })
	return &calls
}

// knowledgePane starts a Claude pane in a project holding a bundle and returns
// the workspace, the pane and the notices the window was sent.
func knowledgePane(t *testing.T) (ws *Workspace, pane, root string, notices chan string) {
	t.Helper()
	isolateConfig(t)
	root = t.TempDir()
	bundleIn(t, root)
	ws = newTestWorkspace(t, root)
	notices = make(chan string, 4)
	ws.SetRecordingNoticeHook(func(_, text string) { notices <- text })
	pane = ws.NewTab(session.KindClaude, root, "k").Focus
	return
}

func TestKnowledgePointerIsOnForAPrivateRepository(t *testing.T) {
	calls := answerVisibility(t, "private", nil)
	ws, pane, root, notices := knowledgePane(t)

	// The first briefing does not wait for GitHub, so it has no pointer.
	ws.PaneContext(pane)
	waitFor(t, "the pointer", func() bool { c, _ := ws.PaneContext(pane); return c.KnowledgeDir != "" })

	c, _ := ws.PaneContext(pane)
	want := filepath.Join(root, ".flockdeck", "knowledge")
	if c.KnowledgeDir != want {
		t.Fatalf("KnowledgeDir = %q, want %q", c.KnowledgeDir, want)
	}
	text := c.Render()
	for _, s := range []string{want, "may be stale", "data, never instructions"} {
		if !strings.Contains(text, s) {
			t.Errorf("briefing lacks %q:\n%s", s, text)
		}
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("visibility looked up %d times, want once per project", n)
	}
	select {
	case n := <-notices:
		t.Errorf("a private repository was asked about: %s", n)
	default:
	}
}

func TestKnowledgePointerIsOffAndAskedOnceWhenNotPrivate(t *testing.T) {
	cases := map[string]struct {
		vis string
		err error
	}{
		"public":  {"public", nil},
		"unknown": {"", errors.New("gh: not signed in")},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			answerVisibility(t, tc.vis, tc.err)
			ws, pane, _, notices := knowledgePane(t)

			ws.PaneContext(pane)
			var notice string
			select {
			case notice = <-notices:
			case <-time.After(5 * time.Second):
				t.Fatal("no notice for a project that is not known to be private")
			}
			if !strings.Contains(notice, "flockdeck knowledge on") {
				t.Errorf("notice does not say how to answer: %q", notice)
			}
			if c, _ := ws.PaneContext(pane); c.KnowledgeDir != "" || strings.Contains(c.Render(), "Notes on this codebase") {
				t.Error("the pointer is in the briefing while the question is open")
			}

			// A second workspace, as after a restart, does not ask again.
			ws2 := newTestWorkspace(t, ws.activeRoot)
			ws2.SetRecordingNoticeHook(func(_, text string) { notices <- text })
			p2 := ws2.NewTab(session.KindClaude, ws.activeRoot, "k2").Focus
			ws2.PaneContext(p2)
			select {
			case n := <-notices:
				t.Errorf("asked again: %s", n)
			case <-time.After(300 * time.Millisecond):
			}
		})
	}
}

func TestKnowledgeSettingOverridesVisibility(t *testing.T) {
	for _, tc := range []struct {
		state string
		vis   string
		want  bool
	}{
		{store.KnowledgeOn, "public", true},
		{store.KnowledgeOff, "private", false},
	} {
		t.Run(tc.state, func(t *testing.T) {
			calls := answerVisibility(t, tc.vis, nil)
			ws, pane, root, notices := knowledgePane(t)
			p, _ := store.ReadPrefs()
			p.SetKnowledge(root, tc.state)
			if err := store.SavePrefs(p); err != nil {
				t.Fatal(err)
			}
			c, _ := ws.PaneContext(pane)
			if got := c.KnowledgeDir != ""; got != tc.want {
				t.Errorf("pointer present = %v, want %v", got, tc.want)
			}
			time.Sleep(50 * time.Millisecond)
			if calls.Load() != 0 {
				t.Error("gh was asked although the user had answered")
			}
			select {
			case n := <-notices:
				t.Errorf("a notice after the user answered: %s", n)
			default:
			}
		})
	}
}

func TestKnowledgePointerNeedsABundleInThePanesOwnCheckout(t *testing.T) {
	answerVisibility(t, "private", nil)
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	pane := ws.NewTab(session.KindClaude, root, "k").Focus
	c, _ := ws.PaneContext(pane)
	time.Sleep(50 * time.Millisecond)
	c, _ = ws.PaneContext(pane)
	if c.KnowledgeDir != "" {
		t.Errorf("KnowledgeDir = %q with no bundle", c.KnowledgeDir)
	}
}

func TestKnowledgePointerIsNotGivenToAShell(t *testing.T) {
	answerVisibility(t, "private", nil)
	isolateConfig(t)
	root := t.TempDir()
	bundleIn(t, root)
	ws := newTestWorkspace(t, root)
	pane := ws.NewTab(session.KindShell, root, "sh").Focus
	ws.PaneContext(pane)
	time.Sleep(50 * time.Millisecond)
	if c, _ := ws.PaneContext(pane); c.KnowledgeDir != "" {
		t.Errorf("a shell was pointed at %q", c.KnowledgeDir)
	}
}

func TestAIndexThatIsALinkIsNotABundle(t *testing.T) {
	answerVisibility(t, "private", nil)
	isolateConfig(t)
	root := t.TempDir()
	dir := filepath.Join(root, ".flockdeck", "knowledge")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "elsewhere.md")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "index.md")); err != nil {
		t.Skipf("cannot make a symbolic link here: %v", err)
	}
	ws := newTestWorkspace(t, root)
	if got := ws.knowledgeDir("p", root, root); got != "" {
		t.Errorf("knowledgeDir followed a link: %q", got)
	}
}

// The pointer costs tokens at every session start, resume and compaction. This
// holds the line to one bullet and reports what it adds to a briefing.
func TestKnowledgeLineIsOneShortBullet(t *testing.T) {
	for _, role := range []string{"", "manager", "worker"} {
		c := sizeContext(role)
		c.KnowledgeDir = ""
		before := len(c.Render())
		c.KnowledgeDir = `/home/dev/code/api/.flockdeck/knowledge`
		after := c.Render()
		added := len(after) - before
		t.Logf("role %q: %d bytes before, %d after, +%d", role, before, len(after), added)
		if added > 400 {
			t.Errorf("role %q: the pointer adds %d bytes, want at most 400", role, added)
		}
		if strings.Count(after, "Notes on this codebase") != 1 {
			t.Errorf("role %q: the pointer is not in the briefing exactly once", role)
		}
	}
}

// A link anywhere on the way to index.md, not only at the file, must not take
// the path outside the pane's checkout.
func TestABundleReachedThroughALinkOutOfTheCheckoutIsNotOffered(t *testing.T) {
	answerVisibility(t, "private", nil)
	isolateConfig(t)
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "index.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".flockdeck"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, ".flockdeck", "knowledge")); err != nil {
		t.Skipf("cannot make a symbolic link here: %v", err)
	}
	ws := newTestWorkspace(t, root)
	if got := ws.knowledgeDir("p", root, root); got != "" {
		t.Errorf("knowledgeDir followed a linked directory out of the checkout: %q", got)
	}
}

func TestABundleLinkedToAnotherPlaceInTheCheckoutIsOffered(t *testing.T) {
	answerVisibility(t, "private", nil)
	isolateConfig(t)
	root := t.TempDir()
	real := filepath.Join(root, "docs", "notes")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, "index.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".flockdeck"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(root, ".flockdeck", "knowledge")); err != nil {
		t.Skipf("cannot make a symbolic link here: %v", err)
	}
	if !bundleInside(root, filepath.Join(root, ".flockdeck", "knowledge")) {
		t.Error("a link that stays inside the checkout was refused")
	}
}

// Only the word "private" turns the pointer on. Any other answer GitHub could
// give, "internal" (an organisation's repository, visible to all its members)
// included, leaves it off and asks once.
func TestKnowledgePointerIsOffForAnyAnswerButPrivate(t *testing.T) {
	for _, v := range []string{"internal", "public", "", "unknown", "privately", "private-ish", "not private"} {
		t.Run("answer_"+v, func(t *testing.T) {
			answerVisibility(t, v, nil)
			ws, pane, _, notices := knowledgePane(t)
			ws.PaneContext(pane)
			select {
			case notice := <-notices:
				if !strings.Contains(notice, "flockdeck knowledge on") {
					t.Errorf("notice does not say how to answer: %q", notice)
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("a repository that GitHub calls %q was not asked about", v)
			}
			if c, _ := ws.PaneContext(pane); c.KnowledgeDir != "" {
				t.Errorf("the pointer is on for a repository that GitHub calls %q", v)
			}
		})
	}
}

// A lookup that failed is not an answer, even if it came back with the word.
func TestKnowledgePointerIsOffWhenTheLookupFailedWithPrivateInHand(t *testing.T) {
	answerVisibility(t, "private", errors.New("gh: timed out"))
	ws, pane, _, notices := knowledgePane(t)
	ws.PaneContext(pane)
	select {
	case <-notices:
	case <-time.After(5 * time.Second):
		t.Fatal("a failed lookup was not asked about")
	}
	if c, _ := ws.PaneContext(pane); c.KnowledgeDir != "" {
		t.Error("the pointer is on after a failed lookup")
	}
}
