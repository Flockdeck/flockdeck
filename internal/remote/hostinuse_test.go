package remote

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jmwri/flockdeck/internal/store"
)

// HostInUse is read-only and local: it must answer for each state a machine
// can be in, and fail toward "in use" when the enrolment cannot be read.
func TestHostInUse(t *testing.T) {
	enrol := func(t *testing.T, disabled bool) {
		t.Helper()
		c := Config{Relay: "https://relay.example", HostID: "h1", AccountID: "a1", Token: "fdh_x", Disabled: disabled}
		if err := c.Save(); err != nil {
			t.Fatal(err)
		}
	}
	tests := []struct {
		name  string
		setup func(t *testing.T) store.Prefs
		want  bool
	}{
		{"never set up", func(t *testing.T) store.Prefs { return store.Prefs{} }, false},
		{"remote access on", func(t *testing.T) store.Prefs { enrol(t, false); return store.Prefs{} }, true},
		{"enrolled but turned off, no device known here", func(t *testing.T) store.Prefs { enrol(t, true); return store.Prefs{} }, false},
		{"turned off, the desk holds a device record", func(t *testing.T) store.Prefs {
			enrol(t, true)
			p, _ := store.Prefs{}.WithAccess("d1", store.DeviceAccess{Role: store.RoleViewer})
			return p
		}, true},
		{"turned off, a device was verified here", func(t *testing.T) store.Prefs {
			enrol(t, true)
			var v verifiedStore
			v.mu.Lock()
			defer v.mu.Unlock()
			if err := v.save([]verifiedRecord{{Device: "d1", Origin: "app"}}); err != nil {
				t.Fatal(err)
			}
			return store.Prefs{}
		}, true},
		{"enrolment unreadable", func(t *testing.T) store.Prefs {
			dir, err := store.Dir()
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, File), []byte("{not json"), 0o600); err != nil {
				t.Fatal(err)
			}
			return store.Prefs{}
		}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			isolate(t)
			p := tc.setup(t)
			if got := HostInUse(p); got != tc.want {
				t.Errorf("HostInUse = %v, want %v", got, tc.want)
			}
		})
	}
}
