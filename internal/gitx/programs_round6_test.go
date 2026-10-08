package gitx

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestNetworkSpellings is the table of how a Windows path can be spelled and
// whether it reaches another machine. The function is pure, so it runs on every
// platform. Anything in a namespace it does not recognise as local reads as a
// share.
func TestNetworkSpellings(t *testing.T) {
	network := []string{
		`\\host\share\x`, `//host/share/x`, `\\host`,
		`\\?\UNC\host\share\x`, `\\?\unc\host\share`, `//?/UNC/host/share`,
		`\\.\UNC\host\share`, `//./UNC/host/share`,
		`\??\UNC\host\share\hooks`, `\??\unc\host\share`, `/??/UNC/host/share`,
		`\\?\Global\UNC\host\share`, `\\?\GLOBALROOT\Device\Mup\host\share`,
		`\Device\Mup\host\share`, `\device\mup\host`, `\DosDevices\UNC\host\share`,
		`\GLOBAL??\UNC\host\share`, `\GLOBAL??\C:\x`,
		`\\?\Volume{01234567-89ab-cdef-0123-456789abcdef}\x`, `\??\Volume{01234567-89ab-cdef-0123-456789abcdef}\x`,
		`\\.\pipe\x`, `\\.\GLOBALROOT\Device\x`, `\??\GLOBALROOT\Device\Mup\host`,
		`\\?\`, `\??\`, `\??\C`, `\\?\C`,
	}
	local := []string{
		`C:\x`, `c:/x`, `C:`, `C:x`, `D:\a\b`,
		`\\?\C:\x`, `\\?\c:\x`, `//?/C:/x`, `\\?\C:`,
		`\??\C:\Windows`, `\??\c:`, `/??/C:/x`,
		`\\.\C:\x`,
		`\Users\x`, `\temp`, `relative\path`, `.\x`, `..\x`, `x`, `/srv/git`,
		``,
	}
	for _, p := range network {
		if !isNetworkSpelling(p) {
			t.Errorf("%q is not read as a share", p)
		}
	}
	for _, p := range local {
		if isNetworkSpelling(p) {
			t.Errorf("%q is read as a share", p)
		}
	}
}

func TestNetworkPathsAreOnlyAProblemOnWindows(t *testing.T) {
	if isNetworkPath(`\\host\share`) != (os.PathSeparator == '\\') {
		t.Error("isNetworkPath does not follow the platform")
	}
}

// fan makes the shape that took a minute to walk: R holds the junctions J1..Jn,
// J1 leads to R and Jk to R\J(k-1) written m times over. Followed without
// memory, each of the m steps resolves the whole chain below it again, m to the
// power n times. It stands in for the links with the readLink seam.
func fan(t *testing.T, tag string, n, m int) (entry string) {
	t.Helper()
	root := t.TempDir()
	x := filepath.Join(root, "x")
	r := filepath.Join(root, "r")
	for _, d := range []string{x, r} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	name := func(k int) string { return "fan" + tag + "_" + strconv.Itoa(k) }
	targets := map[string]string{}
	paths := map[string]string{}
	for k := 1; k <= n; k++ {
		paths[filepath.Join(r, name(k))] = x
		if k == 1 {
			targets[name(k)] = r
			continue
		}
		parts := []string{r}
		for i := 0; i < m; i++ {
			parts = append(parts, name(k-1))
		}
		targets[name(k)] = filepath.Join(parts...)
	}
	links(t, paths, targets)
	return filepath.Join(r, name(n))
}

func TestAFanOfLinksIsWalkedOnceNotOncePerWay(t *testing.T) {
	windowsOnly(t)
	for m := 2; m <= 5; m++ {
		m := m
		t.Run("m"+strconv.Itoa(m), func(t *testing.T) {
			entry := fan(t, "ok"+strconv.Itoa(m), 7, m)
			f := tfs()
			start := time.Now()
			err := f.guard(entry)
			took := time.Since(start)
			if err != nil {
				t.Errorf("a chain of local links = %v, want it let through", err)
			}
			if took > time.Second {
				t.Errorf("m=%d took %s", m, took)
			}
			if f.calls > 200 {
				t.Errorf("m=%d made %d calls to the file system", m, f.calls)
			}
		})
	}
}

func TestAFanDeeperThanTheLimitIsRefusedQuickly(t *testing.T) {
	windowsOnly(t)
	entry := fan(t, "deep", 12, 3)
	f := tfs()
	start := time.Now()
	err := f.guard(entry)
	if !errors.Is(err, errTooManyLinks) {
		t.Errorf("err = %v, want errTooManyLinks", err)
	}
	if took := time.Since(start); took > time.Second {
		t.Errorf("took %s", took)
	}
}

func TestALinkThatLeadsBackToItselfIsRefused(t *testing.T) {
	windowsOnly(t)
	root := t.TempDir()
	r := filepath.Join(root, "r")
	if err := os.MkdirAll(r, 0o755); err != nil {
		t.Fatal(err)
	}
	loop := filepath.Join(r, "loopback")
	links(t, map[string]string{loop: root}, map[string]string{"loopback": filepath.Join(r, "loopback", "loopback")})
	err := tfs().guard(loop)
	if !errors.Is(err, errTooManyLinks) {
		t.Errorf("err = %v, want errTooManyLinks", err)
	}
}

func TestTheWalkStopsAtItsStepAndScanLimits(t *testing.T) {
	windowsOnly(t)
	dir := filepath.Join(t.TempDir(), "a", "b", "c", "d", "e")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	was := maxWalkSteps
	t.Cleanup(func() { maxWalkSteps = was })
	maxWalkSteps = 3
	if err := tfs().guard(dir); !errors.Is(err, errTooManyLinks) {
		t.Errorf("a walk past its step limit = %v, want errTooManyLinks", err)
	}
	maxWalkSteps = was

	// Plain looks have a budget of their own, and running out of it is said of
	// the size of the repository and not of links.
	wasOps := maxStatOps
	t.Cleanup(func() { maxStatOps = wasOps })
	maxStatOps = 2
	err := tfs().guard(dir)
	if !errors.Is(err, errScanTooLarge) || errors.Is(err, errTooManyLinks) {
		t.Errorf("a scan past its budget of plain looks = %v, want errScanTooLarge and not errTooManyLinks", err)
	}
	if item := fsItem(err, dir, ""); !strings.Contains(item.Value, "too large") || strings.Contains(item.Value, "links") {
		t.Errorf("the item says %q", item.Value)
	}
}

func TestAGuardIsRememberedForTheRestOfTheScan(t *testing.T) {
	windowsOnly(t)
	dir := filepath.Join(t.TempDir(), "a", "b")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f := tfs()
	if err := f.guard(dir); err != nil {
		t.Fatal(err)
	}
	calls := f.calls
	for i := 0; i < 20; i++ {
		if err := f.guard(filepath.Join(dir, "hook"+strconv.Itoa(i))); err != nil {
			t.Fatal(err)
		}
	}
	if f.calls > calls+20 {
		t.Errorf("twenty paths in a folder already walked made %d more calls", f.calls-calls)
	}
}

func TestACallThatNeverReturnsHoldsOneSlotOfOneScanAndIsCountedDown(t *testing.T) {
	release := make(chan struct{})
	wasEval, wasTimeout, wasMax := evalSymlinks, statTimeout, maxLeaked
	t.Cleanup(func() {
		evalSymlinks, statTimeout, maxLeaked = wasEval, wasTimeout, wasMax
	})
	evalSymlinks = func(p string) (string, error) {
		if strings.HasSuffix(p, "hang") {
			<-release
		}
		return p, nil
	}
	statTimeout = 20 * time.Millisecond
	closed := false
	closeRelease := func() {
		if !closed {
			closed = true
			close(release)
		}
	}
	t.Cleanup(closeRelease)

	dir := t.TempDir()
	hang := filepath.Join(dir, "hang")
	f := tfs()
	for i := 0; i < maxSlots; i++ {
		if _, err := f.Canonical(hang); !errors.Is(err, errNoAnswer) {
			t.Fatalf("call %d = %v, want errNoAnswer", i, err)
		}
	}
	before := f.calls
	if _, err := f.Canonical(hang); !errors.Is(err, errNoAnswer) || f.calls != before {
		t.Errorf("with every slot of the scan taken, a call was made: err=%v calls %d -> %d", err, before, f.calls)
	}
	if got := leaked.Load(); got != maxSlots {
		t.Errorf("%d calls counted as leaked, want %d", got, maxSlots)
	}

	// Another scan has slots of its own.
	other := tfs()
	if _, err := other.Canonical(dir); err != nil {
		t.Errorf("a fresh scan was refused because another's calls hang: %v", err)
	}

	// The program as a whole tolerates only so many.
	maxLeaked = maxSlots
	if _, err := tfs().Canonical(dir); !errors.Is(err, errNoAnswer) {
		t.Errorf("with the program's limit of hung calls reached, a fresh scan's call = %v, want errNoAnswer", err)
	}

	// And they are counted down as they come back, so it is not for ever.
	closeRelease()
	deadline := time.Now().Add(5 * time.Second)
	for leaked.Load() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := leaked.Load(); got != 0 {
		t.Fatalf("%d calls still counted as leaked after they returned", got)
	}
	if _, err := tfs().Canonical(dir); err != nil {
		t.Errorf("after they returned, a fresh scan's call = %v", err)
	}
}

func TestAScanEndingAbandonsItsCalls(t *testing.T) {
	release := make(chan struct{})
	wasEval := evalSymlinks
	t.Cleanup(func() { evalSymlinks = wasEval })
	evalSymlinks = func(p string) (string, error) { <-release; return p, nil }
	ctx, cancel := context.WithCancel(context.Background())
	f := newScanFS(ctx)
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	start := time.Now()
	_, err := f.Canonical(t.TempDir())
	if !errors.Is(err, errNoAnswer) || time.Since(start) > time.Second {
		t.Errorf("err=%v after %s, want errNoAnswer soon after the scan's context ended", err, time.Since(start))
	}
	close(release)
	deadline := time.Now().Add(5 * time.Second)
	for leaked.Load() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
}

func TestAFileThatIsNotAnOrdinaryFileIsNotOpened(t *testing.T) {
	// A folder where a file is read, and a file where a folder is: neither is
	// opened. (A pipe is the case that matters, tested where pipes exist.)
	dir := t.TempDir()
	f := tfs()
	if _, err := f.ReadFile(dir); !errors.Is(err, errNotRegular) {
		t.Errorf("ReadFile of a folder = %v, want errNotRegular", err)
	}
	if _, _, err := f.Hash(dir, 10); !errors.Is(err, errNotRegular) {
		t.Errorf("Hash of a folder = %v, want errNotRegular", err)
	}
	file := filepath.Join(dir, "f")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.OpenDir(file); !errors.Is(err, errNotRegular) {
		t.Errorf("OpenDir of a file = %v, want errNotRegular", err)
	}
	if data, err := f.ReadFile(file); err != nil || string(data) != "x" {
		t.Errorf("ReadFile of a file = %q, %v", data, err)
	}
}

func TestACleanRepositoryAndABigOneMakeFewCallsToTheFileSystem(t *testing.T) {
	repo := newRepo(t)
	start := time.Now()
	scan(t, repo)
	ordinary := lastScanCalls.Load()
	t.Logf("an ordinary repository: %d calls, %s", ordinary, time.Since(start))
	if ordinary > 150 {
		t.Errorf("an ordinary repository made %d calls to the file system", ordinary)
	}

	modules := filepath.Join(repo, ".git", "modules")
	for i := 0; i < 190; i++ {
		d := filepath.Join(modules, fmt.Sprintf("m%03d", i))
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "HEAD"), []byte("ref: refs/heads/main\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "config"), []byte("[core]\n\tbare = false\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	scan(t, repo)
	big := lastScanCalls.Load()
	t.Logf("a repository with 190 submodules: %d calls", big)
	if big > 20000 {
		t.Errorf("a repository with 190 submodules made %d calls to the file system", big)
	}
}

// A genuine fan of links still reaches the cap on links, with the label for
// links.
func TestAGenuineFanOfLinksHitsTheLinkCapWithTheRightLabel(t *testing.T) {
	windowsOnly(t)
	entry := fan(t, "cap", 7, 3)
	was := maxLinkOps
	t.Cleanup(func() { maxLinkOps = was })
	maxLinkOps = 3
	err := tfs().guard(entry)
	if !errors.Is(err, errTooManyLinks) {
		t.Fatalf("err = %v, want errTooManyLinks", err)
	}
	if item := fsItem(err, entry, ""); !strings.Contains(item.Value, "too many links") {
		t.Errorf("the item says %q", item.Value)
	}
}

// Plain stats never count against the link cap: a long path of ordinary folders
// is not a fan of links.
func TestOrdinaryFoldersDoNotCountAsLinks(t *testing.T) {
	windowsOnly(t)
	dir := filepath.Join(t.TempDir(), "a", "b", "c", "d", "e", "f")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	was := maxLinkOps
	t.Cleanup(func() { maxLinkOps = was })
	maxLinkOps = 0
	if err := tfs().guard(dir); err != nil {
		t.Errorf("a path of ordinary folders with a link budget of none = %v", err)
	}
}

func TestAScanOutOfTimeSaysSo(t *testing.T) {
	release := make(chan struct{})
	wasEval := evalSymlinks
	t.Cleanup(func() { evalSymlinks = wasEval })
	evalSymlinks = func(p string) (string, error) { <-release; return p, nil }
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	f := newScanFS(ctx)
	_, err := f.Canonical(t.TempDir())
	if !errors.Is(err, errScanTooLong) {
		t.Errorf("err = %v, want errScanTooLong", err)
	}
	if item := fsItem(err, "x", ""); !strings.Contains(item.Value, "took too long") {
		t.Errorf("the item says %q", item.Value)
	}
	close(release)
	deadline := time.Now().Add(5 * time.Second)
	for leaked.Load() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
}
