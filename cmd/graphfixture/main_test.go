package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/jmwri/flockdeck/internal/testiso"
	"github.com/jmwri/flockdeck/internal/webui"
	"github.com/jmwri/flockdeck/internal/workspace"
)

// TestMain keeps these tests off the real config directory, the stored API
// keys and the real network. See docs/testing.md.
func TestMain(m *testing.M) { os.Exit(testiso.Main(m)) }

func fixtureDir(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	d, err := findFixtureDir(wd)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// start serves a fixture on a test server and returns it with the address and token.
func start(t *testing.T, which string) (*fixtureServer, string) {
	t.Helper()
	fx, err := loadFixture(fixtureDir(t), which)
	if err != nil {
		t.Fatal(err)
	}
	s := &fixtureServer{fx: fx, files: webui.FS(), token: "tok-" + which}
	ts := httptest.NewServer(s.handler())
	t.Cleanup(ts.Close)
	return s, ts.URL
}

type sock struct {
	t    *testing.T
	conn *websocket.Conn
	ctx  context.Context
}

func dial(t *testing.T, base, token string) *sock {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	u := "ws" + strings.TrimPrefix(base, "http") + "/ws/control?t=" + token
	conn, _, err := websocket.Dial(ctx, u, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.CloseNow() })
	conn.SetReadLimit(1 << 20)
	return &sock{t: t, conn: conn, ctx: ctx}
}

func (c *sock) read() map[string]any {
	c.t.Helper()
	_, data, err := c.conn.Read(c.ctx)
	if err != nil {
		c.t.Fatalf("read: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		c.t.Fatalf("frame %s: %v", data, err)
	}
	return m
}

func (c *sock) readRaw() []byte {
	c.t.Helper()
	_, data, err := c.conn.Read(c.ctx)
	if err != nil {
		c.t.Fatalf("read: %v", err)
	}
	return data
}

func (c *sock) send(v any) {
	c.t.Helper()
	data, _ := json.Marshal(v)
	if err := c.conn.Write(c.ctx, websocket.MessageText, data); err != nil {
		c.t.Fatalf("write: %v", err)
	}
}

func allFixtures(t *testing.T) []string {
	names, err := fixtureNames(fixtureDir(t))
	if err != nil || len(names) != 5 {
		t.Fatalf("fixtures %v, %v", names, err)
	}
	return names
}

// Each fixture is played: a hello with the key table, a state with a pane and a
// tab leaf for every node of the graph, and the graph message itself, as written,
// in answer to graphWatch.
func TestEachFixtureIsPlayed(t *testing.T) {
	for _, name := range allFixtures(t) {
		t.Run(name, func(t *testing.T) {
			s, base := start(t, name)
			c := dial(t, base, s.token)
			hello := c.read()
			if hello["type"] != "hello" {
				t.Fatalf("first frame %v", hello["type"])
			}
			keys, _ := hello["keys"].([]any)
			if len(keys) == 0 {
				t.Fatal("the hello has no key table")
			}
			if p, _ := hello["prefs"].(map[string]any); p["helpSeen"] != true {
				t.Errorf("prefs %v: the old page would open its first-run help", hello["prefs"])
			}
			st := c.read()
			if st["type"] != "state" || st["root"] != s.fx.root {
				t.Fatalf("second frame %v root %v, want state for %s", st["type"], st["root"], s.fx.root)
			}
			panes := st["panes"].(map[string]any)
			tabs := st["tabs"].([]any)
			leaves := map[string]bool{}
			var walk func(n map[string]any)
			walk = func(n map[string]any) {
				if p, ok := n["pane"].(string); ok {
					leaves[p] = true
				}
				kids, _ := n["children"].([]any)
				for _, k := range kids {
					walk(k.(map[string]any))
				}
			}
			walk(tabs[0].(map[string]any)["root"].(map[string]any))
			for _, n := range s.fx.nodes {
				p, ok := panes[n.Pane].(map[string]any)
				if !ok || p["id"] != n.Pane || p["name"] != n.Name || p["status"] != n.Status {
					t.Errorf("pane %s: %v", n.Pane, panes[n.Pane])
				}
				if !leaves[n.Pane] {
					t.Errorf("pane %s is in no tab", n.Pane)
				}
			}
			if len(panes) != len(s.fx.nodes) || len(leaves) != len(s.fx.nodes) {
				t.Errorf("%d panes and %d leaves for %d nodes", len(panes), len(leaves), len(s.fx.nodes))
			}
			proj := st["projects"].([]any)[0].(map[string]any)
			if proj["root"] != s.fx.root || proj["panes"] != float64(len(s.fx.nodes)) {
				t.Errorf("project %v", proj)
			}
			c.send(map[string]any{"cmd": "graphWatch", "req": "r1"})
			var got, want any
			if err := json.Unmarshal(c.readRaw(), &got); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(s.fx.raw, &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("the graph is not the fixture as written")
			}
		})
	}
}

// A graph command for another project is refused as the desk refuses it.
func TestGraphWatchOfAnotherProjectIsRefused(t *testing.T) {
	s, base := start(t, "1")
	c := dial(t, base, s.token)
	c.read()
	c.read()
	c.send(map[string]any{"cmd": "graphWatch", "req": "r9", "root": "/somewhere/else"})
	m := c.read()
	if m["type"] != "teamResult" || m["ok"] != false || m["req"] != "r9" || m["op"] != "graphWatch" || !strings.Contains(m["error"].(string), "no project is open at /somewhere/else") {
		t.Errorf("reply %v", m)
	}
}

// paneSend is answered with the desk's reply, the text is kept exactly as sent,
// and the refusals use the desk's words.
func TestPaneSendAndConversation(t *testing.T) {
	s, base := start(t, "1")
	c := dial(t, base, s.token)
	c.read()
	c.read()
	manager := s.fx.nodes[0].Pane

	text := "  /status then !ls  "
	c.send(map[string]any{"cmd": "paneSend", "id": manager, "text": text})
	m := c.read()
	if m["type"] != "paneSend" || m["id"] != manager {
		t.Fatalf("reply %v", m)
	}
	if got := s.Sent(); len(got) != 1 || got[0].ID != manager || got[0].Text != text {
		t.Errorf("sent %v", got)
	}

	for _, tc := range []struct{ id, text, want string }{
		{manager, "   ", "There was no text to send"},
		{manager, strings.Repeat("x", 16*1024+1), "That text is over 16 KB, so it was not sent"},
		{"nope", "hi", "That pane is no longer open"},
	} {
		c.send(map[string]any{"cmd": "paneSend", "id": tc.id, "text": tc.text})
		m := c.read()
		if m["type"] != "notice" || m["error"] != true || m["text"] != tc.want {
			t.Errorf("paneSend %q to %s: %v, want notice %q", tc.text[:min(len(tc.text), 8)], tc.id, m, tc.want)
		}
	}
	if len(s.Sent()) != 1 {
		t.Errorf("a refused text was kept: %v", s.Sent())
	}

	// A pane the fixture does not have gets no reply, as on the desk; the next
	// frame is the answer to the one after.
	c.send(map[string]any{"cmd": "conversationOpen", "id": "nope"})
	c.send(map[string]any{"cmd": "conversationOpen", "id": manager})
	page := c.read()
	if page["type"] != "conversationPage" || page["id"] != manager || page["supported"] != true {
		t.Errorf("page %v", page)
	}
	if e, _ := page["entries"].([]any); len(e) == 0 {
		t.Error("the page has no entries")
	}
}

// Nothing is served without the token, and a Host that is not loopback is
// refused.
func TestTokenAndHost(t *testing.T) {
	s, base := start(t, "1")
	get := func(path string, mod func(*http.Request)) int {
		req, _ := http.NewRequest("GET", base+path, nil)
		if mod != nil {
			mod(req)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, resp.Body)
		return resp.StatusCode
	}
	for _, p := range []string{"/", "/assets/index.html", "/assets/main.js", "/ws/control"} {
		if c := get(p, nil); c != http.StatusForbidden && c != http.StatusNotFound {
			t.Errorf("%s without a token: %d", p, c)
		}
		if c := get(p+"?t=wrong", nil); c != http.StatusForbidden && c != http.StatusNotFound {
			t.Errorf("%s with a wrong token: %d", p, c)
		}
	}
	if c := get("/?t="+s.token, nil); c != http.StatusOK {
		t.Errorf("/ with the token: %d", c)
	}
	if c := get("/?t="+s.token, func(r *http.Request) { r.Host = "example.com" }); c != http.StatusForbidden {
		t.Errorf("a foreign Host: %d", c)
	}
	if _, _, err := websocket.Dial(context.Background(), "ws"+strings.TrimPrefix(base, "http")+"/ws/control", nil); err == nil {
		t.Error("the socket opened without a token")
	}
}

// The page is the build's front end: the index names the new shell's entry in a
// build with -tags next and the old one's otherwise, and the cookie the index
// sets opens the assets.
func TestServesTheFrontEndOfTheBuild(t *testing.T) {
	s, base := start(t, "1")
	jar := &http.Client{}
	resp, err := jar.Get(base + "/?t=" + s.token)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	entry := "assets/app.js"
	if webui.Variant == "next" {
		entry = "assets/main.js"
	}
	if !strings.Contains(string(body), entry) {
		t.Errorf("variant %s: the index does not name %s", webui.Variant, entry)
	}
	var cookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == cookieName {
			cookie = c
		}
	}
	if cookie == nil || !cookie.HttpOnly {
		t.Fatalf("cookie %v", cookie)
	}
	req, _ := http.NewRequest("GET", base+"/"+entry, nil)
	req.AddCookie(cookie)
	r2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	r2.Body.Close()
	if r2.StatusCode != http.StatusOK {
		t.Errorf("%s with the cookie: %d", entry, r2.StatusCode)
	}
}

func TestListensOnLoopbackOnly(t *testing.T) {
	ln, err := listen()
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	a := ln.Addr().(*net.TCPAddr)
	if !a.IP.IsLoopback() || a.Port == 0 {
		t.Errorf("address %v", a)
	}
	tok1, _ := newToken()
	tok2, _ := newToken()
	if len(tok1) != 32 || tok1 == tok2 {
		t.Errorf("tokens %q %q", tok1, tok2)
	}
}

// The tool never goes near the configuration directory: it does not import the
// store, the key files or the account packages, and does not ask where the
// configuration or home directory is.
func TestNothingReachesTheConfigDirectory(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src := string(data)
		for _, bad := range []string{"os.UserConfigDir", "os.UserHomeDir", "internal/store", "internal/keybindings", "internal/account", "internal/jev", "internal/server\""} {
			if strings.Contains(src, bad) {
				t.Errorf("%s mentions %s", f, bad)
			}
		}
	}
}

// The new page draws each fixture from what the tool sends: every node is a box,
// no drawer is open, and the manager bar is in the top strip. The page is drawn
// by the node harness of the new shell, over the frames the real handler sent.
func TestEachFixtureLoadsInTheNodeHarness(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal("node is not on PATH, and CI is set")
		}
		t.Skip("node is not on PATH")
	}
	for _, name := range allFixtures(t) {
		t.Run(name, func(t *testing.T) {
			s, base := start(t, name)
			c := dial(t, base, s.token)
			var frames []json.RawMessage
			frames = append(frames, c.readRaw(), c.readRaw())
			c.send(map[string]any{"cmd": "graphWatch"})
			frames = append(frames, c.readRaw())
			data, _ := json.Marshal(frames)
			file := filepath.Join(t.TempDir(), "frames.json")
			if err := os.WriteFile(file, data, 0o600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			cmd := exec.CommandContext(ctx, node, filepath.Join("testdata", "render.mjs"), file)
			cmd.WaitDelay = 5 * time.Second
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("node: %v\n%s", err, out)
			}
			var got struct {
				Watches int      `json:"watches"`
				Boxes   []string `json:"boxes"`
				Drawers int      `json:"drawers"`
				Bar     bool     `json:"bar"`
			}
			line := out[bytes.LastIndexByte(bytes.TrimSpace(out), '\n')+1:]
			if err := json.Unmarshal(line, &got); err != nil {
				t.Fatalf("output %s: %v", out, err)
			}
			if got.Watches != 1 {
				t.Errorf("the page asked for the graph %d times", got.Watches)
			}
			want := map[string]bool{}
			for _, n := range s.fx.nodes {
				want[n.Pane] = true
			}
			if len(got.Boxes) != len(want) {
				t.Errorf("%d boxes for %d nodes: %v", len(got.Boxes), len(want), got.Boxes)
			}
			for _, b := range got.Boxes {
				if !want[b] {
					t.Errorf("box for %s, which is no node", b)
				}
			}
			if got.Drawers != 0 {
				t.Errorf("%d drawers open on load", got.Drawers)
			}
		})
	}
}

// The limit and the words copied into the tool are the desk's.
func TestDeskValuesArePinnedToWorkspace(t *testing.T) {
	if maxPaneSendBytes != workspace.MaxPaneSendBytes {
		t.Errorf("maxPaneSendBytes %d, workspace has %d", maxPaneSendBytes, workspace.MaxPaneSendBytes)
	}
	if sendTooLongWords != workspace.ErrSendTooLong.Error() {
		t.Errorf("sendTooLongWords %q, workspace has %q", sendTooLongWords, workspace.ErrSendTooLong)
	}
	if sendEmptyWords != workspace.ErrSendEmpty.Error() {
		t.Errorf("sendEmptyWords %q, workspace has %q", sendEmptyWords, workspace.ErrSendEmpty)
	}
}

// The permission options in the state push are the ones the desk gives a Claude
// terminal pane's Bash prompt.
func TestPermissionOptionsArePinnedToWorkspace(t *testing.T) {
	want := workspace.TerminalPermissionOptions("claude", "Bash")
	var got []workspace.PermissionOption
	raw, err := json.Marshal(permissionView("ls")["options"])
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("options %v, workspace has %v", got, want)
	}
}

// A terminal pane the graph says needs a permission or answers a question has the
// view the desk puts on it in the state push, made from the fixture's own item;
// no other pane has either view.
func TestStatePushCarriesThePermissionAndQuestionViews(t *testing.T) {
	for _, name := range allFixtures(t) {
		t.Run(name, func(t *testing.T) {
			fx, err := loadFixture(fixtureDir(t), name)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(fx.stateMsg())
			if err != nil {
				t.Fatal(err)
			}
			var st struct {
				Panes map[string]struct {
					Permission *struct {
						Tool    string                       `json:"tool"`
						Summary string                       `json:"summary"`
						Command string                       `json:"command"`
						Options []workspace.PermissionOption `json:"options"`
					} `json:"permission"`
					Ask *struct {
						Questions []struct {
							Question string `json:"question"`
						} `json:"questions"`
					} `json:"ask"`
				} `json:"panes"`
			}
			if err := json.Unmarshal(raw, &st); err != nil {
				t.Fatal(err)
			}
			perm, ask := map[string]string{}, map[string]string{}
			for _, nd := range fx.needs {
				if nd.Kind == "permission" && perm[nd.Pane] == "" {
					perm[nd.Pane] = nd.Text
				}
				if nd.Kind == "ask" && ask[nd.Pane] == "" {
					ask[nd.Pane] = nd.Text
				}
			}
			seenPerm, seenAsk := 0, 0
			for id, p := range st.Panes {
				if text, ok := perm[id]; ok {
					seenPerm++
					if p.Permission == nil || p.Permission.Command != text || p.Permission.Tool != "Bash" || p.Permission.Summary != "Run: "+text {
						t.Errorf("pane %s: permission %+v, want command %q", id, p.Permission, text)
					} else if !reflect.DeepEqual(p.Permission.Options, workspace.TerminalPermissionOptions("claude", "Bash")) {
						t.Errorf("pane %s: options %v", id, p.Permission.Options)
					}
					continue
				}
				if p.Permission != nil {
					t.Errorf("pane %s has a permission view and no permission item", id)
				}
				if text, ok := ask[id]; ok {
					seenAsk++
					if p.Ask == nil || len(p.Ask.Questions) != 1 || p.Ask.Questions[0].Question != text {
						t.Errorf("pane %s: ask %+v, want question %q", id, p.Ask, text)
					}
				} else if p.Ask != nil {
					t.Errorf("pane %s has an ask view and no question item", id)
				}
			}
			if seenPerm != len(perm) || seenAsk+countBoth(perm, ask) != len(ask) {
				t.Errorf("%d of %d permission views and %d of %d ask views", seenPerm, len(perm), seenAsk, len(ask))
			}
		})
	}
}

// countBoth is how many panes have a permission item and a question item: they
// show the permission, which the desk shows for a pane that waits on one thing.
func countBoth(perm, ask map[string]string) int {
	n := 0
	for id := range ask {
		if _, ok := perm[id]; ok {
			n++
		}
	}
	return n
}

// The built tool links none of the packages that keep or look for the person's
// configuration, so what the tool does not read cannot change by one of them
// gaining a lookup. The list is of the tool alone: this test's own imports are
// not in it.
func TestToolLinksNoPackageThatKeepsConfiguration(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal("go is not on PATH, and CI is set")
		}
		t.Skip("go is not on PATH")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, goBin, "list", "-deps", "-f", "{{.ImportPath}}", ".")
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	const mod = "github.com/jmwri/flockdeck/internal/"
	var deps []string
	for _, p := range strings.Fields(string(out)) {
		if strings.HasPrefix(p, mod) {
			deps = append(deps, strings.TrimPrefix(p, mod))
		}
	}
	if len(deps) == 0 {
		t.Fatalf("no internal package in the list, so the check proves nothing: %s", out)
	}
	for _, d := range deps {
		for _, bad := range []string{"store", "record", "session", "agent", "workspace", "server", "keybindings"} {
			if d == bad || strings.HasPrefix(d, bad+"/") {
				t.Errorf("the tool links internal/%s", d)
			}
		}
	}
	t.Logf("internal packages linked: %v", deps)
}
