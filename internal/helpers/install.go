package helpers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/jmwri/flockdeck/internal/selfupdate"
	"github.com/jmwri/flockdeck/internal/store"
)

// Options configure an Installer. The zero value, with a Store, is the
// production setup: each helper's own catalogue source, the compiled-in keys,
// this platform.
type Options struct {
	Store *Store
	// AllowURL, when set, decides which URLs may be fetched in place of the
	// rule the catalogue's Source gives (https, that exact host, no
	// credentials, port 443). Tests use it to allow a plain-http loopback
	// server; nothing in the program sets it.
	AllowURL func(*url.URL) bool
	// Stall is how long a request may go with nothing arriving. Zero means a
	// minute.
	Stall time.Duration
	// GOOS and GOARCH name the platform whose archive is installed. Empty means
	// this one.
	GOOS, GOARCH string
	// Busy, when set, says that a helper is in use by this process, so it is
	// not swapped or removed under it.
	Busy func(id string) bool
	// Now is the clock. Nil means time.Now.
	Now func() time.Time
	// Lookup finds a helper by id. Nil means the compiled-in catalogue; tests
	// supply entries with smaller limits.
	Lookup func(id string) (Entry, bool)
	// Hook, when set, is called as an install passes each stage and may fail
	// it. For tests of interrupted installs.
	Hook func(stage string) error
}

// Installer downloads, verifies and installs helpers.
type Installer struct {
	store        *Store
	allow        func(*url.URL) bool
	client       *http.Client
	stall        time.Duration
	goos, goarch string
	busy         func(string) bool
	now          func() time.Time
	hook         func(string) error
	lookup       func(string) (Entry, bool)
	mu           sync.Mutex
	installing   map[string]bool
}

// NewInstaller makes an Installer from options.
func NewInstaller(o Options) *Installer {
	in := &Installer{
		store: o.Store, allow: o.AllowURL, stall: o.Stall,
		goos: o.GOOS, goarch: o.GOARCH, busy: o.Busy, now: o.Now, hook: o.Hook, lookup: o.Lookup,
	}
	if in.stall <= 0 {
		in.stall = time.Minute
	}
	if in.goos == "" {
		in.goos = runtime.GOOS
	}
	if in.goarch == "" {
		in.goarch = runtime.GOARCH
	}
	if in.lookup == nil {
		in.lookup = Lookup
	}
	if in.now == nil {
		in.now = time.Now
	}
	in.client = newClient()
	return in
}

// allowFor is the rule for what an entry's downloads may fetch.
func (in *Installer) allowFor(e Entry) func(*url.URL) bool {
	if in.allow != nil {
		return in.allow
	}
	return SourceAllowURL(e.Source)
}

// begin marks an install of id as under way, and reports false if one already
// is. end clears it.
func (in *Installer) begin(id string) bool {
	in.mu.Lock()
	defer in.mu.Unlock()
	if in.installing == nil {
		in.installing = map[string]bool{}
	}
	if in.installing[id] {
		return false
	}
	in.installing[id] = true
	return true
}

func (in *Installer) end(id string) {
	in.mu.Lock()
	delete(in.installing, id)
	in.mu.Unlock()
}

// Installing reports whether an install of the helper is under way in this
// process. A start and an uninstall are refused while it is.
func (in *Installer) Installing(id string) bool {
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.installing[id]
}

// Store is the apps directory the installer writes to.
func (in *Installer) Store() *Store { return in.store }

// Errors an install can fail with, each a different thing to do about it.
var (
	// ErrNoAsset: the release has no archive for this platform.
	ErrNoAsset = errors.New("not available for this platform")
	// ErrBusy: the helper is running and cannot be swapped.
	ErrBusy = errors.New("the helper is running; stop it first")
)

// SignatureError is a signature that is there and wrong. It can never be
// overridden: only a missing signature can.
type SignatureError struct {
	// File is what failed, "manifest.json" or "checksums.txt".
	File string
	Err  error
}

func (e *SignatureError) Error() string {
	return e.File + " does not carry a valid signature: " + e.Err.Error()
}
func (e *SignatureError) Unwrap() error { return e.Err }

// MismatchError is something the source served that disagrees with what was
// signed, or with the rest of what it served: a manifest signed for another
// version than the one asked for, a checksums.txt that gives another hash, a
// URL that is not the file's own, an entry that is listed twice. It is never
// overridable.
type MismatchError struct{ Msg string }

func (e *MismatchError) Error() string { return e.Msg }

func mismatchf(format string, args ...any) error {
	return &MismatchError{fmt.Sprintf(format, args...)}
}

// UnsignedError is a release with no signature. It carries the plan, so the
// person is shown the archive's hash before they choose to go on.
type UnsignedError struct{ Plan *Plan }

func (e *UnsignedError) Error() string {
	return fmt.Sprintf("%s %s is not signed (no manifest.json.sig), so nothing proves who built it", e.Plan.Entry.Name, e.Plan.Version)
}

// Plan is what an install would do, worked out from the signed manifest and
// before the archive is downloaded.
type Plan struct {
	Entry   Entry
	Version string
	// Archive is the asset's file name and URL its address.
	Archive string
	URL     string
	// SHA256 and Size are the archive's hash and size as the manifest gives
	// them.
	SHA256 string
	Size   int64
	// Date is when the manifest says the release was made.
	Date time.Time
	// Signed is true when manifest.json carried a signature that checked out
	// against the keys compiled into Flockdeck.
	Signed bool
	// Installed is the installed version, or "" when there is none.
	Installed string
	// Repair is true when this is the installed version again, because what is
	// on disk no longer passes VerifyInstall. The folder is replaced by a
	// checked copy.
	Repair bool
}

const (
	maxPointer  = 1 << 10
	maxManifest = 1 << 20
	// futureSkew is how far ahead of this machine's clock a manifest's date may
	// be before it is not believed.
	futureSkew = 24 * time.Hour
)

// latestVersion resolves "the latest" through latest.json. The file is not
// signed, so all it does is say which version's manifest to read; nothing else
// is taken from it, and the manifest must be signed for the version it names.
func (in *Installer) latestVersion(ctx context.Context, e Entry) (string, error) {
	data, err := in.fetchSmall(ctx, in.allowFor(e), e.Source+"/latest.json", maxPointer)
	if err != nil {
		return "", fmt.Errorf("fetch latest.json: %w", err)
	}
	tag, err := selfupdate.CheckPointer(data)
	if err != nil {
		return "", err
	}
	version := strings.TrimPrefix(tag, "v")
	if !strings.HasPrefix(tag, "v") || !validVersion(version) {
		return "", fmt.Errorf("latest.json names %q, which is not a release tag like v1.2.3", tag)
	}
	if strings.Contains(version, "-") {
		// A candidate is never the latest.
		return "", fmt.Errorf("latest.json names %s, a pre-release, which is never the latest", tag)
	}
	return version, nil
}

// Plan fetches and checks a release's signed manifest and applies the version
// rules. version is "" for the latest release. It downloads no archive and
// writes nothing.
func (in *Installer) Plan(ctx context.Context, id, version string) (*Plan, error) {
	return in.PlanVersion(ctx, id, version, version != "")
}

// PlanVersion is Plan with the downgrade rules chosen by the caller instead of
// by whether a version was named. The command line lets a person name an older
// version; the window does not, and it names the version it was shown (so the
// release cannot change under the confirmation) without that being a request
// to go backwards. mayDowngrade lifts the "older than installed" and "older
// than the newest signed version ever installed here" refusals, never
// MinVersion.
func (in *Installer) PlanVersion(ctx context.Context, id, version string, mayDowngrade bool) (*Plan, error) {
	e, ok := in.lookup(id)
	if !ok {
		return nil, fmt.Errorf("%q is not a helper Flockdeck knows", id)
	}
	if version != "" && !validVersion(version) {
		return nil, fmt.Errorf("%q is not a version like 1.2.3", version)
	}
	if version == "" {
		v, err := in.latestVersion(ctx, e)
		if err != nil {
			return nil, err
		}
		version, mayDowngrade = v, false
	}
	return in.plan(ctx, e, version, mayDowngrade)
}

// CheckUpdate says which version an install of the helper would give if it were
// run now, and "" when that is not newer than what is installed. It reads
// latest.json first and fetches and verifies the signed manifest only when the
// version named is newer, so asking costs one small request when there is
// nothing new. Nothing is shown from a manifest that has not passed every check
// Plan makes.
func (in *Installer) CheckUpdate(ctx context.Context, id string) (string, error) {
	e, ok := in.lookup(id)
	if !ok {
		return "", fmt.Errorf("%q is not a helper Flockdeck knows", id)
	}
	v, err := in.latestVersion(ctx, e)
	if err != nil {
		return "", err
	}
	if installed, ok := in.store.Current(e.ID); ok && !selfupdate.Newer("v"+v, "v"+installed) {
		return "", nil
	}
	p, err := in.plan(ctx, e, v, false)
	if err != nil {
		return "", err
	}
	return p.Version, nil
}

// plan is Plan for a version that has been settled, by name or through
// latest.json.
func (in *Installer) plan(ctx context.Context, e Entry, version string, mayDowngrade bool) (*Plan, error) {
	allow := in.allowFor(e)
	tag := "v" + version
	base := e.Source + "/" + tag + "/"

	mdata, err := in.fetchSmall(ctx, allow, base+"manifest.json", maxManifest)
	if err != nil {
		return nil, fmt.Errorf("fetch manifest.json: %w", err)
	}
	signed := false
	msig, err := in.fetchSmall(ctx, allow, base+"manifest.json.sig", maxSignature)
	switch {
	case err == nil:
		// Fails closed, over the exact bytes fetched: no trusted keys, a bad
		// signature, a short or empty one and one over other bytes all end
		// here, and none of them can be overridden.
		if err := selfupdate.VerifyAny(selfupdate.TrustedKeys(), mdata, msig); err != nil {
			return nil, &SignatureError{File: "manifest.json", Err: err}
		}
		signed = true
	case errors.Is(err, errNotFound):
		// The one case with a way forward: no signature at all.
	default:
		return nil, fmt.Errorf("fetch manifest.json.sig: %w", err)
	}
	m, err := selfupdate.ParseManifest(mdata)
	if err != nil {
		return nil, err
	}
	if m.Version != tag {
		// A signed manifest for another release served at this release's
		// address is a replay, and a latest.json that names a release whose
		// manifest says another is lying.
		return nil, mismatchf("the manifest served as %s is for %s", tag, m.Version)
	}
	if m.Date.IsZero() {
		return nil, mismatchf("the manifest for %s has no date", tag)
	}
	if now := in.now(); m.Date.After(now.Add(futureSkew)) {
		return nil, mismatchf("the manifest for %s is dated %s, more than a day ahead of this machine's clock (%s). Either the clock is wrong, which is the usual cause, or the manifest is not what it says", tag, m.Date.Format(time.RFC3339), now.Format(time.RFC3339))
	}

	file, err := in.findFile(e, m, tag)
	if err != nil {
		return nil, err
	}
	if err := in.crossCheck(ctx, e, base, file, signed); err != nil {
		return nil, err
	}

	installed, _ := in.store.Current(e.ID)
	if err := in.store.checkSignedRequired(e, version, signed); err != nil {
		return nil, err
	}
	// The installed version again, because it no longer passes the check made
	// before a start, is a repair and not "already installed".
	repair := installed != "" && installed == version && in.store.VerifyInstall(e, in.goos) != nil
	against := installed
	if repair {
		against = ""
	}
	if err := checkVersion(e, version, against, mayDowngrade); err != nil {
		return nil, err
	}
	// A repair puts back the version that is installed, which is not a downgrade
	// whatever the mark is.
	if !repair {
		if err := checkHighWater(version, in.store.HighWater(e.ID), mayDowngrade); err != nil {
			return nil, err
		}
	}
	return &Plan{
		Entry: e, Version: version, Archive: file.Name, URL: file.URL, SHA256: strings.ToLower(file.SHA256), Size: file.Size, Date: m.Date,
		Signed: signed, Installed: installed, Repair: repair,
	}, nil
}

// findFile finds this platform's archive in the manifest, which has to list it
// exactly once, at the address the source serves it from, with a size that is
// within the entry's limit. A name listed twice anywhere in the manifest is
// refused, since which of two entries is meant is not something to guess.
func (in *Installer) findFile(e Entry, m *selfupdate.Manifest, tag string) (selfupdate.ManifestFile, error) {
	seen := map[string]bool{}
	for _, f := range m.Files {
		if seen[f.Name] {
			return selfupdate.ManifestFile{}, mismatchf("the manifest lists %s more than once", f.Name)
		}
		seen[f.Name] = true
	}
	want := e.ArchiveName(strings.TrimPrefix(tag, "v"), in.goos, in.goarch)
	for _, f := range m.Files {
		if f.Name != want {
			continue
		}
		if f.URL != e.Source+"/"+tag+"/"+want {
			return f, mismatchf("the manifest lists %s at %s, not at %s/%s/%s", want, describeURLString(f.URL), e.Source, tag, want)
		}
		if f.Size <= 0 || f.Size > e.maxArchive() {
			return f, mismatchf("the manifest gives %s a size of %d bytes, outside the 1 to %d allowed", want, f.Size, e.maxArchive())
		}
		return f, nil
	}
	return selfupdate.ManifestFile{}, fmt.Errorf("%s %s: %w", e.Name, tag, ErrNoAsset)
}

// describeURLString is a URL for a message, without its query.
func describeURLString(raw string) string {
	if u, err := url.Parse(raw); err == nil {
		return u.Scheme + "://" + u.Host + u.Path
	}
	return "an address that is not a URL"
}

// crossCheck reads checksums.txt, which is signed separately, and requires it
// to give the archive the same hash as the manifest. When the manifest is
// signed the checksums must be too, and their signature is held to the same
// rule: both are signed, so a disagreement or a missing signature means
// something served is not what was released.
func (in *Installer) crossCheck(ctx context.Context, e Entry, base string, file selfupdate.ManifestFile, signed bool) error {
	allow := in.allowFor(e)
	sums, err := in.fetchSmall(ctx, allow, base+"checksums.txt", maxChecksums)
	if err != nil {
		return fmt.Errorf("fetch checksums.txt: %w", err)
	}
	sig, err := in.fetchSmall(ctx, allow, base+"checksums.txt.sig", maxSignature)
	switch {
	case err == nil:
		if verr := selfupdate.VerifyAny(selfupdate.TrustedKeys(), sums, sig); verr != nil && signed {
			return &SignatureError{File: "checksums.txt", Err: verr}
		}
	case errors.Is(err, errNotFound):
		if signed {
			return &SignatureError{File: "checksums.txt", Err: errors.New("the manifest is signed, but checksums.txt.sig is missing")}
		}
	default:
		return fmt.Errorf("fetch checksums.txt.sig: %w", err)
	}
	got := ""
	count := 0
	for _, line := range strings.Split(string(sums), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && f[1] == file.Name {
			got = strings.ToLower(f[0])
			count++
		}
	}
	switch {
	case count == 0:
		return mismatchf("checksums.txt does not list %s", file.Name)
	case count > 1:
		return mismatchf("checksums.txt lists %s more than once", file.Name)
	case got != strings.ToLower(file.SHA256):
		return mismatchf("checksums.txt gives %s a SHA-256 other than the manifest does", file.Name)
	}
	return nil
}

// Install installs a helper: version "" is the latest. An unsigned release is
// installed only when allowUnsigned is true, and then it is recorded as
// unsigned. Nothing from the archive is run.
func (in *Installer) Install(ctx context.Context, id, version string, allowUnsigned bool) (*InstallInfo, error) {
	plan, err := in.Plan(ctx, id, version)
	if err != nil {
		return nil, err
	}
	if !plan.Signed && !allowUnsigned {
		return nil, &UnsignedError{plan}
	}
	return in.InstallPlan(ctx, plan)
}

// stage runs the install hook, which tests use to fail an install at a stage.
func (in *Installer) stage(name string) error {
	if in.hook == nil {
		return nil
	}
	return in.hook(name)
}

// InstallPlan carries out a plan: download, verify, extract into a staging
// folder, then swap in. A plan that is not signed is carried out as an
// override, so the caller has to have asked for that.
func (in *Installer) InstallPlan(ctx context.Context, p *Plan) (_ *InstallInfo, err error) {
	id, st := p.Entry.ID, in.store
	if !validID(id) || !validVersion(p.Version) {
		return nil, fmt.Errorf("refusing to install %q %q", id, p.Version)
	}
	// The entry is the catalogue's, never the plan's own copy: a plan built by
	// hand with RequireSigned cleared or another Source must not change the rules.
	e, known := in.lookup(id)
	if !known {
		return nil, fmt.Errorf("%q is not a helper Flockdeck knows", id)
	}
	if err := st.checkSignedRequired(e, p.Version, p.Signed); err != nil {
		return nil, err
	}
	if !in.begin(id) {
		return nil, fmt.Errorf("%w: it is already being installed", ErrBusy)
	}
	defer in.end(id)
	if in.busy != nil && in.busy(id) {
		return nil, ErrBusy
	}
	if _, running := st.RunningPID(id); running {
		return nil, ErrBusy
	}
	if cur, _ := st.Current(id); cur == p.Version && !p.Repair {
		return nil, fmt.Errorf("%w: %s", ErrAlreadyInstalled, p.Version)
	}
	if err := os.MkdirAll(st.appDir(id), 0o700); err != nil {
		return nil, err
	}
	st.SweepStaging(id, in.now())
	staging, err := os.MkdirTemp(st.appDir(id), "staging.new-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(staging)

	archive := filepath.Join(staging, "archive"+archiveExt(in.goos))
	if err := in.download(ctx, in.allowFor(e), p.URL, archive, e.maxArchive(), p.SHA256, p.Size); err != nil {
		return nil, err
	}
	if err := in.stage("downloaded"); err != nil {
		return nil, err
	}

	top := e.TopFolder(p.Version, in.goos, in.goarch)
	unpacked := filepath.Join(staging, "x")
	if err := os.Mkdir(unpacked, 0o755); err != nil {
		return nil, err
	}
	rules := ExtractRules{
		Top: top, Binary: e.BinaryName(in.goos),
		MaxFiles: e.maxFiles(), MaxTotal: e.maxUnpacked(), MaxFile: e.maxUnpacked(), MaxRatio: maxRatio,
	}
	if err := extractVerified(archive, p.SHA256, unpacked, rules); err != nil {
		return nil, err
	}
	if err := in.stage("extracted"); err != nil {
		return nil, err
	}

	built := filepath.Join(unpacked, top)
	if err := setModes(built, e.BinaryName(in.goos)); err != nil {
		return nil, err
	}
	binSum, err := fileSHA256(filepath.Join(built, e.BinaryName(in.goos)))
	if err != nil {
		return nil, err
	}
	info := InstallInfo{Version: p.Version, Source: p.URL, SHA256: p.SHA256, BinarySHA256: binSum, Signed: p.Signed, InstalledAt: in.now().UTC()}
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(built, "install.json"), data, 0o644); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(st.versionsDir(id), 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(st.DataDir(id), 0o700); err != nil {
		return nil, err
	}

	previous, _ := st.Current(id)
	if previous == p.Version && !p.Repair {
		return nil, fmt.Errorf("%w: %s", ErrAlreadyInstalled, p.Version)
	}
	final := st.versionDir(id, p.Version)
	// Whatever is at the final name is either what an interrupted install left
	// or, for a repair, the version being replaced. It is moved aside inside the
	// staging folder, which is removed at the end, and put back if the install
	// fails, so a failed repair leaves what was there.
	aside := ""
	if _, statErr := os.Lstat(final); statErr == nil {
		aside = filepath.Join(staging, "aside")
		if err := store.RenameWithRetry(final, aside); err != nil {
			return nil, err
		}
	}
	if err := store.RenameWithRetry(built, final); err != nil {
		if aside != "" {
			_ = store.RenameWithRetry(aside, final)
		}
		return nil, err
	}
	defer func() {
		// A version that was swapped in and not made current is not wanted, and
		// the old one is still current.
		if err != nil {
			os.RemoveAll(final)
			if aside != "" {
				_ = store.RenameWithRetry(aside, final)
			}
		}
	}()
	if err := in.stage("renamed"); err != nil {
		return nil, err
	}
	// The record that a signed version was installed goes down before the
	// pointer moves, and an install that cannot write it fails: with the pointer
	// first, a failure here would leave a signed install that nothing remembers,
	// and the next unsigned release would be accepted for it. The high-water mark
	// rises only once the pointer has moved.
	if info.Signed {
		if err := st.recordSigned(id); err != nil {
			return nil, err
		}
	}
	cur, _ := json.Marshal(currentFile{Version: p.Version})
	if err := atomicWrite(st.currentFile(id), cur, 0o644); err != nil {
		return nil, err
	}
	if info.Signed {
		if err := st.raiseHighWater(id, p.Version); err != nil {
			in.restoreCurrent(id, previous)
			return nil, err
		}
	}
	if err := in.stage("current"); err != nil {
		// current.json is already written when a hook fails here, which is a
		// test's way of asking for a crash after the last step. Put the old
		// pointer back so the failure leaves the old version current.
		in.restoreCurrent(id, previous)
		return nil, err
	}
	// A repair replaces the installed version in place: nothing is older than
	// it that this install made, and the folder kept for a rollback is not its
	// to remove.
	if !p.Repair {
		in.prune(id, p.Version, previous)
	}
	return &info, nil
}

// restoreCurrent writes the previous version back, or removes the pointer if
// there was none.
func (in *Installer) restoreCurrent(id, previous string) {
	if previous == "" {
		os.Remove(in.store.currentFile(id))
		return
	}
	cur, _ := json.Marshal(currentFile{Version: previous})
	_ = writeFileAtomic(in.store.currentFile(id), cur, 0o644)
}

// prune removes every version but the new one and the one before it, which
// stays for a rollback.
func (in *Installer) prune(id, keepA, keepB string) {
	entries, err := os.ReadDir(in.store.versionsDir(id))
	if err != nil {
		return
	}
	for _, e := range entries {
		if n := e.Name(); n != keepA && n != keepB {
			_ = os.RemoveAll(filepath.Join(in.store.versionsDir(id), n))
		}
	}
}

// setModes sets what an unpacked release's files and folders are readable and
// executable as: folders 0755, the binary 0755, everything else 0644. It is
// explicit so a umask or an archive's own modes decide nothing.
func setModes(root, binary string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		mode := fs.FileMode(0o644)
		switch {
		case d.IsDir():
			mode = 0o755
		case path == filepath.Join(root, binary):
			mode = 0o755
		}
		return os.Chmod(path, mode)
	})
}

// maxRatio is how many times its own size an archive may unpack to. The
// release binaries are compressed 2 to 4 times; 100 leaves room for a very
// different build and stops a bomb.
const maxRatio = 100

// extractVerified opens the downloaded archive once, hashes what it opened
// against the signed line again, and extracts from that same handle. The file
// was checked when it was downloaded, but it has sat in a folder other
// programs running as the user can write to since; checking the handle that is
// then read from means a swap in between is caught, and a swap after is not
// seen at all because nothing opens the path again.
func extractVerified(path, wantSHA, dir string, rules ExtractRules) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != wantSHA {
		return &ChecksumError{Got: got, Want: wantSHA}
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	rules.CompressedSize = n
	return ExtractFile(f, strings.HasSuffix(path, ".zip"), dir, rules)
}

// GOOS is the operating system whose archive and program name this installer
// uses.
func (in *Installer) GOOS() string { return in.goos }
