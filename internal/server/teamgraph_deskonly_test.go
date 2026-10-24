package server

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
)

// The new shell's store tells the desk-only refusal by its code, and by a
// phrase of its text for a desktop that sends no code. Both are written in
// store.js, so they are read from there and checked against what the server
// sends.
func TestNewShellStoreMatchesTheDeskOnlyRefusal(t *testing.T) {
	b, err := os.ReadFile("../../ui/src/lib/store.js")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	code := regexp.MustCompile(`export const DESK_ONLY_CODE = "([^"]+)"`).FindStringSubmatch(src)
	phrase := regexp.MustCompile(`export const DESK_ONLY_PHRASE = "([^"]+)"`).FindStringSubmatch(src)
	if code == nil || phrase == nil {
		t.Fatal("store.js does not declare DESK_ONLY_CODE and DESK_ONLY_PHRASE")
	}
	if code[1] != teamDeskOnlyCode {
		t.Errorf("store.js tells the refusal by code %q, the server sends %q", code[1], teamDeskOnlyCode)
	}
	if !strings.Contains(teamDeskOnly, phrase[1]) {
		t.Errorf("store.js matches %q, which is not in the refusal text %q", phrase[1], teamDeskOnly)
	}
}

// The refusals that carry the code: a graph command, and a Team panel command,
// from a window through the relay.
func TestDeskOnlyRefusalsCarryTheCode(t *testing.T) {
	e := newGraphEnv(t)
	c := &controlClient{out: make(chan []byte, 8), remote: true}
	e.srv.graphCommand(c, command{Cmd: "graphWatch", Root: e.root, Req: "r"})
	e.srv.teamCommand(c, command{Cmd: "teamTaskAdd", Req: "r2"})
	var codes []string
	for len(c.out) > 0 {
		raw := <-c.out
		if typeOf(raw) != "teamResult" {
			continue
		}
		var res teamResultMsg
		if err := json.Unmarshal(raw, &res); err != nil {
			t.Fatal(err)
		}
		if res.OK || res.Error != teamDeskOnly {
			t.Errorf("result = %+v", res)
		}
		codes = append(codes, res.Code)
	}
	if len(codes) != 2 || codes[0] != teamDeskOnlyCode || codes[1] != teamDeskOnlyCode {
		t.Errorf("codes = %v, want two of %q", codes, teamDeskOnlyCode)
	}
}
