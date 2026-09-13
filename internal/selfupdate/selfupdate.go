// Package selfupdate keeps a running Flockdeck up to date from its releases,
// published at dl.flockdeck.ai (Site) and mirrored on GitHub.
//
// The work is split into three steps that are deliberately kept apart, because
// the application can afford to do the first two at any time and can only ever
// do the third at a moment of the user's choosing:
//
//   - Latest asks what the latest release is, and Newer says whether it is
//     one to move to; an untagged local build never has one.
//   - Stage downloads it, checks it against the published SHA-256, signed by
//     the release key whether it comes from the site or from GitHub, and
//     unpacks the binary into the state directory. Nothing about the
//     installation has changed yet.
//   - Apply swaps the staged binary into place. This is the only step that
//     touches the installed program, and it is never done under a running
//     session: panes hold live agents, and replacing the binary beneath them
//     to save a restart would cost far more work than it saved.
//
// Staging is recorded on disk, so an update downloaded in one run is still
// there to be applied by the next, and a half-finished download is never
// mistaken for a finished one.
//
// On Windows the program has a console twin beside it (chatName), the same
// program with its PE Subsystem set to console, which an API agent's pane
// runs. Stage and Apply carry it with the program when a release has one,
// and EnsureChatTwin keeps it exactly in step with the program at every start
// however the program was put there.
package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"debug/pe"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jmwri/flockdeck/internal/store"
)

// Repo is the repository releases are read from, as owner/name.
const Repo = "jmwri/flockdeck"

// binaryName is what the binary is called inside a release archive.
var binaryName = func() string {
	if runtime.GOOS == "windows" {
		return "flockdeck.exe"
	}
	return "flockdeck"
}()

// chatName is the console twin a Windows release carries beside the program,
// or "" where there is none. It is the same program linked as a console
// program, which is what an API agent's pane runs: a pane is a pseudo-console,
// and Windows attaches one only to a console program. It is updated with the
// program, since the program starts it, and a new program beside an old twin
// would run a chat client of another version in its panes.
var chatName = func() string {
	if runtime.GOOS == "windows" {
		return "flockdeck-chat.exe"
	}
	return ""
}()

// ErrNoAsset is returned when a release carries nothing built for this
// platform, which is what a partly-uploaded release looks like from here.
var ErrNoAsset = errors.New("this release has no build for this platform")

// Asset is one file attached to a release.
type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

// Release is a published release, reduced to what an update needs.
type Release struct {
	Version string  `json:"tag_name"`
	Notes   string  `json:"body"`
	URL     string  `json:"html_url"`
	Draft   bool    `json:"draft"`
	Pre     bool    `json:"prerelease"`
	Assets  []Asset `json:"assets"`

	// A release read from the site (latestFromSite) has these as well, and
	// one read from GitHub's API has none of them: fetchSum finds the
	// signature of its checksums.txt among its Assets instead.
	sumsSig string            // checksums.txt.sig, which checksums.txt must pass
	sums    map[string]string // each file's SHA-256, from the signed manifest; nil for GitHub's API
	mirror  *Release          // the same release on GitHub, if the site fails
}

// Pending is an update that has been downloaded, checked and unpacked, and is
// waiting for a restart to be applied.
type Pending struct {
	Version string    `json:"version"`
	Binary  string    `json:"binary"`
	Chat    string    `json:"chat,omitempty"` // the staged console twin (chatName), when the release has one
	Notes   string    `json:"notes,omitempty"`
	URL     string    `json:"url,omitempty"`
	Staged  time.Time `json:"staged"`
}

// client bounds the whole of a request, because an update is never urgent: a
// check that hangs must not be able to hold a shutdown open or keep a
// goroutine for the life of the process. What stops one that hangs is
// stallTimeout; this is only the outer bound, which a link slower than about
// 3KB/s is the only thing to meet.
//
// It was five minutes, and it covers reading the body. The Windows archive is
// 9.4MB, so on a link slower than about 31KB/s the download could never
// finish, and the background check threw away up to five minutes of one
// every time it tried.
//
// abandonedWork is measured from it: a longer bound would let sweepWork clear
// a download that is still being written.
var client = &http.Client{Timeout: requestLimit}

// requestLimit is the longest a request, the body of a download included, is
// given.
const requestLimit = time.Hour

// stallTimeout is how long a request may go with nothing arriving -- no
// answer, or no more of its body -- before it is given up on. A variable so a
// test need not wait it out.
var stallTimeout = time.Minute

func get(ctx context.Context, url string) (*http.Response, error) {
	// The request is cancelled once stallTimeout passes with nothing arriving,
	// and every byte of the body that does arrive starts that wait again.
	ctx, cancel := context.WithCancel(ctx)
	var stalled atomic.Bool
	timer := time.AfterFunc(stallTimeout, func() { stalled.Store(true); cancel() })
	done := func() { timer.Stop(); cancel() }
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		done()
		return nil, err
	}
	req.Header.Set("User-Agent", "flockdeck-updater")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := client.Do(req)
	if err != nil {
		done()
		if stalled.Load() {
			return nil, stallError(req.URL.Host)
		}
		return nil, err
	}
	resp.Body = &stallReader{body: resp.Body, timer: timer, stalled: &stalled, done: done, host: req.URL.Host}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		// GitHub turns away an address that has asked too often with a 403
		// and says when it may ask again. "403 Forbidden" alone reads as
		// though something were wrong with the release or with this machine.
		if resp.Header.Get("X-RateLimit-Remaining") == "0" {
			when := "later"
			if reset, err := strconv.ParseInt(resp.Header.Get("X-RateLimit-Reset"), 10, 64); err == nil {
				when = "after " + time.Unix(reset, 0).Format("15:04")
			}
			return nil, fmt.Errorf("GitHub is limiting how often this address may ask for releases; try again %s", when)
		}
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	return resp, nil
}

// stallReader is a response body that starts get's wait for something to
// arrive again with every byte that does, and says in words when that wait ran
// out rather than passing on a cancelled context.
type stallReader struct {
	body    io.ReadCloser
	timer   *time.Timer
	stalled *atomic.Bool
	done    func()
	host    string
}

func (r *stallReader) Read(p []byte) (int, error) {
	n, err := r.body.Read(p)
	if n > 0 {
		r.timer.Reset(stallTimeout)
	}
	if err != nil && err != io.EOF && r.stalled.Load() {
		return n, stallError(r.host)
	}
	return n, err
}

func (r *stallReader) Close() error {
	err := r.body.Close()
	r.done()
	return err
}

// stallError is what a request that went stallTimeout with nothing arriving
// fails with.
func stallError(host string) error {
	return fmt.Errorf("%s stopped sending for %s, so the request was given up on; try again", host, stallTimeout)
}

// Latest returns the most recent published release: from the site when it
// answers with a manifest signed by the release key, and otherwise from
// GitHub, as every release before the site did, with the reason logged.
//
// Neither is sent anything but the request itself: no version, no identifier,
// nothing a server could tell one installation from another by.
func Latest(ctx context.Context) (*Release, error) {
	rel, err := latestFromSite(ctx)
	if err == nil {
		return rel, nil
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	fellBack("could not read the latest release from "+siteHost(), err)
	return latestFromGitHub(ctx)
}

// latestFromGitHub asks GitHub's API for the latest release.
func latestFromGitHub(ctx context.Context) (*Release, error) {
	resp, err := get(ctx, githubAPIURL+"/repos/"+Repo+"/releases/latest")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var rel Release
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&rel); err != nil {
		return nil, fmt.Errorf("read release: %w", err)
	}
	// GitHub never gives a pre-release as the latest, but a candidate whose
	// pre-release mark was taken off by hand is given like any other. The
	// site refuses a candidate by its version, and so does this, whichever of
	// the two says it is one.
	if rel.Pre || prerelease(rel.Version) {
		return nil, fmt.Errorf("GitHub names %s, a pre-release, which is never the latest", rel.Version)
	}
	return &rel, nil
}

// assetFor picks the archive built for this platform. The name is the contract
// with cmd/release, which writes flockdeck_<version>_<os>_<arch>.<ext>.
func (r *Release) assetFor(goos, goarch string) (Asset, bool) {
	suffix := fmt.Sprintf("_%s_%s.", goos, goarch)
	for _, a := range r.Assets {
		if strings.Contains(a.Name, suffix) {
			return a, true
		}
	}
	return Asset{}, false
}

// DownloadSize is the size in bytes of the archive Stage would fetch for this
// platform, or 0 when the release has none or does not say.
func (r *Release) DownloadSize() int64 {
	a, _ := r.assetFor(runtime.GOOS, runtime.GOARCH)
	return a.Size
}

func (r *Release) checksums() (Asset, bool) { return r.asset(sumsName) }

// asset is the file of the release called name.
func (r *Release) asset(name string) (Asset, bool) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return Asset{}, false
}

// Stage downloads the release, checks it and unpacks the binary under dir,
// with the console twin beside it when the release has one.
//
// A release read from the site is downloaded from there, and from GitHub when
// that fails for any reason, the reason logged. Either way the archive has to
// match the SHA-256 the release's signed manifest gave for it.
func Stage(ctx context.Context, rel *Release, dir string) (*Pending, error) {
	p, err := stage(ctx, rel, dir)
	if err == nil || rel.mirror == nil || ctx.Err() != nil || errors.Is(err, ErrNoAsset) {
		return p, err
	}
	fellBack("could not download "+rel.Version+" from "+siteHost(), err)
	return stage(ctx, rel.mirror, dir)
}

// stage is Stage from the one place rel says.
//
// The download is hashed as it is written rather than read back afterwards, so
// a file that does not match is never on disk in a state anything could mistake
// for finished, and a truncated transfer fails here rather than at the swap.
func stage(ctx context.Context, rel *Release, dir string) (*Pending, error) {
	asset, ok := rel.assetFor(runtime.GOOS, runtime.GOARCH)
	if !ok {
		return nil, ErrNoAsset
	}
	sumsAsset, ok := rel.checksums()
	if !ok {
		return nil, errors.New("release has no checksums.txt to check the download against")
	}

	want, err := rel.fetchSum(ctx, sumsAsset.URL, asset.Name)
	if err != nil {
		return nil, err
	}

	// The release is fetched beside whatever is staged already, not over it.
	// There is one when a newer release comes out before the last was
	// applied, and clearing it first meant a download that failed, or did
	// not match, left nothing to apply while the top bar went on offering
	// it.
	//
	// Each download has a work directory of its own. Two can be under way at
	// once -- `flockdeck update` run while the application's own check is
	// downloading -- and with one fixed name each began by clearing it: the
	// other's half-written archive went, and that one then failed to unpack
	// it, or on Windows the clearing itself failed on the file held open.
	// What an interrupted attempt left is cleared once nothing can still be
	// writing it.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	sweepWork(dir)
	work, err := os.MkdirTemp(dir, "staging.new-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(work) // nothing left once it has become the staging

	archive := filepath.Join(work, asset.Name)
	if err := download(ctx, asset.URL, archive, want, asset.Size); err != nil {
		return nil, err
	}
	if err := unpack(archive, filepath.Join(work, binaryName), binaryName); err != nil {
		return nil, err
	}
	// A release from before the twin has none, and the program is then
	// updated on its own, as it always was.
	haveChat := false
	if chatName != "" {
		var missing notInArchive
		switch err := unpack(archive, filepath.Join(work, chatName), chatName); {
		case err == nil:
			haveChat = true
		case !errors.As(err, &missing):
			return nil, err
		}
	}
	if err := os.Remove(archive); err != nil {
		return nil, err
	}

	staging := filepath.Join(dir, "staging")
	if err := swapStaging(dir, work, staging); err != nil {
		return nil, err
	}

	p := &Pending{
		Version: rel.Version,
		Binary:  filepath.Join(staging, binaryName),
		Notes:   rel.Notes,
		URL:     rel.URL,
		Staged:  time.Now().UTC(),
	}
	if haveChat {
		p.Chat = filepath.Join(staging, chatName)
	}
	if err := save(dir, p); err != nil {
		// The staging directory holds this release now, and a record still
		// naming the one before would have it installed under that one's
		// version. Nothing staged is better than that.
		Discard(dir)
		return nil, err
	}
	return p, nil
}

// rename is how the updater renames what it stages and what it puts in place:
// through store.RenameWithRetry, which on Windows waits out a moment's hold on
// the file -- most often a virus scanner opening a program just written. A
// variable so a test can have a rename fail, or be held.
var rename = store.RenameWithRetry

// swapStaging puts work, a finished download, at staging in place of whatever
// is staged there already.
//
// What is staged is moved aside rather than removed first, and put back if the
// new download cannot be put in its place. Removing it first meant a rename
// that then failed -- on Windows, a virus scanner still holding the new
// program it had just been shown -- left nothing staged while the top bar
// went on offering the update, and the restart it asked for found nothing to
// apply. The renames are retried for as long as store.RenameWithRetry waits
// out such a hold, which is gone in moments.
//
// The previous download goes into a directory of its own, made now, so that
// sweepWork, which clears those by age, never takes one that is still to be
// put back.
func swapStaging(dir, work, staging string) error {
	if _, err := os.Lstat(staging); errors.Is(err, os.ErrNotExist) {
		return rename(work, staging)
	}
	aside, err := os.MkdirTemp(dir, "staging.old-")
	if err != nil {
		return err
	}
	previous := filepath.Join(aside, "staging")
	if err := rename(staging, previous); err != nil {
		os.RemoveAll(aside)
		return fmt.Errorf("move the update staged before aside: %w", err)
	}
	if err := rename(work, staging); err != nil {
		if rerr := rename(previous, staging); rerr != nil {
			// The previous download is left where it is, for sweepWork:
			// removing it would take the last copy of it.
			return fmt.Errorf("put the download in place: %w; the update staged before could not be put back either: %v", err, rerr)
		}
		os.RemoveAll(aside)
		return fmt.Errorf("put the download in place: %w", err)
	}
	os.RemoveAll(aside)
	return nil
}

// abandonedWork is how old a download's work directory has to be before it is
// taken for one an interrupted attempt left behind. The archive is created
// once the answer arrives, and its download is given up on requestLimit after
// it was asked for; the unpacking that follows writes into the directory again
// within seconds. So nothing older than requestLimit and a minute more is still
// being written, and sweepWork, which clears what is older, never takes the
// work of a download under way -- another process's included.
const abandonedWork = requestLimit + time.Minute

// sweepWork clears the work directories interrupted downloads left in dir: the
// one name every download used before each had its own, and the ones since,
// and the previous downloads swapStaging moved aside and could not remove.
func sweepWork(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "staging.new") && !strings.HasPrefix(e.Name(), "staging.old-") {
			continue
		}
		if fi, err := e.Info(); err == nil && time.Since(fi.ModTime()) > abandonedWork {
			os.RemoveAll(filepath.Join(dir, e.Name()))
		}
	}
}

// fetchSum reads checksums.txt and returns the hash recorded for one file.
//
// For a release from the site, checksums.txt has to carry the release key's
// signature, and the hash it gives has to be the one the signed manifest
// gave; from GitHub's mirror of that release, only the second. A release read
// from GitHub's API has no manifest, so its checksums.txt has to carry the
// release key's signature, checksums.txt.sig, published beside it on GitHub.
//
// That release used to be taken as it was. Latest goes to GitHub whenever the
// site fails, which anybody between a copy and the site can arrange, so the
// release key vouched for nothing against whoever could also publish on
// GitHub. Only a build without a release key, which trusts nothing from the
// site either, still takes GitHub's checksums.txt as it is. Every release it
// could move to is newer than the build itself, and is published by the
// workflow that uploads the signature to GitHub too, so nothing is lost by
// requiring it of every release rather than only of the later ones.
func (r *Release) fetchSum(ctx context.Context, url, name string) (string, error) {
	sigURL, from := r.sumsSig, siteHost()
	keys := TrustedKeys()
	if sigURL == "" && r.sums == nil && len(keys) > 0 {
		from = "GitHub"
		a, ok := r.asset(sumsName + sigExt)
		if !ok {
			return "", signatureError{sumsName, from, fmt.Errorf("release %s carries no %s", r.Version, sumsName+sigExt)}
		}
		// The signature covers checksums.txt alone, which names each archive
		// with its version (assetFor gives the contract). An archive of
		// another version would let an older signed release, one with a
		// known flaw, be republished under a newer tag and installed as an
		// update to the release that fixed it.
		if !strings.HasPrefix(name, "flockdeck_"+r.Version+"_") {
			return "", mismatchError{fmt.Sprintf("GitHub's release %s carries %s, which is not an archive of %s", r.Version, name, r.Version)}
		}
		sigURL = a.URL
	}
	body, err := fetchSmall(ctx, url, 1<<20)
	if err != nil {
		return "", err
	}
	if sigURL != "" {
		sig, err := fetchSmall(ctx, sigURL, 1<<10)
		if err != nil {
			return "", err
		}
		if err := VerifyAny(keys, body, sig); err != nil {
			return "", signatureError{sumsName, from, err}
		}
	}
	sum, err := sumIn(body, name)
	if err != nil {
		return "", err
	}
	if signed, ok := r.sums[name]; ok && signed != sum {
		return "", mismatchError{fmt.Sprintf("checksums.txt gives %s a SHA-256 other than the signed manifest does", name)}
	}
	return sum, nil
}

// sumIn returns the hash checksums.txt records for one file.
func sumIn(body []byte, name string) (string, error) {
	for _, line := range strings.Split(string(body), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && f[1] == name {
			// Anything but a SHA-256 can only fail the comparison, and failing
			// it used to panic on the way to saying so: the message quotes a
			// prefix of each hash. That was in the background watcher, where a
			// panic takes the application and every agent in it down.
			sum := strings.ToLower(f[0])
			if _, err := hex.DecodeString(sum); err != nil || len(sum) != 2*sha256.Size {
				return "", fmt.Errorf("checksums.txt lists %s without a SHA-256", name)
			}
			return sum, nil
		}
	}
	return "", fmt.Errorf("checksums.txt does not list %s", name)
}

// download writes url to dest and checks it against want, the SHA-256 it has
// to have, and size, the bytes the release gives for it, or 0 for a release
// that does not say.
//
// Nothing past size is read. The hash can only be checked once the body has
// ended, so a body that never ended -- from a CDN serving something other than
// the release -- was written to disk for as long as requestLimit allowed, on
// every check. The size is signed in the site's manifest, so one byte more is
// already enough to know the download is not the release.
func download(ctx context.Context, url, dest, want string, size int64) error {
	resp, err := get(ctx, url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	var body io.Reader = resp.Body
	if size > 0 {
		body = io.LimitReader(resp.Body, size+1)
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(dest)
		return err
	}
	if size > 0 && n > size {
		os.Remove(dest)
		return mismatchError{fmt.Sprintf("download is larger than the %d bytes published for it", size)}
	}

	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		os.Remove(dest)
		return mismatchError{fmt.Sprintf("download does not match its published checksum (got %s, want %s)", got[:12], want[:12])}
	}
	return nil
}

// notInArchive is unpack's error for a file the archive does not hold.
type notInArchive struct{ name string }

func (e notInArchive) Error() string { return "archive does not contain " + e.name }

// unpack writes one program, name, out of the archive to dest.
func unpack(archive, dest, name string) error {
	if strings.HasSuffix(archive, ".zip") {
		return unzip(archive, dest, name)
	}
	return untar(archive, dest, name)
}

func unzip(archive, dest, name string) error {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer zr.Close()

	for _, f := range zr.File {
		if filepath.Base(f.Name) != name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		defer rc.Close()
		return writeBinary(dest, rc)
	}
	return notInArchive{name}
}

func untar(archive, dest, name string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if h.Typeflag != tar.TypeReg || filepath.Base(h.Name) != name {
			continue
		}
		return writeBinary(dest, tr)
	}
	return notInArchive{name}
}

func writeBinary(dest string, r io.Reader) error {
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, r)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(dest)
		return err
	}
	// The mode given to OpenFile is only a request, and an existing file keeps
	// the mode it had. On the platforms where it matters an unexecutable binary
	// would fail at the worst possible moment, after the swap.
	return os.Chmod(dest, 0o755)
}

// --- what has been staged -------------------------------------------------

func pendingPath(dir string) string { return filepath.Join(dir, "pending.json") }

// save records what is staged. It goes through store.WriteAtomic rather than a
// temporary file of its own: two instances staging at once each get a
// temporary of their own there, and on Windows a read of pending.json by the
// other at that moment is waited out instead of failing the rename.
func save(dir string, p *Pending) error {
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return store.WriteAtomic(pendingPath(dir), data)
}

// Load returns the update waiting to be applied, if there is one.
//
// A record whose binary has gone is not an update: the file can be cleared out
// from under us by anything that tidies temporary directories, and reporting an
// update that could not possibly be applied would put a badge in the interface
// that a restart would never clear.
func Load(dir string) (*Pending, bool) {
	data, err := os.ReadFile(pendingPath(dir))
	if err != nil {
		return nil, false
	}
	var p Pending
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, false
	}
	if p.Version == "" || p.Binary == "" {
		return nil, false
	}
	if _, err := os.Stat(p.Binary); err != nil {
		return nil, false
	}
	if p.Chat != "" {
		if _, err := os.Stat(p.Chat); err != nil {
			return nil, false
		}
	}
	return &p, true
}

// Discard forgets a staged update and removes what it downloaded.
func Discard(dir string) {
	os.Remove(pendingPath(dir))
	os.RemoveAll(filepath.Join(dir, "staging"))
}

// --- putting it in place --------------------------------------------------

// Apply replaces the running program's file with the staged binary, and on
// Windows the console twin beside it (chatName) with the staged one.
//
// The two go in together or not at all: a new program beside an old twin, or
// the other way round, would run a chat client of another version in its
// panes. So the twin goes in first and is taken back out if the program then
// cannot be put in, and the update stays staged for the next attempt.
func Apply(dir, exePath string) error {
	p, ok := Load(dir)
	if !ok {
		return errors.New("no update has been staged")
	}

	exePath, err := filepath.EvalSymlinks(exePath)
	if err != nil {
		return err
	}
	// Run as the twin, as `flockdeck-chat update` is, the program to replace
	// is the one beside it. Taken as the program itself, the twin's file got
	// the twin and then the program over it: a GUI program where the panes
	// need the console one, and the program itself left as it was.
	if chatName != "" && strings.EqualFold(filepath.Base(exePath), chatName) {
		exePath = filepath.Join(filepath.Dir(exePath), binaryName)
	}

	var undoChat func() error
	if p.Chat != "" {
		undo, err := replace(p.Chat, filepath.Join(filepath.Dir(exePath), chatName))
		if err != nil {
			return fmt.Errorf("put the new %s in place: %w", chatName, err)
		}
		undoChat = undo
	}
	if _, err := replace(p.Binary, exePath); err != nil {
		if undoChat != nil {
			if uerr := undoChat(); uerr != nil {
				return fmt.Errorf("%w; the new %s, already in place, could not be taken back out either: %v", err, chatName, uerr)
			}
		}
		return err
	}

	Discard(dir)
	return nil
}

// replace puts the staged file src at target, which may be running.
//
// A file there is moved aside rather than written over. Windows will not let
// an executable that is running be replaced, but it will let it be renamed, so
// moving it out of the way and putting the new one at the old name works while
// the program is still running from it. The moved-aside file is swept up by
// the next start, once nothing holds it open.
//
// The staged file is copied rather than renamed into place because the state
// directory and the installation are frequently on different volumes, and a
// rename across volumes fails.
//
// Every rename here waits out a moment's hold (rename). A virus scanner opens
// each program written, the copy landed beside the program among them, and a
// rename refused while it had that open rolled the update back, with advice to
// try again from an administrator shell that would have changed nothing.
//
// undo takes the new file back out: what was there before is put back, or,
// where there was nothing, the new file is removed.
func replace(src, target string) (undo func() error, err error) {
	// Landing the copy beside the program, rather than copying straight over
	// it, keeps the window in which the file exists but is incomplete off the
	// name that is about to be run.
	next := target + ".new"
	if err := copyFile(src, next); err != nil {
		return nil, fmt.Errorf("write the new version beside the old one: %w", err)
	}

	// The twin is missing from an installation made before there was one.
	if _, err := os.Lstat(target); errors.Is(err, os.ErrNotExist) {
		if err := rename(next, target); err != nil {
			os.Remove(next)
			return nil, fmt.Errorf("put the new version in place: %w", err)
		}
		return func() error { return os.Remove(target) }, nil
	}

	// The running program normally goes to <name>.old, but what is there can
	// still be running itself: an instance started before one update runs
	// from it when a second is put in place, and Windows will neither delete
	// it nor rename anything over it. That one is left alone and this one
	// takes a name of its own, which the sweep clears as well.
	old := target + ".old"
	if err := os.Remove(old); err != nil && !errors.Is(err, os.ErrNotExist) {
		old = fmt.Sprintf("%s.old-%d", target, time.Now().UnixNano())
	}
	if err := rename(target, old); err != nil {
		os.Remove(next)
		return nil, fmt.Errorf("move the running version aside: %w", err)
	}
	if err := rename(next, target); err != nil {
		// Put back what was there. Leaving no program at all under the name
		// the user starts is far worse than failing to update — and when
		// even that fails, where the program went is the one thing they
		// need to be told.
		if rerr := rename(old, target); rerr != nil {
			return nil, fmt.Errorf("put the new version in place: %w; the previous version could not be put back either and is now %s — rename it to %s to run flockdeck again", err, old, target)
		}
		os.Remove(next)
		return nil, fmt.Errorf("put the new version in place: %w", err)
	}
	// Nothing has started the new file yet, so it can simply be removed.
	return func() error {
		if err := os.Remove(target); err != nil {
			return err
		}
		return rename(old, target)
	}, nil
}

// Sweep removes the files previous Applies moved aside, the console twin's as
// well as the program's, and what an interrupted EnsureChatTwin left. It is
// called at startup, by which time nothing holds the old program open — or,
// for one an instance still running is using, fails to and leaves it for next
// time.
func Sweep(exePath string) {
	if exePath == "" {
		return
	}
	// Apply moves aside the file a link leads to, not the link, so that is
	// where its .old is. Started through a link -- which macOS reports as the
	// link -- this looked beside the link, and the old program stayed on disk.
	if p, err := filepath.EvalSymlinks(exePath); err == nil {
		exePath = p
	}
	sweepAside(exePath)
	if chatName == "" {
		return
	}
	sweepAside(filepath.Join(filepath.Dir(exePath), chatName))
	// And what an EnsureChatTwin that was cut short left of the twin it was
	// writing.
	dir := filepath.Dir(exePath)
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if n := e.Name(); strings.HasPrefix(n, chatName+".") && strings.HasSuffix(n, ".tmp") {
			os.Remove(filepath.Join(dir, n))
		}
	}
}

// sweepAside removes what an Apply moved aside from path.
func sweepAside(path string) {
	os.Remove(path + ".old")
	os.Remove(path + ".new")
	dir, base := filepath.Split(path)
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), base+".old-") {
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	// The copy is renamed onto the name the program is started by, and that
	// name has just been vacated, so no file system treats the rename as the
	// replacement it is and flushes the data ahead of it. Without this a crash
	// soon after an update could leave an empty file where the program was.
	if err == nil {
		err = out.Sync()
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(dst)
		return err
	}
	return os.Chmod(dst, 0o755)
}

// EnsureChatTwin keeps the console twin (chatName) beside the program at
// exePath exactly what the program makes of itself (ConsoleTwin), writing it
// when it is missing or differs.
//
// A release carries the twin, but the first update to one is put in place by
// the release before it, whose updater knows only the program, and an
// installation made by an install script of before, or by `go install`, has
// none either: without one an API agent's pane is blank. And a program
// replaced by any route that leaves the twin as it was — a copy by hand, an
// updater of before — would have its panes run a chat client of another
// version, whose hook events, prompts and transcripts can have drifted.
//
// Only a file of exactly that name in the program's own directory is ever
// written, never through a link, a reparse point or anything else that is
// not a regular file. The new twin is written beside it and renamed over it;
// a twin a chat pane is running from cannot be renamed over on Windows, and
// is left for the next start, the pane working on meanwhile. It does nothing
// off Windows, for the twin itself, or for a program that is not a GUI build,
// which runs in a pane as it is.
func EnsureChatTwin(exePath string) error {
	if chatName == "" || exePath == "" {
		return nil
	}
	exePath, err := filepath.EvalSymlinks(exePath)
	if err != nil {
		return err
	}
	if !strings.EqualFold(filepath.Base(exePath), binaryName) {
		return nil
	}
	self, err := os.ReadFile(exePath)
	if err != nil {
		return err
	}
	want, ok := ConsoleTwin(self)
	if !ok {
		return nil
	}

	dir := filepath.Dir(exePath)
	twin := filepath.Join(dir, chatName)
	switch fi, err := os.Lstat(twin); {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return err
	case !fi.Mode().IsRegular():
		return nil
	case fi.Size() == int64(len(want)):
		// The size is compared first, so a stale twin of another build is
		// told apart without reading it; the same size is read and compared.
		if have, err := os.ReadFile(twin); err == nil && bytes.Equal(have, want) {
			return nil
		}
	}

	tmp, err := os.CreateTemp(dir, chatName+".*.tmp")
	if err != nil {
		return err
	}
	_, err = tmp.Write(want)
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp.Name(), 0o755)
	}
	if err == nil {
		err = rename(tmp.Name(), twin)
	}
	if err != nil {
		// Most often a chat pane running from the twin, which Windows will
		// not let anything be renamed over. The rename waits out a moment's
		// hold, a scanner's on the file just written, and gives up on this
		// after a second; the next start tries again.
		os.Remove(tmp.Name())
	}
	return nil
}

// ConsoleTwin is program with its PE Subsystem field set to console: the
// console twin of a GUI build, which is all that tells the two apart and all
// that decides whether Windows gives a pane's process a console. It is false
// for anything that is not a GUI-subsystem PE file.
func ConsoleTwin(program []byte) ([]byte, bool) {
	off, ok := subsystemOffset(program)
	if !ok || binary.LittleEndian.Uint16(program[off:]) != pe.IMAGE_SUBSYSTEM_WINDOWS_GUI {
		return nil, false
	}
	twin := bytes.Clone(program)
	binary.LittleEndian.PutUint16(twin[off:], pe.IMAGE_SUBSYSTEM_WINDOWS_CUI)
	return twin, true
}

// subsystemOffset is where a PE file keeps its Subsystem field: in the
// optional header after the PE signature and the file header, 68 bytes in for
// PE32 and PE32+ alike. It is false for anything that is not a PE file.
func subsystemOffset(data []byte) (int, bool) {
	if len(data) < 0x40 || data[0] != 'M' || data[1] != 'Z' {
		return 0, false
	}
	at := int(binary.LittleEndian.Uint32(data[0x3c:]))
	if at <= 0 || at+24+70 > len(data) || string(data[at:at+4]) != "PE\x00\x00" {
		return 0, false
	}
	if magic := binary.LittleEndian.Uint16(data[at+24:]); magic != 0x10b && magic != 0x20b {
		return 0, false
	}
	return at + 24 + 68, true
}

// Dir is where updates are staged, given the application's state directory.
func Dir(state string) (string, error) {
	dir := filepath.Join(state, "updates")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}
