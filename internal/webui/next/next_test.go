package next

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/help"
	"github.com/jmwri/flockdeck/internal/webui"
)

const nextShellMarker = `<meta name="flockdeck-shell" content="next">`

// serve answers the page and /assets/ from the tagged build's file system the
// way the server does, so these tests do not start an instance.
func serve(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		b, err := fs.ReadFile(webui.FS(), "index.html")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(b)
	})
	mux.Handle("/assets/", http.StripPrefix("/assets/", http.FileServerFS(webui.FS())))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func get(t *testing.T, url string) (body, contentType string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: %s", url, resp.Status)
	}
	return string(b), resp.Header.Get("Content-Type")
}

var (
	moduleScript = regexp.MustCompile(`<script\s+type="module"\s+src="([^"]+)"\s*></script>`)
	// Static imports, bare side-effect imports, re-exports and import(...).
	importSpec = regexp.MustCompile(`(?:\bfrom\s*|\bimport\s*\(?\s*)["']([^"']+)["']`)

	scriptTag  = regexp.MustCompile(`(?is)<script\b[^>]*>`)
	hasSrc     = regexp.MustCompile(`(?i)\bsrc\s*=`)
	srcOrHref  = regexp.MustCompile(`(?i)\b(?:src|href|action)\s*=\s*["']([^"']*)["']`)
	eventAttr  = regexp.MustCompile(`(?i)\son[a-z]+\s*=`)
	styleInUse = regexp.MustCompile(`(?i)<style\b|\sstyle\s*=`)
	absolute   = regexp.MustCompile(`(?i)(?:https?:)?//[a-z0-9.-]+\.[a-z]{2,}|\bwss?://|\bftp://|\bdata:text/html`)
	evalUse    = regexp.MustCompile(`\beval\s*\(|\bnew\s+Function\b|\bFunction\s*\(`)
	// Ways to turn text into markup or code that eval and new Function do not
	// cover. Preact's own source mentions some, so these apply to the files
	// written here and not to vendor/.
	markupSink = regexp.MustCompile(`\.(?:innerHTML|outerHTML)\b|\binsertAdjacentHTML\b|\bdangerouslySetInnerHTML\b|\bdocument\s*\.\s*write(?:ln)?\s*\(|\bset(?:Timeout|Interval)\s*\(\s*["\x27` + "`" + `]`)
	dynamicImp = regexp.MustCompile(`\bimport\s*\(\s*(?:[^"'\s]|["'][^"']*["']\s*[^)\s])`)
)

// The page loads: index.html names a module, the module and everything it
// imports is served as JavaScript, and the hello component is among them.
func TestPageLoadsTheLiveShell(t *testing.T) {
	srv := serve(t)
	page, ct := get(t, srv.URL+"/")
	if !strings.HasPrefix(ct, "text/html") {
		t.Errorf("page Content-Type = %q", ct)
	}
	if !strings.Contains(page, nextShellMarker) {
		t.Error("the page is not the new shell's")
	}
	if !strings.Contains(page, `<div id="app"></div>`) {
		t.Error("the page has nowhere to draw")
	}
	m := moduleScript.FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("no module script in the page:\n%s", page)
	}

	// Follow the imports from the entry module the way a browser would.
	seen := map[string]bool{}
	var visit func(url string)
	visit = func(url string) {
		if seen[url] {
			return
		}
		seen[url] = true
		src, ct := get(t, url)
		if !strings.Contains(ct, "javascript") {
			t.Errorf("%s is served as %q, which a browser refuses as a module", url, ct)
		}
		base := url[:strings.LastIndex(url, "/")+1]
		for _, sm := range importSpec.FindAllStringSubmatch(src, -1) {
			ref := sm[1]
			if !strings.HasPrefix(ref, "./") && !strings.HasPrefix(ref, "../") {
				t.Errorf("%s imports %q, which is not a relative path", url, ref)
				continue
			}
			visit(srv.URL + path.Clean(strings.TrimPrefix(base, srv.URL)+ref))
		}
	}
	// "assets/main.js" from a page at the root is /assets/main.js.
	visit(srv.URL + "/" + m[1])
	// The entry module wires the live store: the socket, the store, the home
	// screen with the graph, and the end-to-end key's loader.
	var got []string
	for k := range seen {
		got = append(got, k)
	}
	sort.Strings(got)
	for _, want := range []string{"lib/store.js", "lib/socket.js", "lib/e2e-boot.js", "shell/Live.js", "shell/Home.js", "graph/Graph.js", "graph/Edges.js", "graph/ManagerBar.js", "drawer/Conversation.js", "drawer/Structured.js"} {
		if !seen[srv.URL+"/assets/"+want] {
			t.Errorf("%s is not reached from the entry module; reached %v", want, got)
		}
	}
}

// The relay serves the page under script-src 'self'. Nothing here may need a
// looser policy: no inline script or style, no handler attribute, no eval, no
// absolute URL, and no src that leaves the files served beside the page.
func TestNextAssetsKeepToTheContentPolicy(t *testing.T) {
	var checked int
	err := fs.WalkDir(os.DirFS("assets"), ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		ext := path.Ext(name)
		if ext != ".html" && ext != ".js" && ext != ".mjs" {
			return nil
		}
		b, err := os.ReadFile(filepath.Join("assets", filepath.FromSlash(name)))
		if err != nil {
			return err
		}
		checked++
		src := string(b)

		if evalUse.MatchString(src) {
			t.Errorf("%s evaluates text as code", name)
		}
		if dynamicImp.MatchString(src) {
			t.Errorf("%s imports a path computed at run time", name)
		}
		if ext != ".html" {
			for _, sm := range importSpec.FindAllStringSubmatch(src, -1) {
				ref := sm[1]
				if !strings.HasPrefix(ref, "./") && !strings.HasPrefix(ref, "../") {
					t.Errorf("%s imports %q: only relative paths load without an import map", name, ref)
					continue
				}
				target := path.Join(path.Dir(name), ref)
				if _, err := os.Stat(filepath.Join("assets", filepath.FromSlash(target))); err != nil {
					t.Errorf("%s imports %s, which is not in next/assets", name, ref)
				}
			}
		}
		// A library's own strings hold namespace URLs; the files written here
		// must hold none.
		if !strings.HasPrefix(name, "vendor/") {
			if absolute.MatchString(src) {
				t.Errorf("%s holds an absolute URL", name)
			}
			if markupSink.MatchString(src) {
				t.Errorf("%s turns text into markup or code", name)
			}
		}
		if ext == ".html" {
			for _, tag := range scriptTag.FindAllString(src, -1) {
				if !hasSrc.MatchString(tag) {
					t.Errorf("%s has an inline script: %s", name, tag)
				}
			}
			if eventAttr.MatchString(src) {
				t.Errorf("%s has an inline event handler", name)
			}
			if styleInUse.MatchString(src) {
				t.Errorf("%s has inline style", name)
			}
			for _, sm := range srcOrHref.FindAllStringSubmatch(src, -1) {
				v := sm[1]
				if strings.HasPrefix(v, "/") || strings.Contains(v, ":") || strings.HasPrefix(v, "..") || !strings.HasPrefix(path.Clean(v), "assets/") {
					t.Errorf("%s has %q, which is not a path under assets/", name, v)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 6 {
		t.Errorf("only %d files were checked", checked)
	}
}

// The scanner has to fail on what it is there to catch.
func TestContentPolicyPatternsCatchWhatTheyAreFor(t *testing.T) {
	for _, s := range []string{"eval(x)", "x = new Function('a')", "Function('return 1')()"} {
		if !evalUse.MatchString(s) {
			t.Errorf("eval pattern misses %q", s)
		}
	}
	for _, s := range []string{
		`el.innerHTML = x`, `el.outerHTML = x`, `el.insertAdjacentHTML("beforeend", x)`,
		`<div dangerouslySetInnerHTML=${{ __html: x }} />`, `document.write(x)`, `document . writeln (x)`,
		`setTimeout("go()", 5)`, `setInterval('go()', 5)`, "setTimeout(`go()`, 5)",
	} {
		if !markupSink.MatchString(s) {
			t.Errorf("markup sink pattern misses %q", s)
		}
	}
	for _, s := range []string{`setTimeout(() => go(), 5)`, `setTimeout(go, 5)`, `el.textContent = x`} {
		if markupSink.MatchString(s) {
			t.Errorf("markup sink pattern catches %q", s)
		}
	}
	for _, s := range []string{`import(name)`, `import( "./a.js" + x)`} {
		if !dynamicImp.MatchString(s) {
			t.Errorf("dynamic import pattern misses %q", s)
		}
	}
	for _, s := range []string{`https://cdn.example.com/x.js`, `//cdn.example.com/x.js`, `wss://host/ws`} {
		if !absolute.MatchString(s) {
			t.Errorf("absolute URL pattern misses %q", s)
		}
	}
	for _, s := range []string{`<script>alert(1)</script>`, `<script type="module">x</script>`} {
		if tag := scriptTag.FindString(s); tag == "" || hasSrc.MatchString(tag) {
			t.Errorf("inline script pattern misses %q", s)
		}
	}
	for _, s := range []string{`<a onclick="x()">`} {
		if !eventAttr.MatchString(s) {
			t.Errorf("handler pattern misses %q", s)
		}
	}
	for _, s := range []string{`<p style="color:red">`, `<style>p{}</style>`} {
		if !styleInUse.MatchString(s) {
			t.Errorf("style pattern misses %q", s)
		}
	}
	for _, s := range []string{`import "lodash"`, `import { h } from 'preact'`} {
		m := importSpec.FindStringSubmatch(s)
		if m == nil || strings.HasPrefix(m[1], ".") {
			t.Errorf("import pattern misses a bare specifier in %q", s)
		}
	}
}

// The vendored files are the ones recorded in checksums.txt, and no module is
// there without a record.
func TestVendoredFilesMatchTheirRecordedHash(t *testing.T) {
	raw, err := os.ReadFile("checksums.txt")
	if err != nil {
		t.Fatal(err)
	}
	recorded := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			t.Fatalf("checksums.txt line %q", line)
		}
		recorded[fields[1]] = fields[0]
	}
	for name, want := range recorded {
		b, err := os.ReadFile(filepath.FromSlash(name))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		sum := sha256.Sum256(b)
		if got := hex.EncodeToString(sum[:]); got != want {
			t.Errorf("%s has hash %s, recorded %s", name, got, want)
		}
	}
	for _, dir := range []string{"assets/vendor", "test/vendor"} {
		ents, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range ents {
			name := dir + "/" + e.Name()
			if strings.HasSuffix(name, ".mjs") && recorded[name] == "" {
				t.Errorf("%s has no recorded hash", name)
			}
		}
	}
	if len(recorded) < 4 {
		t.Errorf("only %d files recorded", len(recorded))
	}
	lic, err := os.ReadFile("assets/vendor/LICENSES.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"preact", "htm", "10.29.8", "3.1.1", "Apache License", "The MIT License"} {
		if !strings.Contains(string(lic), want) {
			t.Errorf("LICENSES.md does not mention %q", want)
		}
	}
}

// The node tests run from here. Node is a development and CI tool only: it is
// not bundled and is not part of any release.
//
// Each test file is its own node process and its own subtest, so a failure or a
// hang names the file, and the suite is no longer one process whose limit is the
// sum of all of them. At most nodeTestProcesses run at once: node's own default
// is one fewer than the machine's cores, which on a many-core machine held about
// 25 GB between the processes, and on a three-core CI runner is not the same
// number as on another. The step's time on CI was 38 to 66 s for the whole
// package (Linux, macOS and Windows), and one macOS run was killed at the old
// single limit of 120 s, so a file gets nodeFileLimit, well past the longest
// file, and a single test in it gets nodeTestLimit so a hung one is reported by
// name before the file's limit.
//
// Killing the Go test binary itself (the go test -timeout panic) does not stop
// the node processes it started on Windows: they stay alive until they finish or
// are ended by hand.
const (
	nodeTestProcesses = 2
	nodeFileLimit     = 4 * time.Minute
	nodeTestLimitMS   = "60000"
)

// node reports a run in one of two forms, by version and by whether the output
// is a terminal: the spec form ("ℹ pass 28", "✔ file.test.mjs (5ms)") and the
// TAP form ("# pass 28", "ok 1 - file.test.mjs"; the CI runners' node prints it).
// A file with no tests in it is reported as one pass, of the file itself.
var (
	nodePassed   = regexp.MustCompile(`(?m)^(?:ℹ|#) pass (\d+)`)
	nodeFileOnly = regexp.MustCompile(`(?m)^(?:✔ \S*\.test\.mjs \(|ok \d+ - \S*\.test\.mjs$)`)
)

// nodeRanTests says whether node's report holds at least one test that passed
// besides the file itself.
func nodeRanTests(out []byte) bool {
	m := nodePassed.FindSubmatch(out)
	if m == nil || string(m[1]) == "0" {
		return false
	}
	return !(string(m[1]) == "1" && nodeFileOnly.Match(out))
}

func TestNodeRanTests(t *testing.T) {
	for _, c := range []struct {
		name, out string
		want      bool
	}{
		{"spec, tests", "✔ one (1ms)\n✔ two (1ms)\nℹ tests 2\nℹ suites 0\nℹ pass 2\nℹ fail 0\n", true},
		{"spec, one test", "✔ only one (1ms)\nℹ tests 1\nℹ pass 1\nℹ fail 0\n", true},
		{"spec, empty file", "✔ zz.test.mjs (48.7919ms)\nℹ tests 1\nℹ suites 0\nℹ pass 1\nℹ fail 0\n", false},
		{"spec, none passed", "ℹ tests 1\nℹ pass 0\nℹ fail 1\n", false},
		{"tap, tests", "TAP version 13\n# Subtest: the palette key, read from the table, opens the palette and is claimed\nok 1 - the palette key, read from the table, opens the palette and is claimed\n  ---\n  duration_ms: 548.6287\n  type: 'test'\n  ...\nok 28 - a binding that cannot be read is said, not silently dead\n1..28\n# tests 28\n# suites 0\n# pass 28\n# fail 0\n", true},
		{"tap, one test", "TAP version 13\n# Subtest: one\nok 1 - one\n1..1\n# tests 1\n# pass 1\n# fail 0\n", true},
		{"tap, empty file", "TAP version 13\n# Subtest: zz.test.mjs\nok 1 - zz.test.mjs\n  ---\n  duration_ms: 119.4751\n  type: 'test'\n  ...\n1..1\n# tests 1\n# suites 0\n# pass 1\n# fail 0\n", false},
		{"tap, empty file with a path", "TAP version 13\n# Subtest: test\\zz.test.mjs\nok 1 - test\\zz.test.mjs\n1..1\n# tests 1\n# pass 1\n# fail 0\n", false},
		{"no summary", "", false},
	} {
		if got := nodeRanTests([]byte(c.out)); got != c.want {
			t.Errorf("%s: nodeRanTests = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestNodeTests(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal("node is not on PATH, and CI is set: the new shell's tests cannot run where they are meant to")
		}
		t.Skip("node is not on PATH, so the new shell's tests cannot run")
	}
	files, err := filepath.Glob(filepath.Join("test", "*.test.mjs"))
	if err != nil || len(files) < 2 {
		t.Fatalf("test files %v, %v", files, err)
	}
	// Every test file in the directory must become a subtest, so a loop that
	// skipped or stopped early cannot pass as a smaller suite.
	entries, err := os.ReadDir("test")
	if err != nil {
		t.Fatal(err)
	}
	want := 0
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".test.mjs") {
			want++
		}
	}
	var ran atomic.Int32
	t.Cleanup(func() {
		if got := int(ran.Load()); got != want && !t.Failed() {
			t.Errorf("%d node test files ran, the directory has %d", got, want)
		}
	})
	// The server's own key table, as the hello sends it, so the shell's tests
	// run against the bindings that ship and not a copy written out here.
	table, err := json.Marshal(help.Keys)
	if err != nil {
		t.Fatal(err)
	}
	keysFile := filepath.Join(t.TempDir(), "keys.json")
	if err := os.WriteFile(keysFile, table, 0o600); err != nil {
		t.Fatal(err)
	}
	// The help pages as the server sends them to this shell, for the tests of the
	// help dialog to draw every real page.
	pages, err := help.TreePages()
	if err != nil {
		t.Fatal(err)
	}
	helpJSON, err := json.Marshal(map[string]any{"pages": pages})
	if err != nil {
		t.Fatal(err)
	}
	helpFile := filepath.Join(t.TempDir(), "help.json")
	if err := os.WriteFile(helpFile, helpJSON, 0o600); err != nil {
		t.Fatal(err)
	}
	slots := make(chan struct{}, nodeTestProcesses)
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			t.Parallel()
			ran.Add(1)
			slots <- struct{}{}
			defer func() { <-slots }()
			ctx, cancel := context.WithTimeout(context.Background(), nodeFileLimit)
			defer cancel()
			cmd := exec.CommandContext(ctx, node, "--test", "--test-timeout="+nodeTestLimitMS, file)
			// testiso.Main has already pointed the home and config variables at a
			// throwaway directory and cleared the credentials; node inherits that.
			cmd.Env = append(os.Environ(), "NEXT_KEYS_JSON="+keysFile, "NEXT_HELP_JSON="+helpFile)
			cmd.WaitDelay = 5 * time.Second
			start := time.Now()
			out, err := cmd.CombinedOutput()
			t.Logf("%s took %s", file, time.Since(start).Round(100*time.Millisecond))
			if ctx.Err() == context.DeadlineExceeded {
				t.Fatalf("killed after %s\n%s", nodeFileLimit, out)
			}
			if err != nil {
				t.Fatalf("node tests failed: %v\n%s", err, out)
			}
			// An empty file exits 0 and node counts the file itself as the one pass,
			// so the count is read, not the word "pass".
			if !nodeRanTests(out) {
				t.Errorf("node reported no passes:\n%s", out)
			}
		})
	}
}

// The end-to-end layer is the old window's file, served as it is. The terminal
// module uses it through FlockdeckE2E, so a copy that drifted from the old one
// would seal frames the desktop cannot open.
func TestE2EIsTheOldWindowsFileByteForByte(t *testing.T) {
	served, err := fs.ReadFile(webui.FS(), "e2e.js")
	if err != nil {
		t.Fatal(err)
	}
	own, err := os.ReadFile(filepath.Join("assets", "e2e.js"))
	if err != nil {
		t.Fatalf("next/assets has no e2e.js: %v", err)
	}
	if string(served) != string(own) {
		t.Error("the e2e.js served is not next/assets/e2e.js")
	}
}

// The terminal's libraries are not in the page: as blocking scripts they were
// about 1.1 s of first paint (docs/first-load.md). The page holds the module
// and nothing else, and the loader names the three files, in the order a
// terminal needs them, each of which the tagged build serves as JavaScript from
// the old tree.
func TestPageHoldsOnlyTheModuleAndTheLoaderNamesTheLibraries(t *testing.T) {
	page, err := os.ReadFile(filepath.Join("assets", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	tags := scriptTag.FindAllString(string(page), -1)
	if len(tags) != 1 || !strings.Contains(tags[0], `type="module"`) || !strings.Contains(tags[0], `src="assets/main.js"`) {
		t.Fatalf("the page's script tags are %v, want only the module", tags)
	}
	loader, err := os.ReadFile(filepath.Join("assets", "lib", "load-script.js"))
	if err != nil {
		t.Fatal(err)
	}
	srcs := regexp.MustCompile(`src: "([^"]+)"`).FindAllStringSubmatch(string(loader), -1)
	// The last is not a terminal library: Find in terminal loads it on first use.
	want := []string{"assets/vendor/xterm.js", "assets/vendor/addon-fit.js", "assets/e2e.js", "assets/vendor/addon-search.js"}
	if len(srcs) != len(want) {
		t.Fatalf("the loader names %d files, want %d", len(srcs), len(want))
	}
	srv := serve(t)
	for i, m := range srcs {
		if m[1] != want[i] {
			t.Errorf("loader file %d is %s, want %s", i, m[1], want[i])
		}
		body, ct := get(t, srv.URL+"/"+m[1])
		if !strings.Contains(ct, "javascript") || len(body) == 0 {
			t.Errorf("%s is served as %q (%d bytes)", m[1], ct, len(body))
		}
	}
	// The stylesheet for xterm stays in the head.
	if !strings.Contains(string(page), `href="assets/vendor/xterm.css"`) {
		t.Error("the page does not load xterm's stylesheet")
	}
}

// The help dialog checks a page's tags again before it draws them, against a
// list of its own. The two lists have to be the same, or a tag the server lets
// through is silently dropped, or one the server drops is let in.
func TestHelpTagListsAgree(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("assets", "lib", "help.js"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?s)export const TAGS = \[(.*?)\];`).FindSubmatch(src)
	if m == nil {
		t.Fatal("lib/help.js has no TAGS list")
	}
	var got []string
	for _, q := range regexp.MustCompile(`"([a-z0-9]+)"`).FindAllSubmatch(m[1], -1) {
		got = append(got, string(q[1]))
	}
	sort.Strings(got)
	if want := help.AllowedTags(); !slices.Equal(got, want) {
		t.Errorf("lib/help.js TAGS = %v; help.AllowedTags() = %v", got, want)
	}
}
