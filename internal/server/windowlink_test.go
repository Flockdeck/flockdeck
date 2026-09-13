package server

import (
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
	"time"
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
