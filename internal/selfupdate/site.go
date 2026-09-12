package selfupdate

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/url"
	"strings"
	"time"
)

// Site is where releases are downloaded from first: a DigitalOcean Space
// behind DigitalOcean's CDN, laid out as
//
//	/latest.json                            {"version":"v1.2.3"}: which release is the latest
//	/<version>/manifest.json(.sig)          that release's files and their SHA-256s, signed
//	/<version>/<archive>                    each release's files
//	/<version>/checksums.txt(.sig)          and their SHA-256s, signed
//	/latest/<archive without the version>   for the site's download buttons
//
// Everything under /<version>/ is written once, when the release is published,
// and never changes, so it is cached for a year. latest.json is the one file
// that moves, and it is neither signed nor purged from the CDN's edges when
// it does; it needs to be neither. All it can do is name a version, and the
// updater trusts nothing until that version's own manifest has passed the
// release key's signature and names the same version. So a stale latest.json,
// or a forged one, can only name another signed release: an older one, which
// Newer never moves to, or a pre-release, which latestFromSite refuses. The
// most it can do is hold an update back for as long as it is cached; it can
// never have anything unsigned installed, nor anything older.
//
// GitHub carries every release as well and is where the updater goes when the
// site cannot be reached, answers with something it cannot use, or fails a
// signature. That is also the only place a copy from before the site knows
// about, which is why every release is still published there.
//
// Asking here first is what keeps a shared office network from running out of
// GitHub's sixty unauthenticated API calls an hour: the site has no such limit.
const Site = "https://dl.flockdeck.ai"

// Where the updater looks. Tests point these at servers of their own.
var (
	siteURL      = Site
	githubAPIURL = "https://api.github.com"
	githubURL    = "https://github.com"
)

// logf is where the updater says why it went to GitHub instead of the site.
// The application sends the standard logger to its terminal, when it has one.
var logf = log.Printf

// Manifest is one release as the site holds it, <version>/manifest.json:
// every file of it, with the SHA-256 and size of each, and its notes. It is
// signed, in manifest.json.sig, and never changes once published. cmd/release
// writes it and the updater reads it, through this one type.
type Manifest struct {
	Version  string         `json:"version"`
	Date     time.Time      `json:"date"`
	NotesURL string         `json:"notes_url"`
	Notes    string         `json:"notes,omitempty"`
	Files    []ManifestFile `json:"files"`
}

// ManifestFile is one file of a release on the site.
type ManifestFile struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// Pointer is latest.json, the one file on the site that changes: it names the
// latest release and says nothing else about it. It is not signed; Site says
// why it need not be. cmd/release writes it and the updater reads it, through
// this one type.
type Pointer struct {
	Version string `json:"version"`
}

// The files every release carries besides its archives, and the one that
// names the latest.
const (
	sumsName     = "checksums.txt"
	manifestName = "manifest.json"
	pointerName  = "latest.json"
	sigExt       = ".sig"
)

// CheckPointer reads latest.json and returns the version it names, refusing
// anything that does not name one.
func CheckPointer(data []byte) (string, error) {
	var p Pointer
	if err := json.Unmarshal(data, &p); err != nil {
		return "", fmt.Errorf("%s does not name a release: %w", pointerName, err)
	}
	if !Parseable(p.Version) {
		return "", fmt.Errorf("%s names %q, which is not a version", pointerName, p.Version)
	}
	return p.Version, nil
}

// CheckManifest reads a release's manifest.json once its signature, the
// content of manifest.json.sig, has been checked against key, and refuses a
// manifest the updater could not act on. cmd/release reads back what it wrote
// through this, so a release is never published with a manifest the updater
// would refuse.
func CheckManifest(key ed25519.PublicKey, data, sig []byte) (*Manifest, error) {
	if err := Verify(key, data, sig); err != nil {
		return nil, signatureError{manifestName, err}
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("%s is not a manifest: %w", manifestName, err)
	}
	if !Parseable(m.Version) {
		return nil, fmt.Errorf("%s names %q, which is not a version", manifestName, m.Version)
	}
	have := map[string]bool{}
	for _, f := range m.Files {
		if f.Name == "" || f.URL == "" || strings.ContainsAny(f.Name, `/\`) {
			return nil, fmt.Errorf("%s lists a file without a plain name and a URL", manifestName)
		}
		if !isSHA256(f.SHA256) {
			return nil, fmt.Errorf("%s lists %s without a SHA-256", manifestName, f.Name)
		}
		have[f.Name] = true
	}
	for _, n := range []string{sumsName, sumsName + sigExt} {
		if !have[n] {
			return nil, fmt.Errorf("%s does not list %s", manifestName, n)
		}
	}
	return &m, nil
}

// signatureError is a signature that did not check: never a network problem,
// and so always worth a warning rather than a note.
type signatureError struct {
	file string
	err  error
}

func (e signatureError) Error() string {
	return e.file + " from " + siteHost() + " is not signed by the release key: " + e.err.Error()
}

// suspicious reports whether err means what the site served was not what was
// released, as opposed to the site being out of reach. Those are logged as
// warnings, since they are the case somebody should look into.
func suspicious(err error) bool {
	var sig signatureError
	var mismatch mismatchError
	return errors.As(err, &sig) || errors.As(err, &mismatch)
}

// mismatchError is something the site served that disagrees with what was
// signed for it: a download, or a checksums.txt, that does not match the
// manifest, or a manifest signed for another version than the one it was
// served as.
type mismatchError struct{ msg string }

func (e mismatchError) Error() string { return e.msg }

func isSHA256(s string) bool {
	_, err := hex.DecodeString(s)
	return err == nil && len(s) == 2*sha256.Size
}

// prerelease reports whether v, a version Parseable accepts, is a candidate
// such as v1.4.0-rc.1 rather than a release.
func prerelease(v string) bool {
	pv, _ := parseVersion(v)
	return pv.pre != ""
}

// siteHost is the site as a person would name it in a message.
func siteHost() string {
	if u, err := url.Parse(siteURL); err == nil && u.Host != "" {
		return u.Host
	}
	return siteURL
}

// latestFromSite reads the latest release from the site: its version from
// latest.json, and then that version's manifest, trusted only once its
// signature has checked against the compiled-in key and it names the version
// latest.json did.
//
// A pre-release is refused. Every release's manifest is signed and published,
// a candidate's as well, but a candidate is never the latest, just as GitHub's
// latest release never is one.
//
// The release it returns downloads from the site, checks checksums.txt's own
// signature before believing it, and carries the same release on GitHub for
// Stage to fall back to.
func latestFromSite(ctx context.Context) (*Release, error) {
	if trustedKey == nil {
		return nil, errNoKey
	}
	data, err := fetchSmall(ctx, siteURL+"/"+pointerName, 1<<10)
	if err != nil {
		return nil, err
	}
	version, err := CheckPointer(data)
	if err != nil {
		return nil, err
	}
	if prerelease(version) {
		return nil, fmt.Errorf("%s names %s, a pre-release, which is never the latest", pointerName, version)
	}
	at := siteURL + "/" + url.PathEscape(version) + "/" + manifestName
	data, err = fetchSmall(ctx, at, 1<<20)
	if err != nil {
		return nil, err
	}
	sig, err := fetchSmall(ctx, at+sigExt, 1<<10)
	if err != nil {
		return nil, err
	}
	m, err := CheckManifest(trustedKey, data, sig)
	if err != nil {
		return nil, err
	}
	if m.Version != version {
		return nil, mismatchError{fmt.Sprintf("the manifest %s serves as %s's is signed for %s", siteHost(), version, m.Version)}
	}

	rel := &Release{Version: m.Version, Notes: m.Notes, URL: m.NotesURL, sums: map[string]string{}}
	mirror := &Release{Version: m.Version, Notes: m.Notes, URL: m.NotesURL, sums: rel.sums}
	for _, f := range m.Files {
		rel.sums[f.Name] = strings.ToLower(f.SHA256)
		if f.Name == sumsName+sigExt {
			rel.sumsSig = f.URL
			continue // GitHub carries no signature
		}
		rel.Assets = append(rel.Assets, Asset{Name: f.Name, URL: f.URL, Size: f.Size})
		mirror.Assets = append(mirror.Assets, Asset{Name: f.Name, URL: githubDownload(m.Version, f.Name), Size: f.Size})
	}
	rel.mirror = mirror
	return rel, nil
}

// githubDownload is where GitHub serves one file of a release. It is a plain
// download, not an API call, so it is not counted against the API's limit.
func githubDownload(version, name string) string {
	return githubURL + "/" + Repo + "/releases/download/" + url.PathEscape(version) + "/" + url.PathEscape(name)
}

// fetchSmall reads a file of at most limit bytes; one any larger is refused
// rather than cut short, since a truncated file would only fail its signature.
func fetchSmall(ctx context.Context, url string, limit int64) ([]byte, error) {
	resp, err := get(ctx, url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("%s is larger than %d bytes", url, limit)
	}
	return body, nil
}

// fellBack logs why the site was not used for doing, so that an updater that
// has quietly been going to GitHub all along can be found out from its log. A
// failed signature or a download that does not match is a warning: the site
// served something that was not released.
func fellBack(doing string, err error) {
	if suspicious(err) {
		logf("flockdeck: warning: %s: %v; using GitHub instead", doing, err)
		return
	}
	logf("flockdeck: %s: %v; using GitHub instead", doing, err)
}
