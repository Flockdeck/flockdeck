package baton

import (
	"regexp"
	"strings"
)

// This file holds what decides that a value is code or a time and not a secret, in the
// tightest form that has been found to be safe: see passNameSpans, notAValue and callSpans.

// callRe is a value that is only a call that reads a secret from somewhere: it is the
// whole word, with at most closing brackets and a comma or semicolon after it, and the
// call has nothing but a name in it (quoted, or in capitals). os.Getenv("X") is one;
// os.Getenv("X")+"hunter2" and ENV.fetch("X", "hunter2") are not, because what follows may
// be the secret.
var callRe = regexp.MustCompile(`^(?:` +
	`(?:os\.Getenv|os\.getenv|System\.getenv|Getenv|getenv|env\.get|os\.environ\.get|ENV\.fetch|std::env::var|env)\((?:"[A-Za-z0-9_.\-]+"|'[A-Za-z0-9_.\-]+'|[A-Z][A-Z0-9_]*)\)` +
	`|(?:ENV|os\.environ|process\.env)\[(?:"[A-Za-z0-9_.\-]+"|'[A-Za-z0-9_.\-]+')\]` +
	`|process\.env\.[A-Za-z_][A-Za-z0-9_]*` +
	`|len\([A-Za-z_][A-Za-z0-9_.]*\)` +
	`)[)\]]*[,;]?$`)

// exactPlaceholder reports whether v (the rest of a line) is one call that reads the
// environment or one ERB tag that does, followed by nothing that could be a value: see
// tailOK.
func exactPlaceholder(v string) bool {
	if erbRe.MatchString(v) {
		return true
	}
	fw := v
	if k := strings.IndexAny(fw, " \t"); k >= 0 {
		fw = fw[:k]
	}
	if !callRe.MatchString(strings.Trim(fw, "\"'")) {
		return false
	}
	return tailOK(v[len(fw):])
}

// tailOK reports whether what follows a call on its line is nothing that could hold a
// secret: only white space, or a comma, semicolon or closing bracket (what ends an argument
// or a field; what comes after belongs to the next one), or a comment with no word in it
// that looks chosen. Anything else (+ "hunter2", .suffix, a word) is read as more of the
// value.
func tailOK(tail string) bool {
	tail = strings.TrimSpace(tail)
	if tail == "" || strings.IndexByte(",;)]}", tail[0]) >= 0 {
		return true
	}
	if !strings.HasPrefix(tail, "//") && !strings.HasPrefix(tail, "#") {
		return false
	}
	for _, w := range strings.Fields(tail) {
		w = strings.Trim(w, ".,;:!?()[]{}\"'`")
		if len(w) >= 8 && (secretWord(w) || mixedClasses(w)) {
			return false
		}
	}
	return true
}

const safeArg = `[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*(?:\[[A-Za-z0-9_:]*\])?(?:\.\.\.)?`

// safeCallRe is a call that is code, and not a password: the callee is a name or a dotted
// path (bytes.Clone, privateKey.Bytes, C._new) whose first part has no digit in it, and its
// arguments are plain names, with a slice or an ellipsis at most. A call whose arguments
// are numbers or text, or that has anything stuck to it, is not one.
var safeCallRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*\((?:` + safeArg + `(?:,[ \t]*` + safeArg + `)*)?\)`)

// sliceRe is a name indexed or sliced, as the start of the value: keyMaterial[:keyLen].
var sliceRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*\[(?:[A-Za-z_][A-Za-z0-9_]*)?:[A-Za-z0-9_]*\]`)

// safeCode reports whether the rest of a line, from the value, is only a call or a slice of
// the safe shapes (see safeCallRe, sliceRe) and then what tailOK allows. It is what is left
// of the code-like exemption: a value that is words or numbers in brackets is not code.
func safeCode(rest string) bool {
	rest = strings.TrimSpace(rest)
	if m := safeCallRe.FindString(rest); m != "" {
		return tailOK(rest[len(m):])
	}
	if m := sliceRe.FindString(rest); m != "" {
		return tailOK(rest[len(m):])
	}
	return false
}

// looksChosen reports whether a value reads as chosen and not written: letters of both
// cases or a digit, or letters with a symbol in them (ABCD.EFGH.IJKL). An underscore and a
// dash are in names (MY_API_KEY) and are not symbols here.
func looksChosen(v string) bool {
	if mixedClasses(v) {
		return true
	}
	letter, symbol := false, false
	for i := 0; i < len(v); i++ {
		switch c := v[i]; {
		case isLetter(c):
			letter = true
		case c >= '0' && c <= '9', c == '_', c == '-':
		default:
			symbol = true
		}
	}
	return letter && symbol
}

// ---- times as numbers under secret-looking names ----

// epochShape is a time as a number: ten digits that start with 1 (seconds, 2001 to 2033) or
// thirteen (milliseconds).
var epochShape = regexp.MustCompile(`^1[0-9]{9}$|^1[0-9]{12}$`)

// epochSecretWords is the rule for a name that has a secret word in it and ends as a time
// does (_at, _expires, _expiry, _date, _time): a number under it is kept only when it is
// one of the shapes here for every secret word the name has, and the name says what the
// time is of (expires, expiry, issued, created, updated). A word that is not here (pass,
// password, secret, api key, auth) never lets a number through: password_expires=1712345678
// is a password. token_expires_at=1700000000 and access_key_id_expires=1700000000 are times.
var epochSecretWords = map[string]*regexp.Regexp{
	"token":  epochShape,
	"key_id": epochShape,
}

// epochSaysWhat are the words that say a time is of something, for a name with a secret
// word in it.
var epochSaysWhat = map[string]bool{"expires": true, "expiry": true, "expire": true, "expiration": true, "issued": true, "created": true, "updated": true}

// secretNameWords are the words that make a name a secret's, split as keyWords does.
var secretNameWords = map[string]bool{
	"pass": true, "passwd": true, "password": true, "pwd": true, "pw": true, "secret": true, "token": true, "key": true,
	"credential": true, "credentials": true, "auth": true, "apikey": true, "passphrase": true,
}

// epochKept reports whether a number of 9 to 13 digits under a name that is a time (_at,
// expires, _date, _time) is a time and not a secret. Under a name that has no secret word
// in it, any such number is. Otherwise see epochSecretWords.
func epochKept(key, value string) bool {
	if !epochRe.MatchString(value) {
		return false
	}
	words := keyWords(strings.Trim(key, "-"))
	var secrets []string
	says := false
	for i, w := range words {
		if epochSaysWhat[w] {
			says = true
		}
		switch {
		case w == "key" && i+1 < len(words) && words[i+1] == "id":
			secrets = append(secrets, "key_id")
		case w == "id" && i > 0 && words[i-1] == "key":
		case secretNameWords[w]:
			secrets = append(secrets, w)
		}
	}
	if len(secrets) == 0 {
		return true
	}
	if !says {
		return false
	}
	for _, w := range secrets {
		shape, ok := epochSecretWords[w]
		if !ok || !shape.MatchString(value) {
			return false
		}
	}
	return true
}
