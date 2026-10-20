package server

import "testing"

// The new shell's modules are text and go through the relay gzipped like the
// old scripts; an image is already compressed.
func TestCompressibleCoversModules(t *testing.T) {
	for name, want := range map[string]bool{
		"app.js": true, "vendor/preact.mjs": true, "app.css": true, "index.html": true,
		"icon-256.png": false, "fonts/x.woff2": false,
	} {
		if got := compressible(name); got != want {
			t.Errorf("compressible(%q) = %v, want %v", name, got, want)
		}
	}
}
