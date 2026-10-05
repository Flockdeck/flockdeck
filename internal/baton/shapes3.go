package baton

import (
	"regexp"
	"strings"

	"github.com/jmwri/flockdeck/internal/record"
)

// This file holds more command-line tools that take a
// secret as an argument (see toolFlags, which dbFlags hands the command to), names that
// end in pass, pwd, pw or key with a random-looking value, and the exact forms of the
// values that are not secrets (a call that reads the environment, an ERB tag, a number
// that is a time).

// ---- exact placeholders ----

// erbRe is an ERB tag that reads the environment and is the whole of the value.
var erbRe = regexp.MustCompile(`^<%=?[ \t]*(?:ENV\[(?:"[A-Za-z0-9_]+"|'[A-Za-z0-9_]+')\]|ENV\.fetch\((?:"[A-Za-z0-9_]+"|'[A-Za-z0-9_]+')\))[ \t]*-?%>$`)

// maskRe is a value that is only a mask: eight or more of one of the characters a mask
// is made of. An x is not one: xxxxxxxx is a password as often as a mask.
var maskRe = regexp.MustCompile(`^(?:\*{8,}|•{8,}|#{8,})$`)

// epochRe is a time as a number of seconds or milliseconds.
var epochRe = regexp.MustCompile(`^[0-9]{9,13}$`)

// quantityPair is a name=number pair of a line that reports amounts.
var quantityPair = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_.\-]*)=[0-9]{1,9}(?:\.[0-9]+)?(?:[ \t]+|$)`)

// quantityPairs reports whether s is only name=number pairs after a first number, all of
// the names quantities (input_tokens=1200 output_tokens=340): the value of the first name
// was taken for the whole of the rest of the line.
func quantityPairs(s string) (ok, leadingNumber bool) {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	rest := s
	if i > 0 {
		if i >= len(s) || (s[i] != ' ' && s[i] != '\t') {
			return false, true
		}
		rest = strings.TrimLeft(s[i:], " \t")
	}
	if rest == "" {
		return false, i > 0
	}
	used := 0
	for _, m := range quantityPair.FindAllStringSubmatchIndex(rest, -1) {
		if m[0] != used || !quantityKey(rest[m[2]:m[3]]) {
			return false, i > 0
		}
		used = m[1]
	}
	return used == len(rest), i > 0
}

// callTagRe is a secret's name and a value that starts as a call that reads the
// environment, or as an ERB tag, and runs to the end of the line.
var callTagRe = regexp.MustCompile(`(?m)(?:^|[\s,;{("'])["']?([A-Za-z_][A-Za-z0-9_.\-]*)["']?[ \t]*[:=][ \t]*((?:os\.Getenv|os\.getenv|System\.getenv|Getenv|getenv|env\.get|os\.environ|ENV|process\.env|std::env::var|len|<%)[^\n]*)`)

// callSpans takes the whole of the rest of the line under a secret's name when what
// it starts as is a call that reads the environment but is more than that: the call
// and then something else (+ "hunter2", or "hunter2" as the default of ENV.fetch), or an
// ERB tag that is not an environment read (<%= "hunter2" %>). record stops at the first
// quote or space, which leaves the rest of it in the text. The call or the tag alone is
// a placeholder, and is not taken (notAValue says so).
func callSpans(text string) []span {
	var out []span
	if !strings.Contains(text, "env") && !strings.Contains(text, "ENV") && !strings.Contains(text, "<%") && !strings.Contains(text, "etenv") && !strings.Contains(text, "len(") {
		return nil
	}
	for _, m := range callTagRe.FindAllStringSubmatchIndex(text, -1) {
		name := strings.ToLower(text[m[2]:m[3]])
		if !(record.SecretName(name) && endsInSecretWord(name)) && !passFamilyName(name) {
			continue
		}
		a, b := m[4], m[5]
		for b > a && strings.IndexByte(" \t\r", text[b-1]) >= 0 {
			b--
		}
		v := text[a:b]
		if exactPlaceholder(v) || strings.HasPrefix(v, "[REDACTED: ") {
			continue
		}
		out = append(out, span{a, b, "secret-value"})
	}
	return out
}

// ---- export and setx names in lower case ----

// lowerNameOK reports whether a command that sets a variable with a name that has lower
// case letters in it is read as setting a secret. setx takes its name and value
// as two words, so the name is read. export NAME value exports two variables, and is not
// an assignment: it is read only when the value is one word that looks chosen, which is
// what somebody who meant an assignment wrote.
func lowerNameOK(cmd, value string) bool {
	if cmd == "setx" {
		return true
	}
	v := strings.Trim(strings.TrimSpace(value), `"'`)
	return v != "" && !strings.ContainsAny(v, " \t") && len(v) >= 8 && mixedClasses(v) && !identLike(v)
}

// ---- names that end in pass, pwd, pw or key ----

// passNameRe is a name that ends in one of passSuffixes (so a name that does not, like
// https in front of a URL, does not take in the one inside the query) followed by : or =
// and one word, in the places a configuration
// puts one: the start of a line, after a space, a comma, a semicolon, a brace, a quote,
// or ? or &.
var passNameRe = regexp.MustCompile(`(?m)(?:^|[\s,;{?&"'(])["']?([A-Za-z_][A-Za-z0-9_.\-]*(?i:pass|passwd|pwd|pw|key))["']?[ \t]*[:=][ \t]*("[^"\n]*"|'[^'\n]*'|[^\s"'&,;]+)`)

// passSuffixes are what a name ends in to be read here; the longer secret words are
// kvSpans' and record's.
var passSuffixes = []string{"pass", "passwd", "pwd", "pw", "key"}

// notKeySuffixes are names that end in key and are not a secret: a public key, a cache
// key, a sort key, a primary key.
var notKeySuffixes = []string{"public_key", "publickey", "pubkey", "cache_key", "cachekey", "primary_key", "foreign_key", "sort_key", "partition_key", "hash_key", "range_key", "routing_key", "object_key", "idempotency_key", "lookup_key", "shard_key", "unique_key", "group_key", "map_key", "i18n_key", "translation_key", "storage_key", "localstorage_key", "sessionstorage_key"}

// passNameSpans finds the value of a name that ends in pass, passwd, pwd, pw or key
// when it looks chosen at random: at least 12 characters, with letters of both cases or
// digits, and not a plain identifier, a name, a path or a placeholder. A name that says
// only that it holds a password (db_password) is kvSpans' and record's, which take any
// value.
func passNameSpans(text string) []span {
	var out []span
	for _, m := range passMatches(text) {
		name := strings.ToLower(strings.ReplaceAll(text[m[2]:m[3]], "-", "_"))
		ok := false
		for _, s := range passSuffixes {
			if strings.HasSuffix(name, s) {
				ok = true
				break
			}
		}
		if !ok || record.SecretName(name) && endsInSecretWord(name) {
			continue
		}
		skip := false
		for _, s := range notKeySuffixes {
			if strings.HasSuffix(name, s) {
				skip = true
			}
		}
		if skip || quantityKey(name) {
			continue
		}
		a, b := m[4], m[5]
		if b-a >= 2 && (text[a] == '"' || text[a] == '\'') && text[b-1] == text[a] {
			a, b = a+1, b-1
		}
		v := text[a:b]
		if strings.HasPrefix(v, "[REDACTED: ") || placeholderAt(v) || startsAsPath(v) || strings.Contains(v, "://") || isoDate.MatchString(v) || !looksChosen(v) || strings.HasSuffix(v, "\\") && followedByText(text, b) {
			continue
		}
		// (A value that ends in a backslash is wrapped on to the next line, which the rules
		// for a wrapped value take, with the kind of what it is.)
		// Under these names nothing is left alone for looking like code, which is what a
		// password with brackets or dots in it looks like. What is left alone is only what
		// reads an environment variable by name, a call or a slice of the safe shapes, and a
		// value that is too short to be chosen.
		// Only the next lookAhead bytes of the line are read, and a value whose line goes on
		// past that is not left alone: the rest of a long line is never scanned for each
		// match, which made a long line of many of them quadratic.
		if win, ok := lookAheadWindow(text, a); ok && (exactPlaceholder(win) || safeCode(win)) {
			continue
		}
		// A name that ends in key holds identifiers as often as keys, so it needs more.
		least := 8
		if strings.HasSuffix(name, "key") {
			least = 12
		}
		if len(v) < least {
			continue
		}
		out = append(out, span{a, b, "secret-value"})
	}
	return out
}

// ---- more command-line tools ----

// inner is a word without the quotes that wrap the whole of it.
func inner(text string, w word) (int, int) {
	a, b := w.a, w.b
	if b-a >= 2 && (text[a] == '"' || text[a] == '\'') && text[b-1] == text[a] {
		return a + 1, b - 1
	}
	return a, b
}

// toolFlags reads the words of the commands that take a secret as an argument and are
// not the database clients of dbFlags: vault login, az login -p, doctl -t, zip and
// unzip -P, gpg --passphrase, 7z -p, aws configure set, httpie's -a, and the secret that
// is piped into docker login --password-stdin. It reports whether it handled cmd.
func toolFlags(text string, words []word, cmd string, start int) (out []span, handled bool) {
	add := func(a, b int) {
		if b > a && !strings.HasPrefix(text[a:b], "[REDACTED: ") && !placeholderAt(text[a:b]) {
			out = append(out, span{a, b, "secret-flag"})
		}
	}
	// the word after words[i] when it is a value and not another flag
	valueAfter := func(i int) (word, bool) {
		if i+1 < len(words) {
			n := text[words[i+1].a:words[i+1].b]
			if !strings.HasPrefix(n, "-") && !redirect(n) {
				return words[i+1], true
			}
		}
		return word{}, false
	}
	// the value of a long or short flag: attached with = or after a space
	flagValue := func(i int, names ...string) (word, bool) {
		t := text[words[i].a:words[i].b]
		for _, n := range names {
			if t == n {
				return valueAfter(i)
			}
			if strings.HasPrefix(t, n+"=") && len(t) > len(n)+1 {
				return word{words[i].a + len(n) + 1, words[i].b}, true
			}
		}
		return word{}, false
	}
	switch cmd {
	case "vault login":
		// vault login TOKEN: the first word that is not a flag or a key=value pair.
		for i, w := range words {
			t := text[w.a:w.b]
			if strings.HasPrefix(t, "-") || strings.Contains(t, "=") || redirect(t) {
				continue
			}
			_ = i
			a, b := inner(text, w)
			add(a, b)
			break
		}
		return out, true
	case "az login":
		for i := range words {
			if v, ok := flagValue(i, "-p", "--password"); ok {
				a, b := inner(text, v)
				add(a, b)
			}
		}
		return out, true
	case "doctl":
		for i := range words {
			if v, ok := flagValue(i, "-t", "--access-token"); ok {
				a, b := inner(text, v)
				add(a, b)
			}
		}
		return out, true
	case "unzip", "zip", "zipcloak":
		for i, w := range words {
			t := text[w.a:w.b]
			if t == "-P" {
				if v, ok := valueAfter(i); ok {
					a, b := inner(text, v)
					add(a, b)
				}
			} else if strings.HasPrefix(t, "-P") && len(t) > 2 && !strings.HasPrefix(t, "--") {
				add(w.a+2, w.b)
			}
		}
		return out, true
	case "gpg", "gpg2":
		for i := range words {
			if v, ok := flagValue(i, "--passphrase"); ok {
				a, b := inner(text, v)
				add(a, b)
			}
		}
		return out, true
	case "7z", "7za", "7zr", "7zz":
		for _, w := range words {
			t := text[w.a:w.b]
			if strings.HasPrefix(t, "-p") && len(t) > 2 {
				a, b := inner(text, word{w.a + 2, w.b})
				add(a, b)
			}
		}
		return out, true
	case "aws configure set":
		var pos []word
		for i, w := range words {
			t := text[w.a:w.b]
			if strings.HasPrefix(t, "-") {
				continue
			}
			if i > 0 && strings.HasPrefix(text[words[i-1].a:words[i-1].b], "--") && !strings.Contains(text[words[i-1].a:words[i-1].b], "=") {
				continue // --profile NAME
			}
			pos = append(pos, w)
		}
		if len(pos) >= 2 {
			switch strings.ToLower(text[pos[0].a:pos[0].b]) {
			case "aws_secret_access_key", "aws_session_token", "secret_access_key", "session_token":
				a, b := inner(text, pos[1])
				add(a, b)
			}
		}
		return out, true
	case "http", "https", "xh", "xhs":
		for i := range words {
			v, ok := flagValue(i, "-a", "--auth")
			if !ok {
				continue
			}
			a, b := inner(text, v)
			if k := colonOutsideMarks(text[a:b]); k >= 0 {
				add(a+k+1, b)
			} else if secretWord(text[a:b]) {
				add(a, b)
			}
		}
		return out, true
	case "docker login", "podman login", "helm registry login":
		stdin := false
		for _, w := range words {
			if text[w.a:w.b] == "--password-stdin" {
				stdin = true
			}
		}
		if !stdin {
			return nil, false
		}
		for i, w := range words {
			t := text[w.a:w.b]
			if t == "<<<" && i+1 < len(words) {
				a, b := inner(text, words[i+1])
				add(a, b)
			} else if strings.HasPrefix(t, "<<<") && len(t) > 3 {
				a, b := inner(text, word{w.a + 3, w.b})
				add(a, b)
			}
		}
		if a, b, ok := pipedSecret(text, start); ok {
			add(a, b)
		}
		// The -p and -u flags are read by dbFlags as for any other docker login.
		return out, false
	}
	return nil, false
}

// pipedSecret finds the word that echo or printf writes into a pipe that ends at the
// command starting at cmdStart: echo SECRET | docker login --password-stdin. Only the
// same line is looked at, and a value that is a variable or a command is not the secret.
func pipedSecret(text string, cmdStart int) (int, int, bool) {
	// Only the 4 KiB before the command are looked at, so a long line of many commands
	// is not read back to its start for each of them.
	from0 := max(0, cmdStart-4096)
	ls := from0 + strings.LastIndexByte(text[from0:cmdStart], '\n') + 1
	seg := strings.TrimRight(text[ls:cmdStart], " \t")
	if !strings.HasSuffix(seg, "|") || strings.HasSuffix(seg, "||") {
		return 0, 0, false
	}
	seg = strings.TrimRight(strings.TrimSuffix(seg, "|"), " \t")
	if seg == "" {
		return 0, 0, false
	}
	// The command that writes starts after the last separator; quotes that hold one are
	// not read, so a secret with a ; in it is missed here, and found by what it looks like.
	from := 0
	for i := len(seg) - 1; i >= 0; i-- {
		if c := seg[i]; c == ';' || c == '(' || c == '&' || c == '|' {
			from = i + 1
			break
		}
	}
	words := commandWords(text, ls+from, ls+len(seg))
	for len(words) > 0 && strings.TrimSpace(text[words[0].a:words[0].b]) == "" {
		words = words[1:]
	}
	if len(words) < 2 {
		return 0, 0, false
	}
	switch text[words[0].a:words[0].b] {
	case "echo":
	case "printf":
		if len(words) < 3 {
			return 0, 0, false
		}
	default:
		return 0, 0, false
	}
	last := words[len(words)-1]
	t := text[last.a:last.b]
	if strings.HasPrefix(t, "-") || strings.HasPrefix(t, "$") || strings.HasPrefix(t, "`") {
		return 0, 0, false
	}
	a, b := inner(text, last)
	return a, b, true
}

// pairsOfAmounts reports whether the value of key is a line of amounts: name=number pairs
// with quantity names, and a number first when the key is a quantity name itself.
func pairsOfAmounts(v, key string) bool {
	ok, lead := quantityPairs(v)
	return ok && (!lead || key != "" && quantityKey(key))
}

// skipRules names rules of the scrubber that are switched off. Only a test sets it: the
// mutation test of the gate switches a rule off to see that the gate notices. It is empty
// in use, and a rule is then not skipped.
var skipRules = map[string]bool{}

// rule is the spans a named rule found, or none when a test has switched it off.
func rule(name string, spans []span) []span {
	if len(skipRules) > 0 && skipRules[name] {
		return nil
	}
	return spans
}

// lookAhead is how much of a line after a value is read to decide that the value is code.
const lookAhead = 256

// lookAheadWindow is the text from a to the end of its line, when that is within lookAhead
// bytes (or the end of the text); ok is false when the line goes on past that.
func lookAheadWindow(text string, a int) (string, bool) {
	end := min(a+lookAhead, len(text))
	win := text[a:end]
	if k := strings.IndexByte(win, '\n'); k >= 0 {
		return win[:k], true
	}
	return win, end == len(text)
}

// passBareWords are the names that are only the word. They are looked for by hand, not by
// an expression like passNameRe with an optional prefix, which is several times slower.
var passBareWords = []string{"passwd", "pass", "pwd", "pw", "key"}

// bareValueMax is the most of one value that is read: a longer one has its first bareValueMax
// bytes taken and the search goes on after them. It keeps a text of many names, each followed
// by a value that never ends ({key={key={key=...), linear.
const bareValueMax = 4096

// passMatches are the matches of passNameRe and the bare names, in the shape of
// FindAllStringSubmatchIndex: the whole match, the name, the value. A bare name that starts
// inside the value of the one before it (of the same word) is part of that value, and is not
// looked at again.
func passMatches(text string) [][]int {
	out := passNameRe.FindAllStringSubmatchIndex(text, -1)
	low := asciiLower(text)
	for _, w := range passBareWords {
		skip := 0
		for at := 0; ; {
			k := strings.Index(low[at:], w)
			if k < 0 {
				break
			}
			i := at + k
			at = i + 1
			if i < skip {
				continue
			}
			if m := bareMatchAt(text, i, len(w)); m != nil {
				out = append(out, m)
				skip = m[5]
			}
		}
	}
	return out
}

// bareMatchAt reads name, [:=] and a value around the word at i, as passNameRe does: a
// boundary in front (the start, white space or one of ,;{?&"'( ; a quote in front is itself
// the boundary), an optional quote after, spaces, a colon or equals sign, spaces, and a quoted
// value or a run with no space, quote, ampersand, comma or semicolon, of at most bareValueMax.
func bareMatchAt(text string, i, n int) []int {
	j := i
	if j > 0 {
		c := text[j-1]
		if c == '"' || c == '\'' {
			j--
		} else if !strings.ContainsRune(" \t\r\n\f\v,;{?&(", rune(c)) {
			return nil
		}
	}
	k := i + n
	if k < len(text) && (text[k] == '"' || text[k] == '\'') {
		k++
	}
	for k < len(text) && (text[k] == ' ' || text[k] == '\t') {
		k++
	}
	if k >= len(text) || (text[k] != ':' && text[k] != '=') {
		return nil
	}
	k++
	for k < len(text) && (text[k] == ' ' || text[k] == '\t') {
		k++
	}
	if k >= len(text) {
		return nil
	}
	start, end := k, k
	switch c := text[k]; c {
	case '"', '\'':
		window := text[k+1 : min(len(text), k+1+bareValueMax)]
		e := strings.IndexAny(window, string(c)+"\n")
		switch {
		case e >= 0 && window[e] == c:
			end = k + 1 + e + 1
		case e < 0 && len(window) == bareValueMax:
			end = k + 1 + bareValueMax // too long to read whole: the first part is taken
		default:
			return nil
		}
	default:
		for end < len(text) && end-start < bareValueMax && !strings.ContainsRune(" \t\r\n\f\v\"'&,;", rune(text[end])) {
			end++
		}
		if end == start {
			return nil
		}
	}
	return []int{j, end, i, i + n, start, end}
}

// asciiLower lower-cases the ASCII letters of s and nothing else, so that the offsets in the
// result are those of s.
func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}

// passFamilyName reports whether a name, lower case, is one passNameSpans reads: it ends in
// pass, passwd, pwd, pw or key (and is not one that holds keys of a kind that is not secret).
func passFamilyName(name string) bool {
	name = strings.ReplaceAll(name, "-", "_")
	ok := false
	for _, s := range passSuffixes {
		if strings.HasSuffix(name, s) {
			ok = true
		}
	}
	if !ok {
		return false
	}
	for _, s := range notKeySuffixes {
		if strings.HasSuffix(name, s) {
			return false
		}
	}
	return true
}

// followedByText reports whether anything but white space comes after position b, looking at
// no more than 256 bytes: a value that ends in a backslash is wrapped on to a next line only if
// there is one with text on it. When the next 256 bytes are only white space it is not taken
// for one (redact when in doubt).
func followedByText(text string, b int) bool {
	return strings.TrimSpace(text[b:min(len(text), b+256)]) != ""
}
