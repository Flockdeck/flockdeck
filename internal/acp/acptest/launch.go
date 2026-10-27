package acptest

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Option changes how the fake agent behaves.
type Option func(*launch)

type launch struct{ env []string }

// WithAuth makes the agent refuse until authenticate is called: at "new" it
// answers session/new with -32000 "Authentication required", at "prompt"
// session/prompt. It then also lists two auth methods at initialize.
func WithAuth(at string) Option {
	return func(l *launch) { l.env = append(l.env, envAuth+"="+at) }
}

// WithConfigOptions makes session/new list a model and a mode as config options
// and initialize claim the capabilities of a fuller agent, as opencode and Codex
// do.
func WithConfigOptions() Option {
	return func(l *launch) { l.env = append(l.env, envShape+"=config") }
}

// WithModes makes session/new send a modes block and a models block, as Gemini
// does, and initialize claim the capabilities of a fuller agent.
func WithModes() Option {
	return func(l *launch) { l.env = append(l.env, envShape+"=modes") }
}

// WithSettingLog makes the agent append the method and params of each setting
// request (session/set_mode, session/set_model, session/set_config_option) to
// path. The agent answers them when it has a shape (WithConfigOptions or
// WithModes): a value the shape lists is taken, any other is refused with an
// error whose text a test can look for in what a person is shown.
func WithSettingLog(path string) Option {
	return func(l *launch) { l.env = append(l.env, envSetLog+"="+path) }
}

// WithBooleanOption adds a boolean option "fast" to the config shape.
func WithBooleanOption() Option { return withSetOpt("bool") }

// WithBareSettingReplies makes the agent answer session/set_config_option with
// no options, which the protocol allows an agent that has nothing to add.
func WithBareSettingReplies() Option { return withSetOpt("bare") }

// WithRefusedSettings makes the agent refuse every setting request, whatever the value.
func WithRefusedSettings() Option { return withSetOpt("refuse") }

// WithSlowSettings makes the agent take 300 ms over each setting request.
func WithSlowSettings() Option { return withSetOpt("slow") }

// WithQuietModeChanges makes the agent send no current_mode_update after it
// takes a mode, which the protocol allows.
func WithQuietModeChanges() Option { return withSetOpt("quiet") }

// WithSilentSettings makes the agent never answer a setting request.
func WithSilentSettings() Option { return withSetOpt("silent") }

func withSetOpt(name string) Option {
	return func(l *launch) { l.env = append(l.env, envSetOpts+"_"+name+"=1") }
}

// WithSessionLoad gives the agent the loadSession capability (see the package
// comment for what it then does).
func WithSessionLoad() Option { return withSession("load") }

// WithSessionResume gives the agent the session/resume capability.
func WithSessionResume() Option { return withSession("resume") }

// WithKnownSessions makes the agent know these session ids; it refuses every
// other id with an error.
func WithKnownSessions(ids ...string) Option {
	return func(l *launch) {
		for _, id := range ids {
			withSession("id=" + id)(l)
		}
	}
}

// WithReplayThenExit makes a session/load of a known id send its replay and
// then one more message and exit, as an agent that dies part-way does.
func WithReplayThenExit() Option { return withSession("replay-hang") }

// WithLateReplay makes session/new slow (600ms) and a session/load of a known id send one replayed message,
// never answer, and keep sending replayed messages (the text "LATE") for about a
// second, as an agent that is slow to load does after the client has given up.
func WithLateReplay() Option { return withSession("late-replay") }

// WithLoadRefused makes session/load fail with an error for every id, while
// session/resume works for the ids the agent knows.
func WithLoadRefused() Option { return withSession("load-refused") }

// withSession adds one item to the agent's list of session options.
func withSession(item string) Option {
	return func(l *launch) {
		for i, kv := range l.env {
			if strings.HasPrefix(kv, envSessions+"=") {
				l.env[i] = kv + "," + item
				return
			}
		}
		l.env = append(l.env, envSessions+"="+item)
	}
}

// WithMarker makes the hold-perm scenario write the outcome it received to
// path.
func WithMarker(path string) Option {
	return func(l *launch) { l.env = append(l.env, envMarker+"="+path) }
}

// Command returns what to start the fake agent with: an absolute path, its
// arguments and a full environment. The environment is an allowlist, not the
// test's own, so nothing of the user's reaches the child.
func Command(t testing.TB, opts ...Option) (path string, args []string, env []string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("acptest: find the test binary: %v", err)
	}
	l := &launch{env: []string{envAgent + "=1"}}
	for _, o := range opts {
		o(l)
	}
	for _, k := range []string{"PATH", "SYSTEMROOT", "SystemRoot", "SYSTEMDRIVE", "COMSPEC", "PATHEXT", "TEMP", "TMP", "TMPDIR"} {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	return exe, nil, append(env, l.env...)
}

// SessionDir returns a fresh directory to use as a session's cwd, outside the
// user's profile where it can be. An adapter walks up from the cwd looking for
// project settings, and with a cwd under the profile it found the user's own
// skills. On Windows the temp directory is under the profile, so the drive's
// root is used; if a directory cannot be made there, t.TempDir is the
// fallback. It is removed when the test ends.
func SessionDir(t testing.TB) string {
	t.Helper()
	// The profile cannot be asked for: the test isolation redirects it. The
	// temp directory is under it on Windows and outside it elsewhere (/tmp,
	// /var/folders), so Windows goes to the drive's root first.
	roots := []string{os.TempDir()}
	if runtime.GOOS == "windows" {
		roots = []string{volumeRoot(), os.TempDir()}
	}
	for _, root := range roots {
		if root == "" {
			continue
		}
		dir, err := os.MkdirTemp(root, "flockdeck-acp-")
		if err != nil {
			continue
		}
		t.Cleanup(func() { _ = os.RemoveAll(dir) })
		abs, err := filepath.Abs(dir)
		if err != nil {
			return dir
		}
		return abs
	}
	return t.TempDir()
}

func volumeRoot() string {
	if v := filepath.VolumeName(os.TempDir()); v != "" {
		return v + string(filepath.Separator)
	}
	return ""
}

// WithMute makes the agent read everything and answer nothing.
func WithMute() Option {
	return func(l *launch) { l.env = append(l.env, envMute+"=1") }
}

// WithDeaf makes the agent never read its input, so writes to it fill the
// pipe and then block.
func WithDeaf() Option {
	return func(l *launch) { l.env = append(l.env, envDeaf+"=1") }
}

// WithSlowTurnStart makes each turn begin d after its prompt was read, as it
// does when the machine is busy. A cancel that comes in that time is for the turn.
func WithSlowTurnStart(d time.Duration) Option {
	return func(l *launch) { l.env = append(l.env, envSlow+"="+strconv.Itoa(int(d.Milliseconds()))) }
}
