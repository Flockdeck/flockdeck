package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/mdexport"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/session/transcript"
)

func TestStructuredExportAnswersWhenTheExportPanics(t *testing.T) {
	e := newStructuredEnv(t, "plain")
	e.status(t, session.StatusIdle)
	old := exportRender
	exportRender = func([]transcript.Entry, mdexport.Options) (mdexport.Result, error) { panic("boom") }
	t.Cleanup(func() { exportRender = old })
	dir := t.TempDir()
	res := e.exportOp(t, command{Dir: dir})
	if res.OK || res.Error != structuredExportFailed || strings.Contains(res.Error, "boom") {
		t.Fatalf("result = %+v", res)
	}
	if left, _ := os.ReadDir(dir); len(left) != 0 {
		t.Errorf("files written: %v", left)
	}
	// The pane is free for another export afterwards.
	exportRender = old
	if res := e.exportOp(t, command{Dir: dir}); !res.OK {
		t.Errorf("the next export was refused: %+v", res)
	}
}

func TestStructuredExportSaysWhenTextWasTooLargeToRedact(t *testing.T) {
	e := newStructuredEnv(t, "plain")
	e.status(t, session.StatusIdle)
	old := exportRender
	exportRender = func(es []transcript.Entry, o mdexport.Options) (mdexport.Result, error) {
		r, err := old(es, o)
		r.Markdown += "\n> [REDACTED: too-large-to-scrub]\n"
		return r, err
	}
	t.Cleanup(func() { exportRender = old })
	res := e.exportOp(t, command{Dir: t.TempDir()})
	if !res.OK || !strings.Contains(res.Warning, "too large or took too long to redact") {
		t.Errorf("result = %+v", res)
	}
}

func TestStructuredExportRefusesWhenTheRedactorCannotBeMadeWhole(t *testing.T) {
	cwd := t.TempDir()
	// A .env file over what the scrubber reads: its values would not be removed.
	if err := os.WriteFile(filepath.Join(cwd, ".env"), []byte("TOKEN=abcdefghijklmnop\n"+strings.Repeat("#", 1<<20+10)), 0o600); err != nil {
		t.Fatal(err)
	}
	e := newStructuredEnvWith(t, "plain", structuredEnvOpts{Cwd: cwd})
	e.status(t, session.StatusIdle)
	dir := t.TempDir()
	res := e.exportOp(t, command{Dir: dir})
	if res.OK || res.Error != structuredExportNoScrub {
		t.Fatalf("result = %+v", res)
	}
	if left, _ := os.ReadDir(dir); len(left) != 0 {
		t.Errorf("files written: %v", left)
	}
}
