package main

import (
	"bytes"
	"crypto/ed25519"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/selfupdate"
)

// fakeAWS stands in for the aws CLI in the tests of scripts/publish-downloads.sh:
// the bucket is a directory, and every call is written to a log shared with
// the fake site, so the order of everything the script did can be read back.
const fakeAWS = `#!/bin/sh
log() { printf '%s\n' "$*" >> "$AWS_STUB_LOG"; }
[ "$1" = s3 ] && [ "$2" = cp ] || { log "unexpected: $*"; exit 2; }
src=$3 dst=$4
shift 4
acl= type= cache= meta= endpoint=
while [ $# -gt 0 ]; do
	case $1 in
		--acl) acl=$2; shift 2 ;;
		--content-type) type=$2; shift 2 ;;
		--cache-control) cache=$2; shift 2 ;;
		--metadata) meta=$2; shift 2 ;;
		--endpoint-url) endpoint=$2; shift 2 ;;
		--only-show-errors) shift ;;
		*) log "unexpected flag $1"; exit 2 ;;
	esac
done
case $src in
	s3://*)
		key=${src#s3://*/}
		[ -f "$AWS_STUB_STORE/$key" ] || exit 1
		cp "$AWS_STUB_STORE/$key" "$dst"
		log "get $key" ;;
	*)
		bucket=${dst#s3://}
		bucket=${bucket%%/*}
		key=${dst#s3://*/}
		mkdir -p "$AWS_STUB_STORE/$(dirname "$key")"
		cp "$src" "$AWS_STUB_STORE/$key"
		log "put $key|$bucket|$endpoint|$acl|$type|$cache|$meta|$AWS_ACCESS_KEY_ID" ;;
esac
`

// publishing is one run of the publish script against a bucket that is a
// directory and a site that serves it.
type publishing struct {
	t       *testing.T
	dist    string
	store   string
	logPath string
	site    *httptest.Server
	signing signing // how dist was signed, so a test can sign it again

	mu       sync.Mutex
	listable bool // the site answers a listing of the bucket
	unserved bool // the site serves nothing
}

func newPublishing(t *testing.T, version string) *publishing {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the publish script runs on the release workflow's Linux runner, or by hand in a POSIX shell")
	}
	for _, tool := range []string{"sh", "curl"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("no %s to run the publish script with", tool)
		}
	}
	p := &publishing{t: t, dist: fakeRelease(t, version), store: t.TempDir()}
	p.logPath = filepath.Join(t.TempDir(), "log")

	_, key, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	p.signing = signing{version: version, out: p.dist, base: "https://dl.example.invalid", key: selfupdate.EncodeSigningKey(key), anyKey: true, now: time.Now()}
	if err := runSign(p.signing); err != nil {
		t.Fatal(err)
	}

	p.site = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		listable, unserved := p.listable, p.unserved
		p.mu.Unlock()
		switch {
		case r.URL.Path == "/":
			if !listable {
				http.Error(w, "AccessDenied", http.StatusForbidden)
				return
			}
			fmt.Fprint(w, "<ListBucketResult/>")
		case unserved:
			http.NotFound(w, r)
		default:
			p.log("served " + strings.TrimPrefix(r.URL.Path, "/"))
			http.ServeFile(w, r, filepath.Join(p.store, filepath.FromSlash(strings.TrimPrefix(r.URL.Path, "/"))))
		}
	}))
	t.Cleanup(p.site.Close)
	return p
}

// spoil changes how the fake site answers.
func (p *publishing) spoil(f func()) {
	p.mu.Lock()
	defer p.mu.Unlock()
	f()
}

func (p *publishing) log(line string) {
	f, err := os.OpenFile(p.logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		p.t.Error(err)
		return
	}
	defer f.Close()
	fmt.Fprintln(f, line)
}

// run runs the script for version with the settings given on top of a full
// set, where a setting of "" leaves it out. The full set holds no
// DigitalOcean API token: nothing is purged, so none is needed.
func (p *publishing) run(version string, settings map[string]string) (string, error) {
	p.t.Helper()
	bin := p.t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "aws"), []byte(fakeAWS), 0o755); err != nil {
		p.t.Fatal(err)
	}
	env := map[string]string{
		"DO_SPACES_KEY": "the-key", "DO_SPACES_SECRET": "the-secret", "DO_SPACES_BUCKET": "downloads",
		"DO_SPACES_REGION": "lon1", "DO_SPACES_ENDPOINT": "https://s3.example.invalid",
		"FLOCKDECK_DL_URL": p.site.URL, "FLOCKDECK_CHECK_WAIT": "0",
		"AWS_STUB_LOG": p.logPath, "AWS_STUB_STORE": p.store,
	}
	for k, v := range settings {
		env[k] = v
	}
	cmd := exec.Command("sh", filepath.Join("..", "..", "scripts", "publish-downloads.sh"), version, p.dist)
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); strings.HasPrefix(k, "DO_") || strings.HasPrefix(k, "AWS_") || strings.HasPrefix(k, "FLOCKDECK_") {
			continue
		}
		if strings.HasPrefix(kv, "PATH=") {
			kv = "PATH=" + bin + string(os.PathListSeparator) + strings.TrimPrefix(kv, "PATH=")
		}
		cmd.Env = append(cmd.Env, kv)
	}
	for k, v := range env {
		if v != "" {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// events is everything the script did, in order.
func (p *publishing) events() []string {
	b, _ := os.ReadFile(p.logPath)
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

// puts are the keys uploaded, in order, each with how it was uploaded.
func (p *publishing) puts() (keys []string, how map[string][]string) {
	how = map[string][]string{}
	for _, e := range p.events() {
		if rest, ok := strings.CutPrefix(e, "put "); ok {
			f := strings.Split(rest, "|")
			keys = append(keys, f[0])
			how[f[0]] = f[1:]
		}
	}
	return keys, how
}

func index(events []string, prefix string) int {
	for i, e := range events {
		if strings.HasPrefix(e, prefix) {
			return i
		}
	}
	return -1
}

// A release goes up in the order that makes it switch over in one step: its
// own files under its version, its signed manifest among them, one read back
// through the public address, the unversioned copies for the site's buttons,
// and latest.json last, naming the version. Everything is public to read, and
// cached for a year or for a minute depending on whether it can ever
// change. Nothing is purged.
func TestPublishScriptOrder(t *testing.T) {
	p := newPublishing(t, "v9.9.9")
	out, err := p.run("v9.9.9", nil)
	if err != nil {
		t.Fatalf("publish: %v\n%s", err, out)
	}
	ev := p.events()
	keys, how := p.puts()

	if len(keys) == 0 || keys[len(keys)-1] != "latest.json" {
		t.Errorf("uploaded %v; want latest.json last", keys)
	}
	served := index(ev, "served v9.9.9/checksums.txt")
	firstLatest := index(ev, "put latest/")
	lastVersioned := -1
	for i, e := range ev {
		if strings.HasPrefix(e, "put v9.9.9/") {
			lastVersioned = i
		}
	}
	if !(lastVersioned >= 0 && lastVersioned < served && served < firstLatest) {
		t.Errorf("events %v: want every versioned file, then the check through the site, then /latest/", ev)
	}
	for _, k := range []string{"v9.9.9/manifest.json", "v9.9.9/manifest.json.sig"} {
		if _, ok := how[k]; !ok {
			t.Errorf("%s was not uploaded: %v", k, keys)
		}
	}
	if got, _ := os.ReadFile(filepath.Join(p.store, "latest.json")); string(got) != `{"version":"v9.9.9"}`+"\n" {
		t.Errorf("latest.json is %q, want it to name v9.9.9", got)
	}
	if strings.Contains(out, "purg") {
		t.Errorf("the script spoke of purging:\n%s", out)
	}

	var archives int
	for _, pl := range platforms {
		name := fmt.Sprintf("flockdeck_%s_%s%s", pl.OS, pl.Arch, archiveExt(pl.OS))
		versioned := fmt.Sprintf("flockdeck_v9.9.9_%s_%s%s", pl.OS, pl.Arch, archiveExt(pl.OS))
		a, err := os.ReadFile(filepath.Join(p.store, "latest", name))
		b, _ := os.ReadFile(filepath.Join(p.dist, versioned))
		if err != nil || !bytes.Equal(a, b) {
			t.Errorf("latest/%s is not %s (%v)", name, versioned, err)
		}
		archives++
	}
	for _, k := range keys {
		h := how[k]
		bucket, endpoint, acl, typ, cache, meta, id := h[0], h[1], h[2], h[3], h[4], h[5], h[6]
		if bucket != "downloads" || endpoint != "https://s3.example.invalid" || id != "the-key" {
			t.Errorf("%s went to %s at %s as %s", k, bucket, endpoint, id)
		}
		if acl != "public-read" {
			t.Errorf("%s was uploaded %q, want public-read", k, acl)
		}
		wantType := map[string]string{".zip": "application/zip", ".gz": "application/gzip", ".json": "application/json", ".txt": "text/plain; charset=utf-8", ".sig": "text/plain; charset=utf-8"}[filepath.Ext(k)]
		if typ != wantType {
			t.Errorf("%s is %q, want %q", k, typ, wantType)
		}
		forever := strings.HasPrefix(k, "v9.9.9/")
		if wantCache := map[bool]string{true: "public, max-age=31536000, immutable", false: "public, max-age=60"}[forever]; cache != wantCache {
			t.Errorf("%s is cached %q, want %q", k, cache, wantCache)
		}
		if wantMeta := map[bool]string{true: "max-age=31536000", false: "max-age=60"}[forever]; meta != wantMeta {
			t.Errorf("%s has %q, want %q", k, meta, wantMeta)
		}
	}
	if want := 2*archives + 5; len(keys) != want {
		t.Errorf("uploaded %d files, want %d: %v", len(keys), want, keys)
	}
}

// A pre-release goes up under its version, signed manifest and all, and
// nothing else moves: it is never the latest.
func TestPublishScriptLeavesLatestAloneForAPreRelease(t *testing.T) {
	p := newPublishing(t, "v9.9.9-rc.1")
	out, err := p.run("v9.9.9-rc.1", nil)
	if err != nil {
		t.Fatalf("publish: %v\n%s", err, out)
	}
	keys, _ := p.puts()
	for _, k := range keys {
		if !strings.HasPrefix(k, "v9.9.9-rc.1/") {
			t.Errorf("a pre-release uploaded %s", k)
		}
	}
	if len(keys) != len(platforms)+4 {
		t.Errorf("uploaded %v, want the archives, the signed checksums and the signed manifest", keys)
	}
	if !strings.Contains(out, "pre-release") {
		t.Errorf("output %q does not say latest.json was left alone", out)
	}
}

// Every setting that is missing is named, all at once, before anything is
// uploaded.
func TestPublishScriptNamesWhatIsMissing(t *testing.T) {
	p := newPublishing(t, "v9.9.9")
	out, err := p.run("v9.9.9", map[string]string{"DO_SPACES_SECRET": "", "DO_SPACES_BUCKET": ""})
	if err == nil {
		t.Fatal("publish succeeded without its secrets")
	}
	if !strings.Contains(out, "not set: DO_SPACES_SECRET DO_SPACES_BUCKET") {
		t.Errorf("output %q, want both missing settings named", out)
	}
	if keys, _ := p.puts(); len(keys) > 0 {
		t.Errorf("uploaded %v all the same", keys)
	}
}

// Nothing is switched over to a release the site does not serve, nor while
// anyone can list the bucket; and a version already published with other
// files is not written over, since nothing would purge the copies of them
// the CDN's edges hold.
func TestPublishScriptStopsBeforeTheSwitch(t *testing.T) {
	cases := map[string]func(p *publishing){
		"the site serves nothing":  func(p *publishing) { p.spoil(func() { p.unserved = true }) },
		"the bucket can be listed": func(p *publishing) { p.spoil(func() { p.listable = true }) },
		"the version is published": func(p *publishing) {
			os.MkdirAll(filepath.Join(p.store, "v9.9.9"), 0o755)
			os.WriteFile(filepath.Join(p.store, "v9.9.9", "checksums.txt"), []byte("other files\n"), 0o644)
		},
	}
	for name, spoil := range cases {
		t.Run(name, func(t *testing.T) {
			p := newPublishing(t, "v9.9.9")
			spoil(p)
			out, err := p.run("v9.9.9", nil)
			if err == nil {
				t.Fatalf("publish succeeded:\n%s", out)
			}
			keys, _ := p.puts()
			for _, k := range keys {
				if strings.HasPrefix(k, "latest") {
					t.Errorf("uploaded %s all the same", k)
				}
			}
		})
	}
}

// A version published again from the same build uploads its files again,
// which changes nothing, but keeps the manifest it was first published with:
// signed again, it carries another date, and an edge holding the first
// manifest.json beside the second's signature would fail it for a year.
func TestPublishScriptKeepsAVersionsFirstManifest(t *testing.T) {
	p := newPublishing(t, "v9.9.9")
	if out, err := p.run("v9.9.9", nil); err != nil {
		t.Fatalf("publish: %v\n%s", err, out)
	}
	first, err := os.ReadFile(filepath.Join(p.store, "v9.9.9", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}

	p.signing.now = p.signing.now.Add(time.Hour)
	if err := runSign(p.signing); err != nil {
		t.Fatal(err)
	}
	if again, _ := os.ReadFile(filepath.Join(p.dist, "manifest.json")); bytes.Equal(again, first) {
		t.Fatal("signing again wrote the same manifest, so this tests nothing")
	}
	os.Remove(p.logPath)
	out, err := p.run("v9.9.9", nil)
	if err != nil {
		t.Fatalf("publish again: %v\n%s", err, out)
	}

	keys, _ := p.puts()
	for _, k := range keys {
		if strings.HasPrefix(k, "v9.9.9/manifest.json") {
			t.Errorf("uploaded %s again", k)
		}
	}
	if len(keys) == 0 || keys[len(keys)-1] != "latest.json" {
		t.Errorf("uploaded %v; want the release switched over to all the same", keys)
	}
	if kept, _ := os.ReadFile(filepath.Join(p.store, "v9.9.9", "manifest.json")); !bytes.Equal(kept, first) {
		t.Error("the published manifest was replaced")
	}
	if !strings.Contains(out, "as they were first published") {
		t.Errorf("output %q does not say the manifest was kept", out)
	}
}
