package server

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/store"
)

// browserClient is a client with a cookie jar of its own, as a browser
// profile is.
func browserClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{Jar: jar}
}

func getStatus(t *testing.T, c *http.Client, url string) int {
	t.Helper()
	resp, err := c.Get(url)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

// getBody is getStatus, with the body too, for a test reading what the page
// actually says.
func getBody(t *testing.T, c *http.Client, url string) (int, string) {
	t.Helper()
	resp, err := c.Get(url)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}
	return resp.StatusCode, string(data)
}

// The address a window's browser is started with stays on its command line,
// where anybody on the machine can read it, for as long as the window is
// open. So it does not carry the token: it carries a link that the page
// exchanges for the token as it loads, and that opens nothing after that.
func TestTheWindowIsOpenedWithoutTheToken(t *testing.T) {
	srv, _ := newTestServer(t)

	link := srv.WindowURL()
	if strings.Contains(link, srv.Token()) {
		t.Fatal("the window's address carries the token")
	}
	window := browserClient(t)
	if code := getStatus(t, window, link); code != http.StatusOK {
		t.Fatalf("the window's first load = %d, want 200", code)
	}
	// The page is then the window's own, by its cookie, for its assets and a
	// reload at the same address alike.
	if code := getStatus(t, window, srv.baseURL()+"/assets/app.js"); code != http.StatusOK {
		t.Errorf("the window's own asset = %d, want 200", code)
	}
	if code := getStatus(t, window, link); code != http.StatusOK {
		t.Errorf("the window reloaded = %d, want 200", code)
	}

	// Somebody else who read the address has nothing: it has been used.
	if code := getStatus(t, browserClient(t), link); code != http.StatusForbidden {
		t.Errorf("the link used a second time = %d, want 403", code)
	}
}

// A link that nobody used goes stale, so one read off a command line after the
// window failed to load it is no good either.
func TestAWindowLinkRunsOut(t *testing.T) {
	srv, _ := newTestServer(t)
	// A life already over, so the link has run out by the time it is used
	// without the test having to wait for it.
	srv.linkLife = -time.Second
	if code := getStatus(t, browserClient(t), srv.WindowURL()); code != http.StatusForbidden {
		t.Errorf("a link that has run out = %d, want 403", code)
	}
}

// Another launch that shows a window onto a running instance asks it for a
// link, rather than start its browser with the token from the instance record.
func TestAnotherLaunchIsGivenAWindowLink(t *testing.T) {
	srv, _ := newTestServer(t)
	link, err := RequestWindowURL(srv.BaseURL(), srv.Token())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(link, srv.Token()) || !strings.HasPrefix(link, srv.BaseURL()+"/?") {
		t.Fatalf("link = %q, want one onto the instance, without the token", strings.ReplaceAll(link, srv.Token(), "<token>"))
	}
	if code := getStatus(t, browserClient(t), link); code != http.StatusOK {
		t.Errorf("the link = %d, want 200", code)
	}
	if _, err := RequestWindowURL(srv.BaseURL(), "not-the-token"); err == nil {
		t.Error("a link was given to a launch without the token")
	}
}

// A link already used says so, in the house style, distinguishing it from
// one that merely ran out (R3.7.2): somebody who did not open a second
// window themselves has reason to wonder whether somebody else on the
// machine got to this link first. Both are written to error.log.
func TestALinkAlreadyUsedSaysSo(t *testing.T) {
	srv, _ := newTestServer(t)
	link := srv.WindowURL()

	if code := getStatus(t, browserClient(t), link); code != http.StatusOK {
		t.Fatalf("first load = %d, want 200", code)
	}
	code, body := getBody(t, browserClient(t), link)
	if code != http.StatusForbidden {
		t.Fatalf("second load = %d, want 403", code)
	}
	if !strings.Contains(body, "already been used") {
		t.Errorf("body = %q, want it to say the link was already used", body)
	}
	if strings.Contains(body, "expired") {
		t.Errorf("body = %q, want it not to call a used link expired", body)
	}
	assertErrorLogHas(t, "already used")
}

// A link that ran out before anybody used it says that instead, calmly --
// this is the ordinary case of a window that took a moment too long to
// load, not somebody else's doing.
func TestALinkThatRanOutSaysSo(t *testing.T) {
	srv, _ := newTestServer(t)
	srv.linkLife = -time.Second
	link := srv.WindowURL()

	code, body := getBody(t, browserClient(t), link)
	if code != http.StatusForbidden {
		t.Fatalf("load = %d, want 403", code)
	}
	if !strings.Contains(body, "expired") {
		t.Errorf("body = %q, want it to say the link expired", body)
	}
	if strings.Contains(body, "already been used") {
		t.Errorf("body = %q, want it not to call an expired link used", body)
	}
	assertErrorLogHas(t, "expired")
}

// A link this instance never gave out at all -- garbage, or one from an
// older run -- gets a page too, worded for neither of the above.
func TestAnUnrecognisedLinkSaysSo(t *testing.T) {
	srv, _ := newTestServer(t)
	code, body := getBody(t, browserClient(t), srv.baseURL()+"/?w=not-a-real-link")
	if code != http.StatusForbidden {
		t.Fatalf("load = %d, want 403", code)
	}
	if !strings.Contains(body, "isn't valid") {
		t.Errorf("body = %q, want it to say the link is not valid", body)
	}
}

// assertErrorLogHas fails the test unless error.log in the state directory
// this test is using contains want somewhere in it.
func assertErrorLogHas(t *testing.T, want string) {
	t.Helper()
	dir, err := store.Dir()
	if err != nil {
		t.Fatalf("store.Dir: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "error.log"))
	if err != nil {
		t.Fatalf("reading error.log: %v", err)
	}
	if !strings.Contains(string(data), want) {
		t.Errorf("error.log = %q, want it to mention %q", data, want)
	}
}
