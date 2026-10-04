package helpers

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// Checks made before a helper is started, because what is on disk now is not
// what was checked when it was installed: any program running as the same
// user can write to these folders. The checks catch a swap, a link put where
// a folder was, and an edit, and they fail closed. They do not defend against
// a program that already runs as the user and can change install.json as well
// as the binary, or one that swaps the binary in the moment after the check;
// nothing short of an operating system boundary does, and the dialog says so.

// plainDir reports whether path is a real folder: not a symlink, and not a
// junction or other reparse point (which Go reports as irregular).
func plainDir(path string) error {
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if fi.Mode()&(fs.ModeSymlink|fs.ModeIrregular) != 0 || !fi.IsDir() {
		return fmt.Errorf("%s is not a plain folder (a link or junction was put there)", path)
	}
	return nil
}

// VerifyInstall refuses to go on unless the installed version is exactly what
// was installed: its folders are plain folders, its binary is a plain file, and
// the binary's SHA-256 is the one install.json recorded when it was unpacked.
// The error says to reinstall, which puts a checked copy back.
func (s *Store) VerifyInstall(e Entry, goos string) error {
	v, ok := s.Current(e.ID)
	if !ok {
		return fmt.Errorf("%s is not installed", e.Name)
	}
	reinstall := fmt.Sprintf("; reinstall it with: flockdeck helpers install %s", e.ID)
	for _, d := range []string{s.appDir(e.ID), s.versionsDir(e.ID), s.versionDir(e.ID, v)} {
		if err := plainDir(d); err != nil {
			return fmt.Errorf("refusing to start %s: %w%s", e.Name, err, reinstall)
		}
	}
	bin := filepath.Join(s.versionDir(e.ID, v), e.BinaryName(goos))
	fi, err := os.Lstat(bin)
	if err != nil {
		return fmt.Errorf("refusing to start %s: its program is missing%s", e.Name, reinstall)
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("refusing to start %s: its program is a link or special file, not the file that was installed%s", e.Name, reinstall)
	}
	data, err := os.ReadFile(filepath.Join(s.versionDir(e.ID, v), "install.json"))
	if err != nil {
		return fmt.Errorf("refusing to start %s: install.json cannot be read%s", e.Name, reinstall)
	}
	var info InstallInfo
	if json.Unmarshal(data, &info) != nil || len(info.BinarySHA256) != 2*sha256.Size {
		return fmt.Errorf("refusing to start %s: install.json has no checksum of the program to compare with%s", e.Name, reinstall)
	}
	got, err := fileSHA256(bin)
	if err != nil {
		return fmt.Errorf("refusing to start %s: %w", e.Name, err)
	}
	if got != info.BinarySHA256 {
		return fmt.Errorf("refusing to start %s: its program has changed since it was installed (SHA-256 %s, installed as %s)%s", e.Name, got[:12], info.BinarySHA256[:12], reinstall)
	}
	return nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// The trust record: once a helper has been installed from a signed release,
// an unsigned one is never accepted for it. Without this, anyone who can
// make the signature file disappear (a CDN, a proxy, a bucket) turns a signed
// helper into one that asks the user to click through an override.

type trustRecord struct {
	EverSigned bool   `json:"everSigned"`
	Version    string `json:"version"`
}

func (s *Store) trustFile(id string) string { return filepath.Join(s.appDir(id), "trust.json") }

// EverSigned reports whether a signed version of the helper was ever installed
// here. An uninstall keeps the record; deleting the helper's data does not.
func (s *Store) EverSigned(id string) bool {
	if !validID(id) {
		return false
	}
	data, err := os.ReadFile(s.trustFile(id))
	if err != nil {
		return false
	}
	var t trustRecord
	return json.Unmarshal(data, &t) == nil && t.EverSigned
}

func (s *Store) markSigned(id, version string) error {
	data, err := json.Marshal(trustRecord{EverSigned: true, Version: version})
	if err != nil {
		return err
	}
	return writeFileAtomic(s.trustFile(id), data, 0o644)
}

// SignedRequiredError is an unsigned release of a helper that has to be
// signed, either because its catalogue entry says so or because a signed
// version was installed before. Unlike UnsignedError it has no override.
type SignedRequiredError struct {
	Name, Version string
	// Earlier is true when the reason is a signed version installed before,
	// false when the catalogue requires signatures.
	Earlier bool
}

func (e *SignedRequiredError) Error() string {
	why := "the catalogue requires every release of it to be signed"
	if e.Earlier {
		why = "an earlier version of it that you installed was signed"
	}
	return fmt.Sprintf("%s %s has no signature, but %s. It was refused and cannot be overridden: a release that loses its signature is not the one that was signed", e.Name, e.Version, why)
}

// checkSignedRequired refuses an unsigned plan for a helper that has to be
// signed.
func (s *Store) checkSignedRequired(e Entry, version string, signed bool) error {
	if signed {
		return nil
	}
	if e.RequireSigned {
		return &SignedRequiredError{Name: e.Name, Version: version}
	}
	if s.EverSigned(e.ID) {
		return &SignedRequiredError{Name: e.Name, Version: version, Earlier: true}
	}
	return nil
}
