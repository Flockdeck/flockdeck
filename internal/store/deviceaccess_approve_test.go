package store

import (
	"testing"
	"time"
)

func TestApproveIsOffUnlessSwitchedOnAtTheCurrentWording(t *testing.T) {
	now := time.Now().UTC()
	cases := []struct {
		name string
		a    DeviceAccess
		want bool
	}{
		{"no record", DeviceAccess{Role: RoleFull}, false},
		{"on, never agreed", DeviceAccess{Role: RoleFull, Approve: true}, false},
		{"agreed, switched off", DeviceAccess{Role: RoleFull, ApproveV: 2, ApproveAt: now}, false},
		{"older wording", DeviceAccess{Role: RoleFull, Approve: true, ApproveV: 1, ApproveAt: now}, false},
		{"current wording", DeviceAccess{Role: RoleFull, Approve: true, ApproveV: 2, ApproveAt: now}, true},
		{"newer wording", DeviceAccess{Role: RoleViewer, Approve: true, ApproveV: 3, ApproveAt: now}, true},
	}
	for _, c := range cases {
		if got := c.a.ApproveOn(2); got != c.want {
			t.Errorf("%s: ApproveOn(2) = %v, want %v", c.name, got, c.want)
		}
	}
	var p Prefs
	if p.AccessFor("any").ApproveOn(1) {
		t.Error("a device with no record may approve")
	}
}

func TestAFullDeviceThatMayApproveIsKeptAndOneThatMayNotIsNot(t *testing.T) {
	isolateConfig(t)
	a := DeviceAccess{Role: RoleFull, Approve: true, ApproveV: 1, ApproveAt: time.Now().UTC().Truncate(time.Second)}
	p, ok := LoadPrefs().WithAccess("d1", a)
	if !ok {
		t.Fatal("refused")
	}
	if err := SavePrefs(p); err != nil {
		t.Fatal(err)
	}
	got := LoadPrefs().AccessFor("d1")
	if !got.ApproveOn(1) || got.EffectiveRole() != RoleFull || !got.ApproveAt.Equal(a.ApproveAt) {
		t.Errorf("read back %+v, want %+v", got, a)
	}
	// An agreement without the switch is not kept, and a full device with
	// nothing left to say leaves the file.
	p, _ = p.WithAccess("d1", DeviceAccess{Role: RoleFull, ApproveV: 1, ApproveAt: a.ApproveAt, WatchPanes: true})
	if len(p.Devices) != 0 {
		t.Errorf("records = %+v, want none", p.Devices)
	}
}

func TestARecordWithoutApprovalOrWithLostRecordsGrantsNone(t *testing.T) {
	isolateConfig(t)
	p, _ := LoadPrefs().WithAccess("d1", DeviceAccess{Role: RoleViewer, WatchPanes: true})
	if err := SavePrefs(p); err != nil {
		t.Fatal(err)
	}
	if LoadPrefs().AccessFor("d1").ApproveOn(0) {
		t.Error("a record written without approval grants it")
	}
	lost := Prefs{}
	lost.KeepAccessUnknown()
	if lost.AccessFor("d1").ApproveOn(0) {
		t.Error("an unreadable record grants approval")
	}
}

func TestWithoutApprovalsSwitchesEveryDeviceOffAndKeepsTheRest(t *testing.T) {
	now := time.Now().UTC()
	var p Prefs
	p, _ = p.WithAccess("full", DeviceAccess{Role: RoleFull, Approve: true, ApproveV: 1, ApproveAt: now})
	p, _ = p.WithAccess("watch", DeviceAccess{Role: RoleViewer, WatchPanes: true, Approve: true, ApproveV: 1, ApproveAt: now})
	p, _ = p.WithAccess("plain", DeviceAccess{Role: RoleViewer})
	got, was := p.WithoutApprovals()
	if len(was) != 2 {
		t.Errorf("was on for %v, want two", was)
	}
	if _, has := got.Devices["full"]; has {
		t.Errorf("a full device with nothing left is kept: %+v", got.Devices["full"])
	}
	if w := got.Devices["watch"]; w.Approve || w.ApproveV != 0 || !w.ApproveAt.IsZero() || w.Role != RoleViewer || !w.WatchPanes {
		t.Errorf("watch = %+v, want a viewer that watches and may not approve", w)
	}
	if got.Devices["plain"] != (DeviceAccess{Role: RoleViewer}) {
		t.Errorf("plain = %+v, want unchanged", got.Devices["plain"])
	}
	if !p.Devices["full"].Approve {
		t.Error("the receiver was changed")
	}
	if again, was := got.WithoutApprovals(); len(was) != 0 || len(again.Devices) != len(got.Devices) {
		t.Errorf("a second pass changed %v", was)
	}
	var empty Prefs
	if next, was := empty.WithoutApprovals(); len(was) != 0 || next.Devices != nil {
		t.Errorf("empty prefs gave %+v, %v", next.Devices, was)
	}
}

func TestApproveForNeedsTheKeyTheGrantWasMadeFor(t *testing.T) {
	now := time.Now().UTC()
	key := []byte("device key")
	h := KeyHash(key)
	a := DeviceAccess{Role: RoleFull, Approve: true, ApproveV: 2, ApproveAt: now, ApproveKey: h}
	if !a.ApproveFor(2, h) {
		t.Error("the key the grant was made for is refused")
	}
	if a.ApproveFor(2, KeyHash([]byte("another key"))) {
		t.Error("another key is accepted")
	}
	if a.ApproveFor(2, "") || KeyHash(nil) != "" {
		t.Error("no key is accepted")
	}
	a.ApproveKey = ""
	if a.ApproveFor(2, h) {
		t.Error("a grant with no key recorded is accepted")
	}
	a.ApproveKey = h
	if a.ApproveFor(3, h) {
		t.Error("an older wording is accepted")
	}
}

func TestSwitchingApprovalOffAlsoDropsTheKey(t *testing.T) {
	now := time.Now().UTC()
	var p Prefs
	p, _ = p.WithAccess("d", DeviceAccess{Role: RoleViewer, WatchPanes: true, Approve: true, ApproveV: 2, ApproveAt: now, ApproveKey: "h"})
	off, _ := p.WithAccess("d", DeviceAccess{Role: RoleViewer, WatchPanes: true, ApproveV: 2, ApproveAt: now, ApproveKey: "h"})
	if a := off.Devices["d"]; a.ApproveKey != "" || a.ApproveV != 0 || !a.ApproveAt.IsZero() {
		t.Errorf("switched off, the record keeps %+v", a)
	}
	got, _ := p.WithoutApprovals()
	if a := got.Devices["d"]; a.ApproveKey != "" || a.Approve {
		t.Errorf("WithoutApprovals keeps %+v", a)
	}
}

func TestAdoptAccessOnlyTakesApprovalTheFileStillAllows(t *testing.T) {
	now := time.Now().UTC()
	grant := DeviceAccess{Role: RoleFull, Approve: true, ApproveV: 2, ApproveAt: now, ApproveKey: "h"}
	var mem Prefs
	mem, _ = mem.WithAccess("a", grant)
	mem, _ = mem.WithAccess("b", grant)
	mem, _ = mem.WithAccess("c", DeviceAccess{Role: RoleViewer, WatchPanes: true, Approve: true, ApproveV: 2, ApproveAt: now, ApproveKey: "h"})

	// The file, as `flockdeck remote approvals off` left it: a still allows,
	// b and c do not (c keeps its role).
	var file Prefs
	file, _ = file.WithAccess("a", grant)
	file, _ = file.WithAccess("c", DeviceAccess{Role: RoleViewer, WatchPanes: true})

	file.AdoptAccess(mem)
	if !file.Devices["a"].ApproveFor(2, "h") {
		t.Errorf("a lost a grant the file allows: %+v", file.Devices["a"])
	}
	if _, has := file.Devices["b"]; has {
		t.Errorf("b was written back: %+v", file.Devices["b"])
	}
	if c := file.Devices["c"]; c.Approve || c.ApproveKey != "" || c.Role != RoleViewer || !c.WatchPanes {
		t.Errorf("c = %+v, want its role and no approval", c)
	}
	if !mem.Devices["b"].Approve {
		t.Error("the source was changed")
	}
}
