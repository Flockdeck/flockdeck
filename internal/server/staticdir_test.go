package server

import (
	"bytes"
	"compress/gzip"
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

// The compressed copy a relay window is sent is of the built-in file, not of the
// folder: the copies are kept by name for the life of the process, and a folder's
// files change under it.
func TestARelayWindowAskingForGzipIsSentTheBuiltInFileNotTheFolders(t *testing.T) {
	srv, _ := newTestServer(t)
	builtIn, err := fs.ReadFile(srv.staticFS(), "app.css")
	if err != nil || len(builtIn) == 0 {
		t.Fatalf("the built-in app.css: %v", err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "app.css"), []byte("/* the folder's, not for a phone */"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv.staticRemote = srv.static
	srv.static = os.DirFS(dir)
	assets := http.StripPrefix("/assets/", srv.authFiles(srv.staticFS(), srv.staticRemoteFS()))

	req := httptest.NewRequest(http.MethodGet, "/assets/app.css", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	req = req.WithContext(context.WithValue(req.Context(), remoteKey{}, true))
	rec := httptest.NewRecorder()
	assets.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /assets/app.css = %d", rec.Code)
	}
	if enc := rec.Header().Get("Content-Encoding"); enc != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", enc)
	}
	zr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, builtIn) {
		t.Fatalf("the relay window was sent %d bytes that are not the built-in app.css (%d bytes): %.60q", len(got), len(builtIn), got)
	}
}
