package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRemoteArtifactsPrefsAreOffAndLeftOutByDefault(t *testing.T) {
	isolateConfig(t)
	dir, _ := Dir()
	path := filepath.Join(dir, prefsFile)
	if err := os.WriteFile(path, []byte(`{"helpSeen":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	p := LoadPrefs()
	if p.RemoteArtifacts.KindOn("recordings") || p.RemoteArtifacts.HasDevice("d1") || p.RemoteArtifacts.Acked("recordings", 1) {
		t.Fatalf("an older prefs.json read as %+v", p.RemoteArtifacts)
	}
	if err := SavePrefs(p); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); strings.Contains(string(data), "remoteArtifacts") {
		t.Errorf("the default settings were written out:\n%s", data)
	}
}

func TestRemoteArtifactsPrefsRoundTrip(t *testing.T) {
	isolateConfig(t)
	p := LoadPrefs()
	p.RemoteArtifacts.SetAck("recordings", ArtifactAck{Version: 2, At: time.Unix(1700000000, 0).UTC()})
	p.RemoteArtifacts.SetKind("recordings", true)
	p.RemoteArtifacts.AddDevice("d1")
	if err := SavePrefs(p); err != nil {
		t.Fatal(err)
	}
	got := LoadPrefs().RemoteArtifacts
	if !got.KindOn("recordings") || !got.HasDevice("d1") || !got.Acked("recordings", 2) || got.Acked("recordings", 3) {
		t.Errorf("round trip: %+v", got)
	}
	if got.KindOn("files") || got.HasDevice("d2") || got.HasDevice("") {
		t.Errorf("round trip granted more than was set: %+v", got)
	}
}

func TestRemoteArtifactsPrefsEditing(t *testing.T) {
	var a RemoteArtifactsPrefs
	if a.SetKind("recordings", false) || a.RemoveDevice("d1") || a.AllOff() || a.AddDevice("") {
		t.Error("a no-op reported a change")
	}
	if !a.SetKind("recordings", true) || a.SetKind("recordings", true) {
		t.Error("SetKind change reporting")
	}
	if !a.AddDevice("d1") || a.AddDevice("d1") {
		t.Error("AddDevice change reporting")
	}
	a.SetAck("recordings", ArtifactAck{Version: 1})
	if !a.AllOff() || a.KindOn("recordings") {
		t.Error("AllOff left a kind on")
	}
	if !a.HasDevice("d1") || !a.Acked("recordings", 1) {
		t.Error("AllOff dropped the device list or the acknowledgement")
	}
	if !a.RemoveDevice("d1") || a.HasDevice("d1") || a.Devices != nil {
		t.Errorf("RemoveDevice: %+v", a)
	}
}
