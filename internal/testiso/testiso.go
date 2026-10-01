// Package testiso is what a package's TestMain uses to keep its tests off the
// real config directory, the stored API keys and the real network:
//
//	func TestMain(m *testing.M) { os.Exit(testiso.Main(m)) }
//
// See internal/testiso/iso for what that does, and docs/testing.md for how to
// write a test that stays isolated. The store package's own tests cannot import
// this (it would be a cycle) and use iso directly.
package testiso

import (
	"testing"

	"github.com/jmwri/flockdeck/internal/store"
	"github.com/jmwri/flockdeck/internal/testiso/iso"
)

// Main runs the package's tests isolated and returns the exit code.
func Main(m *testing.M) int {
	store.DirGuard = iso.DirGuard
	return iso.Main(m)
}
