package store

import "testing"

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
