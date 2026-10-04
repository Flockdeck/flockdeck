package helpers

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/selfupdate"
)

// signature checks ------------------------------------------------------

func TestInstallWithGoodSignature(t *testing.T) {
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
}

func TestSignatureFromWrongKeyIsRefused(t *testing.T) {
	f := newFixture(t)
	_, other, _ := ed25519.GenerateKey(nil)
	f.site.release(t, f.entry, releaseSpec{version: "0.4.0", key: other, latest: true})
	_, err := f.install("")
	var se *SignatureError
	if !errors.As(err, &se) {
		t.Fatalf("err = %v, want a SignatureError", err)
	}
	if n := f.site.hitCount("/Flockdeck/lens/releases/download/v0.4.0/" + f.entry.ArchiveName("0.4.0", "linux", "amd64")); n != 0 {
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
	var se *SignatureError
	if !errors.As(err, &se) {
		t.Fatalf("allowUnsigned let a bad signature through: %v", err)
	}
	if _, ok := f.store.Current("lens"); ok {
		t.Fatal("installed")
	}
}

func TestSignatureFailures(t *testing.T) {
	good := func(sums []byte, key ed25519.PrivateKey) []byte { return selfupdate.Sign(key, sums) }
	cases := []struct {
		name string
		sig  func(sums []byte, key ed25519.PrivateKey) []byte
	}{
		{"truncated base64", func(s []byte, k ed25519.PrivateKey) []byte { g := good(s, k); return g[:len(g)/2] }},
		{"not base64", func([]byte, ed25519.PrivateKey) []byte { return []byte("!!!!not base64!!!!\n") }},
		{"empty", func([]byte, ed25519.PrivateKey) []byte { return []byte{} }},
		{"signature over different bytes", func(s []byte, k ed25519.PrivateKey) []byte {
			return good(append([]byte("x"), s...), k)
		}},
		{"right length of zeros", func([]byte, ed25519.PrivateKey) []byte {
			return []byte(base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize)) + "\n")
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			// Built twice: once to learn the checksums, once to sign them.
			_, sums := f.site.release(t, f.entry, releaseSpec{version: "0.4.0", noSig: true, latest: true})
			sig := c.sig(sums, f.key)
			if sig == nil {
				sig = []byte{} // an empty .sig is a file that is there, not no file
			}
			f.site.release(t, f.entry, releaseSpec{version: "0.4.0", sigOverride: sig, latest: true})
			_, err := f.in.Install(t.Context(), "lens", "", true)
			var se *SignatureError
			if !errors.As(err, &se) {
				t.Fatalf("err = %v, want a SignatureError", err)
			}
		})
	}
}

func TestNoTrustedKeysRefusesEverything(t *testing.T) {
	f := newFixture(t)
	f.publish("0.4.0")
	restore := selfupdate.TrustKeysForTest(nil, nil)
	defer restore()
	_, err := f.in.Install(t.Context(), "lens", "", true)
	var se *SignatureError
	if !errors.As(err, &se) {
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

func TestSignatureServerErrorFailsClosed(t *testing.T) {
	f := newFixture(t)
	f.publish("0.4.0")
	f.site.mu.Lock()
	f.site.status["/Flockdeck/lens/releases/latest/download/checksums.txt.sig"] = 500
	f.site.mu.Unlock()
	_, err := f.in.Install(t.Context(), "lens", "", true)
	if err == nil {
		t.Fatal("a 500 on the signature was treated as unsigned")
	}
	var ue *UnsignedError
	if errors.As(err, &ue) {
		t.Fatalf("a 500 on the signature offered the unsigned override: %v", err)
	}
}

func TestUnsignedIsRefusedUntilOverridden(t *testing.T) {
	f := newFixture(t)
	archive, _ := f.site.release(t, f.entry, releaseSpec{version: "0.4.0", noSig: true, latest: true})
	_, err := f.install("")
	var ue *UnsignedError
	if !errors.As(err, &ue) {
		t.Fatalf("err = %v, want an UnsignedError", err)
	}
	if ue.Plan.SHA256 != sumHex(archive) || ue.Plan.Signed {
		t.Fatalf("plan = %+v", ue.Plan)
	}
	if n := f.site.hitCount("/Flockdeck/lens/releases/download/v0.4.0/" + ue.Plan.Archive); n != 0 {
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
	_, err := f.in.Install(t.Context(), "lens", "", false)
	var ue *UnsignedError
	if !errors.As(err, &ue) {
		t.Fatalf("the update did not ask again: %v", err)
	}
}

// checksum checks --------------------------------------------------------

func TestChecksumMismatchIsRefused(t *testing.T) {
	f := newFixture(t)
	f.site.release(t, f.entry, releaseSpec{version: "0.4.0", key: f.key, latest: true, sumOverride: strings.Repeat("ab", 32)})
	_, err := f.install("")
	var ce *ChecksumError
	if !errors.As(err, &ce) {
		t.Fatalf("err = %v, want a ChecksumError", err)
	}
	if _, ok := f.store.Current("lens"); ok {
		t.Fatal("installed a download that did not match")
	}
	assertNoStaging(t, f.store, "lens")
}

func TestChecksumsWithoutThisPlatform(t *testing.T) {
	f := newFixture(t)
	f.site.release(t, f.entry, releaseSpec{version: "0.4.0", goos: "darwin", goarch: "arm64", key: f.key, latest: true})
	_, err := f.install("")
	if !errors.Is(err, ErrNoAsset) {
		t.Fatalf("err = %v, want ErrNoAsset", err)
	}
}

func TestChecksumsLineLookup(t *testing.T) {
	f := newFixture(t)
	other := f.entry.ArchiveName("0.4.0", "darwin", "arm64")
	extra := strings.Repeat("cd", 32) + "  " + other + "\n" + strings.Repeat("ef", 32) + "  lens_0.4.0_linux_amd64.tar.gz.sbom\n"
	f.site.release(t, f.entry, releaseSpec{version: "0.4.0", key: f.key, latest: true, extraLines: extra})
	if _, err := f.install(""); err != nil {
		t.Fatalf("other platforms' lines got in the way: %v", err)
	}
}

func TestChecksumsListingTwoVersionsIsRefused(t *testing.T) {
	f := newFixture(t)
	extra := strings.Repeat("cd", 32) + "  " + f.entry.ArchiveName("0.3.0", "linux", "amd64") + "\n"
	f.site.release(t, f.entry, releaseSpec{version: "0.4.0", key: f.key, latest: true, extraLines: extra})
	if _, err := f.install(""); err == nil || !strings.Contains(err.Error(), "more than one") {
		t.Fatalf("err = %v", err)
	}
}

func TestChecksumLineWithoutAHash(t *testing.T) {
	f := newFixture(t)
	f.site.release(t, f.entry, releaseSpec{version: "0.4.0", key: f.key, latest: true, sumOverride: "nothex"})
	if _, err := f.install(""); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("err = %v", err)
	}
}

func TestOversizeChecksumsAndSignature(t *testing.T) {
	f := newFixture(t)
	f.publish("0.4.0")
	latest := "/Flockdeck/lens/releases/latest/download/"
	f.site.put(latest+"checksums.txt", make([]byte, maxChecksums+1))
	if _, err := f.install(""); err == nil || !strings.Contains(err.Error(), "larger") {
		t.Fatalf("an oversize checksums.txt: err = %v", err)
	}
	f.publish("0.4.0")
	f.site.put(latest+"checksums.txt.sig", make([]byte, maxSignature+1))
	_, err := f.install("")
	if err == nil || !strings.Contains(err.Error(), "larger") {
		t.Fatalf("an oversize signature: err = %v", err)
	}
	var ue *UnsignedError
	if errors.As(err, &ue) {
		t.Fatal("an oversize signature was treated as no signature")
	}
}

func TestOversizeArchiveIsRefusedWithoutDownloadingIt(t *testing.T) {
	f := newFixture(t)
	archive := f.publish("0.4.0")
	_ = archive
	path := "/Flockdeck/lens/releases/download/v0.4.0/" + f.entry.ArchiveName("0.4.0", "linux", "amd64")
	f.site.mu.Lock()
	f.site.bigLength[path] = f.entry.MaxArchive + 1<<20
	f.site.mu.Unlock()
	if _, err := f.install(""); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("err = %v", err)
	}
	assertNoStaging(t, f.store, "lens")
}

func TestArchiveThatKeepsGoingPastTheCap(t *testing.T) {
	f := newFixture(t)
	// A body longer than the cap, sent chunked so there is no Content-Length to
	// give it away, and whose hash is what checksums.txt says: only the cap on
	// what is read can stop it.
	big := make([]byte, f.entry.MaxArchive+1)
	f.site.release(t, f.entry, releaseSpec{version: "0.4.0", key: f.key, latest: true, archive: big, sumOverride: sumHex(big)})
	f.site.mu.Lock()
	f.site.chunked["/Flockdeck/lens/releases/download/v0.4.0/"+f.entry.ArchiveName("0.4.0", "linux", "amd64")] = true
	f.site.mu.Unlock()
	if _, err := f.install(""); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("an archive over the cap: err = %v", err)
	}
	assertNoStaging(t, f.store, "lens")
}

func TestStalledDownload(t *testing.T) {
	f := newFixture(t, func(o *Options) { o.Stall = 150 * time.Millisecond })
	f.publish("0.4.0")
	path := "/Flockdeck/lens/releases/download/v0.4.0/" + f.entry.ArchiveName("0.4.0", "linux", "amd64")
	f.site.mu.Lock()
	f.site.hang[path] = true
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

func TestMissingChecksumsFile(t *testing.T) {
	f := newFixture(t)
	if _, err := f.install(""); err == nil {
		t.Fatal("installed with nothing published")
	}
}

// redirects --------------------------------------------------------------

func TestDefaultAllowURL(t *testing.T) {
	allowed := []string{
		"https://github.com/Flockdeck/lens/releases/download/v1.0.0/x.zip",
		"https://objects.githubusercontent.com/github-production-release-asset/abc",
		"https://release-assets.githubusercontent.com/x",
		"https://GitHub.com/x",
		"https://github.com:443/x",
	}
	refused := []string{
		"http://github.com/x",
		"https://example.com/x",
		"https://github.com.evil.example/x",
		"https://evilgithub.com/x",
		"https://evilgithubusercontent.com/x",
		"https://githubusercontent.com/x",
		"https://user:pw@github.com/x",
		"https://github.com:8443/x",
		"https://127.0.0.1/x",
		"http://127.0.0.1:1234/x",
		"ftp://github.com/x",
		"file:///etc/passwd",
		"https://github.com@evil.example/x",
		"https://[::1]/x",
		"https://localhost/x",
	}
	for _, s := range allowed {
		u, _ := url.Parse(s)
		if !DefaultAllowURL(u) {
			t.Errorf("%s was refused", s)
		}
	}
	for _, s := range refused {
		u, err := url.Parse(s)
		if err != nil {
			continue
		}
		if DefaultAllowURL(u) {
			t.Errorf("%s was allowed", s)
		}
	}
	if DefaultAllowURL(nil) {
		t.Error("nil was allowed")
	}
}

func TestDefaultAllowlistAppliesToTheFirstRequest(t *testing.T) {
	f := newFixture(t, func(o *Options) { o.AllowURL = nil })
	f.publish("0.4.0")
	_, err := f.install("")
	if err == nil || !strings.Contains(err.Error(), "only downloaded from github.com") {
		t.Fatalf("err = %v", err)
	}
	if n := len(f.site.hits); n != 0 {
		t.Fatalf("the fake site was reached %d times", n)
	}
}

func TestRedirectToAnotherHostIsRefused(t *testing.T) {
	evil := newSite(t)
	f := newFixture(t, func(o *Options) {
		// Only the first site is allowed, as only github.com is in production.
		o.AllowURL = func(u *url.URL) bool { return u.Host == strings.TrimPrefix(o.Base, "http://") }
	})
	f.publish("0.4.0")
	path := "/Flockdeck/lens/releases/download/v0.4.0/" + f.entry.ArchiveName("0.4.0", "linux", "amd64")
	f.site.mu.Lock()
	f.site.redirects[path] = evil.URL + "/payload"
	f.site.mu.Unlock()
	evil.put("/payload", []byte("evil"))
	_, err := f.install("")
	if err == nil || !strings.Contains(err.Error(), "refused a redirect") {
		t.Fatalf("err = %v", err)
	}
	if evil.hitCount("/payload") != 0 {
		t.Fatal("the redirect was followed")
	}
}

func TestRedirectToAnAllowedHostIsFollowed(t *testing.T) {
	cdn := newSite(t)
	f := newFixture(t)
	archive := f.publish("0.4.0")
	path := "/Flockdeck/lens/releases/download/v0.4.0/" + f.entry.ArchiveName("0.4.0", "linux", "amd64")
	cdn.put("/asset", archive)
	f.site.mu.Lock()
	f.site.redirects[path] = cdn.URL + "/asset"
	f.site.mu.Unlock()
	if _, err := f.install(""); err != nil {
		t.Fatal(err)
	}
	if cdn.hitCount("/asset") != 1 {
		t.Fatal("the redirect was not followed")
	}
}

func TestRedirectLoopStops(t *testing.T) {
	f := newFixture(t)
	f.publish("0.4.0")
	path := "/Flockdeck/lens/releases/download/v0.4.0/" + f.entry.ArchiveName("0.4.0", "linux", "amd64")
	f.site.mu.Lock()
	f.site.redirects[path] = f.site.URL + path
	f.site.mu.Unlock()
	_, err := f.install("")
	if err == nil || !strings.Contains(err.Error(), "redirects") {
		t.Fatalf("err = %v", err)
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

// version rules ----------------------------------------------------------

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

func TestLatestOlderThanInstalledIsRefused(t *testing.T) {
	f := newFixture(t)
	f.publish("0.5.0")
	if _, err := f.install(""); err != nil {
		t.Fatal(err)
	}
	// An older signed release served as the latest: a replay.
	f.site.release(t, f.entry, releaseSpec{version: "0.3.0", key: f.key, latest: true})
	_, err := f.install("")
	if !errors.Is(err, ErrDowngrade) {
		t.Fatalf("err = %v, want ErrDowngrade", err)
	}
	if v, _ := f.store.Current("lens"); v != "0.5.0" {
		t.Fatalf("current = %q", v)
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

func TestSignedReleaseBelowMinimumIsRefused(t *testing.T) {
	f := newFixture(t)
	f.publish("0.1.0")
	for _, version := range []string{"", "0.1.0"} {
		if _, err := f.install(version); !errors.Is(err, ErrBelowMinimum) {
			t.Fatalf("install(%q): err = %v, want ErrBelowMinimum", version, err)
		}
	}
}

func TestChecksumsForAnotherVersionAtThisTag(t *testing.T) {
	f := newFixture(t)
	// A genuinely signed 0.3.0 checksums.txt, put where v0.5.0's belongs.
	_, sums := f.site.release(t, f.entry, releaseSpec{version: "0.3.0", key: f.key})
	base := "/Flockdeck/lens/releases/download/v0.5.0/"
	f.site.put(base+"checksums.txt", sums)
	f.site.put(base+"checksums.txt.sig", selfupdate.Sign(f.key, sums))
	if _, err := f.install("0.5.0"); err == nil || !strings.Contains(err.Error(), "not for 0.5.0") {
		t.Fatalf("err = %v", err)
	}
}

func TestVersionFromAFileNameCannotEscape(t *testing.T) {
	f := newFixture(t)
	// The signed file names a "version" with a path in it.
	sums := []byte(strings.Repeat("ab", 32) + "  lens_..%2f..%2fx_linux_amd64.tar.gz\n" +
		strings.Repeat("ab", 32) + "  lens_../x_linux_amd64.tar.gz\n")
	base := "/Flockdeck/lens/releases/latest/download/"
	f.site.put(base+"checksums.txt", sums)
	f.site.put(base+"checksums.txt.sig", selfupdate.Sign(f.key, sums))
	_, err := f.install("")
	if !errors.Is(err, ErrNoAsset) {
		t.Fatalf("err = %v, want ErrNoAsset", err)
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
