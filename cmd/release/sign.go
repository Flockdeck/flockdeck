package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jmwri/flockdeck/internal/selfupdate"
)

// A release is signed so that the updater can trust what it downloads from
// dl.flockdeck.ai, which is a bucket and a CDN rather than GitHub. The key is
// Ed25519, and its public half is compiled into internal/selfupdate.
// Terraform makes it (svc/flockdeck-site/release.tf in terrawost) and writes
// the private half into the release workflow's FLOCKDECK_SIGNING_KEY secret,
// so it is in no file anybody holds. -keygen makes one by hand instead, to try
// the pipeline with, or for a key kept away from Terraform.
//
// Signing is a step of its own, after the build, rather than part of it: the
// build runs on every developer's machine through `make package`, where there
// is no key. In the workflow it comes before the release is published
// anywhere, because the updater holds a release it reads from GitHub to
// checksums.txt.sig as well, so GitHub's copy has to carry it. The site's
// other secrets are needed only after GitHub's copy is published, so a
// missing one of those costs the site's copy and never GitHub's.

// signingKeyEnv holds the signing key: the PKCS#8 PEM Terraform writes there,
// or the base64 seed -keygen writes to its file.
const signingKeyEnv = "FLOCKDECK_SIGNING_KEY"

// runKeygen makes a new release signing key. The private key goes to path,
// readable by its owner alone and never over a file already there, since that
// could be the only copy of the key every installed copy trusts. Only the
// public key is printed, on stdout, so it can be piped or pasted; what to do
// with each half goes to stderr.
func runKeygen(path string, stdout, stderr io.Writer) error {
	pub, key, err := ed25519.GenerateKey(nil)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("%s already exists, and a signing key is never written over: it may be the only copy of the key releases are signed with", path)
	}
	if err != nil {
		return err
	}
	_, err = io.WriteString(f, selfupdate.EncodeSigningKey(key))
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(path)
		return err
	}

	fmt.Fprintln(stdout, selfupdate.EncodePublicKey(pub))
	fmt.Fprintf(stderr, "release: the private key is in %s, readable only by you. Store its contents as the\n", path)
	fmt.Fprintf(stderr, "release: repository secret %s, keep a copy somewhere offline, and delete the file.\n", signingKeyEnv)
	fmt.Fprintf(stderr, "release: the public key, printed above, goes in releaseKey in internal/selfupdate/releasekey.go.\n")
	return nil
}

// signing is what -sign works from.
type signing struct {
	version string
	out     string    // where the build left the release
	base    string    // where the site serves it, for the manifest's URLs
	notes   string    // a file of release notes for the manifest, or ""
	key     string    // FLOCKDECK_SIGNING_KEY's value
	anyKey  bool      // -test-key: sign with a key the updater does not trust
	now     time.Time // the manifest's date
}

// runSign signs the release the build left in s.out and writes what the site
// serves besides the archives: checksums.txt.sig; the release's manifest.json
// and manifest.json.sig, which go up under its version and never change; and
// latest.json, which names it, and which the publish script puts up only for
// a full release.
//
// It refuses a key the updater could not check, a release that does not match
// its own checksums.txt, and one missing a platform, and it reads what it
// wrote back through the updater's own checks before leaving it, so that
// nothing it writes is something every installed copy would refuse.
func runSign(s signing) error {
	if !selfupdate.Parseable(s.version) {
		return fmt.Errorf("-sign needs -version with the release's tag, such as v1.4.0, not %q", s.version)
	}
	if strings.TrimSpace(s.key) == "" {
		return fmt.Errorf("%s is not set: it holds the release signing key, which Terraform writes into the repository's secrets, or one `go run ./cmd/release -keygen <file>` wrote", signingKeyEnv)
	}
	key, err := selfupdate.ParseSigningKey(s.key)
	if err != nil {
		return fmt.Errorf("%s is %w", signingKeyEnv, err)
	}
	pub := key.Public().(ed25519.PublicKey)
	if !s.anyKey {
		compiled, ok := selfupdate.ReleaseKey()
		switch {
		case !ok:
			return errors.New("internal/selfupdate/releasekey.go still holds the placeholder, so nothing built from here could check a signature: put the release key's public half in releaseKey first, as `terraform output -raw flockdeck_release_public_key` prints it")
		case !compiled.Equal(pub):
			return fmt.Errorf("%s is not the key whose public half is in internal/selfupdate/releasekey.go, so the updater would refuse everything signed with it", signingKeyEnv)
		}
	}

	sums, err := os.ReadFile(filepath.Join(s.out, "checksums.txt"))
	if err != nil {
		return fmt.Errorf("no release to sign in %s: %w", s.out, err)
	}
	files, err := releasedFiles(s.out, s.version, s.base, sums)
	if err != nil {
		return err
	}
	sumsSig := selfupdate.Sign(key, sums)
	files = append(files,
		manifestFile(s, "checksums.txt", sums),
		manifestFile(s, "checksums.txt.sig", sumsSig))

	var notes []byte
	if s.notes != "" {
		if notes, err = os.ReadFile(s.notes); err != nil {
			return err
		}
	}
	m := selfupdate.Manifest{
		Version:  s.version,
		Date:     s.now.UTC().Truncate(time.Second),
		NotesURL: "https://github.com/" + selfupdate.Repo + "/releases/tag/" + s.version,
		Notes:    strings.TrimSpace(string(notes)),
		Files:    files,
	}
	manifest, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	manifest = append(manifest, '\n')
	manifestSig := selfupdate.Sign(key, manifest)
	if _, err := selfupdate.CheckManifest(pub, manifest, manifestSig); err != nil {
		return fmt.Errorf("the updater would refuse the manifest.json written: %w", err)
	}
	pointer, err := json.Marshal(selfupdate.Pointer{Version: s.version})
	if err != nil {
		return err
	}
	pointer = append(pointer, '\n')
	if v, err := selfupdate.CheckPointer(pointer); err != nil || v != s.version {
		return fmt.Errorf("the updater would not read %s from the latest.json written: %v", s.version, err)
	}

	for name, data := range map[string][]byte{
		"checksums.txt.sig": sumsSig,
		"manifest.json":     manifest,
		"manifest.json.sig": manifestSig,
		"latest.json":       pointer,
	} {
		if err := os.WriteFile(filepath.Join(s.out, name), data, 0o644); err != nil {
			return err
		}
	}
	fmt.Printf("signed %s: checksums.txt.sig, manifest.json, manifest.json.sig and latest.json are in %s\n", s.version, s.out)
	return nil
}

// releasedFiles is the manifest's entry for every archive checksums.txt lists,
// each read back and checked against its line. Every platform has to be
// there, and nothing of another version: the directory is published as it
// stands, and a stale or partial one is refused here rather than served.
func releasedFiles(out, version, base string, sums []byte) ([]selfupdate.ManifestFile, error) {
	listed := map[string]string{}
	for _, line := range strings.Split(string(sums), "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		if len(f) != 2 || !strings.HasPrefix(f[1], binary+"_"+version+"_") {
			return nil, fmt.Errorf("checksums.txt in %s lists %q, which is not an archive of %s", out, line, version)
		}
		listed[f[1]] = f[0]
	}
	for _, p := range platforms {
		if name := fmt.Sprintf("%s_%s_%s_%s%s", binary, version, p.OS, p.Arch, archiveExt(p.OS)); listed[name] == "" {
			return nil, fmt.Errorf("checksums.txt in %s does not list %s; build the release again", out, name)
		}
	}

	names := make([]string, 0, len(listed))
	for n := range listed {
		names = append(names, n)
	}
	sort.Strings(names)
	var files []selfupdate.ManifestFile
	for _, n := range names {
		path := filepath.Join(out, n)
		got, err := sha256File(path)
		if err != nil {
			return nil, err
		}
		if got != listed[n] {
			return nil, fmt.Errorf("%s does not match checksums.txt; build the release again", n)
		}
		fi, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		files = append(files, selfupdate.ManifestFile{Name: n, URL: fileURL(base, version, n), SHA256: got, Size: fi.Size()})
	}
	return files, nil
}

// manifestFile is the manifest's entry for one small file written here.
func manifestFile(s signing, name string, data []byte) selfupdate.ManifestFile {
	return selfupdate.ManifestFile{Name: name, URL: fileURL(s.base, s.version, name), SHA256: sha256Bytes(data), Size: int64(len(data))}
}

func sha256Bytes(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// fileURL is where the site serves one file of a release.
func fileURL(base, version, name string) string {
	return strings.TrimSuffix(base, "/") + "/" + version + "/" + name
}
