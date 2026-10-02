package secretname

import (
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/review"
)

// Threat: a secret file read because its name was not recognised. Every name a
// reviewer or a previous check found missing, and the ones that were already
// known, are secret; ordinary files are not.
func TestComponent(t *testing.T) {
	secret := []string{
		// what internal/review and internal/record already treat as secret
		".env", ".env.local", ".ENV", "id_rsa", "id_ed25519", "id_rsa.bak", ".npmrc", ".netrc", "_netrc", ".pgpass",
		".git-credentials", "aws-credentials.json", "credentials", "server.pem", "tls.key", "a.p12", "a.pfx",
		"a.ppk", "a.keystore", "a.jks",
		// names that review.SecretPath, an argv matcher, misses
		"-prod.pem", "-id_rsa", "key.pem=", "key.pem#", "key.pem.bak", "key.pem~", "KEY.PEM.", "key.pem ",
		".envrc", "prod.env", ".env-prod", "secrets.yml", "secrets.json", "secret.toml", "my-secrets.txt",
		"db_password.txt", "passwd", "prod.tfvars", "terraform.tfstate", "terraform.tfstate.backup",
		".htpasswd", ".pypirc", ".dockercfg", ".yarnrc.yml", ".boto", ".s3cfg", ".vault-token", "kubeconfig",
		"service-account.json", "vault.kdbx", "backup.gpg", ".bash_history", ".zsh_history", "wp-config.php",
		"secrets.yml.enc", "credentials.yml.enc", "master.key", "client.ovpn", "AuthKey_ABC.p8",
		// folders, as names
		".git", ".ssh", ".aws", ".gnupg", ".kube", ".docker", ".azure", ".terraform", "secrets", "Private", ".secrets",
	}
	for _, n := range secret {
		if !Component(n) {
			t.Errorf("Component(%q) = false, want secret", n)
		}
	}
	plain := []string{
		"README.md", "main.go", "notes.txt", "config.json", "id_rsa.pub", "id_ed25519.pub", "environment.md",
		"secretary.md", "keyboard.txt", "monkey.png", "tokens.css", "design-tokens.json",
		"git.md", "private.go", ".gitignore", ".github", ".gitattributes", "docs", "src", "report.html", "", ".", "..",
	}
	for _, n := range plain {
		if Component(n) {
			t.Errorf("Component(%q) = true, want an ordinary name", n)
		}
	}
}

// Threat: a secret reached through a folder that is only a folder: every
// component is judged, not only the last, and a folder pair that holds a token
// (.config/gh) is caught though neither half is.
func TestPathJudgesEveryComponent(t *testing.T) {
	for p, want := range map[string]bool{
		"src/main.go":                  false,
		"a/b/c/d.txt":                  false,
		".aws/config":                  true,
		"a/.aws/config":                true,
		"secrets/x.txt":                true,
		"a/Private/x.txt":              true,
		"a/private/b/readme.md":        true,
		"x/.terraform/providers/a":     true,
		".config/gh/hosts.yml":         true,
		".config/gcloud/creds.db":      true,
		".config/htop/htoprc":          false,
		"config/secrets.yml.enc":       true,
		`a\.ssh\id_rsa.pub`:            true, // a public key, but under .ssh
		"docs/.github/workflows/a.yml": false,
	} {
		if got := Path(p); got != want {
			t.Errorf("Path(%q) = %v, want %v", p, got, want)
		}
	}
}

// Parity: nothing internal/review.SecretPath calls secret may be let through
// here. That function is the check recordings and auto-review use; the two
// must not drift apart, and this one must only ever be stricter. Names it
// reads as arguments (a leading "-", an "=") are outside what it was written
// for and are the point of this package.
func TestAtLeastAsStrictAsReview(t *testing.T) {
	for _, n := range []string{
		".env", ".env.x", "id_rsa", "id_dsa.old", ".npmrc", ".netrc", "_netrc", ".pgpass", ".git-credentials",
		"x-credential-y", "a.pem", "a.key", "a.p12", "a.pfx", "a.ppk", "a.keystore", "a.jks", "A.PEM", "dir/.env", `dir\id_rsa`,
	} {
		if review.SecretPath(n) && !Path(n) {
			t.Errorf("review calls %q secret and this does not", n)
		}
	}
}

func FuzzAtLeastAsStrictAsReview(f *testing.F) {
	for _, s := range []string{".env", "id_rsa", "a.pem", "x/y/.npmrc", "a.pub", "credential", ".ENV.local"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, n string) {
		if strings.ContainsRune(n, '=') || strings.HasPrefix(n, "-") {
			return // an argument's own syntax, which is not a file name
		}
		if review.SecretPath(n) && !Path(n) {
			t.Fatalf("review calls %q secret and this does not", n)
		}
	})
}
