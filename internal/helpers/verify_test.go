package helpers

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/selfupdate"
)

func (f *fixture) setStatus(path string, code int) {
	f.site.mu.Lock()
	f.site.status[path] = code
	f.site.mu.Unlock()
}

func (f *fixture) setRedirect(path, to string) {
	f.site.mu.Lock()
	f.site.redirects[path] = to
	f.site.mu.Unlock()
}

func asErr[T error](err error) (T, bool) {
	var t T
	ok := errors.As(err, &t)
	return t, ok
}

// the happy path ------------------------------------------------------------

func TestInstallFromTheCDN(t *testing.T) {
	f := newFixture(t)
	f.publish("0.4.0")
	info, err := f.install("")
	if err != nil {
		t.Fatal(err)
	}
	if !info.Signed || info.Version != "0.4.0" {
		t.Fatalf("info = %+v", info)
	}
	if v, ok := f.store.Current("lens"); !ok || v != "0.4.0" {
		t.Fatalf("current = %q, %v", v, ok)
	}
	// The latest was found through latest.json, and nothing else was asked of
	// the site beyond that release's own files.
	for _, p := range []string{"/lens/latest.json", cdnBase("0.4.0") + "manifest.json", cdnBase("0.4.0") + "manifest.json.sig",
		cdnBase("0.4.0") + "checksums.txt", cdnBase("0.4.0") + "checksums.txt.sig", f.archivePath("0.4.0")} {
		if f.site.hitCount(p) == 0 {
			t.Errorf("%s was never fetched", p)
		}
	}
	f.site.mu.Lock()
	defer f.site.mu.Unlock()
	for p := range f.site.hits {
		if !strings.HasPrefix(p, "/lens/") {
			t.Errorf("a request went to %s", p)
		}
	}
}

func TestInstallByVersionNeedsNoLatestJSON(t *testing.T) {
	f := newFixture(t)
	f.publish("0.4.0")
	f.site.remove("/lens/latest.json")
	if _, err := f.install("0.4.0"); err != nil {
		t.Fatal(err)
	}
	if f.site.hitCount("/lens/latest.json") != 0 {
		t.Fatal("latest.json was read for an explicit version")
	}
}

// signature checks ------------------------------------------------------

func TestSignatureFromWrongKeyIsRefused(t *testing.T) {
	f := newFixture(t)
	_, other, _ := ed25519.GenerateKey(nil)
	f.site.release(t, f.entry, releaseSpec{version: "0.4.0", key: other, latest: true})
	_, err := f.install("")
	if se, ok := asErr[*SignatureError](err); !ok || se.File != "manifest.json" {
		t.Fatalf("err = %v, want a SignatureError for manifest.json", err)
	}
	if n := f.site.hitCount(f.archivePath("0.4.0")); n != 0 {
		t.Fatalf("the archive was fetched %d times after a bad signature", n)
	}
	if _, ok := f.store.Current("lens"); ok {
		t.Fatal("installed after a bad signature")
	}
}

func TestBadSignatureCannotBeOverridden(t *testing.T) {
	f := newFixture(t)
	_, other, _ := ed25519.GenerateKey(nil)
	f.site.release(t, f.entry, releaseSpec{version: "0.4.0", key: other, latest: true})
	_, err := f.in.Install(t.Context(), "lens", "", true)
	if _, ok := asErr[*SignatureError](err); !ok {
		t.Fatalf("allowUnsigned let a bad signature through: %v", err)
	}
	if _, ok := f.store.Current("lens"); ok {
		t.Fatal("installed")
	}
}

func TestManifestSignatureFailures(t *testing.T) {
	cases := []struct {
		name string
		sig  func(manifest []byte, key ed25519.PrivateKey) []byte
	}{
		{"truncated base64", func(m []byte, k ed25519.PrivateKey) []byte { g := selfupdate.Sign(k, m); return g[:len(g)/2] }},
		{"not base64", func([]byte, ed25519.PrivateKey) []byte { return []byte("!!!!not base64!!!!\n") }},
		{"empty", func([]byte, ed25519.PrivateKey) []byte { return []byte{} }},
		{"whitespace only", func([]byte, ed25519.PrivateKey) []byte { return []byte("  \n") }},
		{"signature over different bytes", func(m []byte, k ed25519.PrivateKey) []byte { return selfupdate.Sign(k, append([]byte(" "), m...)) }},
		{"right length of zeros", func([]byte, ed25519.PrivateKey) []byte {
			return []byte(base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize)) + "\n")
		}},
		{"a signature of another file", func(m []byte, k ed25519.PrivateKey) []byte { return selfupdate.Sign(k, []byte("checksums")) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			_, manifest := f.site.release(t, f.entry, releaseSpec{version: "0.4.0", key: f.key, latest: true})
			sig := c.sig(manifest, f.key)
			if sig == nil {
				sig = []byte{}
			}
			f.site.release(t, f.entry, releaseSpec{version: "0.4.0", sigOverride: sig, key: f.key, latest: true})
			for _, allow := range []bool{false, true} {
				_, err := f.in.Install(t.Context(), "lens", "", allow)
				if _, ok := asErr[*SignatureError](err); !ok {
					t.Fatalf("allowUnsigned=%v: err = %v, want a SignatureError", allow, err)
				}
			}
			if _, ok := f.store.Current("lens"); ok {
				t.Fatal("installed")
			}
		})
	}
}

func TestManifestSignedOverOtherBytesThanServed(t *testing.T) {
	// The signature is good for a manifest that names the right archive hash;
	// what is served is a manifest that names another. The signature is checked
	// over exactly the bytes served.
	f := newFixture(t)
	_, good := f.site.release(t, f.entry, releaseSpec{version: "0.4.0", key: f.key, latest: true})
	sig := selfupdate.Sign(f.key, good)
	var m selfupdate.Manifest
	if err := json.Unmarshal(good, &m); err != nil {
		t.Fatal(err)
	}
	m.Files[0].SHA256 = strings.Repeat("ab", 32)
	evil, _ := json.Marshal(m)
	f.site.release(t, f.entry, releaseSpec{version: "0.4.0", manifestBytes: evil, sigOverride: sig, latest: true})
	if _, err := f.install(""); err == nil {
		t.Fatal("a manifest other than the signed one was accepted")
	} else if _, ok := asErr[*SignatureError](err); !ok {
		t.Fatalf("err = %v", err)
	}
}

func TestNoTrustedKeysRefusesEverything(t *testing.T) {
	f := newFixture(t)
	f.publish("0.4.0")
	restore := selfupdate.TrustKeysForTest(nil, nil)
	defer restore()
	_, err := f.in.Install(t.Context(), "lens", "", true)
	if _, ok := asErr[*SignatureError](err); !ok {
		t.Fatalf("err = %v, want a SignatureError with no trusted keys", err)
	}
}

func TestStandbyKeyIsAccepted(t *testing.T) {
	f := newFixture(t)
	standbyPub, standbyPriv, _ := ed25519.GenerateKey(nil)
	primaryPub, _, _ := ed25519.GenerateKey(nil)
	restore := selfupdate.TrustKeysForTest(primaryPub, standbyPub)
	defer restore()
	f.site.release(t, f.entry, releaseSpec{version: "0.4.0", key: standbyPriv, latest: true})
	if _, err := f.install(""); err != nil {
		t.Fatal(err)
	}
}

func TestManifestSignatureServerErrorFailsClosed(t *testing.T) {
	f := newFixture(t)
	f.publish("0.4.0")
	f.setStatus(cdnBase("0.4.0")+"manifest.json.sig", 500)
	_, err := f.in.Install(t.Context(), "lens", "", true)
	if err == nil {
		t.Fatal("a 500 on the signature was treated as unsigned")
	}
	if _, ok := asErr[*UnsignedError](err); ok {
		t.Fatalf("a 500 on the signature offered the unsigned override: %v", err)
	}
}

func TestChecksumsSignatureIsRequiredWhenTheManifestIsSigned(t *testing.T) {
	f := newFixture(t)
	f.site.release(t, f.entry, releaseSpec{version: "0.4.0", key: f.key, noSumsSig: true, latest: true})
	_, err := f.install("")
	if se, ok := asErr[*SignatureError](err); !ok || se.File != "checksums.txt" {
		t.Fatalf("err = %v", err)
	}
	_, other, _ := ed25519.GenerateKey(nil)
	sums := []byte(strings.Repeat("0", 64) + "  x\n")
	f.site.release(t, f.entry, releaseSpec{version: "0.4.0", key: f.key, sumsSigOverride: selfupdate.Sign(other, sums), latest: true})
	_, err = f.install("")
	if se, ok := asErr[*SignatureError](err); !ok || se.File != "checksums.txt" {
		t.Fatalf("a wrong checksums signature: %v", err)
	}
}

func TestUnsignedIsRefusedUntilOverridden(t *testing.T) {
	f := newFixture(t)
	archive, _ := f.site.release(t, f.entry, releaseSpec{version: "0.4.0", noSig: true, latest: true})
	_, err := f.install("")
	ue, ok := asErr[*UnsignedError](err)
	if !ok {
		t.Fatalf("err = %v, want an UnsignedError", err)
	}
	if ue.Plan.SHA256 != sumHex(archive) || ue.Plan.Signed {
		t.Fatalf("plan = %+v", ue.Plan)
	}
	if n := f.site.hitCount(f.archivePath("0.4.0")); n != 0 {
		t.Fatalf("the archive was fetched %d times before the override", n)
	}
	if _, ok := f.store.Current("lens"); ok {
		t.Fatal("installed without the override")
	}
	info, err := f.in.Install(t.Context(), "lens", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if info.Signed {
		t.Fatal("an unsigned install was recorded as signed")
	}
	got, _ := f.store.Info("lens")
	if got.Signed || got.SHA256 != sumHex(archive) {
		t.Fatalf("install.json = %+v", got)
	}
}

func TestOverrideAppliesToOneVersion(t *testing.T) {
	f := newFixture(t)
	f.site.release(t, f.entry, releaseSpec{version: "0.4.0", noSig: true, latest: true})
	if _, err := f.in.Install(t.Context(), "lens", "", true); err != nil {
		t.Fatal(err)
	}
	f.site.release(t, f.entry, releaseSpec{version: "0.5.0", noSig: true, latest: true})
	if _, err := f.in.Install(t.Context(), "lens", "", false); err == nil {
		t.Fatal("the update did not ask again")
	} else if _, ok := asErr[*UnsignedError](err); !ok {
		t.Fatalf("err = %v", err)
	}
}

// the manifest ---------------------------------------------------------------

func TestManifestWithoutThisPlatform(t *testing.T) {
	f := newFixture(t)
	f.site.release(t, f.entry, releaseSpec{version: "0.4.0", goos: "darwin", goarch: "arm64", key: f.key, latest: true})
	if _, err := f.install(""); !errors.Is(err, ErrNoAsset) {
		t.Fatalf("err = %v, want ErrNoAsset", err)
	}
}

func TestManifestForAnotherHelper(t *testing.T) {
	f := newFixture(t)
	other := f.entry
	other.ID, other.Name = "other", "other"
	// A signed manifest for the right version whose archive is another
	// helper's: the name this helper asks for is not in it.
	f.site.release(t, other, releaseSpec{version: "0.4.0", key: f.key, latest: true})
	if _, err := f.install(""); !errors.Is(err, ErrNoAsset) {
		t.Fatalf("err = %v, want ErrNoAsset", err)
	}
}

func TestManifestListingAnEntryTwice(t *testing.T) {
	f := newFixture(t)
	name := f.entry.ArchiveName("0.4.0", "linux", "amd64")
	f.site.release(t, f.entry, releaseSpec{version: "0.4.0", key: f.key, latest: true,
		extraFiles: []selfupdate.ManifestFile{{Name: name, URL: f.site.URL + cdnBase("0.4.0") + name, SHA256: strings.Repeat("cd", 32), Size: 10}}})
	if _, err := f.install(""); err == nil || !strings.Contains(err.Error(), "more than once") {
		t.Fatalf("err = %v", err)
	}
	// And a duplicate of some other name is just as much a refusal.
	f.site.release(t, f.entry, releaseSpec{version: "0.4.0", key: f.key, latest: true,
		extraFiles: []selfupdate.ManifestFile{{Name: "README.txt", URL: "u", SHA256: strings.Repeat("cd", 32)}, {Name: "README.txt", URL: "u", SHA256: strings.Repeat("cd", 32)}}})
	if _, err := f.install(""); err == nil || !strings.Contains(err.Error(), "more than once") {
		t.Fatalf("err = %v", err)
	}
}

func TestManifestsTheUpdaterWouldRefuseAreRefused(t *testing.T) {
	for name, mutate := range map[string]func(*selfupdate.Manifest){
		"no checksums.txt listed": func(m *selfupdate.Manifest) { m.Files = append(m.Files[:1], m.Files[2:]...) },
		"a file with a path in its name": func(m *selfupdate.Manifest) {
			m.Files = append(m.Files, selfupdate.ManifestFile{Name: "../x", URL: "u", SHA256: strings.Repeat("a", 64)})
		},
		"a file without a hash":     func(m *selfupdate.Manifest) { m.Files[0].SHA256 = "nothex" },
		"a version that is not one": func(m *selfupdate.Manifest) { m.Version = "banana" },
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			f.site.release(t, f.entry, releaseSpec{version: "0.4.0", key: f.key, latest: true, mutate: mutate})
			if _, err := f.install(""); err == nil {
				t.Fatal("accepted")
			}
		})
	}
	f := newFixture(t)
	f.site.release(t, f.entry, releaseSpec{version: "0.4.0", key: f.key, latest: true, manifestBytes: []byte("{not json")})
	if _, err := f.install(""); err == nil {
		t.Fatal("a manifest that is not JSON was accepted")
	}
}

func TestManifestFileURLMustBeTheFilesOwn(t *testing.T) {
	f := newFixture(t)
	for name, u := range map[string]string{
		"another host":    "http://127.0.0.2:1/lens/v0.4.0/" + f.entry.ArchiveName("0.4.0", "linux", "amd64"),
		"another path":    f.site.URL + "/lens/v0.4.0/other.tar.gz",
		"another version": f.site.URL + "/lens/v0.3.0/" + f.entry.ArchiveName("0.4.0", "linux", "amd64"),
		"with a query":    f.site.URL + cdnBase("0.4.0") + f.entry.ArchiveName("0.4.0", "linux", "amd64") + "?x=1",
		"https instead":   strings.Replace(f.site.URL, "http://", "https://", 1) + cdnBase("0.4.0") + f.entry.ArchiveName("0.4.0", "linux", "amd64"),
	} {
		f.site.release(t, f.entry, releaseSpec{version: "0.4.0", key: f.key, latest: true, url: u})
		if _, err := f.install(""); err == nil {
			t.Errorf("%s: accepted", name)
		} else if _, ok := asErr[*MismatchError](err); !ok {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if _, ok := f.store.Current("lens"); ok {
		t.Fatal("installed")
	}
}

func TestManifestSizeIsBoundedAndBinds(t *testing.T) {
	f := newFixture(t)
	for _, size := range []int64{-1, 0 + 0, f.entry.MaxArchive + 1} {
		if size == 0 {
			continue // zero in the spec means "the real size"
		}
		f.site.release(t, f.entry, releaseSpec{version: "0.4.0", key: f.key, latest: true, size: size})
		if _, err := f.install(""); err == nil || !strings.Contains(err.Error(), "size of") {
			t.Fatalf("size %d: err = %v", size, err)
		}
		if f.site.hitCount(f.archivePath("0.4.0")) != 0 {
			t.Fatal("the archive was fetched although its signed size is outside the limit")
		}
	}
	// A size smaller than the real archive: the signed size is the size, so the
	// body is cut off at it and the hash cannot match.
	archive, _ := f.site.release(t, f.entry, releaseSpec{version: "0.4.0", key: f.key, latest: true, size: 10})
	if len(archive) <= 10 {
		t.Fatal("test archive too small")
	}
	if _, err := f.install(""); err == nil {
		t.Fatal("a download larger than its signed size was accepted")
	}
	// A size larger than the real one: a short body is not the signed file.
	f.site.release(t, f.entry, releaseSpec{version: "0.4.0", key: f.key, latest: true, size: int64(len(archive)) + 100})
	if _, err := f.install(""); err == nil {
		t.Fatal("a download shorter than its signed size was accepted")
	}
	assertNoStaging(t, f.store, "lens")
}

// hashes ---------------------------------------------------------------------

func TestChecksumMismatchIsRefused(t *testing.T) {
	f := newFixture(t)
	f.site.release(t, f.entry, releaseSpec{version: "0.4.0", key: f.key, latest: true, sumOverride: strings.Repeat("ab", 32)})
	_, err := f.install("")
	if _, ok := asErr[*ChecksumError](err); !ok {
		t.Fatalf("err = %v, want a ChecksumError", err)
	}
	if _, ok := f.store.Current("lens"); ok {
		t.Fatal("installed a download that did not match")
	}
	assertNoStaging(t, f.store, "lens")
}

func TestChecksumsTxtMustAgreeWithTheManifest(t *testing.T) {
	f := newFixture(t)
	f.site.release(t, f.entry, releaseSpec{version: "0.4.0", key: f.key, latest: true, sumsHash: strings.Repeat("cd", 32)})
	if _, err := f.install(""); err == nil || !strings.Contains(err.Error(), "other than the manifest does") {
		t.Fatalf("err = %v", err)
	}
	if f.site.hitCount(f.archivePath("0.4.0")) != 0 {
		t.Fatal("the archive was fetched although the signed files disagree")
	}
	// A line for another file only, and a line twice.
	f.site.release(t, f.entry, releaseSpec{version: "0.4.0", key: f.key, latest: true})
	sums := []byte(strings.Repeat("ab", 32) + "  something_else.tar.gz\n")
	f.site.put(cdnBase("0.4.0")+"checksums.txt", sums)
	f.site.put(cdnBase("0.4.0")+"checksums.txt.sig", selfupdate.Sign(f.key, sums))
	if _, err := f.install(""); err == nil || !strings.Contains(err.Error(), "does not list") {
		t.Fatalf("err = %v", err)
	}
	name := f.entry.ArchiveName("0.4.0", "linux", "amd64")
	sums = []byte(strings.Repeat("ab", 32) + "  " + name + "\n" + strings.Repeat("ab", 32) + "  " + name + "\n")
	f.site.put(cdnBase("0.4.0")+"checksums.txt", sums)
	f.site.put(cdnBase("0.4.0")+"checksums.txt.sig", selfupdate.Sign(f.key, sums))
	if _, err := f.install(""); err == nil || !strings.Contains(err.Error(), "more than once") {
		t.Fatalf("err = %v", err)
	}
}

// limits ----------------------------------------------------------------------

func TestOversizeFilesAreRefused(t *testing.T) {
	f := newFixture(t)
	f.publish("0.4.0")
	b := cdnBase("0.4.0")
	for path, size := range map[string]int{
		"/lens/latest.json":     maxPointer + 1,
		b + "manifest.json":     maxManifest + 1,
		b + "manifest.json.sig": maxSignature + 1,
		b + "checksums.txt":     maxChecksums + 1,
		b + "checksums.txt.sig": maxSignature + 1,
	} {
		f.publish("0.4.0")
		f.site.put(path, make([]byte, size))
		_, err := f.install("")
		if err == nil || !strings.Contains(err.Error(), "larger") {
			t.Errorf("%s: err = %v", path, err)
		}
		if _, ok := asErr[*UnsignedError](err); ok {
			t.Errorf("%s: an oversize signature was treated as no signature", path)
		}
	}
}

func TestArchiveThatKeepsGoingPastItsSignedSize(t *testing.T) {
	f := newFixture(t)
	// A body longer than the signed size, sent chunked so there is no
	// Content-Length to give it away: only the cap on what is read can stop it.
	big := make([]byte, 4096)
	f.site.release(t, f.entry, releaseSpec{version: "0.4.0", key: f.key, latest: true, archive: big, size: 1024})
	f.site.mu.Lock()
	f.site.chunked[f.archivePath("0.4.0")] = true
	f.site.mu.Unlock()
	if _, err := f.install(""); err == nil {
		t.Fatal("installed an archive past its signed size")
	}
	assertNoStaging(t, f.store, "lens")
}

func TestStalledDownload(t *testing.T) {
	f := newFixture(t, func(o *Options) { o.Stall = 150 * time.Millisecond })
	f.publish("0.4.0")
	f.site.mu.Lock()
	f.site.hang[f.archivePath("0.4.0")] = true
	f.site.mu.Unlock()
	start := time.Now()
	_, err := f.install("")
	if err == nil || !strings.Contains(err.Error(), "stopped sending") {
		t.Fatalf("err = %v", err)
	}
	if time.Since(start) > 10*time.Second {
		t.Fatal("the stall was not noticed")
	}
}

func TestNothingPublished(t *testing.T) {
	f := newFixture(t)
	if _, err := f.install(""); err == nil {
		t.Fatal("installed with nothing published")
	}
	if _, err := f.install("0.4.0"); err == nil {
		t.Fatal("installed a version that is not published")
	}
}

// host and redirects ----------------------------------------------------------

func TestSourceAllowURL(t *testing.T) {
	allow := SourceAllowURL("https://dl.flockdeck.ai/lens")
	good := []string{
		"https://dl.flockdeck.ai/lens/latest.json",
		"https://DL.Flockdeck.AI/lens/v0.1.0/x",
		"https://dl.flockdeck.ai:443/lens/x",
		"https://dl.flockdeck.ai/anything",
	}
	bad := []string{
		"http://dl.flockdeck.ai/lens/x",
		"https://flockdeck.ai/lens/x",
		"https://evil.dl.flockdeck.ai/lens/x",
		"https://dl.flockdeck.ai.evil.example/lens/x",
		"https://xdl.flockdeck.ai/lens/x",
		"https://dl.flockdeck.ai@evil.example/lens/x",
		"https://user:pw@dl.flockdeck.ai/lens/x",
		"https://dl.flockdeck.ai:8443/lens/x",
		"https://github.com/Flockdeck/lens/releases/download/v1/x",
		"https://objects.githubusercontent.com/x",
		"https://127.0.0.1/lens/x",
		"https://localhost/lens/x",
		"ftp://dl.flockdeck.ai/lens/x",
		"file:///etc/passwd",
		"https://[::1]/x",
	}
	for _, s := range good {
		u, _ := url.Parse(s)
		if !allow(u) {
			t.Errorf("%s was refused", s)
		}
	}
	for _, s := range bad {
		u, err := url.Parse(s)
		if err != nil {
			continue
		}
		if allow(u) {
			t.Errorf("%s was allowed", s)
		}
	}
	if allow(nil) {
		t.Error("nil was allowed")
	}
	// A source that is not https allows nothing, whatever it is asked.
	for _, source := range []string{"http://dl.flockdeck.ai/lens", "", "dl.flockdeck.ai", "ftp://x/y", "://"} {
		u, _ := url.Parse("https://dl.flockdeck.ai/lens/x")
		if SourceAllowURL(source)(u) {
			t.Errorf("source %q allows a request", source)
		}
	}
}

func TestTheCatalogueSourceIsTheCDN(t *testing.T) {
	if lens.Source != "https://dl.flockdeck.ai/lens" {
		t.Fatalf("lens.Source = %q", lens.Source)
	}
	u, _ := url.Parse(lens.Source + "/latest.json")
	if !SourceAllowURL(lens.Source)(u) {
		t.Fatal("the entry's own source is refused by its own rule")
	}
}

func TestDefaultRuleAppliesToTheFirstRequest(t *testing.T) {
	f := newFixture(t, func(o *Options) { o.AllowURL = nil })
	f.publish("0.4.0")
	_, err := f.install("")
	if err == nil || !strings.Contains(err.Error(), "only fetched from its own source over https") {
		t.Fatalf("err = %v", err)
	}
	f.site.mu.Lock()
	n := len(f.site.hits)
	f.site.mu.Unlock()
	if n != 0 {
		t.Fatalf("the fake site was reached %d times", n)
	}
}

func TestNoRedirectIsFollowed(t *testing.T) {
	evil := newSite(t)
	evil.put("/payload", []byte("evil"))
	f := newFixture(t)
	f.publish("0.4.0")
	for _, path := range []string{"/lens/latest.json", cdnBase("0.4.0") + "manifest.json", cdnBase("0.4.0") + "manifest.json.sig",
		cdnBase("0.4.0") + "checksums.txt", cdnBase("0.4.0") + "checksums.txt.sig", f.archivePath("0.4.0")} {
		f.publish("0.4.0")
		for name, to := range map[string]string{"another host": evil.URL + "/payload", "the same host": f.site.URL + "/lens/elsewhere"} {
			f.setRedirect(path, to)
			_, err := f.install("")
			if err == nil || !strings.Contains(err.Error(), "refused a redirect") {
				t.Errorf("%s (%s): err = %v", path, name, err)
			}
			f.site.mu.Lock()
			delete(f.site.redirects, path)
			f.site.mu.Unlock()
		}
	}
	if evil.hitCount("/payload") != 0 {
		t.Fatal("a redirect was followed")
	}
	if _, ok := f.store.Current("lens"); ok {
		t.Fatal("installed through a redirect")
	}
}

func TestDownloadsCarryNoCredentials(t *testing.T) {
	f := newFixture(t)
	f.publish("0.4.0")
	t.Setenv("GITHUB_TOKEN", "ghp_secret")
	var sawAuth bool
	f.in.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			sawAuth = true
		}
		return http.DefaultTransport.RoundTrip(r)
	})
	if _, err := f.install(""); err != nil {
		t.Fatal(err)
	}
	if sawAuth {
		t.Fatal("a request carried credentials")
	}
}

// latest.json is not signed ----------------------------------------------------

func TestLatestJSONThatNamesAnotherReleaseThanItsManifestSays(t *testing.T) {
	f := newFixture(t)
	// A genuinely signed manifest for 0.3.0, served where 0.5.0's belongs.
	_, m3 := f.site.release(t, f.entry, releaseSpec{version: "0.3.0", key: f.key})
	f.site.put(cdnBase("0.5.0")+"manifest.json", m3)
	f.site.put(cdnBase("0.5.0")+"manifest.json.sig", selfupdate.Sign(f.key, m3))
	f.site.put(cdnBase("0.5.0")+"checksums.txt", []byte("x"))
	f.site.put("/lens/latest.json", []byte(`{"version":"v0.5.0"}`))
	for _, version := range []string{"", "0.5.0"} {
		_, err := f.install(version)
		if _, ok := asErr[*MismatchError](err); !ok || !strings.Contains(err.Error(), "is for v0.3.0") {
			t.Fatalf("install(%q): err = %v", version, err)
		}
	}
	if _, ok := f.store.Current("lens"); ok {
		t.Fatal("installed")
	}
}

func TestLatestJSONThatLies(t *testing.T) {
	cases := map[string]string{
		"not json":              `{`,
		"no version":            `{}`,
		"a path":                `{"version":"v../../x"}`,
		"a traversal":           `{"version":"v1.0.0/../../x"}`,
		"no v":                  `{"version":"0.4.0"}`,
		"not a version":         `{"version":"vbanana"}`,
		"a pre-release":         `{"version":"v0.4.0-rc.1"}`,
		"a number":              `{"version":4}`,
		"an unknown release":    `{"version":"v9.9.9"}`,
		"a query":               `{"version":"v0.4.0?x=1"}`,
		"a long version":        `{"version":"v` + strings.Repeat("1", 100) + `"}`,
		"a release that is not": `{"version":"v0.4.1"}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			f.publish("0.4.0")
			f.site.put("/lens/latest.json", []byte(body))
			if _, err := f.install(""); err == nil {
				t.Fatal("accepted")
			}
			if _, ok := f.store.Current("lens"); ok {
				t.Fatal("installed")
			}
			f.site.mu.Lock()
			defer f.site.mu.Unlock()
			for p := range f.site.hits {
				if !strings.HasPrefix(p, "/lens/") || strings.Contains(p, "..") {
					t.Errorf("a request went to %s", p)
				}
			}
		})
	}
}

func TestLatestJSONPointingBackIsReplayProtected(t *testing.T) {
	f := newFixture(t)
	f.publish("0.5.0")
	f.publish("0.3.0")
	if _, err := f.install("0.5.0"); err != nil {
		t.Fatal(err)
	}
	// latest.json is made to name an older release whose manifest is genuinely
	// signed: what is served is real, and old.
	f.site.put("/lens/latest.json", []byte(`{"version":"v0.3.0"}`))
	_, err := f.install("")
	if !errors.Is(err, ErrDowngrade) {
		t.Fatalf("err = %v, want ErrDowngrade", err)
	}
	if v, _ := f.store.Current("lens"); v != "0.5.0" {
		t.Fatalf("current = %q", v)
	}
}

func TestSignedReleaseBelowMinimumIsRefused(t *testing.T) {
	f := newFixture(t)
	f.publish("0.1.0")
	for _, version := range []string{"", "0.1.0"} {
		if _, err := f.install(version); !errors.Is(err, ErrBelowMinimum) {
			t.Fatalf("install(%q): err = %v, want ErrBelowMinimum", version, err)
		}
	}
}

func TestOlderSignedManifestReplay(t *testing.T) {
	f := newFixture(t)
	f.publish("0.5.0")
	f.publish("0.4.0")
	if _, err := f.install("0.5.0"); err != nil {
		t.Fatal(err)
	}
	// The older manifest, signed, is served as the latest.
	f.site.put("/lens/latest.json", []byte(`{"version":"v0.4.0"}`))
	if _, err := f.install(""); !errors.Is(err, ErrDowngrade) {
		t.Fatalf("an older signed release as the latest: %v", err)
	}
	// And an update check does not offer it.
	if v, err := f.in.CheckUpdate(t.Context(), "lens"); err != nil || v != "" {
		t.Fatalf("CheckUpdate = %q, %v", v, err)
	}
	// A named older version is the person's choice, once it is above the minimum.
	if _, err := f.install("0.4.0"); err != nil {
		t.Fatalf("a named downgrade: %v", err)
	}
}

// date ---------------------------------------------------------------------------

func TestManifestDateRules(t *testing.T) {
	f := newFixture(t)
	f.site.release(t, f.entry, releaseSpec{version: "0.4.0", key: f.key, latest: true, mutate: func(m *selfupdate.Manifest) { m.Date = time.Time{} }})
	if _, err := f.install(""); err == nil || !strings.Contains(err.Error(), "has no date") {
		t.Fatalf("no date: %v", err)
	}
	f.site.release(t, f.entry, releaseSpec{version: "0.4.0", key: f.key, latest: true, date: time.Now().Add(72 * time.Hour)})
	if _, err := f.install(""); err == nil || !strings.Contains(err.Error(), "ahead of this machine's clock") {
		t.Fatalf("future date: %v", err)
	}
}

// The manifest's date is not compared with the installed version's: a hotfix
// can be dated after a release that is numbered above it and was made first,
// and refusing the later release for it would be a false refusal that protects
// nothing (the version in the same signed manifest already carries replay
// protection).
func TestAnUpdateDatedBeforeTheInstalledVersionIsAccepted(t *testing.T) {
	f := newFixture(t)
	now := time.Now()
	f.site.release(t, f.entry, releaseSpec{version: "0.4.1", key: f.key, latest: true, date: now.Add(-time.Hour)})
	if _, err := f.install(""); err != nil {
		t.Fatal(err)
	}
	f.site.release(t, f.entry, releaseSpec{version: "0.5.0", key: f.key, latest: true, date: now.Add(-48 * time.Hour)})
	if _, err := f.install(""); err != nil {
		t.Fatalf("a legitimately earlier-dated 0.5.0 was refused after a later-dated hotfix: %v", err)
	}
}

// A clock that is a day or more behind makes every manifest look future-dated.
// The refusal names the clock, which is the usual cause.
func TestAClockThatIsBehindIsNamedInTheRefusal(t *testing.T) {
	f := newFixture(t, func(o *Options) { o.Now = func() time.Time { return time.Now().Add(-72 * time.Hour) } })
	f.publish("0.4.0")
	_, err := f.install("")
	if err == nil || !strings.Contains(err.Error(), "machine's clock") || !strings.Contains(err.Error(), "clock is wrong") {
		t.Fatalf("err = %v", err)
	}
}

// updates -----------------------------------------------------------------------

func TestCheckUpdateOnlyFetchesTheManifestWhenThereIsSomethingNewer(t *testing.T) {
	f := newFixture(t)
	f.publish("0.4.0")
	if _, err := f.install(""); err != nil {
		t.Fatal(err)
	}
	f.site.mu.Lock()
	f.site.hits = map[string]int{}
	f.site.mu.Unlock()
	v, err := f.in.CheckUpdate(t.Context(), "lens")
	if err != nil || v != "" {
		t.Fatalf("CheckUpdate = %q, %v", v, err)
	}
	if f.site.hitCount(cdnBase("0.4.0")+"manifest.json") != 0 {
		t.Fatal("the manifest was fetched with nothing newer")
	}
	f.publish("0.5.0")
	if v, err := f.in.CheckUpdate(t.Context(), "lens"); err != nil || v != "0.5.0" {
		t.Fatalf("CheckUpdate = %q, %v", v, err)
	}
	// A newer version whose manifest is not good is not shown as an update.
	f.site.release(t, f.entry, releaseSpec{version: "0.6.0", latest: true, key: func() ed25519.PrivateKey { _, k, _ := ed25519.GenerateKey(nil); return k }()})
	if v, err := f.in.CheckUpdate(t.Context(), "lens"); err == nil || v != "" {
		t.Fatalf("CheckUpdate = %q, %v", v, err)
	}
	// Not installed: the version that would be installed.
	g := newFixture(t)
	g.publish("0.4.0")
	if v, err := g.in.CheckUpdate(t.Context(), "lens"); err != nil || v != "0.4.0" {
		t.Fatalf("not installed: %q, %v", v, err)
	}
}

// version rules ------------------------------------------------------------------

func TestValidVersion(t *testing.T) {
	good := []string{"0.1.0", "1.2.3", "10.20.30", "1.0.0-rc.1", "1.0.0-beta"}
	bad := []string{"", "v1.0.0", "1.0", "1.0.0.0", "../1.0.0", "1.0.0/..", `1.0.0\x`, "1.0.0-", "1.0.0-..", "1.0.0-a/b",
		"1.0.0+build", "١.٠.٠", "1.0.0 ", " 1.0.0", "1.0.0\n", "1.0.0-rc..1", strings.Repeat("1", 10) + ".0.0", "1.0.0-dirty"}
	for _, s := range good {
		if !validVersion(s) {
			t.Errorf("%q was refused", s)
		}
	}
	for _, s := range bad {
		if validVersion(s) {
			t.Errorf("%q was accepted", s)
		}
	}
}

func TestCheckVersionRules(t *testing.T) {
	e := testEntry() // MinVersion 0.2.0
	cases := []struct {
		name      string
		version   string
		installed string
		explicit  bool
		want      error
		ok        bool
	}{
		{"fresh install", "0.3.0", "", false, nil, true},
		{"the minimum itself", "0.2.0", "", false, nil, true},
		{"below the minimum", "0.1.9", "", false, ErrBelowMinimum, false},
		{"below the minimum, named", "0.1.9", "", true, ErrBelowMinimum, false},
		{"below the minimum, with something installed", "0.1.0", "0.5.0", true, ErrBelowMinimum, false},
		{"pre-release of the minimum", "0.2.0-rc.1", "", false, ErrBelowMinimum, false},
		{"update upward", "0.5.0", "0.4.0", false, nil, true},
		{"same version", "0.4.0", "0.4.0", false, ErrAlreadyInstalled, false},
		{"same version, named", "0.4.0", "0.4.0", true, ErrAlreadyInstalled, false},
		{"downgrade", "0.3.0", "0.4.0", false, ErrDowngrade, false},
		{"downgrade named", "0.3.0", "0.4.0", true, nil, true},
		{"release over its candidate", "0.4.0", "0.4.0-rc.1", false, nil, true},
		{"candidate under its release", "0.4.0-rc.1", "0.4.0", false, ErrDowngrade, false},
		{"a version that is not one", "../0.4.0", "", false, nil, false},
	}
	for _, c := range cases {
		err := checkVersion(e, c.version, c.installed, c.explicit)
		if c.ok && err != nil {
			t.Errorf("%s: %v", c.name, err)
		}
		if !c.ok && err == nil {
			t.Errorf("%s: no error", c.name)
		}
		if c.want != nil && !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.want)
		}
	}
	bad := e
	bad.MinVersion = "nonsense"
	if checkVersion(bad, "9.9.9", "", false) == nil {
		t.Error("a catalogue minimum that is not a version let everything through")
	}
}

func TestUpdateOnlyOfferedUpward(t *testing.T) {
	e := testEntry()
	if !UpdateAvailable(e, "0.5.0", "0.4.0") {
		t.Error("0.5.0 is an update from 0.4.0")
	}
	for _, c := range [][2]string{{"0.4.0", "0.4.0"}, {"0.3.0", "0.4.0"}, {"", "0.4.0"}, {"0.5.0", ""}, {"x", "0.4.0"}} {
		if UpdateAvailable(e, c[0], c[1]) {
			t.Errorf("update offered from %q to %q", c[1], c[0])
		}
	}
}

func TestExplicitDowngradeIsAllowed(t *testing.T) {
	f := newFixture(t)
	f.publish("0.5.0")
	f.publish("0.3.0")
	if _, err := f.install("0.5.0"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.install("0.3.0"); err != nil {
		t.Fatalf("a named downgrade: %v", err)
	}
	if v, _ := f.store.Current("lens"); v != "0.3.0" {
		t.Fatalf("current = %q", v)
	}
}

func TestUnknownHelper(t *testing.T) {
	f := newFixture(t)
	if _, err := f.in.Install(t.Context(), "nope", "", false); err == nil {
		t.Fatal("installed a helper that is not in the catalogue")
	}
	if _, err := f.in.Plan(t.Context(), "../lens", ""); err == nil {
		t.Fatal("planned a helper with a path for a name")
	}
	if _, err := f.in.Plan(t.Context(), "lens", "../1.0.0"); err == nil {
		t.Fatal("planned a version with a path for a name")
	}
	if _, err := f.in.CheckUpdate(t.Context(), "nope"); err == nil {
		t.Fatal("checked a helper that is not in the catalogue")
	}
}

func assertNoStaging(t *testing.T, s *Store, id string) {
	t.Helper()
	entries, _ := os.ReadDir(s.appDir(id))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "staging.new-") {
			t.Errorf("staging folder %s was left behind", e.Name())
		}
	}
}

// A candidate is never the latest, even when it is genuinely signed and
// latest.json names it.
func TestLatestJSONNamingAGenuineSignedPreReleaseIsRefused(t *testing.T) {
	f := newFixture(t)
	f.site.release(t, f.entry, releaseSpec{version: "0.5.0-rc.1", key: f.key, latest: true})
	_, err := f.install("")
	if err == nil || !strings.Contains(err.Error(), "pre-release") {
		t.Fatalf("err = %v", err)
	}
	// Named by hand it is the person's choice.
	if _, err := f.install("0.5.0-rc.1"); err != nil {
		t.Fatalf("named: %v", err)
	}
}
