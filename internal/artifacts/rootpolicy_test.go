package artifacts

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// env makes a guardEnv from a map, with no password file and no system answer,
// so that no test depends on or changes the process's own environment.
func env(m map[string]string) guardEnv {
	return guardEnv{get: func(k string) string { return m[k] }, uid: os.Getuid()}
}

func mkdir(t *testing.T, p string) string {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// Threat: a root judged against a home directory that is not known or is
// wrong -- HOME and USERPROFILE unset, empty, relative, not there, or pointing
// at somewhere else (a daemon, a launchd or systemd unit, env -i). Unless a
// home is found that exists (and, where the platform says, is the user's own),
// every root is refused.
func TestNoHomeFailsClosed(t *testing.T) {
	base := t.TempDir()
	proj := mkdir(t, filepath.Join(base, "work", "proj"))
	for name, m := range map[string]map[string]string{
		"nothing set":       {},
		"empty":             {"HOME": "", "USERPROFILE": ""},
		"relative":          {"HOME": "relative/home", "USERPROFILE": "relative/home"},
		"not there":         {"HOME": filepath.Join(base, "nowhere"), "USERPROFILE": filepath.Join(base, "nowhere")},
		"drive and path":    {"HOMEDRIVE": filepath.VolumeName(base), "HOMEPATH": `\no\such\place`},
		"appdata elsewhere": {"APPDATA": filepath.Join(base, "nowhere", "Roaming")},
	} {
		if why := forbiddenRoot(proj, env(m)); why == "" {
			t.Errorf("%s: forbiddenRoot allowed %q with no usable home", name, proj)
		}
	}
	// With a real, existing home, the same project is fine.
	home := mkdir(t, filepath.Join(base, "home"))
	if why := forbiddenRoot(proj, withSystemAnswer(env(map[string]string{"HOME": home}))); why != "" {
		t.Errorf("a project beside a known home was refused: %s", why)
	}
}

// Threat: HOME is set, exists, and is wrong (the project, a temp folder), and
// nothing else says where the real home is: the real one must still be
// recognised by where it is, not only by what HOME says.
func TestAHomeIsAHomeWhateverHomeSays(t *testing.T) {
	base := t.TempDir()
	wrong := mkdir(t, filepath.Join(base, "elsewhere"))
	sep := string(filepath.Separator)
	vol := filepath.VolumeName(base)
	me := vol + sep + "home" + sep + "me" // a home that is not there to look at
	if runtime.GOOS == "windows" {
		me = vol + sep + "Users" + sep + "me"
	}
	homes := []string{filepath.ToSlash(wrong)}
	for rel, want := range map[string]bool{ // true: refused
		"home/bob":                               true,
		"home/bob/.config":                       true,
		"home/bob/.config/google-chrome/Default": true,
		"home/bob/.ssh/keys":                     true,
		"home/bob/Documents":                     true,
		"home/bob/work/proj":                     true, // not a home this process knows as its own
		"home":                                   true,
		"Users":                                  true,
		"Users/bob/OneDrive/Documents":           true,
		"mnt":                                    true,
		"mnt/c":                                  true,
		"mnt/c/Users":                            true,
		"mnt/c/Users/me":                         true,
		"mnt/c/Users/me/proj":                    true,
		"srv/proj":                               false,
		"opt/work/proj":                          false,
	} {
		real := vol + sep + filepath.FromSlash(rel)
		if got := homeShape(real, homes) != ""; got != want {
			t.Errorf("homeShape(%q) refused = %v, want %v (%s)", real, got, want, homeShape(real, homes))
		}
	}
	// A home this process does know as its own: the home itself, its secret and
	// settings folders and the redirected ones are refused; a project is not.
	homes = []string{filepath.ToSlash(me)}
	for rel, want := range map[string]bool{
		"":                                      true,
		".config":                               true,
		".config/gh/hosts":                      true,
		"Documents":                             true,
		"Documents/proj":                        false,
		"OneDrive":                              true,
		"OneDrive/Documents":                    true,
		"OneDrive/Desktop":                      true,
		"OneDrive/Documents/proj":               false,
		"repos/private/app":                     false,
		"work/secrets-manager":                  false,
		".local/share/keyrings":                 true,
		"AppData/Local/Google/Chrome/User Data": true,
		"AppData/Local":                         true,
		"AppData/Local/Temp/x/001":              false,
	} {
		real := filepath.Join(me, filepath.FromSlash(rel))
		if got := homeShape(real, homes) != ""; got != want {
			t.Errorf("homeShape(%q) refused = %v, want %v (%s)", real, got, want, homeShape(real, homes))
		}
	}
}

// Threat: the home found from the password file: a line ending in CR leaves a
// bogus home, an oversized file is read in full, a line for another uid is used.
func TestPasswdParsing(t *testing.T) {
	data := "root:x:0:0:root:/root:/bin/sh\r\nbob:x:1001:1001:Bob:/home/bob\r:/bin/sh\n" +
		"amy:x:1002:1002::/home/amy:/bin/sh\r\nbroken:x:1003\nnohome:x:1004:1004:::/bin/sh\n"
	for uid, want := range map[int]string{0: "/root", 1002: "/home/amy", 1003: "", 1004: "", 9: ""} {
		if got := parsePasswd([]byte(data), uid); got != want {
			t.Errorf("parsePasswd(uid %d) = %q, want %q", uid, got, want)
		}
	}
	if got := parsePasswd([]byte("bob:x:5:5::/home/bob\r\n"), 5); got != "/home/bob" {
		t.Errorf("a line ending in CR gave %q", got)
	}

	if runtime.GOOS == "windows" {
		return
	}
	dir := t.TempDir()
	home := mkdir(t, filepath.Join(dir, "real home"))
	passwd := filepath.Join(dir, "passwd")
	// A huge file: the home is on a line past the limit and is not found; one
	// before it is.
	pad := strings.Repeat("x:x:1:1::/x:/bin/sh\n", maxPasswdBytes/20+10)
	line := fmt.Sprintf("me:x:%d:%d::%s:/bin/sh\r\n", os.Getuid(), os.Getgid(), home)
	if err := os.WriteFile(passwd, []byte(line+pad), 0o644); err != nil {
		t.Fatal(err)
	}
	ge := guardEnv{get: func(string) string { return "" }, uid: os.Getuid(), passwd: passwd}
	if hs := ge.homes(); len(hs) != 1 || hs[0] != filepath.ToSlash(home) {
		t.Errorf("homes() = %v, want [%s]", hs, home)
	}
	if err := os.WriteFile(passwd, []byte(pad+line), 0o644); err != nil {
		t.Fatal(err)
	}
	if hs := ge.homes(); len(hs) != 0 {
		t.Errorf("a line past the size limit was read: %v", hs)
	}
	// Someone else's folder is not the user's home.
	ge.uid = os.Getuid() + 12345
	if err := os.WriteFile(passwd, []byte(fmt.Sprintf("them:x:%d:1::%s:/bin/sh\n", ge.uid, home)), 0o644); err != nil {
		t.Fatal(err)
	}
	if hs := ge.homes(); len(hs) != 0 {
		t.Errorf("a folder owned by another user was taken as the home: %v", hs)
	}
}

// Threat: a root inside a place that is named as a secret on the way, however
// deep (/mnt/backup/.ssh/a/b), while a project below a folder that is only
// called "private" or "secrets" (~/repos/private/app) or is called
// secrets-manager is an ordinary project.
func TestEveryFolderAboveARootIsJudged(t *testing.T) {
	home := "/h"
	_ = home
	for p, want := range map[string]bool{
		"mnt/backup/.ssh":            true,
		"mnt/backup/.ssh/a/b":        true,
		"x/.git/hooks":               true,
		"x/.config/gh/y":             true,
		"run/secrets":                true,
		"x/private":                  true,
		"x/User Data":                true,
		"repos/private/app":          false,
		"repos/secrets/app":          false,
		"private/var/folders/x/001":  false,
		"repos/secrets-manager":      false,
		"repos/token-service/src":    false,
		"repos/credentials-tool":     false,
		"repos/password-manager/app": false,
	} {
		real := string(filepath.Separator) + filepath.FromSlash(p)
		if got := namedAsSecret(real) != ""; got != want {
			t.Errorf("namedAsSecret(%q) refused = %v, want %v", real, got, want)
		}
	}
}

// Threat: Windows system and program folders as roots (C:\Program Files, an 8.3
// spelling of it): judged by identity and by the environment's names for them.
func TestSystemFoldersAreRefused(t *testing.T) {
	base := t.TempDir()
	pf := mkdir(t, filepath.Join(base, "Program Files"))
	home := mkdir(t, filepath.Join(base, "h"))
	ge := env(map[string]string{"HOME": home, "ProgramFiles": pf})
	if why := forbiddenRoot(mkdir(t, filepath.Join(pf, "App")), ge); why == "" {
		t.Error("a folder below ProgramFiles was allowed")
	}
	if why := forbiddenRoot(pf, ge); why == "" {
		t.Error("ProgramFiles itself was allowed")
	}
}

// withSystemAnswer adds the system's own answer to where the home is, which on
// Windows is where a test's temporary folders are (under the real profile).
func withSystemAnswer(ge guardEnv) guardEnv {
	ge.profile = osProfileDir
	return ge
}
