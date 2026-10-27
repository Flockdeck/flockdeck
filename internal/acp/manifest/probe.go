package manifest

import "os/exec"

// Probe is how the checks look at the machine; tests replace it.
type Probe struct {
	// LookPath finds a program on PATH, like exec.LookPath.
	LookPath func(name string) (string, error)
}

// SystemProbe looks at the real machine.
func SystemProbe() Probe {
	return Probe{LookPath: exec.LookPath}
}

// minimalEnv is what an adapter check runs with: PATH, and on Windows the few
// variables a program cannot start without.
func minimalEnv() []string {
	return minimalEnvFrom(osEnviron())
}
