package main

import (
	"bytes"
	byteorder "encoding/binary"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// These tests drive buildDarwinBundle itself, with a stand-in for the compiled
// program, so they check the bundle's assembly, icon and signature with
// macOS's own tools (plutil, sips, iconutil, codesign) without paying for a
// full build the way TestDarwinArchiveIsASignedAppBundle does. Where that test
// reads the archive back, these read the bundle as macOS would.

// realBundle builds a bundle around a fake program and returns its path.
func realBundle(t *testing.T, version string) string {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skip("bundling darwin needs a Mac's own sips, iconutil and codesign")
	}
	t.Chdir(filepath.Join("..", "..")) // where linuxIconSource is found
	built := filepath.Join(t.TempDir(), "built")
	if err := os.WriteFile(built, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	bundle, err := buildDarwinBundle(built, version)
	if err != nil {
		t.Fatalf("buildDarwinBundle: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(filepath.Dir(bundle)) })
	return bundle
}

func runTool(t *testing.T, name string, args ...string) string {
	t.Helper()
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return string(out)
}

func TestDarwinBundleStructure(t *testing.T) {
	bundle := realBundle(t, "v9.9.9")

	exe, err := os.Stat(filepath.Join(bundle, "Contents", "MacOS", binary))
	if err != nil {
		t.Fatal(err)
	}
	if !exe.Mode().IsRegular() || exe.Mode().Perm()&0o111 != 0o111 {
		t.Errorf("Contents/MacOS/%s is %v, want an executable regular file", binary, exe.Mode())
	}
	icon, err := os.Stat(filepath.Join(bundle, "Contents", "Resources", "AppIcon.icns"))
	if err != nil {
		t.Fatal(err)
	}
	if icon.Size() == 0 {
		t.Error("Contents/Resources/AppIcon.icns is empty")
	}
	if _, err := os.Stat(filepath.Join(bundle, "Contents", "Info.plist")); err != nil {
		t.Error(err)
	}
}

func TestDarwinBundleInfoPlistIsValidAndStamped(t *testing.T) {
	bundle := realBundle(t, "v9.9.9")
	plist := filepath.Join(bundle, "Contents", "Info.plist")

	runTool(t, "plutil", "-lint", plist)

	var got map[string]any
	if err := json.Unmarshal([]byte(runTool(t, "plutil", "-convert", "json", "-o", "-", plist)), &got); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]any{
		"CFBundleExecutable":         binary,
		"CFBundleIdentifier":         darwinBundleID,
		"CFBundleShortVersionString": "9.9.9", // the leading v is dropped
		"CFBundleVersion":            "9.9.9",
		"CFBundleIconFile":           "AppIcon",
		"CFBundlePackageType":        "APPL",
		"NSHighResolutionCapable":    true,
	} {
		if got[key] != want {
			t.Errorf("Info.plist %s = %v, want %v", key, got[key], want)
		}
	}
	// Absent, not false: it keeps the Dock icon and app-switcher entry.
	if _, ok := got["LSUIElement"]; ok {
		t.Error("Info.plist sets LSUIElement, which would hide Flockdeck's Dock icon")
	}
}

func TestDarwinBundleIconIsARealIcns(t *testing.T) {
	bundle := realBundle(t, "v9.9.9")
	icns := filepath.Join(bundle, "Contents", "Resources", "AppIcon.icns")

	data, err := os.ReadFile(icns)
	if err != nil {
		t.Fatal(err)
	}
	// An icns file is the magic "icns" and a big-endian length of the whole
	// file, then its icon entries.
	if len(data) < 8 || !bytes.Equal(data[:4], []byte("icns")) {
		t.Fatalf("AppIcon.icns does not start with the icns magic: % x", data[:min(8, len(data))])
	}
	if n := byteorder.BigEndian.Uint32(data[4:8]); int(n) != len(data) {
		t.Errorf("AppIcon.icns header says %d bytes, the file is %d", n, len(data))
	}

	// macOS itself has to read it: sips names its format, and iconutil unpacks
	// it back into the sizes buildIcns put in.
	if out := runTool(t, "sips", "-g", "format", icns); !strings.Contains(out, "icns") {
		t.Errorf("sips does not see AppIcon.icns as an icns:\n%s", out)
	}
	iconset := filepath.Join(t.TempDir(), "AppIcon.iconset")
	runTool(t, "iconutil", "-c", "iconset", icns, "-o", iconset)
	for _, s := range darwinIconSizes {
		if fi, err := os.Stat(filepath.Join(iconset, s.name)); err != nil || fi.Size() == 0 {
			t.Errorf("round-tripping AppIcon.icns lost %s (%v)", s.name, err)
		}
	}
}

func TestDarwinBundleSignatureVerifies(t *testing.T) {
	bundle := realBundle(t, "v9.9.9")

	runTool(t, "codesign", "--verify", "--deep", "--strict", bundle)
	if out := runTool(t, "codesign", "-dv", bundle); !strings.Contains(out, "Signature=adhoc") {
		t.Errorf("the bundle is not ad-hoc signed:\n%s", out)
	}

	// The verification above only means something if it can fail: change the
	// program after it was sealed and it has to.
	exe := filepath.Join(bundle, "Contents", "MacOS", binary)
	if err := os.WriteFile(exe, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("codesign", "--verify", "--deep", "--strict", bundle).CombinedOutput(); err == nil {
		t.Errorf("codesign --verify passed a bundle whose program changed after signing:\n%s", out)
	}
}
