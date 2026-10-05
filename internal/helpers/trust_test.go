package helpers

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestUnsignedAfterSignedIsRefusedWithNoOverride(t *testing.T) {
	f := newFixture(t)
	f.publish("0.4.0")
	if _, err := f.install(""); err != nil {
		t.Fatal(err)
	}
	if !f.store.EverSigned("lens") {
		t.Fatal("a signed install left no record")
	}
	// The next release arrives with its signature gone.
	f.site.release(t, f.entry, releaseSpec{version: "0.5.0", noSig: true, latest: true})
	for _, allow := range []bool{false, true} {
		_, err := f.in.Install(t.Context(), "lens", "", allow)
		var sr *SignedRequiredError
		if !errors.As(err, &sr) || !sr.Earlier {
			t.Fatalf("allowUnsigned=%v: err = %v, want a SignedRequiredError about an earlier signed version", allow, err)
		}
		var ue *UnsignedError
		if errors.As(err, &ue) {
			t.Fatal("an overridable error was offered")
		}
	}
	if !strings.Contains((&SignedRequiredError{Name: "lens", Version: "0.5.0", Earlier: true}).Error(), "earlier version") {
		t.Error("the message does not say earlier versions were signed")
	}
	if v, _ := f.store.Current("lens"); v != "0.4.0" {
		t.Fatalf("current = %q", v)
	}
	// The record survives an uninstall.
	if err := f.store.Uninstall("lens", false); err != nil {
		t.Fatal(err)
	}
	if _, err := f.in.Install(t.Context(), "lens", "", true); !isSignedRequired(err) {
		t.Fatalf("after an uninstall: %v", err)
	}
	// Deleting the data clears it, as a person starting over.
	if err := f.store.Uninstall("lens", true); err != nil {
		t.Fatal(err)
	}
	if f.store.EverSigned("lens") {
		t.Fatal("a purge left the record")
	}
	if _, err := f.in.Install(t.Context(), "lens", "", true); err != nil {
		t.Fatalf("after a purge: %v", err)
	}
}

func isSignedRequired(err error) bool {
	var sr *SignedRequiredError
	return errors.As(err, &sr)
}

func TestInstallPlanRefusesAnUnsignedPlanForASignedHelper(t *testing.T) {
	f := newFixture(t)
	f.publish("0.4.0")
	if _, err := f.install(""); err != nil {
		t.Fatal(err)
	}
	// A plan built by hand, as a caller that skipped Plan might.
	f.site.release(t, f.entry, releaseSpec{version: "0.5.0", noSig: true, latest: true})
	plan := &Plan{Entry: f.entry, Version: "0.5.0", Archive: f.entry.ArchiveName("0.5.0", "linux", "amd64"), SHA256: strings.Repeat("a", 64), Signed: false}
	if _, err := f.in.InstallPlan(t.Context(), plan); !isSignedRequired(err) {
		t.Fatalf("err = %v", err)
	}
}

func TestACatalogueThatRequiresSignaturesHasNoOverride(t *testing.T) {
	f := newFixture(t)
	f.entry.RequireSigned = true
	f.rebuild()
	f.site.release(t, f.entry, releaseSpec{version: "0.4.0", noSig: true, latest: true})
	_, err := f.in.Install(t.Context(), "lens", "", true)
	var sr *SignedRequiredError
	if !errors.As(err, &sr) || sr.Earlier {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), "requires every release to be signed") {
		t.Fatalf("the message does not say why: %v", err)
	}
}

func TestLensRequiresSignedReleases(t *testing.T) {
	if !lens.RequireSigned {
		t.Fatal("lens does not require its releases to be signed")
	}
}

// With the real catalogue entry, an unsigned release cannot be installed, with
// or without the flag, and nothing is downloaded for it.
func TestLensRefusesAnUnsignedRelease(t *testing.T) {
	f := newFixture(t)
	f.entry = lens
	f.entry.MinVersion = "0.2.0"
	f.entry.Source = f.site.URL + "/lens"
	f.rebuild()
	if !f.entry.RequireSigned {
		t.Fatal("the test entry is not the catalogue's")
	}
	f.site.release(t, f.entry, releaseSpec{version: "0.4.0", noSig: true, latest: true})
	for _, allow := range []bool{false, true} {
		_, err := f.in.Install(t.Context(), "lens", "", allow)
		sr, ok := asErr[*SignedRequiredError](err)
		if !ok || sr.Earlier || sr.Damaged {
			t.Fatalf("allowUnsigned=%v: err = %v", allow, err)
		}
		if !strings.Contains(err.Error(), "not signed") || !strings.Contains(err.Error(), "requires every release to be signed") {
			t.Fatalf("the message: %v", err)
		}
		if _, ok := asErr[*UnsignedError](err); ok {
			t.Fatal("an override was offered")
		}
	}
	if f.site.hitCount(f.archivePath("0.4.0")) != 0 {
		t.Fatal("the archive was fetched")
	}
	if _, ok := f.store.Current("lens"); ok {
		t.Fatal("installed")
	}
	// And a signed one installs.
	f.publish("0.4.0")
	if _, err := f.install(""); err != nil {
		t.Fatal(err)
	}
}

// start-time integrity -----------------------------------------------------

func installedFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	f.publish("0.4.0")
	if _, err := f.install(""); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestVerifyInstallAcceptsWhatWasInstalled(t *testing.T) {
	f := installedFixture(t)
	if err := f.store.VerifyInstall(f.entry, "linux"); err != nil {
		t.Fatal(err)
	}
	info, _ := f.store.Info("lens")
	if len(info.BinarySHA256) != 64 {
		t.Fatalf("install.json has no binary hash: %+v", info)
	}
}

func TestVerifyInstallRefusesAChangedBinary(t *testing.T) {
	f := installedFixture(t)
	bin := filepath.Join(f.store.versionDir("lens", "0.4.0"), "lens")
	if err := os.WriteFile(bin, []byte("not what was installed"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := f.store.VerifyInstall(f.entry, "linux")
	if err == nil || !strings.Contains(err.Error(), "has changed since it was installed") || !strings.Contains(err.Error(), "reinstall") {
		t.Fatalf("err = %v", err)
	}
}

func TestVerifyInstallRefusesAMissingOrUnreadableRecord(t *testing.T) {
	f := installedFixture(t)
	info := filepath.Join(f.store.versionDir("lens", "0.4.0"), "install.json")
	for name, content := range map[string]string{
		"no hash":       `{"version":"0.4.0","signed":true}`,
		"a short hash":  `{"binarySha256":"abc"}`,
		"not json":      `{`,
		"an empty file": ``,
	} {
		if err := os.WriteFile(info, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := f.store.VerifyInstall(f.entry, "linux"); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if err := os.Remove(info); err != nil {
		t.Fatal(err)
	}
	if err := f.store.VerifyInstall(f.entry, "linux"); err == nil {
		t.Error("a missing install.json was accepted")
	}
}

func TestVerifyInstallRefusesAMissingBinary(t *testing.T) {
	f := installedFixture(t)
	if err := os.Remove(filepath.Join(f.store.versionDir("lens", "0.4.0"), "lens")); err != nil {
		t.Fatal(err)
	}
	if err := f.store.VerifyInstall(f.entry, "linux"); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("err = %v", err)
	}
}

// makeLink puts a link where path was: a symbolic link, or on Windows, where
// that needs a privilege, a junction, which is the other way a folder can stand
// for another.
func makeLink(t *testing.T, path, target string, dir bool) {
	t.Helper()
	if err := os.Symlink(target, path); err == nil {
		return
	} else if runtime.GOOS != "windows" || !dir {
		t.Skipf("cannot make links here: %v", err)
	}
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", path, target).CombinedOutput(); err != nil {
		t.Skipf("cannot make a junction here: %v %s", err, out)
	}
}

func TestVerifyInstallRefusesALinkedBinary(t *testing.T) {
	f := installedFixture(t)
	bin := filepath.Join(f.store.versionDir("lens", "0.4.0"), "lens")
	real := filepath.Join(t.TempDir(), "real")
	data, _ := os.ReadFile(bin)
	if err := os.WriteFile(real, data, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(bin); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, bin); err != nil {
		t.Skipf("cannot make a symbolic link here: %v", err)
	}
	// Same bytes, so the hash alone would pass: the link is what is refused.
	if err := f.store.VerifyInstall(f.entry, "linux"); err == nil || !strings.Contains(err.Error(), "link") {
		t.Fatalf("err = %v", err)
	}
}

func TestVerifyInstallRefusesALinkedVersionFolder(t *testing.T) {
	f := installedFixture(t)
	dir := f.store.versionDir("lens", "0.4.0")
	copyTo := filepath.Join(t.TempDir(), "copy")
	if err := os.Rename(dir, copyTo); err != nil {
		t.Fatal(err)
	}
	makeLink(t, dir, copyTo, true)
	// Current() still sees a folder, since Stat follows the link.
	if err := f.store.VerifyInstall(f.entry, "linux"); err == nil || !strings.Contains(err.Error(), "not a plain folder") {
		t.Fatalf("err = %v", err)
	}
}

func TestVerifyInstallRefusesALinkedVersionsFolder(t *testing.T) {
	f := installedFixture(t)
	dir := f.store.versionsDir("lens")
	copyTo := filepath.Join(t.TempDir(), "versions")
	if err := os.Rename(dir, copyTo); err != nil {
		t.Fatal(err)
	}
	makeLink(t, dir, copyTo, true)
	if err := f.store.VerifyInstall(f.entry, "linux"); err == nil || !strings.Contains(err.Error(), "not a plain folder") {
		t.Fatalf("err = %v", err)
	}
}

// the high-water mark ---------------------------------------------------------

func TestAReplayBelowTheHighWaterMarkIsRefusedEvenAfterAnUninstall(t *testing.T) {
	f := newFixture(t)
	f.publish("0.5.0")
	if _, err := f.install(""); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Uninstall("lens", false); err != nil {
		t.Fatal(err)
	}
	// latest.json is rewound to a genuinely signed, older, still above-minimum
	// release, and nothing is installed to compare it with.
	f.publish("0.2.0")
	f.site.put("/lens/latest.json", []byte(`{"version":"v0.2.0"}`))
	if _, err := f.install(""); !errors.Is(err, ErrBelowHighWater) {
		t.Fatalf("err = %v, want ErrBelowHighWater", err)
	}
	if _, ok := f.store.Current("lens"); ok {
		t.Fatal("installed")
	}
	// The window names the version it was shown, which is not a request to go
	// backwards.
	if _, err := f.in.PlanVersion(t.Context(), "lens", "0.2.0", false); !errors.Is(err, ErrBelowHighWater) {
		t.Fatalf("a pinned plan from the window: %v", err)
	}
	// On the command line a version can be named.
	if _, err := f.install("0.2.0"); err != nil {
		t.Fatalf("a named version: %v", err)
	}
	// But never below MinVersion.
	f.publish("0.1.0")
	if _, err := f.install("0.1.0"); !errors.Is(err, ErrBelowMinimum) {
		t.Fatalf("below the minimum: %v", err)
	}
}

func TestTheHighWaterMarkOnlyRises(t *testing.T) {
	f := newFixture(t)
	f.publish("0.5.0")
	f.publish("0.3.0")
	if _, err := f.install("0.5.0"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.install("0.3.0"); err != nil { // named, allowed
		t.Fatal(err)
	}
	if hw := f.store.HighWater("lens"); hw != "0.5.0" {
		t.Fatalf("high-water = %q after installing a lower version by name", hw)
	}
	if err := checkHighWater("0.5.0", "0.5.0", false); err != nil {
		t.Fatalf("the mark itself is allowed: %v", err)
	}
	if err := checkHighWater("0.5.1", "0.5.0", false); err != nil {
		t.Fatalf("above the mark: %v", err)
	}
	if err := checkHighWater("0.4.9", "", false); err != nil {
		t.Fatalf("no mark: %v", err)
	}
}

// trust record shapes -------------------------------------------------------------

func TestEverSignedFailsClosed(t *testing.T) {
	s := &Store{Root: t.TempDir()}
	if s.EverSigned("lens") {
		t.Fatal("a fresh helper with no history counts as signed")
	}
	if err := os.MkdirAll(s.appDir("lens"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"corrupt":           "{",
		"empty":             "",
		"null":              "null",
		"false":             `{"everSigned":false}`,
		"false with a mark": `{"everSigned":false,"highWater":"0.5.0"}`,
		"an array":          "[]",
		"a number":          "7",
		"true":              `{"everSigned":true,"highWater":"0.5.0"}`,
	} {
		if err := os.WriteFile(s.trustFile("lens"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if !s.EverSigned("lens") {
			t.Errorf("%s: a trust record that is there counts as no history", name)
		}
	}
	// A record that is a folder, which cannot be read as a file.
	_ = os.Remove(s.trustFile("lens"))
	if err := os.Mkdir(s.trustFile("lens"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !s.EverSigned("lens") {
		t.Error("a trust record that cannot be read counts as no history")
	}
}

func TestADeletedTrustRecordIsNotForgottenWhileTheSignedInstallStands(t *testing.T) {
	f := installedFixture(t)
	if err := os.Remove(f.store.trustFile("lens")); err != nil {
		t.Fatal(err)
	}
	if !f.store.EverSigned("lens") {
		t.Fatal("deleting trust.json made a signed install count as never signed")
	}
	if hw := f.store.HighWater("lens"); hw != "0.4.0" {
		t.Fatalf("high-water = %q", hw)
	}
	f.site.release(t, f.entry, releaseSpec{version: "0.5.0", noSig: true, latest: true})
	if _, err := f.in.Install(t.Context(), "lens", "", true); !isSignedRequired(err) {
		t.Fatalf("err = %v", err)
	}
}

func TestAnUnsignedInstallLeavesNoSignedHistory(t *testing.T) {
	f := newFixture(t)
	f.site.release(t, f.entry, releaseSpec{version: "0.4.0", noSig: true, latest: true})
	if _, err := f.in.Install(t.Context(), "lens", "", true); err != nil {
		t.Fatal(err)
	}
	if f.store.EverSigned("lens") {
		t.Fatal("an unsigned install counts as signed history")
	}
	if hw := f.store.HighWater("lens"); hw != "" {
		t.Fatalf("high-water = %q", hw)
	}
}

// The record is written before the pointer moves, and an install that cannot
// write it does not happen.
func TestASignedInstallThatCannotRecordItselfFails(t *testing.T) {
	f := newFixture(t)
	f.publish("0.4.0")
	f.publish("0.5.0")
	if _, err := f.install("0.4.0"); err != nil {
		t.Fatal(err)
	}
	// The record cannot be written: a folder is where the file goes.
	if err := os.Remove(f.store.trustFile("lens")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(f.store.trustFile("lens"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.store.trustFile("lens"), "x"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := f.install("0.5.0")
	if _, ok := asErr[*TrustWriteError](err); !ok || !strings.Contains(err.Error(), f.store.trustFile("lens")) || !strings.Contains(err.Error(), "Delete it or make it writable") {
		t.Fatalf("err = %v", err)
	}
	if v, _ := f.store.Current("lens"); v != "0.4.0" {
		t.Fatalf("current = %q after a failed install", v)
	}
	if _, err := os.Stat(f.store.versionDir("lens", "0.5.0")); err == nil {
		t.Fatal("the version that could not be recorded was left in versions/")
	}
}

// The record goes only once the data has: a purge that is refused keeps it.
func TestAPurgeRefusedForALinkedDataFolderKeepsTheTrustRecord(t *testing.T) {
	f := installedFixture(t)
	target := t.TempDir()
	if err := os.RemoveAll(f.store.DataDir("lens")); err != nil {
		t.Fatal(err)
	}
	makeLink(t, f.store.DataDir("lens"), target, true)
	if err := f.store.Uninstall("lens", true); err == nil {
		t.Fatal("a purge went ahead through a link")
	}
	if _, err := os.Stat(f.store.trustFile("lens")); err != nil {
		t.Fatalf("the trust record was removed by a refused purge: %v", err)
	}
	if !f.store.EverSigned("lens") {
		t.Fatal("EverSigned is false after a refused purge")
	}
}

// repair ----------------------------------------------------------------------------

func TestATamperedInstallIsRepairedByInstallingTheSameVersionAgain(t *testing.T) {
	f := installedFixture(t)
	bin := filepath.Join(f.store.versionDir("lens", "0.4.0"), "lens")
	if err := os.WriteFile(bin, []byte("not what was installed"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := f.store.VerifyInstall(f.entry, "linux"); err == nil {
		t.Fatal("the tamper was not seen")
	}
	plan, err := f.in.Plan(t.Context(), "lens", "")
	if err != nil {
		t.Fatalf("plan for a repair: %v", err)
	}
	if !plan.Repair || plan.Version != "0.4.0" {
		t.Fatalf("plan = %+v", plan)
	}
	if _, err := f.install(""); err != nil {
		t.Fatalf("the repair: %v", err)
	}
	if err := f.store.VerifyInstall(f.entry, "linux"); err != nil {
		t.Fatalf("still refused after a repair: %v", err)
	}
	// And now it is installed, not in need of repair.
	if _, err := f.install(""); !errors.Is(err, ErrAlreadyInstalled) {
		t.Fatalf("err = %v", err)
	}
	if plan, _ := f.in.PlanVersion(t.Context(), "lens", "0.4.0", false); plan != nil {
		t.Fatalf("a pinned plan for an intact install: %+v", plan)
	}
}

func TestAFailedRepairLeavesWhatWasThere(t *testing.T) {
	var failAt string
	f := newFixture(t, func(o *Options) {
		o.Hook = func(s string) error {
			if s == failAt {
				return errors.New("simulated")
			}
			return nil
		}
	})
	f.publish("0.4.0")
	if _, err := f.install(""); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(f.store.versionDir("lens", "0.4.0"), "lens")
	if err := os.WriteFile(bin, []byte("changed"), 0o755); err != nil {
		t.Fatal(err)
	}
	failAt = "current"
	if _, err := f.install(""); err == nil {
		t.Fatal("the repair did not fail")
	}
	if b, err := os.ReadFile(bin); err != nil || string(b) != "changed" {
		t.Fatalf("what was there was not put back: %q, %v", b, err)
	}
	if v, ok := f.store.Current("lens"); !ok || v != "0.4.0" {
		t.Fatalf("current = %q, %v", v, ok)
	}
}

// A trust record that is read-only has the attribute cleared and is written
// once more; the install goes ahead.
func TestAReadOnlyTrustRecordIsWrittenAnyway(t *testing.T) {
	f := newFixture(t)
	f.publish("0.4.0")
	f.publish("0.5.0")
	if _, err := f.install("0.4.0"); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(f.store.trustFile("lens"), 0o444); err != nil {
		t.Fatal(err)
	}
	if _, err := f.install("0.5.0"); err != nil {
		t.Fatalf("a read-only trust record stopped an install: %v", err)
	}
	if hw := f.store.HighWater("lens"); hw != "0.5.0" {
		t.Fatalf("high-water = %q", hw)
	}
}

// A damaged record is said to be damaged, with where it is and how to reset it,
// and is not blamed on an earlier signed version.
func TestADamagedTrustRecordIsNamedAsSuch(t *testing.T) {
	f := newFixture(t)
	if err := os.MkdirAll(f.store.appDir("lens"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.store.trustFile("lens"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.site.release(t, f.entry, releaseSpec{version: "0.4.0", noSig: true, latest: true})
	_, err := f.in.Install(t.Context(), "lens", "", true)
	sr, ok := asErr[*SignedRequiredError](err)
	if !ok || !sr.Damaged || sr.Earlier {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), "damaged") || !strings.Contains(err.Error(), f.store.trustFile("lens")) || !strings.Contains(err.Error(), "delete") {
		t.Fatalf("the message does not say how to recover: %v", err)
	}
	if strings.Contains(err.Error(), "an earlier version of it that you installed was") {
		t.Fatalf("a damaged record was blamed on a signed install: %v", err)
	}
	// Deleting it is the way out.
	if err := os.Remove(f.store.trustFile("lens")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.in.Install(t.Context(), "lens", "", true); err != nil {
		t.Fatalf("after deleting the record: %v", err)
	}
}

// The mark rises once the pointer has moved, and a failure to raise it puts the
// old version back instead of leaving a new one the record knows nothing of.
func TestTheHighWaterMarkRisesAfterThePointerMoves(t *testing.T) {
	f := newFixture(t)
	f.publish("0.4.0")
	f.publish("0.5.0")
	if _, err := f.install("0.4.0"); err != nil {
		t.Fatal(err)
	}
	rec, _, _ := f.store.readTrust("lens")
	if rec.HighWater != "0.4.0" {
		t.Fatalf("record = %+v", rec)
	}
	writes := 0
	prev := atomicWrite
	atomicWrite = func(path string, data []byte, perm os.FileMode) error {
		if strings.HasSuffix(path, "trust.json") {
			writes++
			if writes >= 2 { // the raise, after the pointer moved (and its retry)
				return errors.New("simulated")
			}
		}
		return prev(path, data, perm)
	}
	defer func() { atomicWrite = prev }()
	if _, err := f.install("0.5.0"); err == nil {
		t.Fatal("an install whose mark could not be raised went ahead")
	}
	if writes < 2 {
		t.Fatalf("the record was written %d times", writes)
	}
	if v, _ := f.store.Current("lens"); v != "0.4.0" {
		t.Fatalf("current = %q", v)
	}
	if rec, _, _ := f.store.readTrust("lens"); rec.HighWater != "0.4.0" {
		t.Fatalf("the mark was raised without the install: %+v", rec)
	}
	atomicWrite = prev
	if _, err := f.install("0.5.0"); err != nil {
		t.Fatal(err)
	}
	if rec, _, _ := f.store.readTrust("lens"); rec.HighWater != "0.5.0" {
		t.Fatalf("record = %+v", rec)
	}
}

// A repair puts back the installed version: that is not a downgrade, even with
// the mark above it, and it leaves the folder kept for a rollback alone.
func TestARepairBelowTheMarkWorksAndKeepsTheRollbackFolder(t *testing.T) {
	f := newFixture(t)
	f.publish("0.3.0")
	f.publish("0.5.0")
	if _, err := f.install("0.5.0"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.install("0.3.0"); err != nil { // named: below the mark
		t.Fatal(err)
	}
	if hw := f.store.HighWater("lens"); hw != "0.5.0" {
		t.Fatalf("high-water = %q", hw)
	}
	bin := filepath.Join(f.store.versionDir("lens", "0.3.0"), "lens")
	if err := os.WriteFile(bin, []byte("changed"), 0o755); err != nil {
		t.Fatal(err)
	}
	plan, err := f.in.PlanVersion(t.Context(), "lens", "0.3.0", false)
	if err != nil || !plan.Repair {
		t.Fatalf("the plan for a repair below the mark: %+v, %v", plan, err)
	}
	if _, err := f.in.InstallPlan(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	if err := f.store.VerifyInstall(f.entry, "linux"); err != nil {
		t.Fatalf("not repaired: %v", err)
	}
	if _, err := os.Stat(f.store.versionDir("lens", "0.5.0")); err != nil {
		t.Fatalf("the rollback folder was pruned by a repair: %v", err)
	}
	// An intact install below the mark is still a downgrade for the window.
	if _, err := f.in.PlanVersion(t.Context(), "lens", "0.3.0", false); err == nil {
		t.Fatal("an intact install was planned again")
	}
}

// The rules come from the catalogue entry the installer looks up, not from the
// copy a plan carries: a plan built by hand with RequireSigned cleared does not
// install an unsigned release of a helper that requires signatures.
func TestAPlansOwnEntryDoesNotChangeTheRules(t *testing.T) {
	f := newFixture(t)
	f.entry = lens
	f.entry.MinVersion = "0.2.0"
	f.entry.Source = f.site.URL + "/lens"
	f.rebuild()
	archive, _ := f.site.release(t, f.entry, releaseSpec{version: "0.4.0", noSig: true, latest: true})
	loose := f.entry
	loose.RequireSigned = false
	plan := &Plan{
		Entry: loose, Version: "0.4.0", Signed: false,
		Archive: f.entry.ArchiveName("0.4.0", f.goos, f.arch),
		URL:     f.site.URL + f.archivePath("0.4.0"),
		SHA256:  sumHex(archive), Size: int64(len(archive)),
	}
	_, err := f.in.InstallPlan(t.Context(), plan)
	if _, ok := asErr[*SignedRequiredError](err); !ok {
		t.Fatalf("err = %v", err)
	}
	if _, ok := f.store.Current("lens"); ok {
		t.Fatal("installed")
	}
	if f.site.hitCount(f.archivePath("0.4.0")) != 0 {
		t.Fatal("the archive was fetched")
	}
}

// If current.json cannot be written the mark is not raised and the old version
// stays current: the mark only follows a pointer that moved.
func TestAFailedPointerWriteLeavesTheMarkAlone(t *testing.T) {
	f := newFixture(t)
	f.publish("0.4.0")
	f.publish("0.5.0")
	if _, err := f.install("0.4.0"); err != nil {
		t.Fatal(err)
	}
	prev := atomicWrite
	atomicWrite = func(path string, data []byte, perm os.FileMode) error {
		if strings.HasSuffix(path, "current.json") {
			return errors.New("simulated")
		}
		return prev(path, data, perm)
	}
	defer func() { atomicWrite = prev }()
	if _, err := f.install("0.5.0"); err == nil {
		t.Fatal("an install whose pointer could not be written went ahead")
	}
	if hw := f.store.HighWater("lens"); hw != "0.4.0" {
		t.Fatalf("the mark moved to %q", hw)
	}
	if v, _ := f.store.Current("lens"); v != "0.4.0" {
		t.Fatalf("current = %q", v)
	}
	if _, err := os.Stat(f.store.versionDir("lens", "0.5.0")); err == nil {
		t.Fatal("the new version's folder was left behind")
	}
}
