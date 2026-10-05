package baton

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func quickRecords(t *testing.T) {
	t.Helper()
	recordSleep = func(time.Duration) {}
	t.Cleanup(func() { recordSleep = time.Sleep })
	damagedSeen.Range(func(k, _ any) bool { damagedSeen.Delete(k); return true })
}

// A read that fails (a lock held by a scanner, a refusal) is not damage: nothing is moved aside,
// there is no hold, and nothing is pruned in that run. One that works the second time is fine.
func TestARecordThatCannotBeReadIsNotMovedAsideAndPruningStopsThatRun(t *testing.T) {
	withClock(t)
	quickRecords(t)
	s := NewStore(filepath.Join(t.TempDir(), "batons"))
	id := oldBaton(t, s, 2)
	s.Touch(id)
	ageFile(t, s.Path(id), 90*24*time.Hour)
	used := filepath.Join(s.dir, usedName)
	old := readRecordFile
	t.Cleanup(func() { readRecordFile = old })
	readRecordFile = func(p string) ([]byte, error) {
		if p == used {
			return nil, errors.New("The process cannot access the file because it is being used by another process.")
		}
		return os.ReadFile(p)
	}
	now := time.Now()
	if removed, err := s.Prune(RetainFor, now, nil); err != nil || len(removed) != 0 {
		t.Fatalf("removed %v, err %v", removed, err)
	}
	if _, err := os.Stat(used + ".bad"); err == nil {
		t.Error("a record that could not be read was moved aside")
	}
	if got := s.pruneKey("hold_until"); got != "" {
		t.Errorf("a hold was set: %q", got)
	}
	// A read that fails twice and then works: the record is whole.
	calls := 0
	readRecordFile = func(p string) ([]byte, error) {
		if p == used {
			if calls++; calls <= 2 {
				return nil, errors.New("locked")
			}
		}
		return os.ReadFile(p)
	}
	if st := s.recordState(usedName); st != recOK {
		t.Errorf("a read that worked the third time: state %v", st)
	}
}

// A record found damaged that is whole when the records are locked is not moved aside.
func TestARecordThatWasMendedBeforeTheLockIsNotMovedAside(t *testing.T) {
	withClock(t)
	quickRecords(t)
	s := NewStore(filepath.Join(t.TempDir(), "batons"))
	oldBaton(t, s, 2)
	if err := os.WriteFile(filepath.Join(s.dir, usedName), []byte("{\"x\":"), 0o600); err != nil {
		t.Fatal(err)
	}
	used := filepath.Join(s.dir, usedName)
	old := readRecordFile
	t.Cleanup(func() { readRecordFile = old })
	readRecordFile = func(p string) ([]byte, error) {
		data, err := os.ReadFile(p)
		if p == used {
			// the first look finds it damaged; another process then writes it whole
			_ = os.WriteFile(used, []byte("{}"), 0o600)
			readRecordFile = old
		}
		return data, err
	}
	if removed, err := s.Prune(RetainFor, time.Now(), nil); err != nil || len(removed) != 0 {
		t.Fatalf("removed %v, err %v", removed, err)
	}
	if _, err := os.Stat(used + ".bad"); err == nil {
		t.Error("a record that had been written whole was moved aside")
	}
	if got := s.pruneKey("hold_until"); got != "" {
		t.Errorf("a hold was set: %q", got)
	}
}

// A damaged record that cannot be kept aside is left as it is: Touch does not write over it.
func TestTouchDoesNotWriteOverARecordItCouldNotKeepAside(t *testing.T) {
	quickRecords(t)
	s := NewStore(filepath.Join(t.TempDir(), "batons"))
	id := oldBaton(t, s, 2)
	used := filepath.Join(s.dir, usedName)
	if err := os.WriteFile(used, []byte("damaged"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldRename := renameFile
	renameFile = func(string, string) error { return os.ErrPermission }
	t.Cleanup(func() { renameFile = oldRename })
	s.Touch(id)
	if data, _ := os.ReadFile(used); string(data) != "damaged" {
		t.Errorf("the damaged record was written over: %q", data)
	}
}

// With 100 copies kept aside no more are kept, the last is not written over, and the record
// starts again.
func TestTheHundredthCopyKeptAsideIsNotWrittenOver(t *testing.T) {
	quickRecords(t)
	s := NewStore(filepath.Join(t.TempDir(), "batons"))
	id := oldBaton(t, s, 2)
	used := filepath.Join(s.dir, usedName)
	if err := os.WriteFile(used+".bad", []byte("kept 0"), 0o600); err != nil {
		t.Fatal(err)
	}
	for i := 1; i < 100; i++ {
		if err := os.WriteFile(fmt.Sprintf("%s.bad.%d", used, i), []byte(fmt.Sprintf("kept %d", i)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(used, []byte("one more"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.Touch(id)
	if data, _ := os.ReadFile(used + ".bad.99"); string(data) != "kept 99" {
		t.Errorf("the last copy kept was written over: %q", data)
	}
	if m, ok := s.readTimes(usedName); !ok || m[id] == "" {
		t.Errorf("the record did not start again: %v %v", m, ok)
	}
}

// The hold on pruning survives a prune record that is lost.
func TestAHoldOnPruningSurvivesAPruneRecordThatIsLost(t *testing.T) {
	withClock(t)
	quickRecords(t)
	s := NewStore(filepath.Join(t.TempDir(), "batons"))
	oldBaton(t, s, 2)
	now := time.Now()
	hold := now.Add(10 * time.Hour).UTC().Format(time.RFC3339)
	// from the bytes of the damaged record
	if err := os.WriteFile(filepath.Join(s.dir, pruneName), []byte(`{"hold_until": "`+hold+`", "seen": "not a time"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Prune(RetainFor, now, nil); err != nil {
		t.Fatal(err)
	}
	if got := s.pruneKey("hold_until"); got != hold {
		t.Errorf("hold_until is %q, want %q", got, hold)
	}
	// from memory, when the bytes say nothing
	if err := os.WriteFile(filepath.Join(s.dir, pruneName), []byte(`{"hold_until": `), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Prune(RetainFor, now.Add(time.Minute), nil); err != nil {
		t.Fatal(err)
	}
	if got := s.pruneKey("hold_until"); got != hold {
		t.Errorf("from memory: hold_until is %q, want %q", got, hold)
	}
}

// Records of the wrong shape are damaged: a list, numbers where text is, text where times are.
func TestRecordsOfTheWrongShapeAreDamaged(t *testing.T) {
	for name, content := range map[string]string{
		usedName:      `[]`,
		sourcesName:   `{"id": 5}`,
		overflowsName: `{"id": "text"}`,
		retriesName:   `{"id": "text"}`,
	} {
		withClock(t)
		quickRecords(t)
		s := NewStore(filepath.Join(t.TempDir(), "batons"))
		oldBaton(t, s, 2)
		if err := os.WriteFile(filepath.Join(s.dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if removed, err := s.Prune(RetainFor, time.Now(), nil); err != nil || len(removed) != 0 {
			t.Errorf("%s: removed %v, err %v", name, removed, err)
		}
		if _, err := os.Stat(filepath.Join(s.dir, name+".bad")); err != nil {
			t.Errorf("%s of the wrong shape was not kept aside: %v", name, err)
		}
	}
	quickRecords(t)
	s := NewStore(filepath.Join(t.TempDir(), "batons"))
	oldBaton(t, s, 2)
	if err := os.WriteFile(filepath.Join(s.dir, usedName), []byte(`{"id": "yesterday"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if s.recordOK(usedName) {
		t.Error("a record of times with a value that is not a time was whole")
	}
}

// A record that is cut short with a complete copy beside it is read from the copy and is not
// moved aside; a copy that is not complete is taken away when the record starts again.
func TestARecordWithACompleteCopyBesideItIsNotMovedAside(t *testing.T) {
	withClock(t)
	quickRecords(t)
	s := NewStore(filepath.Join(t.TempDir(), "batons"))
	id := oldBaton(t, s, 2)
	used := filepath.Join(s.dir, usedName)
	if err := os.WriteFile(used, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cp := filepath.Join(s.dir, ".record-"+usedName+"-1.tmp")
	if err := os.WriteFile(cp, []byte(`{"`+id+`": "`+time.Now().UTC().Format(time.RFC3339)+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if removed, err := s.Prune(RetainFor, time.Now(), nil); err != nil || len(removed) != 0 {
		t.Fatalf("removed %v, err %v", removed, err)
	}
	if _, err := os.Stat(used + ".bad"); err == nil {
		t.Error("a record with a complete copy beside it was moved aside")
	}
	if m, ok := s.readTimes(usedName); !ok || m[id] == "" {
		t.Errorf("not read from the copy: %v %v", m, ok)
	}
	// A copy that is not complete goes when the record starts again.
	s2 := NewStore(filepath.Join(t.TempDir(), "batons"))
	oldBaton(t, s2, 2)
	bad := filepath.Join(s2.dir, ".record-"+usedName+"-7.tmp")
	if err := os.WriteFile(bad, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s2.dir, usedName), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s2.Prune(RetainFor, time.Now(), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(bad); err == nil || !strings.Contains(fmt.Sprint(err), "cannot find") && !os.IsNotExist(err) {
		t.Errorf("the copy that was not complete stayed: %v", err)
	}
}
