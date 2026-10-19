package store

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"
)

func TestKnowledgeSettingRoundTripsAndIsPerProject(t *testing.T) {
	isolateConfig(t)
	p, err := ReadPrefs()
	if err != nil {
		t.Fatal(err)
	}
	if p.KnowledgeSetting("/work/a") != "" {
		t.Fatal("a project has a setting before anyone set one")
	}
	if !p.SetKnowledge("/work/a", KnowledgeOn) || p.SetKnowledge("/work/a", KnowledgeOn) {
		t.Error("SetKnowledge should report a change once")
	}
	if p.SetKnowledge("/work/b", "maybe") {
		t.Error("a state other than on and off was accepted")
	}
	if err := SavePrefs(p); err != nil {
		t.Fatal(err)
	}
	q := LoadPrefs()
	if q.KnowledgeSetting("/work/a") != KnowledgeOn || q.KnowledgeSetting("/work/b") != "" {
		t.Errorf("loaded = %v", q.Knowledge)
	}
	if got := q.ForWindow().Knowledge; got != nil {
		t.Errorf("a window was sent the project paths: %v", got)
	}
}

// A copy of the preferences must not change the original's settings.
func TestSetKnowledgeDoesNotWriteIntoACopysMap(t *testing.T) {
	a := Prefs{}
	a.SetKnowledge("/x", KnowledgeOn)
	b := a
	b.SetKnowledge("/x", KnowledgeOff)
	if a.KnowledgeSetting("/x") != KnowledgeOn {
		t.Error("changing a copy changed the original")
	}
}

func TestProjectContainingFindsTheFolderAProjectWasOpenedAs(t *testing.T) {
	isolateConfig(t)
	parent := t.TempDir() // a folder holding two repositories, itself no repository
	inner := filepath.Join(parent, "api")
	other := t.TempDir()
	if err := TouchRecents(parent, inner, other); err != nil {
		t.Fatal(err)
	}
	FlushRecents()

	got, ok := ProjectContaining(filepath.Join(inner, "pkg", "deep"))
	if !ok || got != inner {
		t.Errorf("ProjectContaining(deep) = %q, %v, want the nearest project %q", got, ok, inner)
	}
	if got, ok := ProjectContaining(filepath.Join(parent, "web")); !ok || got != parent {
		t.Errorf("ProjectContaining(web) = %q, %v, want %q", got, ok, parent)
	}
	if _, ok := ProjectContaining(parent + "-sibling"); ok {
		t.Error("a sibling whose name starts the same was taken for inside the project")
	}
	if _, ok := ProjectContaining(t.TempDir()); ok {
		t.Error("a directory in no project was found in one")
	}
}

func TestUpdatePrefsIsOneStepAndSkipsAnUnchangedWrite(t *testing.T) {
	isolateConfig(t)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = UpdatePrefs(func(p *Prefs) bool { return p.Dismiss(fmt.Sprintf("tip-%d", i)) })
		}(i)
	}
	wg.Wait()
	if n := len(LoadPrefs().DismissedTips); n != 20 {
		t.Errorf("%d of 20 concurrent changes survived", n)
	}
	if err := UpdatePrefs(func(p *Prefs) bool { return false }); err != nil {
		t.Errorf("an unchanged update failed: %v", err)
	}
}
