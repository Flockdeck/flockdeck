package secretname

import (
	"crypto/sha256"
	"sort"
)

// The names this package judges by are kept as hashes, not as text. A table of
// credential and browser-store file names written out in a program is what some
// antivirus engines read as the program's purpose: Windows Defender's
// machine-learning guess flagged the Windows builds of v0.5.0 as an
// information stealer for this alone. A hash of a name does the same job for an
// exact lookup and says nothing to a scanner.
//
// The readable lists live in names_test.go. The tables in hashes_gen.go are
// made from them (go generate), and a test fails if the two disagree.

//go:generate go test -run TestHashTablesAreCurrent -update-hashes .

// domain starts every hashed text, so that a digest of a name here is not a
// digest of the same text anywhere else.
const domain = "flockdeck/secretname/v1\x00"

// kind says which table a digest belongs to, so a file name and a folder name
// that are the same text do not share an entry.
type kind byte

const (
	kindFile          kind = 'f' // a whole file name
	kindFolder        kind = 'd' // a whole folder name
	kindPair          kind = 'p' // "a/b", two folders in a row
	kindSuffix        kind = 's' // the end of a name
	kindPrefixAny     kind = 'a' // the start of a name
	kindPrefixPrivate kind = 'b' // the start of a name, unless it ends .pub
)

// digest is the SHA-256 of the domain, the kind and s.
func digest(k kind, s string) [sha256.Size]byte {
	buf := make([]byte, 0, len(domain)+1+len(s))
	buf = append(buf, domain...)
	buf = append(buf, byte(k))
	buf = append(buf, s...)
	return sha256.Sum256(buf)
}

func table(k kind) (tab string, lens []int) {
	switch k {
	case kindFile:
		return tabFile, nil
	case kindFolder:
		return tabFolder, nil
	case kindPair:
		return tabPair, nil
	case kindSuffix:
		return tabSuffix, lensSuffix
	case kindPrefixAny:
		return tabPrefixAny, lensPrefixAny
	case kindPrefixPrivate:
		return tabPrefixPrivate, lensPrefixPrivate
	}
	return "", nil
}

// find reports whether tab, a sorted run of 32-byte digests, holds d.
func find(tab string, d [sha256.Size]byte) bool {
	want := string(d[:])
	n := len(tab) / sha256.Size
	i := sort.Search(n, func(i int) bool { return tab[i*sha256.Size:(i+1)*sha256.Size] >= want })
	return i < n && tab[i*sha256.Size:(i+1)*sha256.Size] == want
}

// has reports whether s, whole, is in the table for k.
func has(k kind, s string) bool {
	tab, _ := table(k)
	return find(tab, digest(k, s))
}

func isSecretFolder(n string) bool { return has(kindFolder, n) }

func isPair(s string) bool { return has(kindPair, s) }

// hasPrefixIn reports whether n starts with a name in the table for k.
func hasPrefixIn(k kind, n string) bool {
	tab, lens := table(k)
	for _, l := range lens {
		if l <= len(n) && find(tab, digest(k, n[:l])) {
			return true
		}
	}
	return false
}

// hasSuffixIn reports whether n ends with a name in the suffix table.
func hasSuffixIn(n string) bool {
	tab, lens := table(kindSuffix)
	for _, l := range lens {
		if l <= len(n) && find(tab, digest(kindSuffix, n[len(n)-l:])) {
			return true
		}
	}
	return false
}
