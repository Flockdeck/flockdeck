// Package secretname decides, from a file's name alone, whether it is one whose
// contents are a secret: a dotenv file, a private key, a credentials or token
// file, a state file that holds them, a folder that exists to keep them.
//
// It is a leaf: it imports only the standard library's strings and unicode, so
// that a package which must never start a process or reach the network can use
// it without inheriting what internal/review (which runs commands) imports.
//
// It is written for file names, not command-line arguments, which is what
// internal/review.SecretPath was written for and why it is not used here: that
// function reads a "--flag=value" argument's value, so it returns false for a
// name that starts with "-" and cuts a name at its first "=". A file called
// "-prod.pem" or "key.pem=" is a perfectly good file name. Everything
// review.SecretPath treats as secret is secret here too (the test in this
// package holds the two to that), and a good deal more.
//
// # What it cannot do
//
// It is a name check. A secret copied or saved under an unremarkable name
// ("notes.txt", "config.json") is not caught, and neither is a secret inside a
// file that is not itself secret. A caller that shows files must say so to the
// person who turns it on, and must not treat this as a classifier.
package secretname

import (
	"strings"
	"unicode"
)

// Component reports whether one path component, a file or a folder name, is a
// secret by name. It is case-insensitive and ignores trailing dots and spaces,
// which Windows drops.
func Component(name string) bool {
	// Both as written (lower-cased) and cleaned of what a check should not be
	// fooled by: the second can only find more.
	return match(strings.ToLower(name)) || match(normalise(name))
}

func match(n string) bool {
	if n == "" {
		return false
	}
	if secretFolders[n] {
		return true
	}
	if secretFiles[n] {
		return true
	}
	switch {
	case strings.HasPrefix(n, ".env."), strings.HasSuffix(n, ".env"):
		return true // .env.local, prod.env
	case strings.HasPrefix(n, "id_") && !strings.HasSuffix(n, ".pub"):
		return true // a private ssh key; the .pub beside it is public
	case strings.Contains(n, "credential"):
		return true
	case strings.Contains(n, ".tfstate"):
		return true // terraform.tfstate, terraform.tfstate.backup
	case strings.HasPrefix(n, ".env"): // .envrc, .env-prod, .env_local
		return true
	}
	for _, ext := range secretExtensions {
		if strings.HasSuffix(n, ext) {
			return true
		}
	}
	for _, tok := range tokens(n) {
		if secretWords[tok] {
			return true
		}
	}
	return false
}

// backupSuffixes are what an editor or a person adds to a copy: key.pem.bak is
// key.pem.
var backupSuffixes = []string{".bak", ".old", ".orig", ".save", ".backup", ".tmp", ".swp", ".copy", "~"}

// normalise lower-cases a name, drops what Windows would drop (trailing dots
// and spaces) and other trailing punctuation a name check should not be fooled
// by ("key.pem=", "key.pem#"), and takes off backup suffixes.
func normalise(name string) string {
	// A leading "-" is no part of a file name's meaning ("-id_rsa" is id_rsa
	// to anything that was handed it as an argument), and a name check is
	// fooled by it the way an argument parser is.
	n := strings.TrimLeft(strings.ToLower(name), "-")
	for {
		before := n
		n = strings.TrimRightFunc(n, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
		for _, s := range backupSuffixes {
			if len(n) > len(s) {
				n = strings.TrimSuffix(n, s)
			}
		}
		if n == before {
			return n
		}
	}
}

// Path reports whether any component of rel, a path with / or \ separators, is
// a secret by name. Every component is judged, not only the last: a file under
// ".aws/", "secrets/" or "private/" is as secret as the folder says.
func Path(rel string) bool {
	for _, c := range strings.FieldsFunc(rel, func(r rune) bool { return r == '/' || r == '\\' }) {
		if Component(c) {
			return true
		}
	}
	// A folder pair: the folder alone is ordinary, what is under it is not.
	parts := strings.FieldsFunc(strings.ToLower(rel), func(r rune) bool { return r == '/' || r == '\\' })
	for i := 0; i+1 < len(parts); i++ {
		if pairFolders[parts[i]+"/"+parts[i+1]] {
			return true
		}
	}
	return false
}

// tokens splits a name into its words at anything that is not a letter or a
// digit, so "my-secrets.txt" is "my", "secrets", "txt" and "secretary.md" is
// one word that is not "secret".
func tokens(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}

// secretWords make a name secret when they are one of its words.
var secretWords = map[string]bool{
	"secret": true, "secrets": true, "password": true, "passwords": true, "passwd": true,
}

// secretExtensions end a name that holds key material or a protected store.
var secretExtensions = []string{
	".pem", ".key", ".p12", ".pfx", ".ppk", ".p8", ".keystore", ".jks", ".jceks", ".pkcs12",
	".tfvars", ".kdbx", ".kdb", ".gpg", ".keychain", ".keychain-db", ".ovpn",
}

// secretFiles are whole names, lower-case.
var secretFiles = map[string]bool{
	".netrc": true, "_netrc": true, ".npmrc": true, ".pgpass": true, ".git-credentials": true,
	".htpasswd": true, ".pypirc": true, ".dockercfg": true, ".yarnrc": true, ".yarnrc.yml": true,
	".boto": true, ".s3cfg": true, ".vault-token": true, "kubeconfig": true, ".kubeconfig": true,
	"service-account.json": true, "service_account.json": true,
	".bash_history": true, ".zsh_history": true, ".psql_history": true, ".mysql_history": true,
	".sqlite_history": true, ".node_repl_history": true, ".python_history": true, ".lesshst": true,
	"wp-config.php": true, "master.key": true,
}

// secretFolders are folder names whose whole contents are secret, wherever
// they are in a path.
var secretFolders = map[string]bool{
	".git": true, ".ssh": true, ".aws": true, ".gnupg": true, ".kube": true, ".docker": true,
	".azure": true, ".terraform": true, ".gcloud": true, ".password-store": true, ".vault": true,
	".secrets": true, ".m2": true,
	"secrets": true, "secret": true, "private": true,
}

// pairFolders are two folders in a row ("a/b", lower-case) where neither alone
// says anything: .config/gh holds GitHub's token, .config/gcloud Google's.
var pairFolders = map[string]bool{
	".config/gh": true, ".config/gcloud": true, ".config/op": true, ".config/rclone": true,
}
