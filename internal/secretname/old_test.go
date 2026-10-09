package secretname

import (
	"math/rand"
	"strings"
	"sync"
	"testing"
)

// This file keeps the first implementation of the package, which held its
// tables as plain maps and slices, and holds the hashed one to it. The tables
// are built from testdata/names.txt when the test runs, so this file, like the
// shipped program, holds no list of names as text.

type oldTables struct {
	files, folders, pairs map[string]bool
	generic               map[string]bool
	exts                  []string
	prefixAny, prefixPriv []string
}

var (
	oldOnce sync.Once
	oldT    oldTables
)

func oldTabs(t testing.TB) *oldTables {
	l := lists(t)
	oldOnce.Do(func() {
		set := func(s []string) map[string]bool {
			m := map[string]bool{}
			for _, x := range s {
				m[x] = true
			}
			return m
		}
		oldT.files, oldT.folders, oldT.pairs = set(l.files), set(l.folders), set(l.pairs)
		oldT.generic = map[string]bool{"private": true, "secret": true, "secrets": true}
		for k := range oldT.generic {
			oldT.folders[k] = true
		}
		oldT.exts, oldT.prefixAny, oldT.prefixPriv = l.suffixes, l.prefixAny, l.prefixPrivate
	})
	return &oldT
}

func oldComponent(t testing.TB, name string) bool {
	if len(name) > MaxName {
		return true
	}
	n := fold(name)
	if oldHit(t, n) {
		return true
	}
	i := strings.IndexByte(n, '=')
	if i < 0 {
		return false
	}
	for rest := n[i+1:]; ; {
		if oldHit(t, rest) {
			return true
		}
		j := strings.IndexByte(rest, 0x3d)
		if j < 0 {
			break
		}
		rest = rest[j+1:]
	}
	for _, seg := range strings.Split(n, "=") {
		if seg != "" && oldHit(t, seg) {
			return true
		}
	}
	return false
}

func oldHit(t testing.TB, n string) bool { return oldMatch(t, n) || oldMatch(t, normalise(n)) }

func oldFolder(t testing.TB, name string) bool {
	return oldTabs(t).folders[normalise(fold(name))]
}

func oldStrictFolder(t testing.TB, name string) bool {
	n := normalise(fold(name))
	return oldTabs(t).folders[n] && !oldTabs(t).generic[n]
}

func oldFolderPair(t testing.TB, a, b string) bool { return oldTabs(t).pairs[fold(a)+"/"+fold(b)] }

func oldPath(t testing.TB, rel string) bool {
	for _, c := range strings.FieldsFunc(rel, func(r rune) bool { return r == '/' || r == '\\' }) {
		if oldComponent(t, c) {
			return true
		}
	}
	parts := strings.FieldsFunc(fold(rel), func(r rune) bool { return r == '/' || r == '\\' })
	for i := 0; i+1 < len(parts); i++ {
		if oldTabs(t).pairs[parts[i]+"/"+parts[i+1]] {
			return true
		}
	}
	return false
}

func oldMatch(t testing.TB, n string) bool {
	tab := oldTabs(t)
	if n == "" {
		return false
	}
	if tab.folders[n] {
		return true
	}
	if tab.files[n] {
		return true
	}
	hasAny := func(list []string) bool {
		for _, p := range list {
			if strings.HasPrefix(n, p) {
				return true
			}
		}
		return false
	}
	switch {
	case strings.HasPrefix(n, ".env."), strings.HasSuffix(n, ".env"), strings.Contains(n, ".env."):
		return true
	case hasAny(tab.prefixAny):
		return true
	case hasAny(tab.prefixPriv) && !strings.HasSuffix(n, ".pub"):
		return true
	case strings.Contains(n, "credential"):
		return true
	case strings.Contains(n, ".tfstate"):
		return true
	case strings.HasPrefix(n, ".env"):
		return true
	}
	for _, ext := range tab.exts {
		if strings.HasSuffix(n, ext) {
			return true
		}
	}
	toks := tokens(n)
	for i, tok := range toks {
		if secretWords[tok] {
			return true
		}
		if i+1 < len(toks) && secretWords[tok+toks[i+1]] {
			return true
		}
	}
	return false
}

// equivalenceInputs are names built from every listed name: as written, in
// capitals, with unicode look-alikes for its letters, with trailing dots,
// spaces and backup suffixes, behind a leading "-", inside "=" forms, with a
// ".pub" or other tail, as a folder, under a parent, and cut at every length.
func equivalenceInputs(t testing.TB) []string {
	l := lists(t)
	var all []string
	for _, s := range [][]string{l.files, l.folders, l.pairs, l.suffixes, l.prefixAny, l.prefixPrivate} {
		all = append(all, s...)
	}
	all = append(all, "private", "secret", "secrets", ".env", "credential", "x.tfstate", "token", "plain.txt", "")
	lookalike := map[rune]string{'s': "ſ", 'k': "K", 'i': "İ", 'f': "ﬁ"}
	var out []string
	add := func(s string) { out = append(out, s) }
	for _, n := range all {
		add(n)
		add(strings.ToUpper(n))
		add(strings.Title(n)) //nolint:staticcheck // mixed case on purpose
		var b strings.Builder
		changed := false
		for _, r := range n {
			if x, ok := lookalike[r]; ok && !changed {
				b.WriteString(x)
				changed = true
			} else {
				b.WriteRune(r)
			}
		}
		add(b.String())
		add(strings.ReplaceAll(n, "s", "ſ"))
		add(n[:len(n)/2] + "‍" + n[len(n)/2:])
		for _, tail := range []string{".", "..", " ", ". .", "#", "=", ".pub", ".PUB", ".txt", "x", "-", "_", ".json"} {
			add(n + tail)
			add("x" + n + tail)
		}
		for _, bk := range backupSuffixes {
			add(n + bk)
			add(n + bk + bk)
			add(n + bk + ".")
			add(n + "." + bk)
		}
		for _, head := range []string{"-", "--", "--file=", "-o=", "x=", "=", "a=b=", "x/", "a\\", "x ", "."} {
			add(head + n)
			add(head + n + ".bak")
			add(n + "=" + head + n)
		}
		add("a=" + n + "=b")
		add(n + "/" + n)
		add(".config/" + n)
		add("a/.config/" + n + "/x")
		for i := 0; i <= len(n); i++ {
			add(n[:i])
			add(n[i:])
		}
	}
	// Every pair of listed folders and files, as a path.
	for _, a := range l.pairs {
		p := strings.SplitN(a, "/", 2)
		add(p[0] + "/" + p[1])
		add(strings.ToUpper(p[0]) + "\\" + strings.ToUpper(p[1]))
		add("x/" + p[0] + "/" + p[1] + "/y")
		add(p[0] + "/x/" + p[1])
	}
	return out
}

func sameAnswers(t testing.TB, s string) {
	t.Helper()
	if g, w := Component(s), oldComponent(t, s); g != w {
		t.Fatalf("Component(%q) = %v, was %v", s, g, w)
	}
	if g, w := Folder(s), oldFolder(t, s); g != w {
		t.Fatalf("Folder(%q) = %v, was %v", s, g, w)
	}
	if g, w := StrictFolder(s), oldStrictFolder(t, s); g != w {
		t.Fatalf("StrictFolder(%q) = %v, was %v", s, g, w)
	}
	if g, w := Path(s), oldPath(t, s); g != w {
		t.Fatalf("Path(%q) = %v, was %v", s, g, w)
	}
	if i := strings.IndexAny(s, "/\\"); i >= 0 {
		a, b := s[:i], s[i+1:]
		if g, w := FolderPair(a, b), oldFolderPair(t, a, b); g != w {
			t.Fatalf("FolderPair(%q, %q) = %v, was %v", a, b, g, w)
		}
	}
}

// Threat: the move from readable tables to hashes changes an answer, so a
// secret file is shown or a plain one refused. Every listed name, in the forms
// the matching is built to see through, gets the same answer from both.
func TestHashedTablesAnswerAsTheTablesDid(t *testing.T) {
	in := equivalenceInputs(t)
	hits := 0
	for _, s := range in {
		sameAnswers(t, s)
		if Component(s) {
			hits++
		}
	}
	if hits < 500 {
		t.Fatalf("only %d of %d inputs are secret: the inputs are not exercising the tables", hits, len(in))
	}
	l := lists(t)
	for _, a := range l.pairs {
		p := strings.SplitN(a, "/", 2)
		for _, c := range [][2]string{{p[0], p[1]}, {strings.ToUpper(p[0]), strings.ToUpper(p[1])}, {p[1], p[0]}, {p[0], "x"}, {"x", p[1]}} {
			if g, w := FolderPair(c[0], c[1]), oldFolderPair(t, c[0], c[1]); g != w {
				t.Fatalf("FolderPair(%q, %q) = %v, was %v", c[0], c[1], g, w)
			}
		}
		if !FolderPair(p[0], p[1]) {
			t.Errorf("FolderPair(%q, %q) = false", p[0], p[1])
		}
	}
	// Every listed name is secret in the form it was listed.
	for _, n := range l.files {
		if !Component(n) {
			t.Errorf("Component(%q) = false", n)
		}
	}
	for _, n := range l.folders {
		if !Folder(n) || !StrictFolder(n) || !Component(n) {
			t.Errorf("folder %q is not judged secret", n)
		}
	}
	if Folder("x") || StrictFolder("private") || !Folder("private") {
		t.Error("generic folders: Folder(private) must be true and StrictFolder(private) false")
	}
}

// Random splices of listed names and punctuation, on top of the fixed forms.
func TestHashedTablesAnswerAsTheTablesDidRandom(t *testing.T) {
	l := lists(t)
	var pool []string
	for _, s := range [][]string{l.files, l.folders, l.suffixes, l.prefixAny, l.prefixPrivate} {
		pool = append(pool, s...)
	}
	glue := []string{"", "=", "/", "\\", ".", " ", "-", "_", ".bak", "~", ".pub", "ß", "ſ", "‍", "X", "1"}
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 20000; i++ {
		var b strings.Builder
		for j, n := 0, 1+rng.Intn(3); j < n; j++ {
			b.WriteString(glue[rng.Intn(len(glue))])
			w := pool[rng.Intn(len(pool))]
			if rng.Intn(3) == 0 {
				w = w[rng.Intn(len(w)+1):]
			}
			b.WriteString(w)
		}
		b.WriteString(glue[rng.Intn(len(glue))])
		sameAnswers(t, b.String())
	}
}

func FuzzHashedTablesAnswerAsTheTablesDid(f *testing.F) {
	// The seed corpus is every form above. The fuzz target has no *testing.T
	// before the first call, so the lists are read inside.
	f.Add("cookies.sqlite")
	f.Add("--file=Login Data.bak")
	f.Add(".config/gh/hosts.yml")
	f.Add("ID_RSA.pub")
	f.Add("Key4.db")
	f.Add("a=.mozilla=b")
	f.Add(".SSH . ")
	f.Add("ssh_host_rsa_key")
	f.Add("x.KDBX~")
	f.Fuzz(func(t *testing.T, s string) {
		if len(s) > 2*MaxName {
			return
		}
		sameAnswers(t, s)
	})
}
