package server

import (
	"context"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

// A folder given as the front end (FLOCKDECK_UI_DIR) is what the desk's own
// windows are served, page and assets, read afresh each request; a window
// reached through the relay is served the built-in files, never the folder,
// and the compressed copies the relay keeps are made from those.
func TestAFolderFrontEndIsForTheDeskAndNotTheRelay(t *testing.T) {
	srv, _ := newTestServer(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<p>from the folder</p>"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app.css"), []byte("/* folder */"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv.static = os.DirFS(dir)
	srv.staticRemote = fstest.MapFS{
		"index.html": {Data: []byte("<p>built in</p>")},
		"app.css":    {Data: []byte("/* built in */")},
	}
	assets := http.StripPrefix("/assets/", srv.authFiles(srv.staticFS(), srv.staticRemoteFS()))

	get := func(remote bool, h func(http.ResponseWriter, *http.Request), path string) string {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path+"?t="+srv.Token(), nil)
		req.Header.Set("Accept-Encoding", "identity")
		if remote {
			req = req.WithContext(context.WithValue(req.Context(), remoteKey{}, true))
		}
		rec := httptest.NewRecorder()
		h(rec, req)
		b, _ := io.ReadAll(rec.Result().Body)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s (remote %v) = %d", path, remote, rec.Code)
		}
		return string(b)
	}
	for _, c := range []struct {
		remote bool
		want   string
	}{{false, "folder"}, {true, "built in"}} {
		if got := get(c.remote, srv.handleIndex, "/"); !strings.Contains(got, c.want) {
			t.Errorf("page (remote %v) = %q, want %q", c.remote, got, c.want)
		}
		if got := get(c.remote, assets.ServeHTTP, "/assets/app.css"); !strings.Contains(got, c.want) {
			t.Errorf("app.css (remote %v) = %q, want %q", c.remote, got, c.want)
		}
	}

	// The folder is read on each request, so an edit shows on a reload.
	if err := os.WriteFile(filepath.Join(dir, "app.css"), []byte("/* folder, edited */"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := get(false, assets.ServeHTTP, "/assets/app.css"); !strings.Contains(got, "edited") {
		t.Errorf("an edit to the folder did not show: %q", got)
	}
}

// With no second front end given, a relay window is served the same files.
func TestWithoutAnotherFrontEndTheRelayIsServedTheSame(t *testing.T) {
	srv, _ := newTestServer(t)
	if srv.staticRemote != nil {
		t.Fatal("the test config gives a second front end")
	}
	srv.static = fstest.MapFS{"index.html": {Data: []byte("x")}}
	if _, err := fs.Stat(srv.staticRemoteFS(), "index.html"); err != nil {
		t.Fatalf("a relay window is not served the desk's files when no other was given: %v", err)
	}
}
