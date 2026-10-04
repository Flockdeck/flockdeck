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
	if !strings.Contains(err.Error(), "catalogue requires") {
		t.Fatalf("the message does not say why: %v", err)
	}
}

func TestLensDoesNotYetRequireSignatures(t *testing.T) {
	// TODO(owner): flip this, and the catalogue, when lens ships signed.
	if lens.RequireSigned {
		t.Fatal("lens requires signatures; update this test with the catalogue")
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
