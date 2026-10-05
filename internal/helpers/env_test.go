package helpers

import (
	"strings"
	"testing"
)

func envMap(env []string) map[string]string {
	m := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		m[k] = v
	}
	return m
}

// secretNames are variables that must never reach a helper. The root package's
// test adds every name the usage text and paneEnv mention.
var secretNames = []string{
	"FLOCKDECK_TOKEN", "FLOCKDECK_API", "FLOCKDECK_PANE", "FLOCKDECK_AGENT", "FLOCKDECK_API_KEY",
	"FLOCKDECK_RELAY", "FLOCKDECK_DIR", "FLOCKDECK_UPDATE", "FLOCKDECK_SOMETHING_NEW",
	"PERCH_TOKEN", "PERCH_API", "PERCH_PANE", "PERCH_AGENT",
	"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "TYPESAFE_API_KEY", "GEMINI_API_KEY", "MY_API_KEY",
	"GITHUB_TOKEN", "GH_TOKEN", "AWS_SECRET_ACCESS_KEY", "AWS_ACCESS_KEY_ID", "NPM_TOKEN",
	"SSH_AUTH_SOCK", "CLAUDE_CONFIG_DIR", "CLAUDECODE", "ANTHROPIC_AUTH_TOKEN",
	"flockdeck_token", "Flockdeck_Api", "perch_token", "anthropic_api_key",
}

func TestBuildEnvKeepsSecretsOut(t *testing.T) {
	var parent []string
	for _, n := range secretNames {
		parent = append(parent, n+"=secret-value")
	}
	parent = append(parent, "PATH=/usr/bin", "HOME=/home/x", "LANG=en_GB.UTF-8")
	got, err := BuildEnv(lens, parent, EnvVars{Port: 8123, Host: "127.0.0.1", DataDir: "/d", AllowedHosts: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	for _, kv := range got {
		if strings.Contains(kv, "secret-value") {
			t.Errorf("a secret value reached the child: %s", kv)
		}
		name, _, _ := strings.Cut(kv, "=")
		up := strings.ToUpper(name)
		if strings.HasPrefix(up, "FLOCKDECK_") || strings.HasPrefix(up, "PERCH_") || strings.HasSuffix(up, "_API_KEY") ||
			strings.HasSuffix(up, "_TOKEN") {
			t.Errorf("%s reached the child", name)
		}
	}
}

func TestBuildEnvSetsTheEntrysVariables(t *testing.T) {
	// The parent tries to set every one of them.
	parent := []string{"PORT=1", "HOST=0.0.0.0", "DATA_DIR=/evil", "ALLOWED_HOSTS=*", "LOG_FILE=/evil.log", "port=2", "Host=evil"}
	got, err := BuildEnv(lens, parent, EnvVars{Port: 8123, Host: "127.0.0.1", DataDir: "/data/lens", AllowedHosts: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	m := envMap(got)
	want := map[string]string{"PORT": "8123", "HOST": "127.0.0.1", "DATA_DIR": "/data/lens", "ALLOWED_HOSTS": "127.0.0.1", "LOG_FILE": "-"}
	for k, v := range want {
		if m[k] != v {
			t.Errorf("%s = %q, want %q", k, m[k], v)
		}
	}
	if len(m) != len(want) {
		t.Errorf("env = %v, want exactly %v", m, want)
	}
	seen := map[string]bool{}
	for _, kv := range got {
		name, _, _ := strings.Cut(kv, "=")
		if seen[strings.ToUpper(name)] {
			t.Errorf("%s is set twice", name)
		}
		seen[strings.ToUpper(name)] = true
	}
}

func TestBuildEnvInheritsTheRuntimeSet(t *testing.T) {
	parent := []string{"Path=C:\\Windows", "SystemRoot=C:\\Windows", "LC_ALL=C", "LC_CTYPE=C", "LANG=C", "TMPDIR=/t", "TEMP=C:\\t", "TMP=C:\\t",
		"HTTPS_PROXY=http://p:3128", "https_proxy=http://p:3128", "NO_PROXY=localhost", "SSL_CERT_FILE=/c.pem", "APPDATA=a", "LOCALAPPDATA=l",
		"USERPROFILE=u", "HOME=h", "COMSPEC=cmd", "=C:=C:\\", "NOEQUALS", "EMPTYVALUE=", "RANDOM_THING=x", "EDITOR=vim"}
	got, err := BuildEnv(lens, parent, EnvVars{Port: 1, Host: "h", DataDir: "d", AllowedHosts: "a"})
	if err != nil {
		t.Fatal(err)
	}
	m := envMap(got)
	for _, name := range []string{"Path", "SystemRoot", "LC_ALL", "LC_CTYPE", "LANG", "TMPDIR", "TEMP", "TMP", "HTTPS_PROXY", "https_proxy",
		"NO_PROXY", "SSL_CERT_FILE", "APPDATA", "LOCALAPPDATA", "USERPROFILE", "HOME", "COMSPEC"} {
		if _, ok := m[name]; !ok {
			t.Errorf("%s was not passed on", name)
		}
	}
	for _, name := range []string{"RANDOM_THING", "EDITOR", "=C:", "NOEQUALS", "EMPTYVALUE"} {
		if _, ok := m[name]; ok {
			t.Errorf("%s was passed on", name)
		}
	}
}

func TestBuildEnvIsSorted(t *testing.T) {
	got, _ := BuildEnv(lens, []string{"ZED=1", "PATH=p", "HOME=h"}, EnvVars{Port: 1})
	for i := 1; i < len(got); i++ {
		if got[i-1] > got[i] {
			t.Fatalf("not sorted: %v", got)
		}
	}
}

func TestExpandRefusesUnknownPlaceholders(t *testing.T) {
	bad := Entry{Env: map[string]string{"X": "{prot}"}}
	if _, err := BuildEnv(bad, nil, EnvVars{}); err == nil {
		t.Error("an unknown placeholder in the environment was accepted")
	}
	if _, err := ExpandArgs([]string{"--port", "{prot}"}, EnvVars{}); err == nil {
		t.Error("an unknown placeholder in the arguments was accepted")
	}
	// A brace in a value that comes from the machine is not a placeholder.
	got, err := ExpandArgs([]string{"{data_dir}"}, EnvVars{DataDir: `C:\Users\{odd}\data`})
	if err != nil || got[0] != `C:\Users\{odd}\data` {
		t.Errorf("got %v, %v", got, err)
	}
}

func TestLensArguments(t *testing.T) {
	got, err := ExpandArgs(lens.Args, EnvVars{Port: 8123})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, " ") != "serve --host 127.0.0.1 --port 8123" {
		t.Errorf("args = %v", got)
	}
}

func TestEntryVariablesBeatInheritedOnes(t *testing.T) {
	e := Entry{Env: map[string]string{"home": "{data_dir}"}}
	got, err := BuildEnv(e, []string{"HOME=/real/home", "PATH=p"}, EnvVars{DataDir: "/data"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, "|") != "HOME=/data|PATH=p" {
		t.Fatalf("env = %v", got)
	}
}

// Only the locale names POSIX defines pass, not anything that starts with LC_.
func TestOnlyRealLocaleNamesAreInherited(t *testing.T) {
	good := []string{"LANG", "LANGUAGE", "LC_ALL", "LC_CTYPE", "LC_COLLATE", "LC_MESSAGES", "LC_MONETARY", "LC_NUMERIC", "LC_TIME", "lc_all"}
	bad := []string{"LC_SECRET_TOKEN", "LC_API_KEY", "LC_", "LC_FOO", "LCALL", "LANGX", "LC_PAPER_PASSWORD"}
	var parent []string
	for _, n := range append(append([]string(nil), good...), bad...) {
		parent = append(parent, n+"=v")
	}
	env, err := BuildEnv(Entry{}, parent, EnvVars{})
	if err != nil {
		t.Fatal(err)
	}
	m := envMap(env)
	for _, n := range good {
		if _, ok := m[n]; !ok {
			t.Errorf("%s was not passed on", n)
		}
	}
	for _, n := range bad {
		if _, ok := m[n]; ok {
			t.Errorf("%s was passed on", n)
		}
	}
}

// Names are folded to upper case in ASCII only: strings.ToUpper turns the long
// s (U+017F) into S, which would pass ſsl_cert_file for SSL_CERT_FILE.
func TestInheritedNamesAreFoldedInASCIIOnly(t *testing.T) {
	parent := []string{
		"ſsl_cert_file=/evil.pem", "SSL_CERT_FILE=/good.pem", "ssl_cert_file=/also.pem",
		"ſystemroot=x", "pıth=x", "Key=x", "LC_ſECRET=x",
	}
	got, err := BuildEnv(Entry{}, parent, EnvVars{})
	if err != nil {
		t.Fatal(err)
	}
	m := envMap(got)
	for name := range m {
		for _, r := range name {
			if r > 0x7f {
				t.Errorf("%q, which is not an ASCII name, was passed on", name)
			}
		}
	}
	if m["SSL_CERT_FILE"] != "/good.pem" || m["ssl_cert_file"] != "/also.pem" {
		t.Errorf("the real names were lost: %v", m)
	}
	if asciiUpper("ſsl_cert_file") == "SSL_CERT_FILE" {
		t.Error("asciiUpper folded a letter from another script")
	}
	if asciiUpper("abc_XYZ-1") != "ABC_XYZ-1" {
		t.Errorf("asciiUpper = %q", asciiUpper("abc_XYZ-1"))
	}
}

// What is passed on is said before an install, since a proxy address can carry
// a password.
func TestTheProxySettingsAreSaidToBePassedOn(t *testing.T) {
	said := false
	for _, a := range lens.Allows {
		if strings.Contains(a, "HTTP_PROXY") && strings.Contains(a, "HTTPS_PROXY") && strings.Contains(a, "NO_PROXY") && strings.Contains(a, "password") {
			said = true
		}
	}
	if !said {
		t.Fatalf("the permissions do not say the proxy settings are passed on: %v", lens.Allows)
	}
	for _, n := range []string{"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY"} {
		if !inherited(n) {
			t.Errorf("%s is not passed on, though it is said to be", n)
		}
	}
}
