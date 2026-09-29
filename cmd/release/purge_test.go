package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
)

// The tests of scripts/purge-downloads.sh run it, and the real aws CLI it
// calls, against an S3-compatible store with versioning turned on, since what
// they are about is that every version of every file and every delete marker
// goes, not only what a plain listing shows. The store is named by
// FLOCKDECK_TEST_S3_ENDPOINT (test.yml's purge-downloads job starts moto's S3
// server for it); without one they are skipped. FLOCKDECK_TEST_AWS names the
// aws CLI when it is not "aws" on PATH.
//
// In front of the store is a stand-in for dl.flockdeck.ai: two caches, as the
// real one has, each keeping whatever it once served until it is purged, so
// a file deleted from the bucket goes on being served exactly as the real
// edges would go on serving it for a year. Beside it are stand-ins for
// DigitalOcean's and Cloudflare's purge APIs, which record every request and
// purge those caches as the real ones do.

const (
	purgeDOToken       = "do-token-SECRET-4f1c"
	purgeCFToken       = "cf-token-SECRET-9b27"
	purgeSpacesSecret  = "spaces-secret-SECRET-77d0"
	purgeCDNEndpointID = "cdn-endpoint-1"
	purgeZoneID        = "zone-1"
)

// purgeShim stands in front of a real program, recording each command line it
// is run with, so a test can see that no secret was ever on one.
// PURGE_TEST_REFUSE_BATCH makes aws refuse a multi-object delete, as some
// S3-compatible stores do the checksum a newer aws CLI puts on it.
const purgeShim = `#!/bin/sh
[ -z "${PURGE_TEST_ARGV:-}" ] || printf '%s\n' "$0 $*" >> "$PURGE_TEST_ARGV"
if [ -n "${PURGE_TEST_REFUSE_BATCH:-}" ] && [ "${2:-}" = delete-objects ]; then
	echo "An error occurred (InvalidRequest) when calling the DeleteObjects operation: Missing required header for this request: Content-Md5" >&2
	exit 254
fi
exec REAL "$@"
`

type purgeRequest struct {
	Files    []string `json:"files"`
	Prefixes []string `json:"prefixes"`
}

type purging struct {
	t        *testing.T
	aws      string // the real aws CLI
	endpoint string
	bucket   string
	bin      string // the shims, first on the script's PATH
	argvLog  string
	cdn      *httptest.Server
	doAPI    *httptest.Server
	cfAPI    *httptest.Server

	mu       sync.Mutex
	doCache  map[string]bool // paths DigitalOcean's edge holds
	cfCache  map[string]bool // paths the Cloudflare zone holds
	doReqs   []purgeRequest
	cfReqs   []purgeRequest
	doStatus int  // answer every DigitalOcean purge with this, when set
	cfFail   bool // answer every Cloudflare purge 200 with success false
	sticky   bool // purges are acknowledged and purge nothing
}

func newPurging(t *testing.T) *purging {
	t.Helper()
	endpoint := os.Getenv("FLOCKDECK_TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("FLOCKDECK_TEST_S3_ENDPOINT names no S3-compatible store with versioning to purge; test.yml's purge-downloads job starts one")
	}
	aws := os.Getenv("FLOCKDECK_TEST_AWS")
	if aws == "" {
		var err error
		if aws, err = exec.LookPath("aws"); err != nil {
			t.Skip("no aws CLI to run the purge script with")
		}
	}
	curl, err := exec.LookPath("curl")
	if err != nil {
		t.Skip("no curl to run the purge script with")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh to run the purge script with")
	}
	suffix := make([]byte, 6)
	rand.Read(suffix)
	p := &purging{
		t: t, aws: aws, endpoint: strings.TrimSuffix(endpoint, "/"),
		bucket: "purge-test-" + hex.EncodeToString(suffix), bin: t.TempDir(),
		argvLog: filepath.Join(t.TempDir(), "argv"),
		doCache: map[string]bool{}, cfCache: map[string]bool{},
	}
	for name, real := range map[string]string{"aws": aws, "curl": curl} {
		shim := strings.Replace(purgeShim, "REAL", "'"+filepath.ToSlash(real)+"'", 1)
		if err := os.WriteFile(filepath.Join(p.bin, name), []byte(shim), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	p.awsOK("s3api", "create-bucket", "--bucket", p.bucket)
	p.awsOK("s3api", "put-bucket-versioning", "--bucket", p.bucket, "--versioning-configuration", "Status=Enabled")
	if got := p.awsOK("s3api", "get-bucket-versioning", "--bucket", p.bucket, "--query", "Status", "--output", "text"); strings.TrimSpace(got) != "Enabled" {
		t.Fatalf("the test bucket's versioning is %q; these tests need it on", got)
	}
	// Anyone may read a file, as on dl.flockdeck.ai, where the bucket is
	// private and each file public-read, as publish-downloads.sh uploads it.

	p.cdn = httptest.NewServer(http.HandlerFunc(p.serveCDN))
	p.doAPI = httptest.NewServer(http.HandlerFunc(p.serveDO))
	p.cfAPI = httptest.NewServer(http.HandlerFunc(p.serveCF))
	t.Cleanup(p.cdn.Close)
	t.Cleanup(p.doAPI.Close)
	t.Cleanup(p.cfAPI.Close)
	return p
}

// awsEnv is the environment the test's own aws calls run in.
func (p *purging) awsEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); strings.HasPrefix(k, "AWS_") || strings.HasPrefix(k, "DO_") || strings.HasPrefix(k, "CLOUDFLARE_") || strings.HasPrefix(k, "FLOCKDECK_") {
			continue
		}
		env = append(env, kv)
	}
	return append(env, "AWS_ACCESS_KEY_ID=the-key", "AWS_SECRET_ACCESS_KEY="+purgeSpacesSecret,
		"AWS_DEFAULT_REGION=us-east-1", "AWS_EC2_METADATA_DISABLED=true", "AWS_PAGER=")
}

func (p *purging) awsOK(args ...string) string {
	p.t.Helper()
	cmd := exec.Command("sh", append([]string{filepath.Join(p.bin, "aws")}, append(args, "--endpoint-url", p.endpoint)...)...)
	cmd.Env = p.awsEnv()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		p.t.Fatalf("aws %v: %v\n%s", args, err, stderr.String())
	}
	return string(out)
}

// upload puts files under prefix, each holding its name and round, all in
// one call of the aws CLI.
func (p *purging) upload(prefix string, names []string, round string) {
	p.t.Helper()
	dir := p.t.TempDir()
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(n+" "+round), 0o644); err != nil {
			p.t.Fatal(err)
		}
	}
	p.awsOK("s3", "cp", dir, "s3://"+p.bucket+"/"+prefix, "--recursive", "--acl", "public-read", "--only-show-errors")
}

func (p *purging) put(key, body string) {
	p.t.Helper()
	f := filepath.Join(p.t.TempDir(), "body")
	if err := os.WriteFile(f, []byte(body), 0o644); err != nil {
		p.t.Fatal(err)
	}
	p.awsOK("s3api", "put-object", "--bucket", p.bucket, "--key", key, "--body", f, "--acl", "public-read")
}

// versionsUnder is every file version and delete marker under prefix, each
// as "key version-id kind".
func (p *purging) versionsUnder(prefix string) []string {
	p.t.Helper()
	var out struct {
		Versions      []struct{ Key, VersionId string }
		DeleteMarkers []struct{ Key, VersionId string }
	}
	raw := p.awsOK("s3api", "list-object-versions", "--bucket", p.bucket, "--prefix", prefix, "--output", "json")
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &out); err != nil {
			p.t.Fatalf("list-object-versions: %v\n%s", err, raw)
		}
	}
	var all []string
	for _, v := range out.Versions {
		all = append(all, v.Key+" "+v.VersionId+" version")
	}
	for _, v := range out.DeleteMarkers {
		all = append(all, v.Key+" "+v.VersionId+" marker")
	}
	sort.Strings(all)
	return all
}

// serveCDN is dl.flockdeck.ai: the Cloudflare zone in front of DigitalOcean's
// edge in front of the bucket. A file either cache holds is served from it;
// anything else is asked of the bucket, as anyone may, and a file the bucket
// has is cached by both. The bucket being private, a file it does not have is
// 403, as Spaces answers it.
func (p *purging) serveCDN(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/")
	p.mu.Lock()
	cached := p.cfCache[path] || p.doCache[path]
	p.mu.Unlock()
	if cached {
		fmt.Fprint(w, "cached copy of "+path)
		return
	}
	resp, err := http.Get(p.endpoint + "/" + p.bucket + "/" + path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		http.Error(w, "AccessDenied", http.StatusForbidden)
		return
	}
	p.mu.Lock()
	p.cfCache[path], p.doCache[path] = true, true
	p.mu.Unlock()
	w.Write(body)
}

// status is what the stand-in for dl.flockdeck.ai answers for path.
func (p *purging) status(path string) int {
	p.t.Helper()
	resp, err := http.Get(p.cdn.URL + "/" + path)
	if err != nil {
		p.t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

// warm has every file under the prefixes downloaded through the CDN, so both
// caches hold them, as a release's files are held once anyone has fetched
// them.
func (p *purging) warm(prefixes ...string) {
	p.t.Helper()
	for _, prefix := range prefixes {
		for _, k := range p.currentKeys(prefix) {
			if s := p.status(k); s != http.StatusOK {
				p.t.Fatalf("%s is not served before the purge: %d", k, s)
			}
		}
	}
}

func (p *purging) currentKeys(prefix string) []string {
	p.t.Helper()
	out := p.awsOK("s3api", "list-objects-v2", "--bucket", p.bucket, "--prefix", prefix, "--query", "Contents[].Key", "--output", "text")
	var keys []string
	for _, k := range strings.Fields(out) {
		if k != "None" {
			keys = append(keys, k)
		}
	}
	return keys
}

func (p *purging) serveDO(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete || r.URL.Path != "/v2/cdn/endpoints/"+purgeCDNEndpointID+"/cache" {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if r.Header.Get("Authorization") != "Bearer "+purgeDOToken {
		http.Error(w, `{"id":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	var req purgeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Files) == 0 || len(req.Files) > 50 {
		http.Error(w, `{"id":"bad_request"}`, http.StatusBadRequest)
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.doReqs = append(p.doReqs, req)
	if p.doStatus != 0 {
		http.Error(w, `{"id":"server_error","message":"stand-in failure"}`, p.doStatus)
		return
	}
	if !p.sticky {
		for _, f := range req.Files {
			for k := range p.doCache {
				if k == f || (strings.HasSuffix(f, "*") && strings.HasPrefix(k, strings.TrimSuffix(f, "*"))) {
					delete(p.doCache, k)
				}
			}
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (p *purging) serveCF(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost || r.URL.Path != "/zones/"+purgeZoneID+"/purge_cache" {
		http.Error(w, `{"success":false}`, http.StatusNotFound)
		return
	}
	if r.Header.Get("Authorization") != "Bearer "+purgeCFToken {
		http.Error(w, `{"success":false,"errors":[{"code":10000,"message":"Authentication error"}]}`, http.StatusForbidden)
		return
	}
	var req purgeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Files)+len(req.Prefixes) == 0 ||
		len(req.Files) > 30 || len(req.Prefixes) > 30 || (len(req.Files) > 0 && len(req.Prefixes) > 0) {
		http.Error(w, `{"success":false,"errors":[{"code":1015,"message":"bad request"}]}`, http.StatusBadRequest)
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cfReqs = append(p.cfReqs, req)
	if p.cfFail {
		fmt.Fprint(w, `{"success":false,"errors":[{"code":1134,"message":"stand-in failure"}],"messages":[],"result":null}`)
		return
	}
	if !p.sticky {
		host := strings.TrimPrefix(p.cdn.URL, "http://")
		for k := range p.cfCache {
			for _, f := range req.Files {
				if f == p.cdn.URL+"/"+k {
					delete(p.cfCache, k)
				}
			}
			for _, pre := range req.Prefixes {
				if strings.HasPrefix(host+"/"+k, pre) {
					delete(p.cfCache, k)
				}
			}
		}
	}
	fmt.Fprint(w, `{"success":true,"errors":[],"messages":[],"result":{"id":"purge-1"}}`)
}

// run runs the script with args, and with the settings given on top of a
// full set, where a setting of "" leaves it out.
func (p *purging) run(settings map[string]string, args ...string) (string, error) {
	p.t.Helper()
	env := map[string]string{
		"DO_SPACES_KEY": "the-key", "DO_SPACES_SECRET": purgeSpacesSecret, "DO_SPACES_BUCKET": p.bucket,
		"DO_SPACES_REGION": "lon1", "DO_SPACES_ENDPOINT": p.endpoint,
		"DO_API_TOKEN": purgeDOToken, "DO_CDN_ENDPOINT_ID": purgeCDNEndpointID,
		"CLOUDFLARE_API_TOKEN": purgeCFToken, "CLOUDFLARE_ZONE_ID": purgeZoneID,
		"FLOCKDECK_DL_URL": p.cdn.URL, "FLOCKDECK_DO_API_URL": p.doAPI.URL, "FLOCKDECK_CLOUDFLARE_API_URL": p.cfAPI.URL,
		"FLOCKDECK_DO_PURGE_WAIT": "0", "FLOCKDECK_CHECK_WAIT": "0", "FLOCKDECK_CHECK_TRIES": "2",
		"PURGE_TEST_ARGV": p.argvLog,
	}
	for k, v := range settings {
		env[k] = v
	}
	cmd := exec.Command("sh", append([]string{filepath.Join("..", "..", "scripts", "purge-downloads.sh")}, args...)...)
	for _, kv := range p.awsEnv() {
		if strings.HasPrefix(kv, "AWS_") {
			continue
		}
		if k, v, _ := strings.Cut(kv, "="); strings.EqualFold(k, "PATH") {
			kv = k + "=" + p.bin + string(os.PathListSeparator) + v
		}
		cmd.Env = append(cmd.Env, kv)
	}
	for k, v := range env {
		if v != "" {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}
	out, err := cmd.CombinedOutput()
	// No secret is ever printed, nor put on a command line.
	argv, _ := os.ReadFile(p.argvLog)
	for _, secret := range []string{purgeDOToken, purgeCFToken, purgeSpacesSecret} {
		if bytes.Contains(out, []byte(secret)) {
			p.t.Errorf("the output holds the secret %s:\n%s", secret, out)
		}
		if bytes.Contains(argv, []byte(secret)) {
			p.t.Errorf("a command line held the secret %s:\n%s", secret, argv)
		}
	}
	return string(out), err
}

func (p *purging) requests() (do, cf []purgeRequest) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.doReqs), slices.Clone(p.cfReqs)
}

// release puts up a release's files as publish-downloads.sh does: n archives,
// checksums.txt and manifest.json, each signed.
func releaseFiles(version string, n int) []string {
	names := []string{"checksums.txt", "checksums.txt.sig", "manifest.json", "manifest.json.sig"}
	for i := range n {
		names = append(names, fmt.Sprintf("flockdeck_%s_platform%02d.zip", version, i))
	}
	return names
}

// seed makes the bucket a site: latest.json naming v2.0.0, v2.0.0 and
// v1.0.10, which are kept, and v1.0.1 with n archives to purge. v1.0.1 was
// uploaded twice, so every file has two versions, and one file of it was
// deleted since, so it is a delete marker in front of its bytes.
func (p *purging) seed(n int) []string {
	p.t.Helper()
	p.put("latest.json", `{"version":"v2.0.0"}`)
	p.upload("v2.0.0/", releaseFiles("v2.0.0", 2), "one")
	p.upload("v1.0.10/", releaseFiles("v1.0.10", 2), "one")
	names := append(releaseFiles("v1.0.1", n), "withdrawn.txt")
	p.upload("v1.0.1/", names, "one")
	p.upload("v1.0.1/", names, "two")
	p.warm("v1.0.1/", "v1.0.10/", "v2.0.0/")
	p.awsOK("s3", "rm", "s3://"+p.bucket+"/v1.0.1/withdrawn.txt", "--only-show-errors")
	var keys []string
	for _, name := range names {
		keys = append(keys, "v1.0.1/"+name)
	}
	sort.Strings(keys)
	return keys
}

// A dry run, which is what a run is unless it says --live, reports every
// file it would delete and changes nothing: not the bucket, not a cache, and
// no purge is asked for.
func TestPurgeScriptDryRunChangesNothing(t *testing.T) {
	p := newPurging(t)
	keys := p.seed(3)
	before := p.versionsUnder("")
	out, err := p.run(nil, "v1.0.1")
	if err != nil {
		t.Fatalf("dry run: %v\n%s", err, out)
	}
	if after := p.versionsUnder(""); !slices.Equal(before, after) {
		t.Errorf("a dry run changed the bucket:\nbefore %v\nafter  %v", before, after)
	}
	if do, cf := p.requests(); len(do)+len(cf) > 0 {
		t.Errorf("a dry run asked for purges: %v %v", do, cf)
	}
	for _, k := range keys {
		if !strings.Contains(out, k) {
			t.Errorf("the dry run does not name %s:\n%s", k, out)
		}
	}
	if !strings.Contains(out, "nothing was deleted or purged") || !strings.Contains(out, `"mode":"dry-run"`) {
		t.Errorf("the dry run does not say it changed nothing:\n%s", out)
	}
	if s := p.status("v1.0.1/checksums.txt"); s != http.StatusOK {
		t.Errorf("v1.0.1/checksums.txt answers %d after a dry run", s)
	}
}

// A live run deletes every version of every file under the version and every
// delete marker, not just what a listing shows, and nothing else; purges
// each file from DigitalOcean's CDN by path and from the Cloudflare zone by
// exact URL, 30 to a request; and checks each is gone.
func TestPurgeScriptPurgesEveryVersionAndBothCaches(t *testing.T) {
	p := newPurging(t)
	keys := p.seed(40) // 45 files: two requests to DigitalOcean, two to Cloudflare
	keptBefore := append(p.versionsUnder("v1.0.10/"), p.versionsUnder("v2.0.0/")...)
	under := p.versionsUnder("v1.0.1/")
	var markers int
	for _, v := range under {
		if strings.HasSuffix(v, " marker") {
			markers++
		}
	}
	if markers != 1 || len(under) != 2*len(keys)+1 {
		t.Fatalf("the seeded bucket is not what this tests: %d versions and markers, %d markers", len(under), markers)
	}

	out, err := p.run(nil, "--live", "--confirm", "purge 1 versions", "v1.0.1")
	if err != nil {
		t.Fatalf("purge: %v\n%s", err, out)
	}
	if left := p.versionsUnder("v1.0.1/"); len(left) > 0 {
		t.Errorf("left in the bucket: %v", left)
	}
	if kept := append(p.versionsUnder("v1.0.10/"), p.versionsUnder("v2.0.0/")...); !slices.Equal(kept, keptBefore) {
		t.Errorf("other versions changed:\nbefore %v\nafter  %v", keptBefore, kept)
	}
	if got := p.currentKeys("latest.json"); len(got) != 1 {
		t.Errorf("latest.json is gone")
	}

	do, cf := p.requests()
	var doFiles, cfFiles []string
	for _, r := range do {
		doFiles = append(doFiles, r.Files...)
	}
	for _, r := range cf {
		if len(r.Prefixes) > 0 {
			t.Errorf("Cloudflare was asked to purge by prefix for a version it had the files of: %v", r.Prefixes)
		}
		cfFiles = append(cfFiles, r.Files...)
	}
	sort.Strings(doFiles)
	sort.Strings(cfFiles)
	wantDO := append(slices.Clone(keys), "v1.0.1/*")
	sort.Strings(wantDO)
	if !slices.Equal(doFiles, wantDO) {
		t.Errorf("DigitalOcean was asked to purge\n%v\nwant\n%v", doFiles, wantDO)
	}
	var wantCF []string
	for _, k := range keys {
		wantCF = append(wantCF, p.cdn.URL+"/"+k)
	}
	if !slices.Equal(cfFiles, wantCF) {
		t.Errorf("Cloudflare was asked to purge\n%v\nwant\n%v", cfFiles, wantCF)
	}
	if len(do) != 1 || len(cf) != 2 || len(cf[0].Files) != 30 || len(cf[1].Files) != len(keys)-30 {
		t.Errorf("sent %d requests to DigitalOcean and %d to Cloudflare; want 1, and 30 URLs then %d", len(do), len(cf), len(keys)-30)
	}
	for _, k := range keys {
		if s := p.status(k); s != http.StatusForbidden {
			t.Errorf("%s answers %d after the purge", k, s)
		}
	}
	if s := p.status("v1.0.10/checksums.txt"); s != http.StatusOK {
		t.Errorf("v1.0.10/checksums.txt answers %d; purging v1.0.1 took it too", s)
	}
	if !strings.Contains(out, "done: 1 versions are gone") || !strings.Contains(out, `"outcome":"done"`) {
		t.Errorf("the run does not say it is done:\n%s", out)
	}
}

// DigitalOcean is sent at most 50 paths to a request.
func TestPurgeScriptBatchesDigitalOcean(t *testing.T) {
	p := newPurging(t)
	keys := p.seed(60)
	out, err := p.run(nil, "--live", "--confirm", "purge 1 versions", "v1.0.1")
	if err != nil {
		t.Fatalf("purge: %v\n%s", err, out)
	}
	do, cf := p.requests()
	if len(do) != 2 || len(do[0].Files) != 50 || len(do[1].Files) != len(keys)+1-50 {
		t.Errorf("DigitalOcean was sent %d requests; want 50 paths, then %d", len(do), len(keys)+1-50)
	}
	if len(cf) != 3 {
		t.Errorf("Cloudflare was sent %d requests for %d files; want 3", len(cf), len(keys))
	}
}

// A store that refuses a multi-object delete has the files deleted one at a
// time instead, and the list after is what says it worked.
func TestPurgeScriptDeletesOneAtATimeWhenABatchIsRefused(t *testing.T) {
	p := newPurging(t)
	p.seed(1)
	out, err := p.run(map[string]string{"PURGE_TEST_REFUSE_BATCH": "1"}, "--live", "--confirm", "purge 1 versions", "v1.0.1")
	if err != nil {
		t.Fatalf("purge: %v\n%s", err, out)
	}
	if left := p.versionsUnder("v1.0.1/"); len(left) > 0 {
		t.Errorf("left in the bucket: %v", left)
	}
	if !strings.Contains(out, "deleting one at a time") {
		t.Errorf("the run does not say it deleted one at a time:\n%s", out)
	}
}

// Everything that is refused is refused before anything is deleted or
// purged, and says why.
func TestPurgeScriptRefuses(t *testing.T) {
	many := make([]string, 101)
	for i := range many {
		many[i] = fmt.Sprintf("v0.0.%d", i)
	}
	cases := []struct {
		name  string
		setup func(p *purging)
		args  []string
		want  string
	}{
		{"not a version", nil, []string{"--live", "--confirm", "purge 2 versions", "v1.0.1", "1.0.2"}, "not a version"},
		{"a path", nil, []string{"--live", "--confirm", "purge 1 versions", "v1.0.1/../v2.0.0"}, "not a version"},
		{"a wildcard", nil, []string{"v1.0.*"}, "not a version"},
		{"the latest", nil, []string{"--live", "--confirm", "purge 2 versions", "v1.0.1", "v2.0.0"}, "v2.0.0 (it is the latest release, named by latest.json)"},
		{"in releases.json", func(p *purging) {
			p.put("releases.json", `{"versions":[{"version":"v2.0.0","date":"2026-01-01T00:00:00Z"},{"version":"v1.0.1","notes":"see v1.0.10"}]}`)
		}, []string{"--live", "--confirm", "purge 1 versions", "v1.0.1"}, "v1.0.1 (releases.json names it)"},
		{"in recalled.json", func(p *purging) {
			p.put("recalled.json", `{"versions":[{"version":"v1.0.1","reason":"broken"}]}`)
		}, []string{"--live", "--confirm", "purge 1 versions", "v1.0.1"}, "v1.0.1 (recalled.json names it)"},
		{"no latest.json", func(p *purging) {
			p.awsOK("s3", "rm", "s3://"+p.bucket+"/latest.json", "--only-show-errors")
		}, []string{"--live", "--confirm", "purge 1 versions", "v1.0.1"}, "no latest.json"},
		{"latest.json naming nothing", func(p *purging) { p.put("latest.json", `{}`) },
			[]string{"--live", "--confirm", "purge 1 versions", "v1.0.1"}, "latest.json names no version"},
		{"no versions", nil, []string{"--live", "--confirm", "purge 0 versions"}, "no versions named"},
		{"only spaces", nil, []string{"--live", " \n "}, "no versions named"},
		{"too many", nil, append([]string{"--live", "--confirm", "purge 101 versions"}, many...), "101 versions is more than one run may purge (100)"},
		{"unconfirmed", nil, []string{"--live", "v1.0.1"}, `--confirm "purge 1 versions"`},
		{"miscounted", nil, []string{"--live", "--confirm", "purge 2 versions", "v1.0.1", "v1.0.1"}, `it must say exactly "purge 1 versions"`},
		{"a file that cannot be purged exactly", func(p *purging) { p.put(`v1.0.1/a "quoted" name`, "x") },
			[]string{"--live", "--confirm", "purge 1 versions", "v1.0.1"}, "could not be purged or checked exactly"},
		{"an unknown option", nil, []string{"--liev", "v1.0.1"}, "unknown option --liev"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := newPurging(t)
			p.seed(1)
			if c.setup != nil {
				c.setup(p)
			}
			before := p.versionsUnder("")
			out, err := p.run(nil, c.args...)
			if err == nil {
				t.Fatalf("the run was not refused:\n%s", out)
			}
			if !strings.Contains(out, c.want) {
				t.Errorf("the refusal does not say %q:\n%s", c.want, out)
			}
			if after := p.versionsUnder(""); !slices.Equal(before, after) {
				t.Errorf("the bucket changed:\nbefore %v\nafter  %v", before, after)
			}
			if do, cf := p.requests(); len(do)+len(cf) > 0 {
				t.Errorf("purges were asked for: %v %v", do, cf)
			}
		})
	}
}

// The same version twice is one; more than 100 are let through when --max
// says so; and --check-input says how many there are, needing no settings.
func TestPurgeScriptCountsVersions(t *testing.T) {
	p := newPurging(t)
	p.seed(1)
	out, err := p.run(map[string]string{"DO_SPACES_KEY": "", "DO_SPACES_BUCKET": ""},
		"--check-input", "--confirm", "purge 2 versions", "v1.0.1 v1.0.1\nv1.0.2", "v1.0.1")
	if err != nil || !strings.Contains(out, "2 versions: v1.0.1 v1.0.2") {
		t.Errorf("--check-input: %v\n%s", err, out)
	}
	many := make([]string, 101)
	for i := range many {
		many[i] = fmt.Sprintf("v0.0.%d", i)
	}
	if out, err := p.run(nil, append([]string{"--check-input", "--max", "101"}, many...)...); err != nil {
		t.Errorf("--max 101 refused 101 versions: %v\n%s", err, out)
	}
}

// Every setting a live run needs that is missing is named at once, and a dry
// run, which needs only the bucket, names what a live run would miss.
func TestPurgeScriptNamesWhatIsMissing(t *testing.T) {
	p := newPurging(t)
	p.seed(1)
	unset := map[string]string{"DO_API_TOKEN": "", "CLOUDFLARE_ZONE_ID": "", "DO_SPACES_SECRET": ""}
	out, err := p.run(unset, "--live", "--confirm", "purge 1 versions", "v1.0.1")
	if err == nil || !strings.Contains(out, "not set: DO_SPACES_SECRET DO_API_TOKEN CLOUDFLARE_ZONE_ID") {
		t.Errorf("want all three named: %v\n%s", err, out)
	}
	out, err = p.run(map[string]string{"DO_API_TOKEN": "", "CLOUDFLARE_ZONE_ID": ""}, "v1.0.1")
	if err != nil || !strings.Contains(out, "a live run would also need: DO_API_TOKEN CLOUDFLARE_ZONE_ID") {
		t.Errorf("a dry run: %v\n%s", err, out)
	}
	if left := p.versionsUnder("v1.0.1/"); len(left) == 0 {
		t.Error("the bucket was purged all the same")
	}
}

// A version with nothing left in the bucket is reported, not an error, so a
// run can be repeated; it is still purged by prefix and checked gone.
func TestPurgeScriptRunsAgain(t *testing.T) {
	p := newPurging(t)
	p.seed(2)
	args := []string{"--live", "--confirm", "purge 2 versions", "v1.0.1", "v1.0.3"}
	if out, err := p.run(nil, args...); err != nil {
		t.Fatalf("purge: %v\n%s", err, out)
	}
	out, err := p.run(nil, args...)
	if err != nil {
		t.Fatalf("purge again: %v\n%s", err, out)
	}
	if !strings.Contains(out, "v1.0.1: nothing in the bucket") || !strings.Contains(out, "v1.0.3: nothing in the bucket") {
		t.Errorf("the second run does not say the versions were already gone:\n%s", out)
	}
}

// A purge that fails fails the run, and nothing says it succeeded. The files
// are already gone from the bucket by then, so the run said nothing about
// them to purge next time: run again, the versions are purged by prefix, and
// every file is gone.
func TestPurgeScriptFailsWhenAPurgeFails(t *testing.T) {
	cases := map[string]func(p *purging){
		"DigitalOcean answers 500":         func(p *purging) { p.doStatus = http.StatusInternalServerError },
		"Cloudflare answers success false": func(p *purging) { p.cfFail = true },
	}
	for name, spoil := range cases {
		t.Run(name, func(t *testing.T) {
			p := newPurging(t)
			keys := p.seed(2)
			p.mu.Lock()
			spoil(p)
			p.mu.Unlock()
			out, err := p.run(nil, "--live", "--confirm", "purge 1 versions", "v1.0.1")
			if err == nil {
				t.Fatalf("the run succeeded:\n%s", out)
			}
			if strings.Contains(out, "done:") || strings.Contains(out, `"outcome":"done"`) || !strings.Contains(out, "purge FAILED") {
				t.Errorf("the run does not say it failed, or says it is done:\n%s", out)
			}
			p.mu.Lock()
			p.doStatus, p.cfFail = 0, false
			p.mu.Unlock()
			if out, err := p.run(nil, "--live", "--confirm", "purge 1 versions", "v1.0.1"); err != nil {
				t.Fatalf("run again: %v\n%s", err, out)
			}
			for _, k := range keys {
				if s := p.status(k); s != http.StatusForbidden {
					t.Errorf("%s answers %d after running again", k, s)
				}
			}
			_, cf := p.requests()
			if last := cf[len(cf)-1]; !slices.Equal(last.Prefixes, []string{strings.TrimPrefix(p.cdn.URL, "http://") + "/v1.0.1/"}) {
				t.Errorf("running again purged Cloudflare with %+v; want v1.0.1's prefix", last)
			}
		})
	}
}

// Purges that are acknowledged and purge nothing leave the files served,
// and the run fails, naming them.
func TestPurgeScriptFailsWhenAFileIsStillServed(t *testing.T) {
	p := newPurging(t)
	p.seed(1)
	p.mu.Lock()
	p.sticky = true
	p.mu.Unlock()
	out, err := p.run(nil, "--live", "--confirm", "purge 1 versions", "v1.0.1")
	if err == nil {
		t.Fatalf("the run succeeded:\n%s", out)
	}
	if !strings.Contains(out, "STILL SERVED") || !strings.Contains(out, p.cdn.URL+"/v1.0.1/checksums.txt (HTTP 200)") {
		t.Errorf("the run does not name what is still served:\n%s", out)
	}
	if strings.Contains(out, "done:") {
		t.Errorf("the run says it is done:\n%s", out)
	}
}

// Only an explicit flag leaves the CDN unpurged, and it says, loudly, what
// that leaves; --no-cloudflare leaves out the zone and nothing else.
func TestPurgeScriptLeavesOutPurgesOnlyWhenTold(t *testing.T) {
	p := newPurging(t)
	p.seed(1)
	out, err := p.run(map[string]string{"CLOUDFLARE_API_TOKEN": "", "CLOUDFLARE_ZONE_ID": ""},
		"--live", "--no-cloudflare", "--confirm", "purge 1 versions", "v1.0.1")
	// Cloudflare's cache still holds the files, and the check says so.
	if err == nil || !strings.Contains(out, "--no-cloudflare: no Cloudflare zone is purged") {
		t.Errorf("--no-cloudflare: %v\n%s", err, out)
	}
	if do, cf := p.requests(); len(do) == 0 || len(cf) > 0 {
		t.Errorf("--no-cloudflare sent %d requests to DigitalOcean and %d to Cloudflare", len(do), len(cf))
	}

	p = newPurging(t)
	p.seed(1)
	out, err = p.run(map[string]string{"DO_API_TOKEN": "", "CLOUDFLARE_API_TOKEN": ""},
		"--live", "--no-cdn-purge", "--confirm", "purge 1 versions", "v1.0.1")
	if err != nil {
		t.Fatalf("--no-cdn-purge: %v\n%s", err, out)
	}
	if !strings.Contains(out, "stays downloadable from DigitalOcean's and Cloudflare's edges for up to a year") {
		t.Errorf("--no-cdn-purge does not warn:\n%s", out)
	}
	if do, cf := p.requests(); len(do)+len(cf) > 0 {
		t.Errorf("--no-cdn-purge asked for purges")
	}
}

// The summaries are written where asked, and the Markdown one names every
// file.
func TestPurgeScriptWritesSummaries(t *testing.T) {
	p := newPurging(t)
	keys := p.seed(1)
	dir := t.TempDir()
	js, md := filepath.Join(dir, "s.json"), filepath.Join(dir, "s.md")
	if out, err := p.run(nil, "--json", js, "--markdown", md, "v1.0.1", "v1.0.3"); err != nil {
		t.Fatalf("dry run: %v\n%s", err, out)
	}
	var s struct {
		Mode, Outcome string
		Versions      []struct {
			Version, Status string
			Files           int
			Keys            []string
		}
	}
	raw, _ := os.ReadFile(js)
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("the JSON summary: %v\n%s", err, raw)
	}
	if s.Mode != "dry-run" || len(s.Versions) != 2 || !slices.Equal(s.Versions[0].Keys, keys) || s.Versions[1].Files != 0 {
		t.Errorf("the JSON summary is %+v", s)
	}
	mdText, _ := os.ReadFile(md)
	for _, k := range keys {
		if !strings.Contains(string(mdText), "- "+k) {
			t.Errorf("the Markdown summary does not name %s:\n%s", k, mdText)
		}
	}
}
