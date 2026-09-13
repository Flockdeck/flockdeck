package appwindow

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// A browser installed with Flatpak puts nothing on PATH, and snap's Brave is
// plain `brave`, so none of them was found and the window came up as an
// ordinary tab. They are looked for, and each can still be pinned by its name.
func TestFlatpakAndSnapBrowsersAreLookedFor(t *testing.T) {
	got := linuxCandidates("/home/u")
	for _, want := range []string{
		"brave",
		"/var/lib/flatpak/exports/bin/com.google.Chrome",
		"/home/u/.local/share/flatpak/exports/bin/com.google.Chrome",
		"/var/lib/flatpak/exports/bin/org.chromium.Chromium",
		"/var/lib/flatpak/exports/bin/com.microsoft.Edge",
		"/home/u/.local/share/flatpak/exports/bin/com.brave.Browser",
	} {
		if !slices.Contains(got, want) {
			t.Errorf("Linux browsers looked for = %q, want %q among them", got, want)
		}
	}
	for _, c := range got {
		if !strings.Contains(c, "flatpak") {
			continue
		}
		named := false
		for _, pieces := range browserNames {
			for _, piece := range pieces {
				named = named || strings.Contains(strings.ToLower(c), piece)
			}
		}
		if !named {
			t.Errorf("%s cannot be pinned by any browser name", c)
		}
	}
}

// Snap will not let a browser it confines use a profile in a hidden folder of
// the home folder, which is where Flockdeck keeps its state, so a snap
// Chromium or Brave could not open the window at all. It is given a folder
// under ~/snap/<name>/common, where snap lets it write.
func TestASnapBrowserIsGivenAProfileSnapAllows(t *testing.T) {
	home := filepath.FromSlash("/home/u")
	state := filepath.FromSlash("/home/u/.config/flockdeck/window")
	for path, name := range map[string]string{"/snap/bin/chromium": "chromium", "/snap/bin/brave": "brave"} {
		want := filepath.Join(home, "snap", name, "common", "flockdeck-window")
		if got := profileFor(path, state, home); got != want {
			t.Errorf("profileFor(%s) = %q, want %q", path, got, want)
		}
	}
	if got := profileFor("/usr/bin/google-chrome", state, home); got != state {
		t.Errorf("a browser that is not a snap was moved to %q, want the profile it had", got)
	}
}
