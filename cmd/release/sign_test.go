package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/selfupdate"
)

// The private key goes to its file and nowhere else, readable by its owner
// alone; the one thing printed is the public key, which is exactly the file's
// key's public half. A key already there is never written over.
func TestKeygenPrintsOnlyThePublicKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "release.key")
	var stdout, stderr bytes.Buffer
	if err := runKeygen(path, false, &stdout, &stderr); err != nil {
		t.Fatalf("runKeygen: %v", err)
	}
	file, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	key, err := selfupdate.ParseSigningKey(string(file))
	if err != nil {
		t.Fatalf("the key file does not read back: %v", err)
	}
	want := selfupdate.EncodePublicKey(key.Public().(ed25519.PublicKey)) + "\n"
	if stdout.String() != want {
		t.Errorf("stdout = %q, want the public key alone, %q", stdout.String(), want)
	}
	secret := strings.TrimSpace(string(file))
	if strings.Contains(stdout.String()+stderr.String(), secret) {
		t.Error("the private key was printed")
	}
	if !strings.Contains(stderr.String(), signingKeyEnv) || !strings.Contains(stderr.String(), "releasekey.go") {
		t.Errorf("stderr = %q, want it to say where each half goes", stderr.String())
	}
	if runtime.GOOS != "windows" {
		if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
			t.Errorf("the key file is mode %v, want 0600", fi.Mode().Perm())
		}
	}

	if err := runKeygen(path, false, &stdout, &stderr); err == nil {
		t.Fatal("runKeygen wrote over an existing key")
	}
	if again, _ := os.ReadFile(path); !bytes.Equal(again, file) {
		t.Error("the existing key was changed")
	}
}

// -keygen -standby writes and prints exactly as the primary does, but its
// stderr hints say to keep the file offline only, never as a repository
// secret, and name releaseKeyStandby instead of releaseKey.
func TestKeygenStandbyPrintsOnlyThePublicKeyAndHintsAtOfflineStorage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "standby.key")
	var stdout, stderr bytes.Buffer
	if err := runKeygen(path, true, &stdout, &stderr); err != nil {
		t.Fatalf("runKeygen: %v", err)
	}
	file, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	key, err := selfupdate.ParseSigningKey(string(file))
	if err != nil {
		t.Fatalf("the key file does not read back: %v", err)
	}
	want := selfupdate.EncodePublicKey(key.Public().(ed25519.PublicKey)) + "\n"
	if stdout.String() != want {
		t.Errorf("stdout = %q, want the public key alone, %q", stdout.String(), want)
	}
	secret := strings.TrimSpace(string(file))
	if strings.Contains(stdout.String()+stderr.String(), secret) {
		t.Error("the private key was printed")
	}
	if !strings.Contains(stderr.String(), "releaseKeyStandby") || strings.Contains(stderr.String(), signingKeyEnv) {
		t.Errorf("stderr = %q, want it to name releaseKeyStandby and never %s", stderr.String(), signingKeyEnv)
	}
	if !strings.Contains(stderr.String(), "offline") {
		t.Errorf("stderr = %q, want it to say to keep the file offline", stderr.String())
	}
	if runtime.GOOS != "windows" {
		if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
			t.Errorf("the key file is mode %v, want 0600", fi.Mode().Perm())
		}
	}

	if err := runKeygen(path, true, &stdout, &stderr); err == nil {
		t.Fatal("runKeygen wrote over an existing key")
	}
	if again, _ := os.ReadFile(path); !bytes.Equal(again, file) {
		t.Error("the existing key was changed")
	}
}

// fakeRelease writes a release of every platform to a directory, as the
// build leaves it, and returns the directory.
func fakeRelease(t *testing.T, version string) string {
	t.Helper()
	out := t.TempDir()
	sums := map[string]string{}
	for _, p := range platforms {
		name := fmt.Sprintf("%s_%s_%s_%s%s", binary, version, p.OS, p.Arch, archiveExt(p.OS))
		path := filepath.Join(out, name)
		if err := os.WriteFile(path, []byte("the program for "+p.OS+"/"+p.Arch), 0o644); err != nil {
			t.Fatal(err)
		}
		sum, err := sha256File(path)
		if err != nil {
			t.Fatal(err)
		}
		sums[name] = sum
	}
	if err := writeSums(filepath.Join(out, "checksums.txt"), sums); err != nil {
		t.Fatal(err)
	}
	return out
}

func testSigning(t *testing.T, out string) (signing, ed25519.PublicKey) {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	return signing{
		version: "v9.9.9", out: out, base: "https://dl.example.invalid/",
		key: selfupdate.EncodeSigningKey(key), anyKey: true,
		now: time.Date(2026, 9, 12, 10, 30, 0, 5, time.FixedZone("x", 3600)),
	}, pub
}

// What -sign writes is exactly what the updater accepts: a manifest signed by
// the key, listing every archive, checksums.txt and its signature with the
// site's URL, SHA-256 and size of each; a checksums.txt.sig that is the key's
// signature of checksums.txt as built; and a latest.json naming the version
// and nothing else.
func TestSignWritesWhatTheUpdaterAccepts(t *testing.T) {
	out := fakeRelease(t, "v9.9.9")
	notes := filepath.Join(t.TempDir(), "notes.md")
	if err := os.WriteFile(notes, []byte("## What changed\n\n- things\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, pub := testSigning(t, out)
	s.notes = notes
	if err := runSign(s); err != nil {
		t.Fatalf("runSign: %v", err)
	}

	read := func(name string) []byte {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(out, name))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	if err := selfupdate.Verify(pub, read("checksums.txt"), read("checksums.txt.sig")); err != nil {
		t.Errorf("checksums.txt.sig: %v", err)
	}
	m, err := selfupdate.CheckManifest([]ed25519.PublicKey{pub}, read("manifest.json"), read("manifest.json.sig"))
	if err != nil {
		t.Fatalf("the updater refuses the manifest: %v", err)
	}
	if m.Version != "v9.9.9" || m.NotesURL != "https://github.com/jmwri/flockdeck/releases/tag/v9.9.9" ||
		m.Notes != "## What changed\n\n- things" || !m.Date.Equal(time.Date(2026, 9, 12, 9, 30, 0, 0, time.UTC)) {
		t.Errorf("manifest.json = %+v", m)
	}
	if len(m.Files) != len(platforms)+2 {
		t.Errorf("manifest.json lists %d files, want every archive, checksums.txt and its signature", len(m.Files))
	}
	if got := string(read("latest.json")); got != `{"version":"v9.9.9"}`+"\n" {
		t.Errorf("latest.json = %q, want it to name v9.9.9 and nothing else", got)
	}
	for _, f := range m.Files {
		data := read(f.Name)
		if f.URL != "https://dl.example.invalid/v9.9.9/"+f.Name {
			t.Errorf("%s is at %s", f.Name, f.URL)
		}
		if f.SHA256 != sha256Bytes(data) || f.Size != int64(len(data)) {
			t.Errorf("%s is listed as %s, %d bytes; it is %s, %d", f.Name, f.SHA256, f.Size, sha256Bytes(data), len(data))
		}
	}
}

// A key the updater does not hold is refused, so nothing is ever published
// that every installed copy would reject: while the placeholder is in place
// that is any key at all.
func TestSignRefusesAKeyTheUpdaterDoesNotHold(t *testing.T) {
	s, _ := testSigning(t, fakeRelease(t, "v9.9.9"))
	s.anyKey = false
	err := runSign(s)
	if err == nil || !strings.Contains(err.Error(), "releasekey.go") {
		t.Fatalf("runSign with a stranger's key = %v, want it refused for want of the key in releasekey.go", err)
	}
	if _, err := os.Stat(filepath.Join(s.out, "manifest.json")); err == nil {
		t.Error("manifest.json was written all the same")
	}
}

// runSign accepts a key whose public half is either compiled key, primary or
// standby, so the standby can sign a release in an emergency; a key matching
// neither is refused, and the message says so.
func TestSignAcceptsEitherCompiledKeyAndRefusesAThird(t *testing.T) {
	primaryPub, primaryKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	standbyPub, standbyKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	_, otherKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	restore := selfupdate.TrustKeysForTest(primaryPub, standbyPub)
	t.Cleanup(restore)

	for _, c := range []struct {
		name string
		key  ed25519.PrivateKey
		ok   bool
	}{
		{"the primary key", primaryKey, true},
		{"the standby key", standbyKey, true},
		{"a third key", otherKey, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			out := fakeRelease(t, "v9.9.9")
			s, _ := testSigning(t, out)
			s.key = selfupdate.EncodeSigningKey(c.key)
			s.anyKey = false
			err := runSign(s)
			if c.ok && err != nil {
				t.Errorf("runSign with %s: %v, want it accepted", c.name, err)
			}
			if !c.ok {
				if err == nil || !strings.Contains(err.Error(), "releasekey.go") {
					t.Errorf("runSign with %s = %v, want it refused for matching neither compiled key", c.name, err)
				}
			}
		})
	}
}

// The key as Terraform writes it into the secret, a PKCS#8 PEM, signs just as
// the seed -keygen writes does.
func TestSignTakesTheKeyAsTerraformWritesIt(t *testing.T) {
	out := fakeRelease(t, "v9.9.9")
	s, _ := testSigning(t, out)
	pub, key, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	s.key = string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	if err := runSign(s); err != nil {
		t.Fatalf("runSign with Terraform's key: %v", err)
	}
	sums, _ := os.ReadFile(filepath.Join(out, "checksums.txt"))
	sig, _ := os.ReadFile(filepath.Join(out, "checksums.txt.sig"))
	if err := selfupdate.Verify(pub, sums, sig); err != nil {
		t.Errorf("checksums.txt.sig is not Terraform's key's signature: %v", err)
	}
}

// Without the key there is nothing to sign with, and the message says which
// secret is missing; a key that is not one is never quoted back.
func TestSignNeedsTheKey(t *testing.T) {
	s, _ := testSigning(t, fakeRelease(t, "v9.9.9"))
	s.key = ""
	if err := runSign(s); err == nil || !strings.Contains(err.Error(), signingKeyEnv+" is not set") {
		t.Errorf("runSign without a key = %v", err)
	}
	s.key = "hunter2"
	if err := runSign(s); err == nil || strings.Contains(err.Error(), "hunter2") {
		t.Errorf("runSign with a key that is not one = %v", err)
	}
}

// A directory that is not the release as built is refused rather than
// signed: an archive changed since, one missing, or one of another version.
func TestSignRefusesAReleaseThatIsNotAsBuilt(t *testing.T) {
	cases := map[string]func(out string){
		"an archive changed": func(out string) {
			os.WriteFile(filepath.Join(out, "flockdeck_v9.9.9_linux_amd64.tar.gz"), []byte("something else"), 0o644)
		},
		"an archive missing": func(out string) {
			os.Remove(filepath.Join(out, "flockdeck_v9.9.9_darwin_arm64.tar.gz"))
		},
		"a platform unlisted": func(out string) {
			sums, _ := os.ReadFile(filepath.Join(out, "checksums.txt"))
			var kept []string
			for _, l := range strings.Split(string(sums), "\n") {
				if !strings.Contains(l, "windows_arm64") {
					kept = append(kept, l)
				}
			}
			os.WriteFile(filepath.Join(out, "checksums.txt"), []byte(strings.Join(kept, "\n")), 0o644)
		},
		"another version's checksums": func(out string) {
			sums, _ := os.ReadFile(filepath.Join(out, "checksums.txt"))
			os.WriteFile(filepath.Join(out, "checksums.txt"), bytes.ReplaceAll(sums, []byte("v9.9.9"), []byte("v9.9.8")), 0o644)
		},
	}
	for name, spoil := range cases {
		t.Run(name, func(t *testing.T) {
			out := fakeRelease(t, "v9.9.9")
			spoil(out)
			s, _ := testSigning(t, out)
			if err := runSign(s); err == nil {
				t.Fatal("runSign signed it")
			}
			if _, err := os.Stat(filepath.Join(out, "manifest.json")); err == nil {
				t.Error("manifest.json was written")
			}
		})
	}
}
