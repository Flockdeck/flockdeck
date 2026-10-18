package artifacts

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/jmwri/flockdeck/internal/secretname"
)

// The folders below a home directory that keep secrets (a browser's or a mail
// program's profile, a keychain, ~/.ssh) are not written out in this program:
// a program holding that list as text is what Windows Defender's
// machine-learning guess read as an information stealer. secretname keeps them
// as hashes (secretname.HomeSecret). The readable list is
// internal/secretname/testdata/names.txt, and rootpolicy_home_test.go holds the
// code this replaces to the same answers.
//
// The old code took each listed path, joined it to the home and asked whether
// the root was that folder or below it, by name and, where the folder exists,
// by identity (a link to it, another spelling of it). Without the names that
// is asked the other way round: the root's own path below the home is cut into
// its leading folders and each is looked up; and the real folders on the way
// to a listed path are read and each entry looked up, so one that exists is
// compared with the root by identity as before.

// maxHomeEntries bounds how many entries of one folder are looked at. No
// folder on the way to a listed path comes near it; a folder that does is
// refused, not skipped.
const maxHomeEntries = 200000

// secretUnderHome says why real may not be a root because it is, or is below, a
// folder under the home h that keeps secrets, or "" if it is not.
func secretUnderHome(h, real string) string {
	// By name: real, below h, begins with a listed path. Both are tried as
	// written and with their existing part resolved the same way, so a home
	// that exists and a root that does not still meet in one spelling.
	for _, pair := range [][2]string{
		{canonNear(h), canonNear(real)},
		{filepath.Clean(filepath.FromSlash(h)), filepath.Clean(filepath.FromSlash(real))},
	} {
		if why := secretByName(pair[0], pair[1]); why != "" {
			return why
		}
	}
	// By identity: a listed folder that exists, found by reading the folders
	// on the way to it, and compared with real as a folder.
	budget := maxHomeEntries
	return walkHome(h, "", real, &budget)
}

func secretByName(h, real string) string {
	rel, ok := under(h, real)
	if !ok {
		return ""
	}
	parts := strings.Split(rel, "/")
	for k := 1; k <= len(parts) && k <= secretname.MaxHomeDepth; k++ {
		sub := strings.Join(parts[:k], "/")
		if secretname.HomeSecret(sub, foldCase) {
			return keepsSecrets(sub)
		}
	}
	return ""
}

// canonNear is p with links and short names resolved as far as p exists: the
// longest existing ancestor is resolved and the missing rest is put back as
// written. Two paths into the same tree then share a prefix even when only
// one of them exists.
func canonNear(p string) string {
	p = filepath.Clean(filepath.FromSlash(p))
	rest := ""
	for q := p; ; {
		if c, err := canonical(q); err == nil {
			return filepath.Join(c, rest)
		}
		parent := filepath.Dir(q)
		if parent == q {
			return p
		}
		rest = filepath.Join(filepath.Base(q), rest)
		q = parent
	}
}

func keepsSecrets(sub string) string { return "a folder that keeps secrets (" + sub + ")" }

// walkHome reads the folder h/rel and returns why real may not be a root, if
// real is, or is below, an entry whose path from h is a listed one. It goes
// into the entries that are on the way to a listed path, and no others. budget
// counts the entries still allowed to be looked at, over the whole walk; a walk
// that runs out refuses the root.
func walkHome(h, rel, real string, budget *int) string {
	dir := h
	if rel != "" {
		dir = filepath.Join(h, filepath.FromSlash(rel))
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return "" // not there, or not readable: only the name can relate real to it
	}
	for _, e := range ents {
		if *budget--; *budget < 0 {
			return "a home folder with too many entries to look through for secrets"
		}
		sub := e.Name()
		if rel != "" {
			sub = rel + "/" + sub
		}
		if secretname.HomeSecret(sub, foldCase) && sameOrUnder(filepath.Join(dir, e.Name()), real) {
			return keepsSecrets(sub)
		}
		if !e.Type().IsRegular() && secretname.HomeSecretAbove(sub, foldCase) && !linksUp(dir, e) {
			if why := walkHome(h, sub, real, budget); why != "" {
				return why
			}
		}
	}
	return ""
}

// linksUp is whether the entry e of dir is a link to dir or to a folder above
// it. Following one would read the same folders again, so the walk does not.
func linksUp(dir string, e os.DirEntry) bool {
	if e.IsDir() && e.Type()&os.ModeIrregular == 0 {
		return false // a real folder
	}
	p := filepath.Join(dir, e.Name())
	target, err := canonical(p)
	if err != nil {
		return false // not a link that leads anywhere: ReadDir will say so
	}
	if target == canonNear(p) {
		return false // a regular folder spelled differently, not a link
	}
	here := canonNear(dir)
	if _, ok := under(target, here); ok {
		return true
	}
	return target == here || (foldCase && strings.EqualFold(target, here))
}
