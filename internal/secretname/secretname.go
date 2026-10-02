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
// "-prod.pem" or "key.pem=" is a perfectly good file name.
//
// Everything review.SecretPath treats as secret is secret here too, and a good
// deal more, including the argv forms it understands: a name is also judged by
// the text after each "=" in it ("x=.env.local", "--file=.env.local",
// "-o=.env.local.bak"), at the price of refusing a real file whose name has an
// "=" before a secret-looking tail. A fuzz test holds the two to this for any
// input. That is a property of the matching, not a reason to feed this package
// an argument list: words of a command line are not paths, and a caller that
// has one should use review.SecretPath on it.
//
// Case is folded the way file systems fold it, not only the way ASCII does:
// every rune whose case-folding orbit contains an ASCII letter is read as that
// letter (U+017F "long s" is "s", U+212A Kelvin is "k", U+0130 is "i"), and
// the ligatures and sharp s that expand to ASCII letters are expanded. A name
// that a case-insensitive file system (APFS, ext4 with casefold, vfat, NTFS,
// SMB) would resolve to a secret is therefore usually judged as that secret,
// and default-ignorable code points (zero-width joiners and the like) are
// dropped. That is defence in depth, not the guarantee: no table here can know
// every file system's folding. The guarantee a caller that opens files has to
// provide is to confirm that a name with non-ASCII letters is a real directory
// entry byte for byte (internal/artifacts does, on Unix, and on Windows reads
// the real name back from the open handle).
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
//
// A name longer than MaxName bytes cannot be a file's name on any file system
// this is used with; it is called secret, so that hostile input costs nothing.
// A name with "=" in it is judged whole, then by all that is after its first
// "=", then segment by segment, each text between two "=" on its own, which is how an argument carries a file name
// ("--file=.env.local"). The name is capped, so the work is bounded (about
// MaxName squared byte steps in the worst case, a fraction of a millisecond).
func Component(name string) bool {
	if len(name) > MaxName {
		return true
	}
	n := fold(name)
	if hit(n) {
		return true
	}
	i := strings.IndexByte(n, '=')
	if i < 0 {
		return false
	}
	// All that is after each "=" (the first is what an argument parser hands on)
	// and each text between two "=" on its own. With the name capped at MaxName
	// bytes the work is bounded by MaxName squared, a small constant.
	for rest := n[i+1:]; ; {
		if hit(rest) {
			return true
		}
		j := strings.IndexByte(rest, 0x3d)
		if j < 0 {
			break
		}
		rest = rest[j+1:]
	}
	for _, seg := range strings.Split(n, "=") {
		if seg != "" && hit(seg) {
			return true
		}
	}
	return false
}

// hit is match on a folded name as written and cleaned of what a check should
// not be fooled by (the second can only find more).
func hit(n string) bool { return match(n) || match(normalise(n)) }

// Folder reports whether name, as a folder, is one whose whole contents are
// secret (.ssh, .aws, .git, secrets, private ...).
func Folder(name string) bool { return secretFolders[normalise(fold(name))] }

// StrictFolder is Folder without the plain words that are also ordinary folder
// names ("private", "secret", "secrets"): for judging the folders above a place
// where those are common and harmless (/private/var on macOS, ~/repos/private).
func StrictFolder(name string) bool {
	n := normalise(fold(name))
	return secretFolders[n] && !genericFolders[n]
}

// FolderPair reports whether a then b, two folders in a row, are one of the
// pairs that hold secrets (.config/gh).
func FolderPair(a, b string) bool { return pairFolders[fold(a)+"/"+fold(b)] }

var genericFolders = map[string]bool{"private": true, "secret": true, "secrets": true}

// MaxName is the longest component Component will look at (the longest name any
// common file system allows).
const MaxName = 255

// fold lower-cases a name the way file systems that ignore case compare it,
// for every letter a secret's name is written in: a rune whose case-folding
// orbit contains an ASCII letter becomes that letter, and the runes that expand
// to ASCII letters ("ß" is "ss", the "ﬁ" ligature "fi") are expanded. Any other
// rune is lower-cased exactly as strings.ToLower does, so that nothing
// review.SecretPath, which lower-cases that way, finds is lost.
func fold(s string) string {
	plain := true
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			plain = false
			break
		}
	}
	if plain {
		return strings.ToLower(s)
	}
	var b strings.Builder
	for _, r := range s {
		if ignorable(r) {
			continue
		}
		if x, ok := expansions[r]; ok {
			b.WriteString(x)
			continue
		}
		b.WriteRune(asciiFold(r))
	}
	return b.String()
}

// ignorable is whether r is a code point that shows nothing and that some file
// systems (ext4 casefold) ignore in a name: format characters (zero-width
// joiners, bidi marks, the soft hyphen) and variation selectors.
func ignorable(r rune) bool {
	return unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Variation_Selector, r) || r == 0x034f || r == 0x115f || r == 0x1160 || r == 0x3164 || r == 0xffa0
}

// asciiFold is r lower-cased, or the ASCII letter r folds to.
func asciiFold(r rune) rune {
	l := unicode.ToLower(r)
	if l < 0x80 || r < 0x80 {
		return l
	}
	for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
		if f < 0x80 && (f|0x20 >= 'a' && f|0x20 <= 'z') {
			return unicode.ToLower(f)
		}
	}
	return l
}

// expansions are the runes whose full case folding is several ASCII letters.
var expansions = map[rune]string{
	'ß': "ss", 'ẞ': "ss", 'ﬀ': "ff", 'ﬁ': "fi", 'ﬂ': "fl", 'ﬃ': "ffi", 'ﬄ': "ffl", 'ﬅ': "st", 'ﬆ': "st",
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
	case strings.HasPrefix(n, ".env."), strings.HasSuffix(n, ".env"), strings.Contains(n, ".env."):
		return true // .env.local, prod.env, prod.env.json
	case strings.HasPrefix(n, "kubeconfig"), strings.HasPrefix(n, "login data"), strings.HasPrefix(n, "firebase-adminsdk"):
		return true // kubeconfig.yaml, kubeconfig-prod; a browser's "Login Data"
	case strings.HasPrefix(n, "ssh_host_") && !strings.HasSuffix(n, ".pub"), strings.HasPrefix(n, ".authinfo"), strings.HasPrefix(n, "ntuser.dat"):
		return true // a host's private key; ssh_host_rsa_key.pub is public
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
	toks := tokens(n)
	for i, tok := range toks {
		if secretWords[tok] {
			return true
		}
		// Two words that are one name: api_key, private-key, "access key".
		if i+1 < len(toks) && secretWords[tok+toks[i+1]] {
			return true
		}
	}
	return false
}

// backupSuffixes are what an editor or a person adds to a copy: key.pem.bak is
// key.pem.
var backupSuffixes = []string{".bak", ".old", ".orig", ".save", ".backup", ".tmp", ".swp", ".copy", "~"}

// normalise takes a name already folded (see fold), drops what Windows would drop (trailing dots
// and spaces) and other trailing punctuation a name check should not be fooled
// by ("key.pem=", "key.pem#"), and takes off backup suffixes.
func normalise(name string) string {
	// A leading "-" is no part of a file name's meaning ("-id_rsa" is id_rsa
	// to anything that was handed it as an argument), and a name check is
	// fooled by it the way an argument parser is.
	n := strings.TrimLeft(name, "-")
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
	parts := strings.FieldsFunc(fold(rel), func(r rune) bool { return r == '/' || r == '\\' })
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
//
// "token" is a word, so token.json and .gh_token are secret, and so is token.go
// (a false refusal, accepted); "tokens" is not, so design-tokens.json is not.
var secretWords = map[string]bool{
	"secret": true, "secrets": true, "password": true, "passwords": true, "passwd": true,
	"token": true, "apikey": true, "privatekey": true, "secretkey": true, "accesskey": true,
}

// secretExtensions end a name that holds key material or a protected store.
var secretExtensions = []string{
	".pem", ".key", ".p12", ".pfx", ".ppk", ".p8", ".keystore", ".jks", ".jceks", ".pkcs12",
	".tfvars", ".tfvars.json", ".kdbx", ".kdb", ".gpg", ".keychain", ".keychain-db", ".ovpn", ".keytab",
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
	".my.cnf": true, ".dev.vars": true, "gradle.properties": true, ".terraformrc": true, "terraform.rc": true,
	"auth.json": true, "kube.config": true, ".gitconfig": true, "known_hosts": true,
	"cookies.sqlite": true, "cookies": true, "logins.json": true, "key4.db": true,
	".mylogin.cnf": true, ".rails_master_key": true, ".msmtprc": true, "rclone.conf": true,
	"serviceaccountkey.json": true,
	"web data":               true, "local state": true, "oauth_creds.json": true, "shadow": true, "htpasswd": true, "sam": true,
	"ntds.dit": true, "key.json": true, "azureprofile.json": true, ".vault_pass": true, ".vault-pass": true, "authorized_keys": true, "wallet.dat": true,
}

// secretFolders are folder names whose whole contents are secret, wherever
// they are in a path.
var secretFolders = map[string]bool{
	".git": true, ".ssh": true, ".aws": true, ".gnupg": true, ".kube": true, ".docker": true,
	".azure": true, ".terraform": true, ".gcloud": true, ".password-store": true, ".vault": true,
	".secrets": true, ".m2": true, "user data": true, ".mozilla": true, ".thunderbird": true, "keyrings": true,
	"secrets": true, "secret": true, "private": true,
}

// pairFolders are two folders in a row ("a/b", lower-case) where neither alone
// says anything: .config/gh holds GitHub's token, .config/gcloud Google's.
var pairFolders = map[string]bool{
	".config/gh": true, ".config/gcloud": true, ".config/op": true, ".config/rclone": true,
}
