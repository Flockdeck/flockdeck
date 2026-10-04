package helpers

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// A helper's environment is built from an allowlist, never by removing names
// from Flockdeck's own. A name Flockdeck gains later (a token, a key, a relay
// address) is kept out without anyone remembering to list it, and the cost is
// that a variable a helper wants has to be named here or set by the entry.

// inheritedNames are the variables a program needs to run at all, and the
// proxy settings a person behind one has chosen for everything on the
// machine. Names are compared in upper case, since Windows spells PATH "Path".
var inheritedNames = map[string]bool{
	"PATH": true, "PATHEXT": true,
	"HOME": true, "USERPROFILE": true, "APPDATA": true, "LOCALAPPDATA": true,
	"TEMP": true, "TMP": true, "TMPDIR": true,
	"SYSTEMROOT": true, "SYSTEMDRIVE": true, "WINDIR": true, "COMSPEC": true,
	// The locale names POSIX defines, and nothing that merely starts with LC_:
	// a prefix would pass LC_SECRET_TOKEN.
	"LANG": true, "LANGUAGE": true, "LC_ALL": true, "LC_CTYPE": true, "LC_COLLATE": true,
	"LC_MESSAGES": true, "LC_MONETARY": true, "LC_NUMERIC": true, "LC_TIME": true,
	// Proxy settings can carry credentials in the URL. They are the person's
	// own for this machine and a helper that reaches the network needs them.
	"HTTP_PROXY": true, "HTTPS_PROXY": true, "NO_PROXY": true,
	"SSL_CERT_FILE": true,
}

// inherited reports whether a variable of this name passes to a helper.
func inherited(name string) bool {
	up := strings.ToUpper(name)
	return inheritedNames[up]
}

// EnvVars are the values the entry's templates are filled from.
type EnvVars struct {
	Port         int
	Host         string
	DataDir      string
	AllowedHosts string
}

func (v EnvVars) expand(s string) (string, error) {
	r := strings.NewReplacer(
		"{port}", strconv.Itoa(v.Port),
		"{host}", v.Host,
		"{data_dir}", v.DataDir,
		"{allowed_hosts}", v.AllowedHosts,
	)
	// A placeholder left over means the catalogue names one that does not
	// exist. Checked on the template, so a path that happens to contain a
	// brace is not mistaken for one.
	left := strings.NewReplacer("{port}", "", "{host}", "", "{data_dir}", "", "{allowed_hosts}", "").Replace(s)
	if strings.ContainsAny(left, "{}") {
		return "", fmt.Errorf("%q has a placeholder that is not known", s)
	}
	return r.Replace(s), nil
}

// ExpandArgs fills the entry's argument templates.
func ExpandArgs(args []string, v EnvVars) ([]string, error) {
	out := make([]string, len(args))
	for i, a := range args {
		s, err := v.expand(a)
		if err != nil {
			return nil, err
		}
		out[i] = s
	}
	return out, nil
}

// BuildEnv is the environment for a helper process: the allowlisted part of
// parent (a list of NAME=value, as os.Environ gives), then the entry's own
// variables filled from v, which win over an inherited one of the same name.
func BuildEnv(e Entry, parent []string, v EnvVars) ([]string, error) {
	own := make(map[string]string, len(e.Env))
	for name, tmpl := range e.Env {
		val, err := v.expand(tmpl)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		own[strings.ToUpper(name)] = val
	}
	kept := map[string]string{}
	for _, kv := range parent {
		name, val, ok := strings.Cut(kv, "=")
		if !ok || name == "" || !inherited(name) {
			continue
		}
		if _, overridden := own[strings.ToUpper(name)]; overridden {
			continue
		}
		kept[name] = val
	}
	out := make([]string, 0, len(kept)+len(own))
	for name, val := range kept {
		out = append(out, name+"="+val)
	}
	for name, val := range own {
		out = append(out, name+"="+val)
	}
	sort.Strings(out)
	return out, nil
}
