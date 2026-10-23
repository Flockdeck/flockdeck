//go:build windows

package permprobe

import (
	"context"
	"testing"
)

// createNoWindow is CREATE_NO_WINDOW. The probe runs at every pane start, so a
// console window would flash each time.
const createNoWindow = 0x08000000

func TestTheProbeStartsWithoutAConsoleWindow(t *testing.T) {
	cmd := probeCmd(context.Background(), "opencode", []string{"agent", "list"}, t.TempDir(), []string{"A=1"})
	if cmd.SysProcAttr == nil || cmd.SysProcAttr.CreationFlags&createNoWindow == 0 {
		t.Errorf("the probe's process would open a window: %+v", cmd.SysProcAttr)
	}
}
