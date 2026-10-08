package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestADeviceWithNoRecordIsFull(t *testing.T) {
	var p Prefs
	if got := p.AccessFor("anything"); got.EffectiveRole() != RoleFull || got.WatchPanes {
		t.Errorf("a device with no record = %+v, want full", got)
	}
}

func TestAccessRoundTripsAndFullIsNotKept(t *testing.T) {
	isolateConfig(t)

	p, ok := LoadPrefs().WithAccess("dev-a", DeviceAccess{Role: RoleViewer, WatchPanes: true})
	if !ok {
		t.Fatal("WithAccess refused a first record")
	}
	p, _ = p.WithAccess("dev-b", DeviceAccess{Role: RoleViewer})
	if err := SavePrefs(p); err != nil {
		t.Fatal(err)
	}
	back := LoadPrefs()
	if got := back.AccessFor("dev-a"); got.EffectiveRole() != RoleViewer || !got.WatchPanes {
		t.Errorf("dev-a after a round trip = %+v", got)
	}
	if got := back.AccessFor("dev-b"); got.EffectiveRole() != RoleViewer || got.WatchPanes {
		t.Errorf("dev-b after a round trip = %+v", got)
	}
	if got := back.AccessFor("dev-c"); got.EffectiveRole() != RoleFull {
		t.Errorf("an unlisted device = %+v, want full", got)
	}

	// Making a device full again removes its record rather than keeping a
	// "full" entry, and drops the map once it is empty.
	back, _ = back.WithAccess("dev-a", DeviceAccess{Role: RoleFull, WatchPanes: true})
	back, _ = back.WithAccess("dev-b", DeviceAccess{Role: RoleFull})
	if back.Devices != nil {
		t.Errorf("Devices after both were made full = %v, want none", back.Devices)
	}
	if err := SavePrefs(back); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(mustDir(t), prefsFile))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "devices") {
		t.Errorf("the file still holds a devices key:\n%s", data)
	}
}

func mustDir(t *testing.T) string {
	t.Helper()
	dir, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// A role this build does not understand is never read as more access than a
// viewer has.
func TestAnUnknownRoleIsAViewer(t *testing.T) {
	isolateConfig(t)
	dir := mustDir(t)
	file := `{"devices":{"a":{"role":"admin"},"b":{"role":""},"c":{"role":"FULL"},"d":{}}}`
	if err := os.WriteFile(filepath.Join(dir, prefsFile), []byte(file), 0o600); err != nil {
		t.Fatal(err)
	}
	p := LoadPrefs()
	for _, id := range []string{"a", "b", "c", "d"} {
		if got := p.AccessFor(id).EffectiveRole(); got != RoleViewer {
			t.Errorf("device %q with an unrecognised role = %q, want viewer", id, got)
		}
	}
}

// A preferences file that cannot be trusted must not turn every restricted
// device back into a full one.
func TestADamagedFileMakesEveryDeviceAViewer(t *testing.T) {
	isolateConfig(t)
	dir := mustDir(t)
	if err := os.WriteFile(filepath.Join(dir, prefsFile), []byte(`{"devices": {`), 0o600); err != nil {
		t.Fatal(err)
	}
	p := LoadPrefs()
	if got := p.AccessFor("any-device"); got.EffectiveRole() != RoleViewer || got.WatchPanes {
		t.Errorf("after a damaged file a device = %+v, want a viewer that cannot watch", got)
	}
}

func TestTheRecordHasALimit(t *testing.T) {
	var p Prefs
	for i := 0; i < MaxDeviceAccess; i++ {
		var ok bool
		p, ok = p.WithAccess(fmt.Sprintf("dev-%d", i), DeviceAccess{Role: RoleViewer})
		if !ok {
			t.Fatalf("record %d was refused before the limit", i)
		}
	}
	if _, ok := p.WithAccess("one-too-many", DeviceAccess{Role: RoleViewer}); ok {
		t.Error("a record past the limit was accepted")
	}
	// Changing a device that already has one is not a new record.
	if _, ok := p.WithAccess("dev-0", DeviceAccess{Role: RoleViewer, WatchPanes: true}); !ok {
		t.Error("changing an existing record was refused at the limit")
	}
	// Making one full again frees its place.
	p, _ = p.WithAccess("dev-0", DeviceAccess{Role: RoleFull})
	if _, ok := p.WithAccess("one-too-many", DeviceAccess{Role: RoleViewer}); !ok {
		t.Error("a freed place could not be used")
	}
}

func TestWithAccessLeavesTheOriginalAlone(t *testing.T) {
	p, _ := Prefs{}.WithAccess("a", DeviceAccess{Role: RoleViewer})
	q, _ := p.WithAccess("b", DeviceAccess{Role: RoleViewer})
	if _, there := p.Devices["b"]; there {
		t.Error("WithAccess changed the map of the Prefs it was called on")
	}
	if len(q.Devices) != 2 {
		t.Errorf("result has %d records, want 2", len(q.Devices))
	}
}

func TestForWindowHidesTheRecord(t *testing.T) {
	p, _ := Prefs{HelpSeen: true}.WithAccess("a", DeviceAccess{Role: RoleViewer})
	w := p.ForWindow()
	if w.Devices != nil || !w.HelpSeen {
		t.Errorf("ForWindow = %+v, want the same preferences with no device record", w)
	}
	if p.Devices == nil {
		t.Error("ForWindow cleared the record on the original")
	}
}

// Preferences read from the file again take the running program's roles before
// they are changed, so a file that lost them does not widen anything.
func TestAdoptAccessTakesTheRunningRolesAndTheirState(t *testing.T) {
	var running Prefs
	running, _ = running.WithAccess("a", DeviceAccess{Role: RoleViewer, WatchPanes: true})
	var fresh Prefs // as ReadPrefs answers for a file that is not there
	fresh.AdoptAccess(running)
	if got := fresh.AccessFor("a"); got.EffectiveRole() != RoleViewer || !got.WatchPanes {
		t.Errorf("after adopting, a = %+v", got)
	}
	fresh.Devices["a"] = DeviceAccess{Role: RoleFull}
	if running.AccessFor("a").EffectiveRole() != RoleViewer {
		t.Error("the adopted table is shared with the one it came from")
	}

	lost := Prefs{}
	lost.KeepAccessUnknown()
	var again Prefs
	again.AdoptAccess(lost)
	if !again.AccessUnknown() || again.AccessFor("x").EffectiveRole() != RoleViewer {
		t.Error("the state of not knowing was not adopted")
	}
	// And the other way: roles that are known replace a read that was not.
	damaged := Prefs{}
	damaged.KeepAccessUnknown()
	damaged.AdoptAccess(running)
	if damaged.AccessUnknown() || damaged.AccessFor("zzz").EffectiveRole() != RoleFull {
		t.Error("known roles did not replace a read that could not be trusted")
	}
}

func TestWithoutDevicesNotIn(t *testing.T) {
	var p Prefs
	p, _ = p.WithAccess("a", DeviceAccess{Role: RoleViewer})
	p, _ = p.WithAccess("b", DeviceAccess{Role: RoleViewer, WatchPanes: true})

	for _, ids := range [][]string{nil, {}} {
		if next, changed := p.WithoutDevicesNotIn(ids); changed || len(next.Devices) != 2 {
			t.Errorf("an empty roster (%#v) dropped records: %+v", ids, next.Devices)
		}
	}
	next, changed := p.WithoutDevicesNotIn([]string{"b", "c"})
	if !changed || len(next.Devices) != 1 || next.AccessFor("a").EffectiveRole() != RoleFull || !next.AccessFor("b").WatchPanes {
		t.Errorf("pruning to b and c left %+v", next.Devices)
	}
	if len(p.Devices) != 2 {
		t.Error("pruning changed the preferences it was called on")
	}
	if next, changed := p.WithoutDevicesNotIn([]string{"a", "b"}); changed || len(next.Devices) != 2 {
		t.Error("a roster with everything in it dropped something")
	}
	if next, changed := p.WithoutDevicesNotIn([]string{"z"}); !changed || next.Devices != nil {
		t.Errorf("a roster with none of them left %+v", next.Devices)
	}
	p.KeepAccessUnknown()
	if _, changed := p.WithoutDevicesNotIn([]string{"z"}); changed {
		t.Error("records were pruned while the roles were not known")
	}
}
