package server

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/helpers"
	"github.com/jmwri/flockdeck/internal/selfupdate"
)

// uiRig is a server whose helper installer talks to a fake GitHub, and a
// window to send commands from.
type uiRig struct {
	srv   *Server
	store *helpers.Store
	files map[string][]byte
	mu    sync.Mutex
	key   ed25519.PrivateKey
}

func newUIRig(t *testing.T) *uiRig {
	t.Helper()
	srv, _ := newTestServer(t)
	pub, priv, _ := ed25519.GenerateKey(nil)
	t.Cleanup(selfupdate.TrustKeysForTest(pub, nil))
	r := &uiRig{srv: srv, files: map[string][]byte{}, key: priv, store: &helpers.Store{Root: t.TempDir()}}
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		b, ok := r.files[req.URL.Path]
		r.mu.Unlock()
		if !ok {
			http.NotFound(w, req)
			return
		}
		_, _ = w.Write(b)
	}))
	t.Cleanup(fake.Close)
	srv.SetHelpers(
		helpers.NewSupervisor(helpers.Config{Store: r.store}),
		helpers.NewInstaller(helpers.Options{
			Store: r.store, Base: fake.URL, GOOS: "linux", GOARCH: "amd64",
			AllowURL: func(u *url.URL) bool { return u.Hostname() == "127.0.0.1" },
		}),
	)
	return r
}

// publish serves version for linux/amd64, signed or not, and returns the
// archive's SHA-256.
func (r *uiRig) publish(t *testing.T, version string, signed, badSig bool) string {
	t.Helper()
	top := "lens_" + version + "_linux_amd64"
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: top + "/lens", Typeflag: tar.TypeReg, Mode: 0o755, Size: 3})
	_, _ = tw.Write([]byte("bin"))
	_ = tw.Close()
	_ = gz.Close()
	sum := sha256.Sum256(buf.Bytes())
	hexsum := hex.EncodeToString(sum[:])
	sums := []byte(hexsum + "  " + top + ".tar.gz\n")
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, base := range []string{"/Flockdeck/lens/releases/download/v" + version + "/", "/Flockdeck/lens/releases/latest/download/"} {
		r.files[base+"checksums.txt"] = sums
		delete(r.files, base+"checksums.txt.sig")
		if signed {
			over := sums
			if badSig {
				over = append([]byte("x"), sums...)
			}
			r.files[base+"checksums.txt.sig"] = selfupdate.Sign(r.key, over)
		}
	}
	r.files["/Flockdeck/lens/releases/download/v"+version+"/"+top+".tar.gz"] = buf.Bytes()
	return hexsum
}

func (r *uiRig) send(c *controlClient, cmd command) { r.srv.handleCommand(c, cmd) }

// next reads the next message of a type the window is sent, or fails.
func next(t *testing.T, c *controlClient, typ string) map[string]any {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case raw := <-c.out:
			var m map[string]any
			if err := json.Unmarshal(raw, &m); err != nil {
				t.Fatalf("unreadable message %s", raw)
			}
			if m["type"] == typ {
				return m
			}
		case <-deadline:
			t.Fatalf("no %s message arrived", typ)
		}
	}
}

func notices(c *controlClient, wait time.Duration) []string {
	var out []string
	deadline := time.After(wait)
	for {
		select {
		case raw := <-c.out:
			var m map[string]any
			if json.Unmarshal(raw, &m) == nil && m["type"] == "notice" {
				out = append(out, m["text"].(string))
			}
		case <-deadline:
			return out
		}
	}
}

func TestHelpersListIsSentToTheDeskWindow(t *testing.T) {
	r := newUIRig(t)
	c := &controlClient{out: make(chan []byte, 16)}
	r.send(c, command{Cmd: "helpers"})
	m := next(t, c, "helpers")
	rows := m["rows"].([]any)
	if len(rows) != 1 {
		t.Fatalf("rows = %v", rows)
	}
	row := rows[0].(map[string]any)
	if row["id"] != "lens" || row["state"] != "notinstalled" || len(row["allows"].([]any)) == 0 {
		t.Fatalf("row = %v", row)
	}
}

func TestHelperCommandsAreRefusedFromARelayWindow(t *testing.T) {
	r := newUIRig(t)
	r.publish(t, "0.4.0", true, false)
	for _, cmd := range []command{
		{Cmd: "helpers"}, {Cmd: "helperPlan", ID: "lens"}, {Cmd: "helperInstall", ID: "lens", Confirmed: true},
		{Cmd: "helperStart", ID: "lens"}, {Cmd: "helperStop", ID: "lens"}, {Cmd: "helperOpen", ID: "lens"},
		{Cmd: "helperUninstall", ID: "lens", Purge: true},
	} {
		c := &controlClient{out: make(chan []byte, 16), remote: true}
		r.send(c, cmd)
		got := notices(c, 200*time.Millisecond)
		if len(got) != 1 || !strings.Contains(got[0], "not from a window reached through the relay") {
			t.Errorf("%s from a relay window: notices %v", cmd.Cmd, got)
		}
	}
	if _, ok := r.store.Current("lens"); ok {
		t.Fatal("a relay window installed a helper")
	}
}

func TestHelperPlanShowsTheReleaseWithoutInstalling(t *testing.T) {
	r := newUIRig(t)
	sha := r.publish(t, "0.4.0", true, false)
	c := &controlClient{out: make(chan []byte, 16)}
	r.send(c, command{Cmd: "helperPlan", ID: "lens"})
	m := next(t, c, "helperPlan")
	if m["version"] != "0.4.0" || m["sha256"] != sha || m["signed"] != true || !strings.Contains(m["url"].(string), "/v0.4.0/lens_0.4.0_linux_amd64.tar.gz") {
		t.Fatalf("plan = %v", m)
	}
	if _, ok := r.store.Current("lens"); ok {
		t.Fatal("a plan installed it")
	}
	if len(m["allows"].([]any)) == 0 || m["name"] != "lens" {
		t.Fatalf("plan = %v", m)
	}

	r.publish(t, "0.4.0", false, false)
	r.send(c, command{Cmd: "helperPlan", ID: "lens"})
	if m := next(t, c, "helperPlan"); m["signed"] != false {
		t.Fatalf("an unsigned release is shown as signed: %v", m)
	}

	r.publish(t, "0.4.0", true, true)
	r.send(c, command{Cmd: "helperPlan", ID: "lens"})
	m = next(t, c, "helperPlan")
	if m["fatal"] != true || !strings.Contains(m["error"].(string), "cannot be overridden") {
		t.Fatalf("a bad signature: %v", m)
	}
}

func TestHelperInstallNeedsConfirmationAndTheShownHash(t *testing.T) {
	r := newUIRig(t)
	sha := r.publish(t, "0.4.0", true, false)
	c := &controlClient{out: make(chan []byte, 32)}

	// Not confirmed: nothing happens.
	r.send(c, command{Cmd: "helperInstall", ID: "lens", Text: "0.4.0", SHA256: sha})
	time.Sleep(200 * time.Millisecond)
	if _, ok := r.store.Current("lens"); ok {
		t.Fatal("installed without confirmation")
	}

	// A hash other than the one shown: the release changed under the dialog.
	r.send(c, command{Cmd: "helperInstall", ID: "lens", Text: "0.4.0", SHA256: strings.Repeat("0", 64), Confirmed: true})
	got := notices(c, 2*time.Second)
	if len(got) == 0 || !strings.Contains(strings.Join(got, "|"), "changed since it was shown") {
		t.Fatalf("notices = %v", got)
	}
	if _, ok := r.store.Current("lens"); ok {
		t.Fatal("installed a release other than the one shown")
	}

	r.send(c, command{Cmd: "helperInstall", ID: "lens", Text: "0.4.0", SHA256: sha, Confirmed: true})
	got = notices(c, 3*time.Second)
	if !strings.Contains(strings.Join(got, "|"), "Installed lens 0.4.0") {
		t.Fatalf("notices = %v", got)
	}
	if v, ok := r.store.Current("lens"); !ok || v != "0.4.0" {
		t.Fatalf("current = %q, %v", v, ok)
	}
}

func TestHelperInstallRefusesUnsignedWithoutTheOverride(t *testing.T) {
	r := newUIRig(t)
	sha := r.publish(t, "0.4.0", false, false)
	c := &controlClient{out: make(chan []byte, 32)}
	r.send(c, command{Cmd: "helperInstall", ID: "lens", Text: "0.4.0", SHA256: sha, Confirmed: true})
	got := notices(c, 2*time.Second)
	if !strings.Contains(strings.Join(got, "|"), "unsigned override was not chosen") {
		t.Fatalf("notices = %v", got)
	}
	if _, ok := r.store.Current("lens"); ok {
		t.Fatal("installed an unsigned release without the override")
	}
	r.send(c, command{Cmd: "helperInstall", ID: "lens", Text: "0.4.0", SHA256: sha, Confirmed: true, Unsigned: true})
	got = notices(c, 3*time.Second)
	if !strings.Contains(strings.Join(got, "|"), "(unsigned)") {
		t.Fatalf("notices = %v", got)
	}
	if info, _ := r.store.Info("lens"); info.Signed {
		t.Fatal("recorded as signed")
	}
}

func TestHelperInstallNeverOverridesABadSignature(t *testing.T) {
	r := newUIRig(t)
	sha := r.publish(t, "0.4.0", true, true)
	c := &controlClient{out: make(chan []byte, 32)}
	r.send(c, command{Cmd: "helperInstall", ID: "lens", Text: "0.4.0", SHA256: sha, Confirmed: true, Unsigned: true})
	got := notices(c, 2*time.Second)
	if !strings.Contains(strings.Join(got, "|"), "cannot be overridden") {
		t.Fatalf("notices = %v", got)
	}
	if _, ok := r.store.Current("lens"); ok {
		t.Fatal("installed a release with a bad signature")
	}
}

func TestHelperStartOfAnUninstalledHelper(t *testing.T) {
	r := newUIRig(t)
	c := &controlClient{out: make(chan []byte, 16)}
	r.send(c, command{Cmd: "helperStart", ID: "lens"})
	got := notices(c, 300*time.Millisecond)
	if len(got) != 1 || !strings.Contains(got[0], "not installed") {
		t.Fatalf("notices = %v", got)
	}
	r.send(c, command{Cmd: "helperOpen", ID: "lens"})
	got = notices(c, 300*time.Millisecond)
	if len(got) != 1 || !strings.Contains(got[0], "Start the helper first") {
		t.Fatalf("notices = %v", got)
	}
}

func TestHelperUninstallKeepsDataUnlessAsked(t *testing.T) {
	r := newUIRig(t)
	sha := r.publish(t, "0.4.0", true, false)
	c := &controlClient{out: make(chan []byte, 32)}
	r.send(c, command{Cmd: "helperInstall", ID: "lens", Text: "0.4.0", SHA256: sha, Confirmed: true})
	notices(c, 3*time.Second)
	note := r.store.DataDir("lens") + string(os.PathSeparator) + "notes.db"
	if err := os.WriteFile(note, []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.send(c, command{Cmd: "helperUninstall", ID: "lens"})
	got := notices(c, 500*time.Millisecond)
	if len(got) != 1 || !strings.Contains(got[0], "data folder was kept") {
		t.Fatalf("notices = %v", got)
	}
	if _, err := os.Stat(note); err != nil {
		t.Fatal("the data went with a plain removal")
	}
	r.send(c, command{Cmd: "helperUninstall", ID: "lens", Purge: true})
	notices(c, 500*time.Millisecond)
	if _, err := os.Stat(note); err == nil {
		t.Fatal("a purge left the data")
	}
}

func TestHelperInstallRefusesAnUnsignedReleaseAfterASignedOne(t *testing.T) {
	r := newUIRig(t)
	sha := r.publish(t, "0.4.0", true, false)
	c := &controlClient{out: make(chan []byte, 32)}
	r.send(c, command{Cmd: "helperInstall", ID: "lens", Text: "0.4.0", SHA256: sha, Confirmed: true})
	notices(c, 3*time.Second)
	sha = r.publish(t, "0.5.0", false, false)

	r.send(c, command{Cmd: "helperPlan", ID: "lens"})
	m := next(t, c, "helperPlan")
	if m["fatal"] != true || !strings.Contains(m["error"].(string), "earlier version") {
		t.Fatalf("plan = %v", m)
	}
	r.send(c, command{Cmd: "helperInstall", ID: "lens", Text: "0.5.0", SHA256: sha, Confirmed: true, Unsigned: true})
	got := notices(c, 2*time.Second)
	if !strings.Contains(strings.Join(got, "|"), "earlier version") {
		t.Fatalf("notices = %v", got)
	}
	if v, _ := r.store.Current("lens"); v != "0.4.0" {
		t.Fatalf("current = %q", v)
	}
}
