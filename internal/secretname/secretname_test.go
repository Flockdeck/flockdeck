package secretname

import "testing"

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
		// names the second review found missing
		"prod.tfvars.json", "a.auto.tfvars.json", ".my.cnf", "token.json", ".gh_token", "api_key.txt", "api-key.txt",
		"apikey.txt", "auth.json", "svc.keytab", ".dev.vars", "gradle.properties", ".terraformrc", "private_key.asc",
		"x.env.json", "kubeconfig.yaml", "kubeconfig-prod", "kube.config", ".gitconfig", "known_hosts", "known_hosts.old",
		"Login Data", "Login Data-journal", "cookies.sqlite", "access_key.csv",
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
		"git.md", "private.go", "known_hosts.md", "kubernetes.md", "apis.md", ".gitignore", ".github", ".gitattributes", "docs", "src", "report.html", "", ".", "..",
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
