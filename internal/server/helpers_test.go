package server

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/helpers"
)

// helperServer is a test server with a supervisor and installer over an empty
// apps folder, so nothing is installed and nothing can be started.
func helperServer(t *testing.T) *Server {
	t.Helper()
	srv, _ := newTestServer(t)
	st := &helpers.Store{Root: t.TempDir()}
	srv.SetHelpers(helpers.NewSupervisor(helpers.Config{Store: st}), helpers.NewInstaller(helpers.Options{Store: st}))
	return srv
}

func postHelper(t *testing.T, url string, cookie *http.Cookie) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestHelperEndpointsNeedTheInstanceToken(t *testing.T) {
	srv := helperServer(t)
	for _, action := range []string{"start", "stop", "open"} {
		base := srv.BaseURL() + "/helpers/" + action + "?id=lens"
		for name, url := range map[string]string{
			"no token":            base,
			"empty token":         base + "&t=",
			"wrong token":         base + "&t=nope",
			"a prefix":            base + "&t=" + srv.Token()[:10],
			"the token with more": base + "&t=" + srv.Token() + "x",
		} {
			if code, body := postHelper(t, url, nil); code != http.StatusForbidden {
				t.Errorf("%s %s: status %d (%s), want 403", action, name, code, strings.TrimSpace(body))
			}
		}
		// The cookie alone, which is what a page on this origin has, is not
		// enough: these endpoints act.
		cookie := &http.Cookie{Name: srv.cookieName(), Value: srv.Token()}
		if code, _ := postHelper(t, base, cookie); code != http.StatusForbidden {
			t.Errorf("%s with only the cookie: status %d, want 403", action, code)
		}
	}
}

func TestHelperEndpointsRefuseGET(t *testing.T) {
	srv := helperServer(t)
	for _, action := range []string{"start", "stop", "open"} {
		resp, err := http.Get(srv.BaseURL() + "/helpers/" + action + "?id=lens&t=" + srv.Token())
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed || resp.Header.Get("Allow") != http.MethodPost {
			t.Errorf("GET %s = %d, Allow %q", action, resp.StatusCode, resp.Header.Get("Allow"))
		}
	}
}

func TestHelperEndpointsWithTheToken(t *testing.T) {
	srv := helperServer(t)
	url := func(action, id string) string {
		return srv.BaseURL() + "/helpers/" + action + "?t=" + srv.Token() + "&id=" + id
	}
	if code, body := postHelper(t, url("start", "lens"), nil); code != http.StatusConflict || !strings.Contains(body, "not installed") {
		t.Errorf("start of a helper that is not installed: %d %q", code, body)
	}
	if code, body := postHelper(t, url("start", "nope"), nil); code != http.StatusConflict || !strings.Contains(body, "not a helper") {
		t.Errorf("start of an unknown helper: %d %q", code, body)
	}
	if code, body := postHelper(t, url("open", "lens"), nil); code != http.StatusConflict || !strings.Contains(body, "not running") {
		t.Errorf("open of a stopped helper: %d %q", code, body)
	}
	if code, body := postHelper(t, url("stop", "lens"), nil); code != http.StatusOK || !strings.Contains(body, `"state":"stopped"`) {
		t.Errorf("stop of a stopped helper: %d %q", code, body)
	}
	if code, _ := postHelper(t, srv.BaseURL()+"/helpers/start?t="+srv.Token(), nil); code != http.StatusBadRequest {
		t.Errorf("start with no id: %d, want 400", code)
	}
}

func TestHelperEndpointsWithoutASupervisor(t *testing.T) {
	srv, _ := newTestServer(t)
	if code, _ := postHelper(t, srv.BaseURL()+"/helpers/start?id=lens&t="+srv.Token(), nil); code != http.StatusServiceUnavailable {
		t.Errorf("status %d, want 503", code)
	}
	// The token is still checked first.
	if code, _ := postHelper(t, srv.BaseURL()+"/helpers/start?id=lens&t=bad", nil); code != http.StatusForbidden {
		t.Errorf("status %d, want 403", code)
	}
}

func TestHelperRequestReportsTheInstancesAnswer(t *testing.T) {
	srv := helperServer(t)
	_, err := HelperRequest(srv.BaseURL(), srv.Token(), "start", "lens")
	if err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("err = %v", err)
	}
	if st, err := HelperRequest(srv.BaseURL(), srv.Token(), "stop", "lens"); err != nil || st.State != helpers.StateStopped {
		t.Fatalf("stop: %+v, %v", st, err)
	}
	if _, err := HelperRequest(srv.BaseURL(), "wrong", "stop", "lens"); err == nil {
		t.Fatal("a wrong token was accepted")
	}
	if _, err := HelperRequest(srv.BaseURL(), srv.Token(), "format", "lens"); err == nil {
		t.Fatal("an unknown action was sent")
	}
}
