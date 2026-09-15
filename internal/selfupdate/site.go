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
//	/recalled.json(.sig)                    versions withdrawn after release, signed; see Recall
//
// Everything under /<version>/ is written once, when the release is published,
// and never changes, so it is cached for a year. latest.json is the one file
// that moves, and it is neither signed nor purged from the CDN's edges when
// it does. All it can do is name a version, and the updater trusts nothing
// until that version's own manifest has passed the release key's signature
// and names the same version. So a stale latest.json, or a forged one, can
// only name another signed release: an older one, which Newer never moves to,
// or a pre-release, which latestFromSite refuses. Otherwise it can hold an
// update back for as long as it is cached, and it can never have anything
// unsigned installed, nor anything older than the copy running.
//
// It can name a release that was withdrawn, though: one signed and published,
// then taken down because something was wrong with it. That release is still
// newer than a copy that never moved to it, and its signature still checks.
// So whoever can write the site can point latest.json back at it and have it
// installed, and so can whoever can publish on GitHub, where the updater goes
// whenever the site fails. latest.json alone still cannot stop that: only
// unpublishing the release everywhere Latest reads from does (see
// updateSteps, which already discards a staged download once it is no longer
// the newest published release). recalled.json is the other half, for a copy
// that already applied the bad release before it was pulled: a signed list of
// versions known to be bad, checked by Recall, which the ordinary "is
// something newer" check (Latest, Newer) never answers on its own.
//
// GitHub carries every release as well, checksums.txt.sig with it, and is
// where the updater goes when the site cannot be reached, answers with
// something it cannot use, or fails a signature. What it downloads from there
// is still held to the release key's signature. GitHub is also the only place
// a copy from before the site knows about, which is why every release is
// still published there.
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
// content of manifest.json.sig, has been checked against keys, and refuses a
// manifest the updater could not act on. cmd/release reads back what it wrote
// through this, so a release is never published with a manifest the updater
// would refuse. A signature from any one of keys is accepted, so a release
// signed with the standby passes exactly as one signed with the primary does.
func CheckManifest(keys []ed25519.PublicKey, data, sig []byte) (*Manifest, error) {
	if err := VerifyAny(keys, data, sig); err != nil {
		return nil, signatureError{manifestName, siteHost(), err}
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
	from string // where it came from, as a person would name it
	err  error
}

func (e signatureError) Error() string {
	return e.file + " from " + e.from + " is not signed by the release key: " + e.err.Error()
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
	if len(TrustedKeys()) == 0 {
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
	return releaseFromSite(ctx, version)
}

// releaseFromSite reads one version's manifest from the site directly, rather
// than through latest.json, and builds the Release it describes the same way
// latestFromSite does: trusted only once its signature checks against the
// compiled-in key and it names the version asked for.
//
// The site's per-version files are written once, when the release is
// published, and kept forever (Site's own layout comment), so this works for
// any version still hosted -- Fetch's use of it -- as well as for the one
// latest.json currently names.
func releaseFromSite(ctx context.Context, version string) (*Release, error) {
	keys := TrustedKeys()
	if len(keys) == 0 {
		return nil, errNoKey
	}
	at := siteURL + "/" + url.PathEscape(version) + "/" + manifestName
	data, err := fetchSmall(ctx, at, 1<<20)
	if err != nil {
		return nil, err
	}
	sig, err := fetchSmall(ctx, at+sigExt, 1<<10)
	if err != nil {
		return nil, err
	}
	m, err := CheckManifest(keys, data, sig)
	if err != nil {
		return nil, err
	}
	if m.Version != version {
		return nil, mismatchError{fmt.Sprintf("the manifest %s serves as %s's is signed for %s", siteHost(), version, m.Version)}
	}
	return releaseFromManifest(m), nil
}

// releaseFromManifest builds the Release a signed manifest describes, with a
// GitHub mirror for Stage to fall back to.
func releaseFromManifest(m *Manifest) *Release {
	rel := &Release{Version: m.Version, Notes: m.Notes, URL: m.NotesURL, Published: m.Date, sums: map[string]string{}}
	mirror := &Release{Version: m.Version, Notes: m.Notes, URL: m.NotesURL, Published: m.Date, sums: rel.sums}
	for _, f := range m.Files {
		rel.sums[f.Name] = strings.ToLower(f.SHA256)
		if f.Name == sumsName+sigExt {
			rel.sumsSig = f.URL
			continue // the mirror is held to the manifest's SHA-256s instead
		}
		rel.Assets = append(rel.Assets, Asset{Name: f.Name, URL: f.URL, Size: f.Size})
		mirror.Assets = append(mirror.Assets, Asset{Name: f.Name, URL: githubDownload(m.Version, f.Name), Size: f.Size})
	}
	rel.mirror = mirror
	return rel
}

// recalledName is the site's signed list of published releases withdrawn
// because something was found wrong with them: recalled.json, published
// beside the manifest so a copy that has already installed one of them can
// find out. See Recall.
const recalledName = "recalled.json"

// Recalled is recalled.json as the site holds it.
type Recalled struct {
	Versions []RecalledVersion `json:"versions"`
}

// RecalledVersion is one release recalled.json names: what was wrong with
// it, and, when the person publishing it knows of one, which version to move
// to instead of whatever Latest currently returns.
type RecalledVersion struct {
	Version string `json:"version"`
	Reason  string `json:"reason"`
	Upgrade string `json:"upgrade,omitempty"`
}

// CheckRecalled reads recalled.json once its signature, the content of
// recalled.json.sig, has passed VerifyAny, the same way CheckManifest reads
// a manifest. Signing it matters for the same reason everything else here is
// signed: an unsigned "you are running a known-bad build" is itself a thing
// an attacker could forge to push someone toward installing something else.
func CheckRecalled(keys []ed25519.PublicKey, data, sig []byte) (*Recalled, error) {
	if err := VerifyAny(keys, data, sig); err != nil {
		return nil, signatureError{recalledName, siteHost(), err}
	}
	var r Recalled
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("%s is not a recall list: %w", recalledName, err)
	}
	for _, v := range r.Versions {
		if !Parseable(v.Version) {
			return nil, fmt.Errorf("%s names %q, which is not a version", recalledName, v.Version)
		}
	}
	return &r, nil
}

// Recall reports whether running has been recalled: published, then pulled
// because something was found wrong with it, after copies had already
// updated into it. Latest and Newer only ever look forward from where a
// build already is, so nothing else tells a copy that already applied a bad
// release that the version it is running has a known problem -- this is
// meant to be checked at the same points Latest already is (the CLI's
// `update` and the background watcher), not only when looking for something
// newer.
//
// It is best-effort in exactly the way Latest's use in the background
// watcher already is, and returns nil both when running is not on the list
// and when the list could not be read at all: a machine offline, a site that
// briefly fails, or a build with no release key to check the signature
// against costs nothing but silence until the next check, never a false
// "you are on a bad build." recalled.json has no GitHub mirror to fall back
// to, so a signature that does not check -- the site serving something that
// was not released, worth knowing about -- is logged as a warning the same
// way a bad manifest or checksums.txt from the site already is, rather than
// returned as an error nobody who only wants the answer would check for.
func Recall(ctx context.Context, running string) *RecalledVersion {
	keys := TrustedKeys()
	if len(keys) == 0 {
		return nil
	}
	data, err := fetchSmall(ctx, siteURL+"/"+recalledName, 1<<20)
	if err != nil {
		return nil
	}
	sig, err := fetchSmall(ctx, siteURL+"/"+recalledName+sigExt, 1<<10)
	if err != nil {
		return nil
	}
	r, err := CheckRecalled(keys, data, sig)
	if err != nil {
		if suspicious(err) {
			logf("flockdeck: warning: %s from %s: %v", recalledName, siteHost(), err)
		}
		return nil
	}
	for _, v := range r.Versions {
		if v.Version == running {
			v := v
			return &v
		}
	}
	return nil
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
