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
	"path/filepath"
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
	srv    *Server
	store  *helpers.Store
	files  map[string][]byte
	mu     sync.Mutex
	key    ed25519.PrivateKey
	origin string
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
	r.origin = fake.URL
	srv.SetHelpers(
		helpers.NewSupervisor(helpers.Config{Store: r.store}),
		helpers.NewInstaller(helpers.Options{
			Store: r.store, Lookup: lensAt(fake.URL), GOOS: "linux", GOARCH: "amd64",
			AllowURL: func(u *url.URL) bool { return u.Hostname() == "127.0.0.1" },
		}),
	)
	return r
}

// publish serves version for linux/amd64, signed or not, and returns the
// archive's SHA-256.
func (r *uiRig) publish(t *testing.T, version string, signed, badSig bool) string {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	return publishCDN(r.files, r.origin, r.key, version, signed, badSig)
}

// publishCDN lays a lens release out as the CDN does: latest.json, and under
// /lens/<tag>/ the archive, checksums.txt(.sig) and manifest.json(.sig). sign
// picks whether the signatures are served, and badSig whether they are wrong.
// It returns the archive's SHA-256.
func publishCDN(files map[string][]byte, origin string, key ed25519.PrivateKey, version string, sign, badSig bool) string {
	tag := "v" + version
	top := "lens_" + tag + "_linux_amd64"
	name := top + ".tar.gz"
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
	hexsum := hex.EncodeToString(sum[:])
	sums := []byte(hexsum + "  " + name + "\n")
	sumsSig := selfupdate.Sign(key, sums)
	base := "/lens/" + tag + "/"
	m := selfupdate.Manifest{Version: tag, Date: time.Now().Add(-time.Hour), NotesURL: "https://example.invalid/notes", Files: []selfupdate.ManifestFile{
		{Name: name, URL: origin + base + name, SHA256: hexsum, Size: int64(buf.Len())},
		{Name: "checksums.txt", URL: origin + base + "checksums.txt", SHA256: hexsum, Size: int64(len(sums))},
		{Name: "checksums.txt.sig", URL: origin + base + "checksums.txt.sig", SHA256: hexsum, Size: int64(len(sumsSig))},
	}}
	manifest, _ := json.Marshal(m)
	signed := manifest
	if badSig {
		signed = append([]byte("tampered "), manifest...)
	}
	files[base+name] = buf.Bytes()
	files[base+"checksums.txt"] = sums
	files[base+"manifest.json"] = manifest
	delete(files, base+"manifest.json.sig")
	delete(files, base+"checksums.txt.sig")
	if sign {
		files[base+"manifest.json.sig"] = selfupdate.Sign(key, signed)
		files[base+"checksums.txt.sig"] = sumsSig
	}
	files["/lens/latest.json"] = []byte("{\"version\":\"" + tag + "\"}")
	return hexsum
}

// lensAt is the catalogue's lens, fetched from a fake CDN.
func lensAt(origin string) func(string) (helpers.Entry, bool) {
	e, _ := helpers.Lookup("lens")
	e.Source = origin + "/lens"
	return func(id string) (helpers.Entry, bool) {
		if id == e.ID {
			return e, true
		}
		return helpers.Entry{}, false
	}
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
	if m["version"] != "0.4.0" || m["sha256"] != sha || m["signed"] != true || !strings.Contains(m["url"].(string), "/lens/v0.4.0/lens_v0.4.0_linux_amd64.tar.gz") {
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

// A helper id is one of the catalogue's, not anything that merely looks like
// one: the others are refused before they become a path or a process.
func TestHelperCommandsOnlyTakeCatalogueIDs(t *testing.T) {
	r := newUIRig(t)
	other := filepath.Join(r.store.Root, "nope")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(other, "data.txt")
	if err := os.WriteFile(keep, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"nope", "../nope", "LENS", "lens/..", "lens ", "", "lens\x00"} {
		for _, cmd := range []command{
			{Cmd: "helperPlan", ID: id}, {Cmd: "helperInstall", ID: id, Confirmed: true}, {Cmd: "helperStart", ID: id},
			{Cmd: "helperStop", ID: id}, {Cmd: "helperOpen", ID: id}, {Cmd: "helperUninstall", ID: id},
			{Cmd: "helperUninstall", ID: id, Purge: true},
		} {
			c := &controlClient{out: make(chan []byte, 16)}
			r.send(c, cmd)
			got := notices(c, 150*time.Millisecond)
			if len(got) != 1 || !strings.Contains(got[0], "not a helper Flockdeck knows") {
				t.Errorf("%s %q: notices %v", cmd.Cmd, id, got)
			}
		}
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("a folder that is not a helper's was touched: %v", err)
	}
	// And the endpoints.
	srv := helperServer(t)
	for _, action := range []string{"start", "stop", "open"} {
		if code, _ := postHelper(t, srv.BaseURL()+"/helpers/"+action+"?t="+srv.Token()+"&id=..%2Fnope", nil); code != http.StatusBadRequest {
			t.Errorf("%s with a bad id: %d", action, code)
		}
	}
}

// While an install is under way the helper cannot be started or removed.
func TestAHelperBeingInstalledCannotBeStartedOrRemoved(t *testing.T) {
	r := newUIRig(t)
	if !r.srv.helperUI.setBusy("lens", true) {
		t.Fatal("setBusy")
	}
	c := &controlClient{out: make(chan []byte, 16)}
	for _, cmd := range []command{{Cmd: "helperStart", ID: "lens"}, {Cmd: "helperUninstall", ID: "lens"}, {Cmd: "helperUninstall", ID: "lens", Purge: true}} {
		r.send(c, cmd)
		got := notices(c, 150*time.Millisecond)
		if len(got) != 1 || !strings.Contains(got[0], "being installed") {
			t.Errorf("%s: notices %v", cmd.Cmd, got)
		}
	}
	if code, body := postHelper(t, r.srv.BaseURL()+"/helpers/start?t="+r.srv.Token()+"&id=lens", nil); code != http.StatusConflict || !strings.Contains(body, "being installed") {
		t.Errorf("the endpoint: %d %q", code, body)
	}
	r.srv.helperUI.setBusy("lens", false)
}

// An install is refused while the helper runs or is starting, and says to stop
// it first.
func TestAnInstallIsRefusedWhileTheHelperIsActive(t *testing.T) {
	r := newUIRig(t)
	sha := r.publish(t, "0.4.0", true, false)
	c := &controlClient{out: make(chan []byte, 32)}
	r.send(c, command{Cmd: "helperInstall", ID: "lens", Text: "0.4.0", SHA256: sha, Confirmed: true})
	notices(c, 3*time.Second)
	// A start under way: the record exists from the first moment.
	if err := r.store.WriteStartingForTest("lens"); err != nil {
		t.Fatal(err)
	}
	sha = r.publish(t, "0.5.0", true, false)
	r.send(c, command{Cmd: "helperInstall", ID: "lens", Text: "0.5.0", SHA256: sha, Confirmed: true})
	got := notices(c, 2*time.Second)
	if !strings.Contains(strings.Join(got, "|"), "running") {
		t.Fatalf("notices = %v", got)
	}
	if v, _ := r.store.Current("lens"); v != "0.4.0" {
		t.Fatalf("current = %q", v)
	}
}
