package gitx

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// url.<base>.insteadOf and pushInsteadOf are applied once to the URL a pin holds
// and again by git when it is given that URL, so a chain (A to B, B to C) shows
// one host and connects to another. Any such rule in the repository is refused.
func TestResolvePinRefusesURLRewriteRules(t *testing.T) {
	for _, key := range []string{"insteadOf", "pushInsteadOf"} {
		repo, bare := pinnedRepo(t)
		gitRun(t, repo, "config", "url.https://b.example/.pushInsteadOf", bare)
		gitRun(t, repo, "config", "url.https://c.example/."+key, "https://b.example/")
		var pe *ErrPinProgram
		if _, err := ResolvePin(repo, false); !errors.As(err, &pe) {
			t.Errorf("%s push: %v", key, err)
		}
		if _, err := ResolvePin(repo, true); !errors.As(err, &pe) {
			t.Errorf("%s pull: %v", key, err)
		}
	}
}

// A rewrite rule that is not in the repository (the user's own) that applies twice
// is caught by asking git whether it would change the pinned URL again.
func TestResolvePinRefusesAURLGitRewritesAgain(t *testing.T) {
	repo, _ := pinnedRepo(t)
	gitRun(t, repo, "remote", "set-url", "origin", "https://a.example/x.git")
	if _, err := ResolvePin(repo, false); err != nil {
		t.Fatalf("a plain URL: %v", err)
	}
	if urlStable(repo, "https://a.example/x.git") != true {
		t.Fatal("a plain URL is not stable")
	}
	cfg := filepath.Join(t.TempDir(), "global")
	if err := os.WriteFile(cfg, []byte("[url \"https://b.example/\"]\n\tinsteadOf = https://a.example/\n[url \"https://c.example/\"]\n\tinsteadOf = https://b.example/\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)
	if _, err := ResolvePin(repo, false); !errors.Is(err, ErrPinURL) {
		t.Errorf("a URL rewritten twice: %v", err)
	}
	if _, err := ResolvePin(repo, true); !errors.Is(err, ErrPinURL) {
		t.Errorf("a fetch URL rewritten twice: %v", err)
	}
}

func TestResolvePinRefusesSettingsThatWeakenTheConnection(t *testing.T) {
	for _, kv := range [][2]string{
		{"http.sslVerify", "false"},
		{"http.sslCAInfo", "/tmp/ca.pem"},
		{"http.sslCAPath", "/tmp/ca"},
		{"http.curloptResolve", "example.com:443:127.0.0.1"},
		{"http.https://example.com/.sslVerify", "false"},
		{"http.proxy", "http://127.0.0.1:1"},
		{"ssh.variant", "simple"},
	} {
		repo, _ := pinnedRepo(t)
		gitRun(t, repo, "config", kv[0], kv[1])
		var pe *ErrPinProgram
		if _, err := ResolvePin(repo, false); !errors.As(err, &pe) {
			t.Errorf("%s push: %v", kv[0], err)
		}
		if _, err := ResolvePin(repo, true); !errors.As(err, &pe) {
			t.Errorf("%s pull: %v", kv[0], err)
		}
	}
}

func TestResolvePinRefusesASubmoduleUpdateCommand(t *testing.T) {
	repo, _ := pinnedRepo(t)
	gitRun(t, repo, "config", "submodule.x.update", "!touch pwned")
	var pe *ErrPinProgram
	if _, err := ResolvePin(repo, true); !errors.As(err, &pe) {
		t.Errorf("pull: %v", err)
	}
	gitRun(t, repo, "config", "submodule.x.update", "rebase")
	if _, err := ResolvePin(repo, true); err != nil {
		t.Errorf("a built-in update mode: %v", err)
	}
}

func TestResolvePinRefusesAPushThatWouldSkipAHook(t *testing.T) {
	hook := func(t *testing.T, repo string) {
		if err := os.WriteFile(filepath.Join(repo, ".git", "hooks", "pre-push"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cases := map[string]func(t *testing.T, repo string){
		"pre-push hook": hook,
		"local hooksPath": func(t *testing.T, repo string) {
			gitRun(t, repo, "config", "core.hooksPath", ".githooks")
		},
		"lfs filter": func(t *testing.T, repo string) {
			gitRun(t, repo, "config", "filter.lfs.clean", "git-lfs clean -- %f")
		},
		"lfs attributes": func(t *testing.T, repo string) {
			if err := os.WriteFile(filepath.Join(repo, ".gitattributes"), []byte("*.bin filter=lfs diff=lfs merge=lfs -text\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, set := range cases {
		repo, _ := pinnedRepo(t)
		set(t, repo)
		if _, err := ResolvePin(repo, false); !errors.Is(err, ErrPinHook) {
			t.Errorf("%s: push: %v", name, err)
		}
		if name != "lfs filter" {
			if _, err := ResolvePin(repo, true); errors.Is(err, ErrPinHook) {
				t.Errorf("%s: a pull is not refused for a push hook", name)
			}
		}
	}
}

func TestResolvePinRefusesAPullFromTheRepositoryItself(t *testing.T) {
	repo, _ := pinnedRepo(t)
	gitRun(t, repo, "config", "branch.main.remote", ".")
	if _, err := ResolvePin(repo, true); !errors.Is(err, ErrPullRemote) {
		t.Errorf("remote '.': %v", err)
	}
}
