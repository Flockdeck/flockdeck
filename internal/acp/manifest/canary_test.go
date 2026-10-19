//go:build acp_canary

package manifest

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCanaryInitialize is the weekly canary handshake (see
// .github/workflows/acp-canary.yml). It starts one adapter that the job
// installed into a temporary folder, sends initialize and checks the answer.
// It never logs in, never sends a prompt and never opens a session.
//
// It does nothing unless the job sets FLOCKDECK_CANARY_BIN to the absolute
// path of the adapter. It gives the adapter a home, config, cache and temp
// folders under FLOCKDECK_CANARY_ISO and prints them, so the log shows the
// isolation.
func TestCanaryInitialize(t *testing.T) {
	bin := os.Getenv("FLOCKDECK_CANARY_BIN")
	if bin == "" {
		t.Skip("FLOCKDECK_CANARY_BIN is not set; the canary job sets it")
	}
	iso := os.Getenv("FLOCKDECK_CANARY_ISO")
	if iso == "" || !filepath.IsAbs(iso) {
		t.Fatal("FLOCKDECK_CANARY_ISO must be the absolute path of the temporary folder made for this job")
	}
	id := os.Getenv("FLOCKDECK_CANARY_ADAPTER")
	a, ok := Default().Lookup(id)
	if !ok {
		t.Fatalf("no adapter %q in the manifest", id)
	}
	dirs := map[string]string{
		"HOME":             filepath.Join(iso, "home"),
		"USERPROFILE":      filepath.Join(iso, "home"),
		"XDG_CONFIG_HOME":  filepath.Join(iso, "config"),
		"XDG_DATA_HOME":    filepath.Join(iso, "data"),
		"XDG_CACHE_HOME":   filepath.Join(iso, "cache"),
		"XDG_STATE_HOME":   filepath.Join(iso, "state"),
		"APPDATA":          filepath.Join(iso, "appdata"),
		"LOCALAPPDATA":     filepath.Join(iso, "localappdata"),
		"TMPDIR":           filepath.Join(iso, "tmp"),
		"npm_config_cache": filepath.Join(iso, "npm-cache"),
	}
	env := []string{"PATH=" + os.Getenv("PATH"), "CI=1"}
	for k, v := range dirs {
		if !strings.HasPrefix(v, iso) {
			t.Fatalf("%s=%s is outside the isolation folder %s", k, v, iso)
		}
		if err := os.MkdirAll(v, 0o700); err != nil {
			t.Fatal(err)
		}
		env = append(env, k+"="+v)
	}
	work := filepath.Join(iso, "work")
	if err := os.MkdirAll(work, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Logf("adapter %s: %s %v", id, bin, a.Args)
	t.Logf("working directory: %s", work)
	for _, kv := range env {
		t.Logf("env %s", kv)
	}
	t.Logf("no credentials are passed: the environment above is the whole of it")

	res, err := Handshake(context.Background(), HandshakeConfig{Path: bin, Args: a.Args, Dir: work, Env: env})
	if err != nil {
		t.Fatalf("initialize failed: %v", err)
	}
	t.Logf("initialize ok in %v: protocol %d, agent %q %q, auth methods %v", res.Took, res.ProtocolVersion, res.AgentName, res.AgentVersion, res.AuthMethods)
}
