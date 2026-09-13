package gitx

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestARemoteWantingALoginSaysHowToGiveIt: there is no terminal to type a
// password into, and git said only that it "could not read Username ...
// terminal prompts disabled".
func TestARemoteWantingALoginSaysHowToGiveIt(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", `Basic realm="git"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	repo := newRepo(t)
	// No credential helper, so that none on the machine running the test
	// answers for the server -- or opens a window asking for a login.
	gitRun(t, repo, "config", "credential.helper", "")
	gitRun(t, repo, "remote", "add", "origin", srv.URL+"/x.git")

	for name, op := range map[string]func(string) (string, error){"push": Push, "fetch": Fetch} {
		_, err := op(repo)
		if err == nil || !strings.Contains(err.Error(), "Sign in once from a terminal") ||
			!strings.Contains(err.Error(), srv.URL) || strings.Contains(err.Error(), "terminal prompts disabled") {
			t.Errorf("%s: err = %v, want it to say how to give the login", name, err)
		}
	}
}
