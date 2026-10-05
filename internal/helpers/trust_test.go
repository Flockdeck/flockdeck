package helpers

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Every catalogued helper refuses an unsigned release with the reason, and
// nothing is downloaded for it. A signed one installs.
func TestEveryCataloguedHelperRefusesAnUnsignedRelease(t *testing.T) {
	if len(Catalogue()) == 0 {
		t.Fatal("the catalogue is empty")
	}
	for _, e := range Catalogue() {
		t.Run(e.ID, func(t *testing.T) {
			f := newFixture(t)
			f.entry = e
			f.entry.MinVersion = "0.2.0"
			f.entry.Source = f.site.URL + "/lens"
			f.rebuild()
			f.site.release(t, f.entry, releaseSpec{version: "0.4.0", noSig: true, latest: true})
			_, err := f.in.Install(t.Context(), e.ID, "")
			if !isSignedRequired(err) || !strings.Contains(err.Error(), "requires every release to be signed") {
				t.Fatalf("err = %v", err)
			}
			if f.site.hitCount(f.archivePath("0.4.0")) != 0 {
				t.Fatal("the archive was fetched")
			}
			if _, ok := f.store.Current(e.ID); ok {
				t.Fatal("installed")
			}
			f.publish("0.4.0")
			if _, err := f.install(""); err != nil {
				t.Fatalf("a signed release: %v", err)
			}
		})
	}
}

func isSignedRequired(err error) bool {
	var sr *SignedRequiredError
	return errors.As(err, &sr)
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

// An install whose record cannot be written does not stay: the pointer is put
// back and the message says where the record is.
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
	if hw := f.store.HighWater("lens"); hw == "" {
		t.Fatal("the mark is gone after a refused purge")
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
			return errors.New("simulated") // the raise, after the pointer moved

		}
		return prev(path, data, perm)
	}
	defer func() { atomicWrite = prev }()
	if _, err := f.install("0.5.0"); err == nil {
		t.Fatal("an install whose mark could not be raised went ahead")
	}
	if writes < 1 {
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

// A plan can only be carried out as this installer made it: one built by hand,
// a zero one, one with a field changed afterwards, and one from another
// installer are all refused before anything is fetched.
func TestAPlanIsOnlyCarriedOutAsItsInstallerMadeIt(t *testing.T) {
	f := newFixture(t)
	archive := f.publish("0.4.0")
	good, err := f.in.Plan(t.Context(), "lens", "")
	if err != nil {
		t.Fatal(err)
	}
	refused := func(name string, p *Plan, in *Installer) {
		t.Helper()
		if _, err := in.InstallPlan(t.Context(), p); err == nil || !strings.Contains(err.Error(), "did not make") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	refused("a nil plan", nil, f.in)
	refused("a zero plan", &Plan{}, f.in)
	refused("a plan built by hand", &Plan{
		Entry: f.entry, Version: "0.4.0", Archive: good.Archive, URL: good.URL, SHA256: sumHex(archive), Size: int64(len(archive)),
	}, f.in)
	changed := map[string]func(p *Plan){
		"version":   func(p *Plan) { p.Version = "0.9.0" },
		"url":       func(p *Plan) { p.URL = f.site.URL + "/lens/v0.4.0/other" },
		"sha256":    func(p *Plan) { p.SHA256 = strings.Repeat("b", 64) },
		"size":      func(p *Plan) { p.Size++ },
		"archive":   func(p *Plan) { p.Archive = "other.tar.gz" },
		"repair":    func(p *Plan) { p.Repair = true },
		"installed": func(p *Plan) { p.Installed = "0.1.0" },
		"entry id":  func(p *Plan) { p.Entry.ID = "other" },
		"date":      func(p *Plan) { p.Date = p.Date.Add(time.Hour) },
		"archive and its address": func(p *Plan) {
			p.Archive = "other.tar.gz"
			p.URL = f.site.URL + "/lens/v0.4.0/other.tar.gz"
		},
	}
	for name, mutate := range changed {
		c := *good
		mutate(&c)
		refused("a changed "+name, &c, f.in)
	}
	other := newFixture(t)
	other.publish("0.4.0")
	refused("a plan from another installer", good, other.in)
	if f.site.hitCount(f.archivePath("0.4.0")) != 0 {
		t.Fatal("the archive was fetched for a plan that was refused")
	}
	if _, ok := f.store.Current("lens"); ok {
		t.Fatal("installed")
	}
	// And the plan itself, untouched, installs.
	if _, err := f.in.InstallPlan(t.Context(), good); err != nil {
		t.Fatalf("the installer's own plan: %v", err)
	}
}

// A missing manifest signature is refused as such even when checksums.txt is
// signed correctly: the manifest is what binds the archive, so it has to be
// signed itself.
func TestAMissingManifestSignatureIsRefusedWhateverTheChecksumsSay(t *testing.T) {
	f := newFixture(t)
	f.site.release(t, f.entry, releaseSpec{version: "0.4.0", key: f.key, noSig: true, latest: true})
	_, err := f.in.Install(t.Context(), "lens", "")
	if !isSignedRequired(err) {
		t.Fatalf("err = %v, want a SignedRequiredError", err)
	}
	if f.site.hitCount(f.archivePath("0.4.0")) != 0 {
		t.Fatal("the archive was fetched")
	}
}

// A trust record that is there but is not a valid one fails closed after an
// uninstall: the install is refused with the path and how to reset it. A
// record in the older format with a valid mark still reads as a mark, and no
// record at all is a fresh install.
func TestADamagedTrustRecordRefusesTheInstall(t *testing.T) {
	shapes := map[string]string{
		"corrupt":           "{{{",
		"empty":             "",
		"an array":          "[]",
		"null":              "null",
		"an empty object":   "{}",
		"a bad version":     `{"highWater":"zzz"}`,
		"a number":          `{"highWater":5}`,
		"an old format one": "", // replaced below with a valid one
		"a folder":          "",
	}
	for name, content := range shapes {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			f.publish("0.3.0")
			f.publish("0.5.0")
			if _, err := f.install("0.5.0"); err != nil {
				t.Fatal(err)
			}
			if err := f.store.Uninstall("lens", false); err != nil {
				t.Fatal(err)
			}
			path := f.store.trustFile("lens")
			_ = os.Remove(path)
			switch name {
			case "a folder":
				if err := os.Mkdir(path, 0o755); err != nil {
					t.Fatal(err)
				}
			case "an old format one":
				content = `{"everSigned":true,"highWater":"0.5.0"}`
				fallthrough
			default:
				if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			_, err := f.in.PlanVersion(t.Context(), "lens", "0.3.0", false)
			if name == "an old format one" {
				if !errors.Is(err, ErrBelowHighWater) {
					t.Fatalf("a valid old record: err = %v, want ErrBelowHighWater", err)
				}
				return
			}
			tr, ok := asErr[*TrustReadError](err)
			if !ok || tr.Path != path || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "Delete it to reset it") {
				t.Fatalf("err = %v", err)
			}
			if _, ok := f.store.Current("lens"); ok {
				t.Fatal("installed")
			}
			// Deleting it is the way out, and it is a fresh start.
			_ = os.RemoveAll(path)
			if _, err := f.in.PlanVersion(t.Context(), "lens", "0.3.0", false); err != nil {
				t.Fatalf("after deleting the record: %v", err)
			}
		})
	}
}

// A plan is stale once what is installed has changed: planned for 0.3.0 (a
// named downgrade) while 0.4.0 was installed, then 0.5.0 installed, it is not
// installed, and 0.5.0 stays current.
func TestAStalePlanIsRefusedAfterTheInstalledVersionMoves(t *testing.T) {
	f := newFixture(t)
	f.publish("0.3.0")
	f.publish("0.4.0")
	f.publish("0.5.0")
	if _, err := f.install("0.4.0"); err != nil {
		t.Fatal(err)
	}
	old, err := f.in.Plan(t.Context(), "lens", "0.3.0")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.install("0.5.0"); err != nil {
		t.Fatal(err)
	}
	replay := *old
	if _, err := f.in.InstallPlan(t.Context(), &replay); err == nil || !strings.Contains(err.Error(), "plan it again") {
		t.Fatalf("err = %v", err)
	}
	if v, _ := f.store.Current("lens"); v != "0.5.0" {
		t.Fatalf("current = %q", v)
	}
	if hw := f.store.HighWater("lens"); hw != "0.5.0" {
		t.Fatalf("high-water = %q", hw)
	}
}

// The minimum version and the mark are applied again when the plan is carried
// out: a minimum raised, or a mark that rose, since the plan was made.
func TestTheMinimumAndTheMarkAreAppliedAgainAtInstall(t *testing.T) {
	f := newFixture(t)
	cur := f.entry
	f.rebuild(func(o *Options) {
		o.Lookup = func(id string) (Entry, bool) { return cur, id == cur.ID }
	})
	f.publish("0.3.0")
	f.publish("0.4.0")
	plan, err := f.in.PlanVersion(t.Context(), "lens", "0.3.0", false)
	if err != nil {
		t.Fatal(err)
	}
	cur.MinVersion = "0.4.0"
	if _, err := f.in.InstallPlan(t.Context(), plan); !errors.Is(err, ErrBelowMinimum) {
		t.Fatalf("a minimum raised after the plan: err = %v", err)
	}
	cur.MinVersion = "0.2.0"
	plan, err = f.in.PlanVersion(t.Context(), "lens", "0.3.0", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.writeTrust("lens", trustRecord{HighWater: "0.6.0"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.in.InstallPlan(t.Context(), plan); !errors.Is(err, ErrBelowHighWater) {
		t.Fatalf("a mark raised after the plan: err = %v", err)
	}
	if _, ok := f.store.Current("lens"); ok {
		t.Fatal("installed")
	}
}
