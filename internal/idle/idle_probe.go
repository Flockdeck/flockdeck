//go:build linux || darwin

package idle

import (
	"context"
	"os/exec"

	"github.com/jmwri/flockdeck/internal/sysproc"
)

// runProbe runs name under probeTimeout and returns what it wrote to stdout.
// sysproc.NoWindow is a no-op here -- only Windows needs it -- and is called
// anyway so a probe added later that also runs there is covered without
// anyone having to remember to.
func runProbe(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	sysproc.NoWindow(cmd)
	out, err := cmd.Output()
	return string(out), err
}
