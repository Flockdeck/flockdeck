package testiso

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/store"
	"github.com/jmwri/flockdeck/internal/testiso/iso"
)

func TestMain(m *testing.M) { os.Exit(Main(m)) }

// The config directory a test resolves is a temporary one, not the user's.
func TestConfigDirIsNotTheRealOne(t *testing.T) {
	cfg, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	if iso.UnderRealConfig(cfg) {
		t.Fatalf("os.UserConfigDir() = %q, the real one", cfg)
	}
	dir, err := store.Dir()
	if err != nil {
		t.Fatal(err)
	}
	if iso.UnderRealConfig(dir) {
		t.Fatalf("store.Dir() = %q, under the real one", dir)
	}
	if home, _ := os.UserHomeDir(); home == iso.RealHomeDir() && home != "" {
		t.Errorf("os.UserHomeDir() = %q, the real one", home)
	}
}

// No API key of the developer's reaches a test through the environment.
func TestNoAPIKeysInheritedFromTheEnvironment(t *testing.T) {
	for _, kv := range os.Environ() {
		name, val, _ := strings.Cut(kv, "=")
		if strings.HasSuffix(strings.ToUpper(name), "_API_KEY") && val != "" {
			t.Errorf("%s is set", name)
		}
	}
}

// Regression: a test that points the config directory back at the real one
// trips the guard before anything is created or read there.
func TestGuardTripsOnTheRealConfigDir(t *testing.T) {
	if iso.RealConfigDir() == "" {
		t.Skip("no real config dir on this machine")
	}
	iso.ResetViolations()
	t.Cleanup(iso.ResetViolations)
	// Every variable os.UserConfigDir reads, set to what makes it the real
	// directory on whichever platform this is.
	t.Setenv("APPDATA", iso.RealConfigDir())
	t.Setenv("XDG_CONFIG_HOME", iso.RealConfigDir())
	t.Setenv("HOME", iso.RealHomeDir())

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("store.Dir() on the real config directory did not trip the guard")
		}
		if msg, _ := r.(string); !strings.Contains(msg, "real user config dir") {
			t.Errorf("tripped with %v", r)
		}
		if len(iso.Violations()) != 1 {
			t.Errorf("violations = %v, want the one", iso.Violations())
		}
	}()
	store.Dir()
}

// Regression: a request to a real host is refused before any lookup, through
// the default client and a client with no transport of its own, and the
// refusal is remembered even if the caller swallows it.
func TestGuardRefusesANonLoopbackRequest(t *testing.T) {
	iso.ResetViolations()
	t.Cleanup(iso.ResetViolations)
	for name, c := range map[string]*http.Client{
		"default client": http.DefaultClient,
		"bare client":    {},
	} {
		_, err := c.Get("https://api.typesafe.invalid/v1/anything")
		if !errors.Is(err, iso.ErrNonLoopback) {
			t.Errorf("%s: err = %v, want ErrNonLoopback", name, err)
		}
	}
	if _, err := http.Post("http://203.0.113.7:8080/", "text/plain", strings.NewReader("task text")); !errors.Is(err, iso.ErrNonLoopback) {
		t.Errorf("an IP address: err = %v, want ErrNonLoopback", err)
	}
	if n := len(iso.Violations()); n != 3 {
		t.Errorf("%d violations remembered, want 3: %v", n, iso.Violations())
	}
}

// An in-process fake server, which is how a test talks to anything, still works.
func TestGuardAllowsLoopback(t *testing.T) {
	iso.ResetViolations()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if v := iso.Violations(); len(v) != 0 {
		t.Errorf("loopback was refused: %v", v)
	}
}
