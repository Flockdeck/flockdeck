package helpers

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/jmwri/flockdeck/internal/selfupdate"
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
	if err := s.verifyInstall(e, goos); err != nil {
		return &IntegrityError{err.Error()}
	}
	return nil
}

// IntegrityError is an installed version that no longer passes the check made
// before a start. Installing the same version again repairs it.
type IntegrityError struct{ Msg string }

func (e *IntegrityError) Error() string { return e.Msg }

func (s *Store) verifyInstall(e Entry, goos string) error {
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
// an unsigned one is never accepted for it, and nothing older than the newest
// signed version installed is accepted without being asked for by name on the
// command line. Without the first, anyone who can make the signature file
// disappear turns a signed helper into one that asks to be clicked through; and
// without the second, a signed release from before a fix can be served again
// after an uninstall, when nothing installed is left to compare it with.

type trustRecord struct {
	EverSigned bool `json:"everSigned"`
	// HighWater is the newest signed version ever installed.
	HighWater string `json:"highWater,omitempty"`
}

func (s *Store) trustFile(id string) string { return filepath.Join(s.appDir(id), "trust.json") }

func (s *Store) readTrust(id string) (rec trustRecord, exists, readable bool) {
	data, err := os.ReadFile(s.trustFile(id))
	if errors.Is(err, fs.ErrNotExist) {
		return rec, false, true
	}
	if err != nil {
		return rec, true, false
	}
	if json.Unmarshal(data, &rec) != nil {
		return trustRecord{}, true, false
	}
	return rec, true, true
}

// EverSigned reports whether a signed version of the helper was ever installed
// here, and it fails closed. Flockdeck writes trust.json only to say yes, so a
// file that is there but does not say yes (corrupt, empty, null, `{"everSigned":false}`, a
// folder, unreadable) is damage or tampering and counts as yes. With no file at
// all, the installed version's own install.json is asked, which covers a
// deleted record; and with neither, there is no signed history and this is a
// fresh install. An uninstall keeps the record; deleting the data clears it.
func (s *Store) EverSigned(id string) bool {
	if !validID(id) {
		return false
	}
	if _, exists, _ := s.readTrust(id); exists {
		return true
	}
	info, ok := s.Info(id)
	return ok && info.Signed
}

// HighWater is the newest signed version known to have been installed here: the
// record's, or the installed version's if that is signed and newer. Empty when
// none is known.
func (s *Store) HighWater(id string) string {
	if !validID(id) {
		return ""
	}
	hw := ""
	if rec, _, ok := s.readTrust(id); ok && validVersion(rec.HighWater) {
		hw = rec.HighWater
	}
	if info, ok := s.Info(id); ok && info.Signed && validVersion(info.Version) {
		if hw == "" || selfupdate.Newer(info.Version, hw) {
			hw = info.Version
		}
	}
	return hw
}

// atomicWrite is writeFileAtomic. A variable so a test can make a write fail.
var atomicWrite = writeFileAtomic

// TrustWriteError is a trust record that could not be written. An install that
// cannot record itself does not happen, so this says where and what to do.
type TrustWriteError struct {
	Path string
	Err  error
}

func (e *TrustWriteError) Error() string {
	return fmt.Sprintf("could not write %s (%v), so the install was not made current. Delete it or make it writable, then try again", e.Path, e.Err)
}
func (e *TrustWriteError) Unwrap() error { return e.Err }

// writeTrust writes the record. A file that is read-only (an attribute a person
// or a scanner may have set) has it cleared and is tried once more; a folder or
// anything else is left for the person to deal with.
func (s *Store) writeTrust(id string, rec trustRecord) error {
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	path := s.trustFile(id)
	if err := os.MkdirAll(s.appDir(id), 0o700); err != nil {
		return &TrustWriteError{path, err}
	}
	err = atomicWrite(path, data, 0o644)
	if err != nil {
		if fi, statErr := os.Lstat(path); statErr == nil && fi.Mode().IsRegular() {
			if os.Chmod(path, 0o644) == nil {
				err = atomicWrite(path, data, 0o644)
			}
		}
	}
	if err != nil {
		return &TrustWriteError{path, err}
	}
	return nil
}

// recordSigned writes down, before the pointer moves, that a signed version is
// being installed, leaving the high-water mark as it was: the mark rises only
// once the install has taken effect (see raiseHighWater).
func (s *Store) recordSigned(id string) error {
	rec, _, ok := s.readTrust(id)
	if !ok {
		rec = trustRecord{}
	}
	return s.writeTrust(id, trustRecord{EverSigned: true, HighWater: rec.HighWater})
}

// raiseHighWater lifts the mark to version once it is the installed one, and
// leaves it alone if it is already higher.
func (s *Store) raiseHighWater(id, version string) error {
	rec, _, ok := s.readTrust(id)
	if !ok {
		rec = trustRecord{}
	}
	if validVersion(rec.HighWater) && !selfupdate.Newer(version, rec.HighWater) {
		return nil
	}
	return s.writeTrust(id, trustRecord{EverSigned: true, HighWater: version})
}

// trustDamaged reports a trust record that is there and is not one Flockdeck
// wrote.
func (s *Store) trustDamaged(id string) bool {
	rec, exists, ok := s.readTrust(id)
	return exists && (!ok || !rec.EverSigned)
}

// ErrBelowHighWater is a release older than the newest signed version ever
// installed here. Only a version named on the command line gets past it.
var ErrBelowHighWater = errors.New("version is older than the newest signed version installed here before")

// checkHighWater refuses a version below the high-water mark unless the caller
// may downgrade.
func checkHighWater(version, highWater string, mayDowngrade bool) error {
	if mayDowngrade || highWater == "" || version == highWater {
		return nil
	}
	if selfupdate.Newer(highWater, version) {
		return fmt.Errorf("%w: %s is older than %s, which was installed before; name the version on the command line to install it anyway", ErrBelowHighWater, version, highWater)
	}
	return nil
}

// SignedRequiredError is an unsigned release of a helper that has to be
// signed, either because its catalogue entry says so or because a signed
// version was installed before. Unlike UnsignedError it has no override.
type SignedRequiredError struct {
	Name, Version string
	// Earlier is true when the reason is a signed version installed before,
	// false when the catalogue requires signatures.
	Earlier bool
	// Damaged is true when the trust record is there and unreadable, so it
	// cannot be told whether an earlier version was signed; Path is the record.
	Damaged bool
	Path    string
}

func (e *SignedRequiredError) Error() string {
	switch {
	case e.Damaged:
		return fmt.Sprintf("%s %s is not signed, and %s is damaged, so it cannot be told whether an earlier version was signed. It was refused. If %s never had a signed release here, delete %s to reset it", e.Name, e.Version, e.Path, e.Name, e.Path)
	case e.Earlier:
		return fmt.Sprintf("%s %s is not signed, but an earlier version of it that you installed was. It was refused and cannot be overridden: a release that loses its signature is not the one that was signed", e.Name, e.Version)
	}
	return fmt.Sprintf("%s %s is not signed, and %s requires every release to be signed. It was refused and cannot be overridden", e.Name, e.Version, e.Name)
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
		if s.trustDamaged(e.ID) {
			return &SignedRequiredError{Name: e.Name, Version: version, Damaged: true, Path: s.trustFile(e.ID)}
		}
		return &SignedRequiredError{Name: e.Name, Version: version, Earlier: true}
	}
	return nil
}
