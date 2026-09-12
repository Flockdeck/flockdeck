// Package selfupdate keeps a running Flockdeck up to date from its GitHub
// releases.
//
// The work is split into three steps that are deliberately kept apart, because
// the application can afford to do the first two at any time and can only ever
// do the third at a moment of the user's choosing:
//
//   - Latest asks GitHub what the latest release is, and Newer says whether
//     it is one to move to; an untagged local build never has one.
//   - Stage downloads it, checks it against the published SHA-256 and unpacks
//     the binary into the state directory. Nothing about the installation has
//     changed yet.
//   - Apply swaps the staged binary into place. This is the only step that
//     touches the installed program, and it is never done under a running
//     session: panes hold live agents, and replacing the binary beneath them
//     to save a restart would cost far more work than it saved.
//
// Staging is recorded on disk, so an update downloaded in one run is still
// there to be applied by the next, and a half-finished download is never
// mistaken for a finished one.
package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
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

// client is given a timeout because an update is never urgent: a check that
// hangs must not be able to hold a shutdown open or keep a goroutine for the
// life of the process.
var client = &http.Client{Timeout: 5 * time.Minute}

func get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "flockdeck-updater")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
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

// Latest returns the most recent published release.
func Latest(ctx context.Context) (*Release, error) {
	resp, err := get(ctx, "https://api.github.com/repos/"+Repo+"/releases/latest")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var rel Release
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&rel); err != nil {
		return nil, fmt.Errorf("read release: %w", err)
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

func (r *Release) checksums() (Asset, bool) {
	for _, a := range r.Assets {
		if a.Name == "checksums.txt" {
			return a, true
		}
	}
	return Asset{}, false
}

// Stage downloads the release, checks it and unpacks the binary under dir.
//
// The download is hashed as it is written rather than read back afterwards, so
// a file that does not match is never on disk in a state anything could mistake
// for finished, and a truncated transfer fails here rather than at the swap.
func Stage(ctx context.Context, rel *Release, dir string) (*Pending, error) {
	asset, ok := rel.assetFor(runtime.GOOS, runtime.GOARCH)
	if !ok {
		return nil, ErrNoAsset
	}
	sumsAsset, ok := rel.checksums()
	if !ok {
		return nil, errors.New("release has no checksums.txt to check the download against")
	}

	want, err := fetchSum(ctx, sumsAsset.URL, asset.Name)
	if err != nil {
		return nil, err
	}

	// The release is fetched beside whatever is staged already, not over it.
	// There is one when a newer release comes out before the last was
	// applied, and clearing it first meant a download that failed, or did
	// not match, left nothing to apply while the top bar went on offering
	// it. Anything left in the work directory by an interrupted attempt goes
	// first, so a stale archive is never taken for this run's.
	work := filepath.Join(dir, "staging.new")
	if err := os.RemoveAll(work); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(work, 0o755); err != nil {
		return nil, err
	}
	defer os.RemoveAll(work) // nothing left once it has become the staging

	archive := filepath.Join(work, asset.Name)
	if err := download(ctx, asset.URL, archive, want); err != nil {
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
	if err := os.RemoveAll(staging); err != nil {
		return nil, err
	}
	if err := os.Rename(work, staging); err != nil {
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

// fetchSum reads checksums.txt and returns the hash recorded for one file.
func fetchSum(ctx context.Context, url, name string) (string, error) {
	resp, err := get(ctx, url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
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

func download(ctx context.Context, url, dest, want string) error {
	resp, err := get(ctx, url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(f, h), resp.Body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(dest)
		return err
	}

	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		os.Remove(dest)
		return fmt.Errorf("download does not match its published checksum (got %s, want %s)", got[:12], want[:12])
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
		if err := os.Rename(next, target); err != nil {
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
	if err := os.Rename(target, old); err != nil {
		os.Remove(next)
		return nil, fmt.Errorf("move the running version aside: %w", err)
	}
	if err := os.Rename(next, target); err != nil {
		// Put back what was there. Leaving no program at all under the name
		// the user starts is far worse than failing to update — and when
		// even that fails, where the program went is the one thing they
		// need to be told.
		if rerr := os.Rename(old, target); rerr != nil {
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
		return os.Rename(old, target)
	}, nil
}

// Sweep removes the files previous Applies moved aside. It is called at
// startup, by which time nothing holds the old program open — or, for one an
// instance still running is using, fails to and leaves it for next time.
func Sweep(exePath string) {
	if exePath == "" {
		return
	}
	sweepAside(exePath)
	if chatName != "" {
		sweepAside(filepath.Join(filepath.Dir(exePath), chatName))
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

// Dir is where updates are staged, given the application's state directory.
func Dir(state string) (string, error) {
	dir := filepath.Join(state, "updates")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}
