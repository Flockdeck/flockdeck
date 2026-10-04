package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/jmwri/flockdeck/internal/helpers"
	"github.com/jmwri/flockdeck/internal/selfupdate"
)

// helperRelease serves a lens release for this platform from a fake GitHub.
// Only the platforms that use tar.gz are built; a Windows run installs a zip,
// which the helpers package's own tests cover, so the CLI tests pin the
// platform to linux/amd64 through the installer's options.
type helperRelease struct {
	mu    sync.Mutex
	files map[string][]byte
	srv   *httptest.Server
	key   ed25519.PrivateKey
}

func newHelperRelease(t *testing.T) *helperRelease {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(selfupdate.TrustKeysForTest(pub, nil))
	r := &helperRelease{files: map[string][]byte{}, key: priv}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		b, ok := r.files[req.URL.Path]
		r.mu.Unlock()
		if !ok {
			http.NotFound(w, req)
			return
		}
		_, _ = w.Write(b)
	}))
	t.Cleanup(r.srv.Close)
	return r
}

// publish serves version as the latest and at its own tag. sign picks whether
// checksums.txt.sig is served, and badSig whether it is wrong.
func (r *helperRelease) publish(t *testing.T, version string, sign, badSig bool) {
	t.Helper()
	top := fmt.Sprintf("lens_%s_linux_amd64", version)
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range []struct{ name, body string }{{top + "/lens", "a program"}, {top + "/README.md", "readme"}} {
		_ = tw.WriteHeader(&tar.Header{Name: f.name, Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(f.body))})
		_, _ = tw.Write([]byte(f.body))
	}
	_ = tw.Close()
	_ = gz.Close()
	sum := sha256.Sum256(buf.Bytes())
	sums := []byte(hex.EncodeToString(sum[:]) + "  " + top + ".tar.gz\n")
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, base := range []string{"/Flockdeck/lens/releases/download/v" + version + "/", "/Flockdeck/lens/releases/latest/download/"} {
		r.files[base+"checksums.txt"] = sums
		delete(r.files, base+"checksums.txt.sig")
		if sign {
			signed := sums
			if badSig {
				signed = append([]byte("tampered "), sums...)
			}
			r.files[base+"checksums.txt.sig"] = selfupdate.Sign(r.key, signed)
		}
	}
	r.files["/Flockdeck/lens/releases/download/v"+version+"/"+top+".tar.gz"] = buf.Bytes()
}

type cliRig struct {
	c      *helperCLI
	out    *bytes.Buffer
	errOut *bytes.Buffer
	store  *helpers.Store
	rel    *helperRelease
	asked  *[][]string
}

func newCLIRig(t *testing.T) *cliRig {
	t.Helper()
	rel := newHelperRelease(t)
	st := &helpers.Store{Root: t.TempDir()}
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	var asked [][]string
	c := &helperCLI{
		out: out, errOut: errOut, in: strings.NewReader(""), store: st,
		installer: helpers.NewInstaller(helpers.Options{
			Store: st, Base: rel.srv.URL, GOOS: "linux", GOARCH: "amd64",
			AllowURL: func(u *url.URL) bool { return u.Hostname() == "127.0.0.1" },
		}),
		instance: func() (string, string, error) { return "", "", nil },
		request: func(base, token, action, id string) (helpers.Status, error) {
			asked = append(asked, []string{base, token, action, id})
			return helpers.Status{ID: id, State: helpers.StateRunning, URL: "http://127.0.0.1:5000/"}, nil
		},
	}
	return &cliRig{c: c, out: out, errOut: errOut, store: st, rel: rel, asked: &asked}
}

func (r *cliRig) run(args ...string) error {
	r.out.Reset()
	r.errOut.Reset()
	return r.c.run(args)
}

func TestHelpersInstallAndList(t *testing.T) {
	r := newCLIRig(t)
	r.rel.publish(t, "0.4.0", true, false)

	if err := r.run("list"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.out.String(), "lens") || !strings.Contains(r.out.String(), "not installed") {
		t.Fatalf("list = %q", r.out)
	}

	if err := r.run("install", "-yes", "lens"); err != nil {
		t.Fatalf("install: %v\n%s", err, r.out)
	}
	for _, want := range []string{"Install lens 0.4.0", "From:", "/Flockdeck/lens/releases/download/v0.4.0/lens_0.4.0_linux_amd64.tar.gz",
		"checked against Flockdeck's release key", "SHA-256:", "It may:", "Listens on 127.0.0.1 only", "not a sandbox", "Installed lens 0.4.0.", "flockdeck helpers start lens"} {
		if !strings.Contains(r.out.String(), want) {
			t.Errorf("the install output is missing %q:\n%s", want, r.out)
		}
	}
	if v, ok := r.store.Current("lens"); !ok || v != "0.4.0" {
		t.Fatalf("current = %q, %v", v, ok)
	}

	if err := r.run("list"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.out.String(), "0.4.0") || !strings.Contains(r.out.String(), "stopped") {
		t.Fatalf("list = %q", r.out)
	}
	// Installing again is not an error, and not a download.
	err := r.run("install", "-yes", "lens")
	if err == nil || !strings.Contains(err.Error(), "already installed") {
		t.Fatalf("err = %v", err)
	}
}

func TestHelpersInstallNeedsAnAnswer(t *testing.T) {
	r := newCLIRig(t)
	r.rel.publish(t, "0.4.0", true, false)
	// No terminal and no -yes: nobody to ask, and a pipe is not an answer.
	err := r.run("install", "lens")
	if err == nil || !strings.Contains(err.Error(), "-yes") {
		t.Fatalf("err = %v", err)
	}
	if _, ok := r.store.Current("lens"); ok {
		t.Fatal("installed without being asked")
	}
	// At a terminal, "n" and an empty answer decline.
	r.c.interactive = true
	for _, answer := range []string{"n\n", "\n", ""} {
		r.c.in = strings.NewReader(answer)
		if err := r.run("install", "lens"); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(r.out.String(), "Not installed.") {
			t.Fatalf("answer %q: %s", answer, r.out)
		}
		if _, ok := r.store.Current("lens"); ok {
			t.Fatalf("answer %q installed it", answer)
		}
	}
	r.c.in = strings.NewReader("y\n")
	if err := r.run("install", "lens"); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.store.Current("lens"); !ok {
		t.Fatal("a yes did not install")
	}
}

func TestHelpersInstallRefusesUnsignedUnlessOverridden(t *testing.T) {
	r := newCLIRig(t)
	r.rel.publish(t, "0.4.0", false, false)
	err := r.run("install", "-yes", "lens")
	if err == nil || !strings.Contains(err.Error(), "-allow-unsigned") {
		t.Fatalf("err = %v", err)
	}
	for _, want := range []string{"Signature:  NONE", "WARNING: this release is not signed", "SHA-256:", "does not prove who built it"} {
		if !strings.Contains(r.out.String(), want) {
			t.Errorf("the refusal does not show %q:\n%s", want, r.out)
		}
	}
	if _, ok := r.store.Current("lens"); ok {
		t.Fatal("installed an unsigned release without the override")
	}

	// -yes is not the override: the override is its own flag.
	if err := r.run("install", "-allow-unsigned", "-yes", "lens"); err != nil {
		t.Fatalf("with the override: %v\n%s", err, r.out)
	}
	if !strings.Contains(r.out.String(), "Installed lens 0.4.0 (unsigned).") {
		t.Fatalf("output = %s", r.out)
	}
	if info, _ := r.store.Info("lens"); info.Signed {
		t.Fatal("recorded as signed")
	}
	if err := r.run("list"); err != nil || !strings.Contains(r.out.String(), "Unsigned") {
		t.Fatalf("list does not say Unsigned: %q, %v", r.out, err)
	}
}

func TestHelpersInstallNeverOverridesABadSignature(t *testing.T) {
	r := newCLIRig(t)
	r.rel.publish(t, "0.4.0", true, true)
	err := r.run("install", "-allow-unsigned", "-yes", "lens")
	if err == nil || !strings.Contains(err.Error(), "cannot be overridden") {
		t.Fatalf("err = %v", err)
	}
	if _, ok := r.store.Current("lens"); ok {
		t.Fatal("installed a release with a bad signature")
	}
}

func TestHelpersInstallVersionFlag(t *testing.T) {
	r := newCLIRig(t)
	r.rel.publish(t, "0.4.0", true, false)
	r.rel.publish(t, "0.5.0", true, false)
	if err := r.run("install", "-yes", "-version=v0.4.0", "lens"); err != nil {
		t.Fatalf("%v\n%s", err, r.out)
	}
	if v, _ := r.store.Current("lens"); v != "0.4.0" {
		t.Fatalf("current = %q", v)
	}
	if err := r.run("list", "-check"); err != nil || !strings.Contains(r.out.String(), "Update available 0.5.0") {
		t.Fatalf("list -check = %q, %v", r.out, err)
	}
}

func TestHelpersUninstall(t *testing.T) {
	r := newCLIRig(t)
	r.rel.publish(t, "0.4.0", true, false)
	if err := r.run("install", "-yes", "lens"); err != nil {
		t.Fatal(err)
	}
	note := r.store.DataDir("lens") + string(os.PathSeparator) + "notes.db"
	if err := os.WriteFile(note, []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := r.run("uninstall", "lens"); err == nil || !strings.Contains(err.Error(), "-yes") {
		t.Fatalf("err = %v", err)
	}
	if err := r.run("uninstall", "-yes", "lens"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.out.String(), "Removed lens.") || !strings.Contains(r.out.String(), r.store.DataDir("lens")) ||
		!strings.Contains(r.out.String(), "-purge-data") {
		t.Fatalf("output = %s", r.out)
	}
	if _, err := os.Stat(note); err != nil {
		t.Fatal("the data was deleted by a plain uninstall")
	}
	if err := r.run("uninstall", "-yes", "lens"); err != nil || !strings.Contains(r.out.String(), "not installed") {
		t.Fatalf("%v: %s", err, r.out)
	}
	// Only a purge removes it, and says so.
	if err := r.run("uninstall", "-purge-data", "-yes", "lens"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(note); err == nil {
		t.Fatal("-purge-data left the data")
	}
}

func TestHelpersUninstallPurgeAsksAboutTheData(t *testing.T) {
	r := newCLIRig(t)
	r.rel.publish(t, "0.4.0", true, false)
	if err := r.run("install", "-yes", "lens"); err != nil {
		t.Fatal(err)
	}
	r.c.interactive = true
	r.c.in = strings.NewReader("n\n")
	if err := r.run("uninstall", "-purge-data", "lens"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.out.String(), "DELETE its data folder") || !strings.Contains(r.out.String(), "Not removed.") {
		t.Fatalf("output = %s", r.out)
	}
	if _, ok := r.store.Current("lens"); !ok {
		t.Fatal("a no removed it")
	}
}

func TestHelpersControlNeedsARunningFlockdeck(t *testing.T) {
	r := newCLIRig(t)
	for _, action := range []string{"start", "stop", "open"} {
		err := r.run(action, "lens")
		if err == nil || !strings.Contains(err.Error(), "Flockdeck is not running") {
			t.Fatalf("%s: err = %v", action, err)
		}
	}
	if len(*r.asked) != 0 {
		t.Fatalf("an instance was asked with none running: %v", *r.asked)
	}
}

func TestHelpersControlAsksTheInstanceWithItsToken(t *testing.T) {
	r := newCLIRig(t)
	r.c.instance = func() (string, string, error) { return "http://127.0.0.1:9", "tok", nil }
	for _, action := range []string{"start", "open", "stop"} {
		if err := r.run(action, "lens"); err != nil {
			t.Fatalf("%s: %v", action, err)
		}
	}
	want := [][]string{
		{"http://127.0.0.1:9", "tok", "start", "lens"},
		{"http://127.0.0.1:9", "tok", "open", "lens"},
		{"http://127.0.0.1:9", "tok", "stop", "lens"},
	}
	if fmt.Sprint(*r.asked) != fmt.Sprint(want) {
		t.Fatalf("asked %v, want %v", *r.asked, want)
	}
	if err := r.run("start", "lens"); err != nil || !strings.Contains(r.out.String(), "http://127.0.0.1:5000/") {
		t.Fatalf("output = %q, %v", r.out, err)
	}
}

func TestHelpersStartReportsAFailure(t *testing.T) {
	r := newCLIRig(t)
	r.c.instance = func() (string, string, error) { return "http://127.0.0.1:9", "tok", nil }
	r.c.request = func(string, string, string, string) (helpers.Status, error) {
		return helpers.Status{ID: "lens", State: helpers.StateFailed, Err: "it exited with status 3", Log: []string{"boom", "bang"}}, nil
	}
	err := r.run("start", "lens")
	if !errors.Is(err, errReported) {
		t.Fatalf("err = %v", err)
	}
	for _, want := range []string{"failed", "exited with status 3", "| boom", "| bang"} {
		if !strings.Contains(r.out.String(), want) {
			t.Errorf("output is missing %q: %s", want, r.out)
		}
	}
	r.c.request = func(string, string, string, string) (helpers.Status, error) {
		return helpers.Status{}, errors.New("lens is not installed")
	}
	if err := r.run("start", "lens"); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("err = %v", err)
	}
}

func TestHelpersCommandLineErrors(t *testing.T) {
	r := newCLIRig(t)
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"no command", nil, "flockdeck helpers list"},
		{"unknown command", []string{"frobnicate"}, `unknown command "frobnicate"`},
		{"install with no helper", []string{"install"}, "which helper?"},
		{"unknown helper", []string{"install", "-yes", "nope"}, "not a helper Flockdeck knows"},
		{"a path for a helper", []string{"uninstall", "-yes", "../lens"}, "not a helper Flockdeck knows"},
		{"extra argument", []string{"install", "lens", "extra"}, `unexpected "extra"`},
		{"a flag as a word", []string{"install", "lens", "yes"}, "Did you mean -yes?"},
		{"unknown flag", []string{"install", "-nope", "lens"}, "not defined"},
		{"list takes none", []string{"list", "lens"}, "unexpected"},
	}
	for _, c := range cases {
		err := r.run(c.args...)
		if err == nil {
			t.Errorf("%s: no error", c.name)
			continue
		}
		text := r.errOut.String() + err.Error()
		if !strings.Contains(text, c.want) {
			t.Errorf("%s: %q does not contain %q", c.name, text, c.want)
		}
	}
	if err := r.run("-h"); err != nil || !strings.Contains(r.out.String(), "Usage:") {
		t.Errorf("-h: %v %q", err, r.out)
	}
	if err := r.run("install", "-h"); !errors.Is(err, errHelpAsked) {
		t.Errorf("install -h: %v", err)
	}
}

func TestHelpersCtrlBreakIsHiddenAndChecksItsArgument(t *testing.T) {
	err := runHelpers([]string{"ctrl-break", "not-a-pid"}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "not a process id") {
		t.Fatalf("err = %v", err)
	}
	var buf bytes.Buffer
	fs := flockdeckFlagSet(&cliFlags{})
	fs.SetOutput(&buf)
	usage(fs)
	if strings.Contains(buf.String(), "ctrl-break") {
		t.Fatal("the hidden subcommand is in the usage")
	}
	if runtime.GOOS != "windows" {
		if err := helpers.CtrlBreak(1); err == nil {
			t.Fatal("CtrlBreak did something off Windows")
		}
	}
}

func TestUsageNamesEveryHelpersFlag(t *testing.T) {
	var buf bytes.Buffer
	fs := flockdeckFlagSet(&cliFlags{})
	fs.SetOutput(&buf)
	usage(fs)
	for _, name := range []string{"helpers list", "install", "start|stop|open", "uninstall", "-check", "-version", "-allow-unsigned", "-yes", "-purge-data"} {
		if !strings.Contains(buf.String(), name) {
			t.Errorf("the usage does not mention %q", name)
		}
	}
}

// A helper that was installed from a signed release is never downgraded to an
// unsigned one, with or without -allow-unsigned.
func TestHelpersInstallRefusesAnUnsignedReleaseAfterASignedOne(t *testing.T) {
	r := newCLIRig(t)
	r.rel.publish(t, "0.4.0", true, false)
	if err := r.run("install", "-yes", "lens"); err != nil {
		t.Fatal(err)
	}
	r.rel.publish(t, "0.5.0", false, false)
	for _, args := range [][]string{{"install", "-yes", "lens"}, {"install", "-allow-unsigned", "-yes", "lens"}} {
		err := r.run(args...)
		if err == nil || !strings.Contains(err.Error(), "earlier version of it that you installed was signed") || !strings.Contains(err.Error(), "does not apply") {
			t.Fatalf("%v: err = %v", args, err)
		}
	}
	if v, _ := r.store.Current("lens"); v != "0.4.0" {
		t.Fatalf("current = %q", v)
	}
}
