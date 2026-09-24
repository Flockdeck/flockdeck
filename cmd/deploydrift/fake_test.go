package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeCommit is one commit on a fake repo's main, after its latest tag.
type fakeCommit struct {
	SHA   string
	When  time.Time
	Files []string
}

type fakeRepo struct {
	Tags    []string // as returned by matching-refs (order is arbitrary, like the real API)
	Commits []fakeCommit
	TagDate time.Time // committer date of the latest tag's commit
	GoMod   string    // only read for the relay
	Private bool      // 404 to a token that is not the read token
}

type fakeIssue struct {
	Number int
	Title  string
	Body   string
	State  string
	Author string
	IsPR   bool
}

// fakeGitHub serves the API subset deploydrift uses, from fixtures shaped like
// the real responses, and records every write.
type fakeGitHub struct {
	t      *testing.T
	mu     sync.Mutex
	Repos  map[string]*fakeRepo // by bare name, e.g. "flockdeck-relay"
	Issues []*fakeIssue
	// Writes is one line per mutating request, e.g. "POST comment #3".
	Writes []string
	// Deny, when a repo name is set, makes reads of it return this status.
	Deny map[string]int
	srv  *httptest.Server
}

func newFake(t *testing.T) *fakeGitHub {
	f := &fakeGitHub{t: t, Repos: map[string]*fakeRepo{}, Deny: map[string]int{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeGitHub) writes() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.Writes...)
}

func (f *fakeGitHub) resetWrites() {
	f.mu.Lock()
	f.Writes = nil
	f.mu.Unlock()
}

func jsonOut(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func commitJSON(c fakeCommit, withFiles bool) map[string]any {
	m := map[string]any{
		"sha":    c.SHA,
		"commit": map[string]any{"committer": map[string]any{"date": c.When.UTC().Format(time.RFC3339)}},
	}
	if withFiles {
		var files []map[string]any
		for _, p := range c.Files {
			files = append(files, map[string]any{"filename": p})
		}
		m["files"] = files
	}
	return m
}

func (f *fakeGitHub) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	path := r.URL.Path
	q := r.URL.Query()
	parts := strings.Split(strings.Trim(path, "/"), "/") // repos/Flockdeck/<name>/...
	if len(parts) < 4 || parts[0] != "repos" {
		http.NotFound(w, r)
		return
	}
	name := parts[2]
	rest := strings.Join(parts[3:], "/")

	// The tracking issue lives in the repo the run is for ("flockdeck").
	if rest == "issues" || strings.HasPrefix(rest, "issues/") {
		f.serveIssues(w, r, rest)
		return
	}

	if st := f.Deny[name]; st != 0 {
		http.Error(w, "denied", st)
		return
	}
	repo := f.Repos[name]
	if repo == nil {
		http.NotFound(w, r)
		return
	}
	switch {
	case strings.HasPrefix(rest, "git/matching-refs/tags/"):
		var refs []map[string]string
		for _, tg := range repo.Tags {
			refs = append(refs, map[string]string{"ref": "refs/tags/" + tg})
		}
		jsonOut(w, refs)
	case strings.HasPrefix(rest, "compare/"):
		page, _ := strconv.Atoi(q.Get("page"))
		if page < 1 {
			page = 1
		}
		lo, hi := (page-1)*100, page*100
		if lo > len(repo.Commits) {
			lo = len(repo.Commits)
		}
		if hi > len(repo.Commits) {
			hi = len(repo.Commits)
		}
		var cs []map[string]any
		for _, c := range repo.Commits[lo:hi] {
			cs = append(cs, commitJSON(c, false))
		}
		jsonOut(w, map[string]any{"total_commits": len(repo.Commits), "commits": cs})
	case strings.HasPrefix(rest, "commits/"):
		ref := strings.TrimPrefix(rest, "commits/")
		if strings.HasPrefix(ref, "v") { // a tag
			jsonOut(w, commitJSON(fakeCommit{SHA: "tagsha", When: repo.TagDate}, false))
			return
		}
		for _, c := range repo.Commits {
			if c.SHA == ref {
				jsonOut(w, commitJSON(c, true))
				return
			}
		}
		http.NotFound(w, r)
	case rest == "contents/go.mod":
		if !strings.Contains(r.Header.Get("Accept"), "raw") {
			f.t.Errorf("go.mod fetched without the raw media type")
		}
		_, _ = w.Write([]byte(repo.GoMod))
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeGitHub) serveIssues(w http.ResponseWriter, r *http.Request, rest string) {
	var body map[string]string
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	toJSON := func(i *fakeIssue) map[string]any {
		m := map[string]any{"number": i.Number, "title": i.Title, "body": i.Body, "state": i.State,
			"user": map[string]string{"login": i.Author}}
		if i.IsPR {
			m["pull_request"] = map[string]any{}
		}
		return m
	}
	find := func(n string) *fakeIssue {
		num, _ := strconv.Atoi(n)
		for _, i := range f.Issues {
			if i.Number == num {
				return i
			}
		}
		return nil
	}
	switch {
	case rest == "issues" && r.Method == http.MethodGet:
		state := r.URL.Query().Get("state")
		var out []map[string]any
		for i := len(f.Issues) - 1; i >= 0; i-- { // newest first
			if is := f.Issues[i]; is.State == state {
				out = append(out, toJSON(is))
			}
		}
		jsonOut(w, out)
	case rest == "issues" && r.Method == http.MethodPost:
		is := &fakeIssue{Number: len(f.Issues) + 1, Title: body["title"], Body: body["body"], State: "open", Author: botLogin}
		f.Issues = append(f.Issues, is)
		f.Writes = append(f.Writes, fmt.Sprintf("POST issue #%d", is.Number))
		jsonOut(w, toJSON(is))
	case strings.HasSuffix(rest, "/comments") && r.Method == http.MethodPost:
		n := strings.Split(rest, "/")[1]
		f.Writes = append(f.Writes, "POST comment #"+n)
		jsonOut(w, map[string]any{})
	case strings.HasPrefix(rest, "issues/") && r.Method == http.MethodPatch:
		n := strings.Split(rest, "/")[1]
		is := find(n)
		if is == nil {
			http.NotFound(w, r)
			return
		}
		what := "PATCH body"
		if b, ok := body["body"]; ok {
			is.Body = b
		}
		if s, ok := body["state"]; ok {
			is.State = s
			what = "PATCH state=" + s
		}
		f.Writes = append(f.Writes, what+" #"+n)
		jsonOut(w, toJSON(is))
	default:
		http.NotFound(w, r)
	}
}

// healthy returns a fake where every repo sits exactly on its latest tag.
func healthy(t *testing.T) *fakeGitHub {
	f := newFake(t)
	tagged := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	for _, s := range repos {
		f.Repos[s.Name] = &fakeRepo{Tags: []string{"v0.1.0", "v0.9.0", "v0.10.0", "v0.10.0-rc.1", "vnext"}, TagDate: tagged}
	}
	f.Repos[relayRepo].GoMod = "module github.com/jmwri/flockdeck-relay\n\nrequire (\n\tgithub.com/Flockdeck/flockdeck-remote v0.10.0\n)\n"
	return f
}

var testNow = time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)

func (f *fakeGitHub) client() *ghClient { return newClient(f.srv.URL, "read-token") }
