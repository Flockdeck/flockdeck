package workspace

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jmwri/flockdeck/internal/gitx"
)

// resolveClaudeLink follows a .claude that a commit holds as a link to a folder of the same
// commit, the way a checkout would, and says which tree it leads to. The target is taken exactly
// as it is written (one trailing newline is the only thing left off, and only from a single
// line) and is refused when an operating system could read it another way than a text
// reading would: an empty target, one that is absolute, that has a . or .. part, that starts or
// ends with a space of any kind, that holds a control character, a backslash, a colon or a NUL,
// or an empty part. Each part is then looked up in the tree of the part before it, and none may
// be a link: the path is followed through trees only, never through a link on the way. why is
// not empty when it cannot be followed, and the company is not known.
func resolveClaudeLink(repo, rev string, blob []byte) (treeSha, why string) {
	target := string(blob)
	if strings.HasSuffix(target, "\n") && strings.Count(target, "\n") == 1 {
		target = target[:len(target)-1]
	}
	refuse := func(reason string) (string, string) {
		return "", ".claude is a link whose target is not followed (" + reason + ")"
	}
	if target == "" || !utf8.ValidString(target) {
		return refuse("empty or not text")
	}
	if first, _ := utf8.DecodeRuneInString(target); unicode.IsSpace(first) {
		return refuse("it starts with a space")
	}
	if last, _ := utf8.DecodeLastRuneInString(target); unicode.IsSpace(last) {
		return refuse("it ends with a space")
	}
	for _, r := range target {
		if r < 0x20 || r == 0x7f || r == '\\' || r == ':' {
			return refuse("it holds a character that is read differently by different systems")
		}
	}
	if strings.HasPrefix(target, "/") {
		return refuse("it is absolute")
	}
	parts := strings.Split(strings.TrimSuffix(target, "/"), "/")
	for _, p := range parts {
		if p == "" || p == "." || p == ".." {
			return refuse("it has an empty, . or .. part")
		}
	}
	tree := rev
	for _, p := range parts {
		entries, err := gitx.TreeEntries(repo, tree)
		if err != nil {
			return "", ".claude is a link that cannot be followed: " + err.Error()
		}
		found := false
		for _, e := range entries {
			if e.Name != p {
				continue
			}
			if e.Mode == "120000" {
				return refuse(p + " is itself a link")
			}
			if e.Type != "tree" {
				return refuse(p + " is not a folder in the commit")
			}
			tree, found = e.Sha, true
			break
		}
		if !found {
			return refuse(p + " is not in the commit")
		}
	}
	return tree, ""
}
