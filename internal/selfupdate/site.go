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
//	/latest.json, /latest.json.sig         the latest release, signed
//	/<version>/<archive>                    each release's files, never changed
//	/<version>/checksums.txt(.sig)          and their SHA-256s, signed
//	/latest/<archive without the version>   for the site's download buttons
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

// Manifest is latest.json: the latest release, and every file of it the site
// holds, with the SHA-256 and size of each. cmd/release writes it and the
// updater reads it, through this one type.
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

// sumsName and sigExt name the files every release carries besides its
// archives.
const (
	sumsName = "checksums.txt"
	sigExt   = ".sig"
)

// CheckManifest reads latest.json once its signature, the content of
// latest.json.sig, has been checked against key, and refuses a manifest the
// updater could not act on. cmd/release reads back what it wrote through this,
// so a release is never published with a manifest the updater would refuse.
func CheckManifest(key ed25519.PublicKey, data, sig []byte) (*Manifest, error) {
	if err := Verify(key, data, sig); err != nil {
		return nil, signatureError{"latest.json", err}
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("latest.json is not a manifest: %w", err)
	}
	if !Parseable(m.Version) {
		return nil, fmt.Errorf("latest.json names %q, which is not a version", m.Version)
	}
	have := map[string]bool{}
	for _, f := range m.Files {
		if f.Name == "" || f.URL == "" || strings.ContainsAny(f.Name, `/\`) {
			return nil, fmt.Errorf("latest.json lists a file without a plain name and a URL")
		}
		if !isSHA256(f.SHA256) {
			return nil, fmt.Errorf("latest.json lists %s without a SHA-256", f.Name)
		}
		have[f.Name] = true
	}
	for _, n := range []string{sumsName, sumsName + sigExt} {
		if !have[n] {
			return nil, fmt.Errorf("latest.json does not list %s", n)
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
	var mismatch checksumError
	return errors.As(err, &sig) || errors.As(err, &mismatch)
}

// checksumError is a download, or a checksums.txt, that disagrees with what
// was signed for it.
type checksumError struct{ msg string }

func (e checksumError) Error() string { return e.msg }

func isSHA256(s string) bool {
	_, err := hex.DecodeString(s)
	return err == nil && len(s) == 2*sha256.Size
}

// siteHost is the site as a person would name it in a message.
func siteHost() string {
	if u, err := url.Parse(siteURL); err == nil && u.Host != "" {
		return u.Host
	}
	return siteURL
}

// latestFromSite reads the latest release from the site, trusting it only
// once its signature has been checked against the compiled-in key.
//
// The release it returns downloads from the site, checks checksums.txt's own
// signature before believing it, and carries the same release on GitHub for
// Stage to fall back to.
func latestFromSite(ctx context.Context) (*Release, error) {
	if trustedKey == nil {
		return nil, errNoKey
	}
	data, err := fetchSmall(ctx, siteURL+"/latest.json", 1<<20)
	if err != nil {
		return nil, err
	}
	sig, err := fetchSmall(ctx, siteURL+"/latest.json"+sigExt, 1<<10)
	if err != nil {
		return nil, err
	}
	m, err := CheckManifest(trustedKey, data, sig)
	if err != nil {
		return nil, err
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
