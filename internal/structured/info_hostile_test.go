package structured

import (
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/acp"
)

// plain says s holds no control character (C0 or C1), no escape, no bidi or
// other format character and no line or paragraph separator.
func plain(s string) bool {
	for _, r := range s {
		if r < 0x20 || (r >= 0x7f && r <= 0x9f) || r == 0x200b || r == 0x202e || r == 0x2066 || r == 0x2028 || r == 0x2029 {
			return false
		}
	}
	return true
}

func TestInfoCleansWhateverTheAgentOrTheOptionsSaid(t *testing.T) {
	hostile := "a\x1b[31mb‮c d e\u0085f​g\x00h"
	r := begin(t, fakeOptions(t))
	until(t, r, "ready", func(s Snapshot) bool { return s.Phase == PhaseReady })
	// What initialize and the session gave, as a hostile agent would send it.
	r.noteHello(acp.InitResult{
		AgentInfo:   &acp.AgentInfo{Name: hostile, Version: hostile},
		AuthMethods: []acp.AuthMethod{{ID: hostile, Name: hostile}, {ID: hostile}},
	}, []acp.MCPServer{{Name: hostile}})
	r.mu.Lock()
	r.agentNm, r.session = hostile, hostile
	r.opts.Cwd, r.opts.Spec.ID, r.opts.Spec.ACP.Adapter = hostile, hostile, hostile
	r.mu.Unlock()
	i := r.Info()
	fields := map[string]string{"Name": i.Name, "Version": i.Version, "Catalog": i.Catalog, "Adapter": i.Adapter, "Cwd": i.Cwd, "SessionID": i.SessionID}
	for k, v := range fields {
		if !plain(v) || !strings.HasPrefix(v, "a[31mbc") {
			t.Errorf("%s = %q", k, v)
		}
	}
	for _, l := range [][]string{i.Auth, i.MCP} {
		if len(l) == 0 {
			t.Fatal("a list is empty")
		}
		for _, v := range l {
			if !plain(v) {
				t.Errorf("list entry %q", v)
			}
		}
	}
	if len(i.Auth) != 2 {
		t.Errorf("auth = %q", i.Auth)
	}
}
