//go:build acp_canary

package claudeacp

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/acp"
	testiso "github.com/jmwri/flockdeck/internal/testiso/iso"
)

// TestCanaryClaudeSession is the check of the adapter against a real claude
// (see .github/workflows/acp-canary.yml, and docs/acp-claude-adapter.md for the
// command to run it by hand). It starts `flockdeck acp-adapter claude` from a
// built flockdeck binary, opens a session in a new folder and sends a one-word
// prompt.
//
// It does nothing unless FLOCKDECK_LIVE_AGENT_TESTS is set (without it the
// test harness puts a stub first on PATH where claude is, see internal/testiso),
// FLOCKDECK_CANARY_BIN is the absolute path of the flockdeck binary and
// FLOCKDECK_CANARY_ISO is the absolute path of a new folder made for this run. The adapter gets a home, config, cache and temp
// folders under it, so it sees no login and no key: claude is then expected to
// answer session/new and to refuse the prompt as "Authentication required".
// That is what the scheduled job checks, since it has no login to use.
//
// With FLOCKDECK_CANARY_LOGIN=1 the adapter is given the real home of the
// person running it (the login claude keeps there, not an API key: the test
// harness removes every *_API_KEY variable), and the prompt is expected to be
// answered. The working directory is still the new folder, and the prompt asks
// for one word and nothing else, so no tool runs. The test prints the adapter's
// stderr and the names of the environment it was given, never a value that could
// be a secret.
func TestCanaryClaudeSession(t *testing.T) {
	bin := os.Getenv("FLOCKDECK_CANARY_BIN")
	if bin == "" || os.Getenv("FLOCKDECK_LIVE_AGENT_TESTS") == "" {
		t.Skip("FLOCKDECK_CANARY_BIN and FLOCKDECK_LIVE_AGENT_TESTS are not set; the canary job sets them")
	}
	if p, err := exec.LookPath("claude"); err != nil || testiso.IsStub(p) {
		t.Fatalf("claude on PATH is %q (%v); this test needs the real one", p, err)
	}
	iso := os.Getenv("FLOCKDECK_CANARY_ISO")
	if !filepath.IsAbs(bin) || iso == "" || !filepath.IsAbs(iso) {
		t.Fatal("FLOCKDECK_CANARY_BIN and FLOCKDECK_CANARY_ISO must be absolute paths")
	}
	login := os.Getenv("FLOCKDECK_CANARY_LOGIN") == "1"

	env := []string{"CI=1"}
	for _, k := range []string{"PATH", "SYSTEMROOT", "SystemRoot", "SYSTEMDRIVE", "COMSPEC", "PATHEXT"} {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	if login {
		// The test harness points the home at a test folder; the person's own
		// is the one that holds the login.
		home := testiso.RealHomeDir()
		if home == "" {
			t.Fatal("the real home is not known")
		}
		env = append(env, "HOME="+home, "USERPROFILE="+home)
	} else {
		for k, v := range map[string]string{
			"HOME": "home", "USERPROFILE": "home", "XDG_CONFIG_HOME": "config", "XDG_DATA_HOME": "data",
			"XDG_CACHE_HOME": "cache", "XDG_STATE_HOME": "state", "APPDATA": "appdata", "LOCALAPPDATA": "localappdata", "TMPDIR": "tmp", "TEMP": "tmp", "TMP": "tmp",
		} {
			dir := filepath.Join(iso, v)
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			env = append(env, k+"="+dir)
		}
	}
	work := filepath.Join(iso, "work")
	if err := os.MkdirAll(work, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Logf("login mode: %v; working directory: %s", login, work)
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		if strings.Contains(strings.ToUpper(name), "KEY") {
			kv = name + "=<set>"
		}
		t.Logf("env %s", kv)
	}

	rec := &recorder{}
	c, err := acp.Start(acp.Config{
		Path: bin, Args: []string{"acp-adapter", "claude"}, Dir: work, Env: env, Handler: rec,
		SetupTimeout: 2 * time.Minute,
		Stderr:       func(line string) { t.Logf("adapter stderr: %s", line) },
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	// The prompt is given less time than the whole run: a claude that does not
	// answer must fail this test and not use up the job's time.
	turn, stopTurn := context.WithTimeout(ctx, 90*time.Second)
	defer stopTurn()

	init, err := c.Initialize(ctx)
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
	t.Logf("initialize ok: agent %+v, resume %v", init.AgentInfo, c.Capabilities().Resume)
	created, err := c.NewSessionInfo(ctx, work, nil)
	if err != nil {
		t.Fatalf("session/new: %v (is claude on PATH? the adapter says only that it is unavailable)", err)
	}
	t.Logf("session/new ok: session %s", created.SessionID)

	stop, err := c.Prompt(turn, created.SessionID, "Reply with the single word ok and nothing else.")
	if login {
		if err != nil {
			t.Fatalf("prompt: %v", err)
		}
		if stop != acp.StopEndTurn {
			t.Errorf("stop reason = %q, want %q", stop, acp.StopEndTurn)
		}
		got := strings.TrimSpace(rec.text())
		if got == "" {
			t.Error("the turn produced no text")
		}
		t.Logf("stop reason %q, reply %q", stop, got)
		return
	}
	if !acp.IsAuthRequired(err) {
		var rpc *acp.RPCError
		t.Fatalf("prompt without a login: stop %q, error %v (rpc %v), want authentication required", stop, err, errors.As(err, &rpc))
	}
	t.Logf("prompt without a login: %v", err)
}
