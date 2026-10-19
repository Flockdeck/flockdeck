package manifest

import (
	"os"
	"runtime"
	"strings"
)

func osEnviron() []string { return os.Environ() }

// minimalEnvFrom keeps PATH and, on Windows, the few variables a program
// cannot start without. Nothing else of the parent's environment (keys,
// tokens, proxies, config folders) is passed on.
func minimalEnvFrom(environ []string) []string {
	keep := map[string]bool{"PATH": true}
	if runtime.GOOS == "windows" {
		for _, k := range []string{"SYSTEMROOT", "WINDIR", "COMSPEC", "PATHEXT", "TEMP", "TMP"} {
			keep[k] = true
		}
	}
	var out []string
	for _, kv := range environ {
		k, _, ok := strings.Cut(kv, "=")
		if ok && keep[strings.ToUpper(k)] {
			out = append(out, kv)
		}
	}
	return out
}
