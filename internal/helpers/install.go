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
	"time"

	"github.com/jmwri/flockdeck/internal/selfupdate"
	"github.com/jmwri/flockdeck/internal/store"
)

// githubBase is where releases are fetched from.
const githubBase = "https://github.com"

// Options configure an Installer. The zero value, with a Store, is the
// production setup: github.com, the compiled-in keys, this platform.
type Options struct {
	Store *Store
	// Base is the site releases are fetched from. Tests point it at a fake.
	Base string
	// AllowURL decides which URLs may be fetched or redirected to. Nil means
	// DefaultAllowURL.
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
	base         string
	allow        func(*url.URL) bool
	client       *http.Client
	stall        time.Duration
	goos, goarch string
	busy         func(string) bool
	now          func() time.Time
	hook         func(string) error
	lookup       func(string) (Entry, bool)
}

// NewInstaller makes an Installer from options.
func NewInstaller(o Options) *Installer {
	in := &Installer{
		store: o.Store, base: strings.TrimRight(o.Base, "/"), allow: o.AllowURL, stall: o.Stall,
		goos: o.GOOS, goarch: o.GOARCH, busy: o.Busy, now: o.Now, hook: o.Hook, lookup: o.Lookup,
	}
	if in.base == "" {
		in.base = githubBase
	}
	if in.allow == nil {
		in.allow = DefaultAllowURL
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
	in.client = newClient(in.allow)
	return in
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
type SignatureError struct{ Err error }

func (e *SignatureError) Error() string {
	return "checksums.txt does not carry a valid signature: " + e.Err.Error()
}
func (e *SignatureError) Unwrap() error { return e.Err }

// UnsignedError is a release with no signature. It carries the plan, so the
// person is shown the archive's hash before they choose to go on.
type UnsignedError struct{ Plan *Plan }

func (e *UnsignedError) Error() string {
	return fmt.Sprintf("%s %s is not signed (no checksums.txt.sig), so nothing proves who built it", e.Plan.Entry.Name, e.Plan.Version)
}

// Plan is what an install would do, worked out from the signed checksums and
// before the archive is downloaded.
type Plan struct {
	Entry   Entry
	Version string
	// Archive is the asset's file name and URL its address.
	Archive string
	URL     string
	// SHA256 is the archive's hash as checksums.txt gives it.
	SHA256 string
	// Signed is true when checksums.txt carried a signature that checked out
	// against the keys compiled into Flockdeck.
	Signed bool
	// Installed is the installed version, or "" when there is none.
	Installed string
	// Explicit is true when the version was named, not "the latest".
	Explicit bool
}

func (in *Installer) releaseURL(e Entry, version, file string) string {
	if version == "" {
		return fmt.Sprintf("%s/%s/releases/latest/download/%s", in.base, e.Repo, file)
	}
	return fmt.Sprintf("%s/%s/releases/download/v%s/%s", in.base, e.Repo, version, file)
}

// Plan fetches and checks a release's checksums and applies the version rules.
// version is "" for the latest release. It downloads no archive and writes
// nothing.
func (in *Installer) Plan(ctx context.Context, id, version string) (*Plan, error) {
	e, ok := in.lookup(id)
	if !ok {
		return nil, fmt.Errorf("%q is not a helper Flockdeck knows", id)
	}
	if version != "" && !validVersion(version) {
		return nil, fmt.Errorf("%q is not a version like 1.2.3", version)
	}
	sumsURL := in.releaseURL(e, version, "checksums.txt")
	body, err := in.fetchSmall(ctx, sumsURL, maxChecksums)
	if err != nil {
		return nil, fmt.Errorf("fetch checksums.txt: %w", err)
	}
	signed := false
	sig, err := in.fetchSmall(ctx, sumsURL+".sig", maxSignature)
	switch {
	case err == nil:
		// Fails closed: no trusted keys, a bad signature, a short one and one
		// over other bytes all end here, and none of them can be overridden.
		if err := selfupdate.VerifyAny(selfupdate.TrustedKeys(), body, sig); err != nil {
			return nil, &SignatureError{err}
		}
		signed = true
	case errors.Is(err, errNotFound):
		// The one case with a way forward: no signature at all.
	default:
		return nil, fmt.Errorf("fetch checksums.txt.sig: %w", err)
	}

	name, sum, found, err := in.findAsset(e, body)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("%s: %w", e.Name, ErrNoAsset)
	}
	got := in.versionOf(e, name)
	if version != "" && got != version {
		// A signed file for another release served at this release's address
		// is a replay, not the release that was asked for.
		return nil, fmt.Errorf("the checksums at v%s are for %s, not for %s", version, got, version)
	}
	installed, _ := in.store.Current(e.ID)
	if err := in.store.checkSignedRequired(e, got, signed); err != nil {
		return nil, err
	}
	if err := checkVersion(e, got, installed, version != ""); err != nil {
		return nil, err
	}
	return &Plan{
		Entry: e, Version: got, Archive: name, URL: in.releaseURL(e, got, name), SHA256: sum,
		Signed: signed, Installed: installed, Explicit: version != "",
	}, nil
}

// versionOf reads the version out of an archive name that findAsset matched.
func (in *Installer) versionOf(e Entry, name string) string {
	m := e.assetPattern(in.goos, in.goarch).FindStringSubmatch(name)
	if m == nil {
		return ""
	}
	return m[1]
}

// findAsset finds this platform's archive in checksums.txt. It insists on
// exactly one: a file that lists two versions for one platform is not one to
// guess from.
func (in *Installer) findAsset(e Entry, body []byte) (name, sum string, found bool, err error) {
	re := e.assetPattern(in.goos, in.goarch)
	for _, line := range strings.Split(string(body), "\n") {
		f := strings.Fields(line)
		if len(f) != 2 || !re.MatchString(f[1]) {
			continue
		}
		if found {
			return "", "", false, fmt.Errorf("checksums.txt lists more than one %s archive for %s/%s", e.Name, in.goos, in.goarch)
		}
		sum = strings.ToLower(f[0])
		if b, derr := hex.DecodeString(sum); derr != nil || len(b) != 32 {
			return "", "", false, fmt.Errorf("checksums.txt lists %s without a SHA-256", f[1])
		}
		name, found = f[1], true
	}
	if found && !validVersion(in.versionOf(e, name)) {
		return "", "", false, fmt.Errorf("%s does not carry a version", name)
	}
	return name, sum, found, nil
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
	e, id, st := p.Entry, p.Entry.ID, in.store
	if !validID(id) || !validVersion(p.Version) {
		return nil, fmt.Errorf("refusing to install %q %q", id, p.Version)
	}
	if err := st.checkSignedRequired(e, p.Version, p.Signed); err != nil {
		return nil, err
	}
	if in.busy != nil && in.busy(id) {
		return nil, ErrBusy
	}
	if _, running := st.RunningPID(id); running {
		return nil, ErrBusy
	}
	if cur, _ := st.Current(id); cur == p.Version {
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
	if err := in.download(ctx, p.URL, archive, e.maxArchive(), p.SHA256); err != nil {
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
	if previous == p.Version {
		return nil, fmt.Errorf("%w: %s", ErrAlreadyInstalled, p.Version)
	}
	final := st.versionDir(id, p.Version)
	// What is already at the final name is not the installed version (an
	// install of that version was refused earlier), so it is what an
	// interrupted install left. It is replaced.
	if _, statErr := os.Lstat(final); statErr == nil {
		if err := os.RemoveAll(final); err != nil {
			return nil, err
		}
	}
	if err := store.RenameWithRetry(built, final); err != nil {
		return nil, err
	}
	defer func() {
		// A version that was swapped in and not made current is not wanted,
		// and the old one is still current.
		if err != nil {
			os.RemoveAll(final)
		}
	}()
	if err := in.stage("renamed"); err != nil {
		return nil, err
	}
	cur, _ := json.Marshal(currentFile{Version: p.Version})
	if err := writeFileAtomic(st.currentFile(id), cur, 0o644); err != nil {
		return nil, err
	}
	if err := in.stage("current"); err != nil {
		// current.json is already written when a hook fails here, which is a
		// test's way of asking for a crash after the last step. Put the old
		// pointer back so the failure leaves the old version current.
		in.restoreCurrent(id, previous)
		return nil, err
	}
	if info.Signed {
		// Best effort: a signed install that cannot write this has still
		// installed, and the record is what keeps it from being downgraded.
		_ = st.markSigned(id, p.Version)
	}
	in.prune(id, p.Version, previous)
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
