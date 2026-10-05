package baton

import (
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/jmwri/flockdeck/internal/record"
)

// This file holds the shapes that came after shapes.go: the command-line reader
// (see dbSpans), cookies, export and setx with a space, Kubernetes env entries,
// Go composite literals, form fields, and the filter that keeps record's
// matches off code and placeholders.

// ---- what is not a secret even where a secret is expected ----

var (
	operatorOnly = regexp.MustCompile(`^[=:!<>]+$`)
	numericValue = regexp.MustCompile(`^[0-9]{1,9}$`)
	// The placeholders there are, and nothing wider: <name> in lower case,
	// $NAME and ${NAME} and %NAME% in capitals and underscores, and a template
	// with spaces inside its braces. A secret that is wrapped in brackets, has a
	// digit in it or starts with a dollar sign is still a secret.
	angleHolder  = regexp.MustCompile(`^<[a-z][a-z_\-]{0,30}>`)
	percentHold  = regexp.MustCompile(`^%[A-Z_]+%`)
	dollarName   = regexp.MustCompile(`^\$[A-Z_]+`)
	dollarBrace  = regexp.MustCompile(`^\$\{[A-Z_]+\}`)
	templateHold = regexp.MustCompile(`^\{\{-?[ \t]+[^{}\n]{1,80}?[ \t]+-?\}\}`)
)

// placeholderAt reports whether s starts with a placeholder (see the patterns
// above) that ends where a word ends: ${TOKEN}extra is not one. $(cmd) is a
// command that will run, and not text. A ${NAME:-default} has a default that may
// be the secret, and is not one.
func placeholderAt(s string) bool {
	if strings.HasPrefix(s, "$(") {
		return true
	}
	for _, re := range []*regexp.Regexp{angleHolder, percentHold, dollarBrace, dollarName, templateHold} {
		if loc := re.FindStringIndex(s); loc != nil {
			return loc[1] == len(s) || !glued(s[loc[1]])
		}
	}
	return false
}

// quantityWords are the words in a name that say its number is an amount and not a
// secret: maxTokens, TOKEN_BUCKET_SIZE, num_tokens, token_ttl.
var quantityWords = map[string]bool{
	"max": true, "min": true, "num": true, "count": true, "size": true, "len": true, "length": true, "limit": true,
	"ttl": true, "expires": true, "expiry": true, "budget": true, "bucket": true, "rate": true, "usage": true,
	"total": true, "timeout": true, "interval": true, "retries": true, "offset": true, "index": true,
	// tokens, in the plural, are the units a model counts (input_tokens, prompt_tokens):
	// a secret is a token, one, and a number under tokens is an amount.
	"tokens": true,
}

// keyWords splits a name into its lower case words: on _ - . and between a lower
// case letter and a capital (maxTokens is max and tokens).
func keyWords(name string) []string {
	var words []string
	var cur []byte
	flush := func() {
		if len(cur) > 0 {
			words = append(words, strings.ToLower(string(cur)))
			cur = cur[:0]
		}
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c == '_' || c == '-' || c == '.' || c == ' ':
			flush()
		case isUpper(c) && i > 0 && isLower(name[i-1]):
			flush()
			cur = append(cur, c)
		default:
			cur = append(cur, c)
		}
	}
	flush()
	return words
}

// quantityKey reports whether a name says what it holds is an amount: a number under
// it is not a secret. A name that does not say so, even if it does not say secret
// either (pin, code, key), is left to be redacted.
func quantityKey(name string) bool {
	for _, w := range keyWords(strings.Trim(name, "-")) {
		if quantityWords[w] {
			return true
		}
	}
	return false
}

// keyBefore is the name in front of the value that starts at start: the word
// before an = or a : (maxTokens in "maxTokens = 4096"), lower case, or "".
func keyBefore(work string, start int) string {
	i := start
	for i > 0 && (work[i-1] == ' ' || work[i-1] == '\t') {
		i--
	}
	n := 0
	for i > 0 && (work[i-1] == '=' || work[i-1] == ':') && n < 2 {
		i--
		n++
	}
	if n == 0 {
		return ""
	}
	for i > 0 && (work[i-1] == ' ' || work[i-1] == '\t' || work[i-1] == '"' || work[i-1] == '\'') {
		i--
	}
	e := i
	for i > 0 {
		c := work[i-1]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '.' || c == '-' {
			i--
			continue
		}
		break
	}
	return work[i:e]
}

// notSecret reports whether a match that record or this package's shapes found
// is not one: a placeholder, an operator that stands where a value would, or a
// number under a name that is not a secret's (maxTokens = 4096). internal/record
// is left alone, so what it redacts in a recording does not change.
func notSecret(work string, start, end int, kind string) bool {
	if start >= end || end > len(work) {
		return false
	}
	if kind == "known-secret" {
		return false
	}
	if placeholderAt(work[start:]) || goNested.MatchString(work[start:]) || operatorOnly.MatchString(work[start:end]) {
		return true
	}
	switch kind {
	case "secret-value", "env-secret", "secret-flag":
		if notAValue(work, start, end) {
			return true
		}
		if numericValue.MatchString(work[start:end]) {
			if key := keyBefore(work, start); key != "" && quantityKey(key) {
				return true
			}
		}
	}
	return false
}

// nothingWords are what a value is when it is a nothing, a boolean or the name of a
// type, and not a secret: password: required, secret: false, Token: string,
// password: null. They are the whole list. A word people do use as a password
// (password, changeme, admin, secret) is not on it, and is redacted.
var nothingWords = map[string]bool{
	"none": true, "null": true, "nil": true, "false": true, "true": true, "required": true, "undefined": true,
	"string": true, "int": true, "integer": true, "number": true, "bool": true, "boolean": true, "object": true, "array": true,
}

// yamlKeyAsValue is a key on the next line taken for the value of the one above it.
var yamlKeyAsValue = regexp.MustCompile(`^[A-Za-z_][\w.\-]*:$`)

// (maskRe, callRe and erbRe, the exact forms of values that are not secrets, are in shapes3.go.)

// isoDate is a date, which is what a name ending in _at or _expires holds.
var isoDate = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}(?:[T ][\d:.]+Z?)?$`)

// yourHere is a placeholder written in words: your-token-here.
var yourHere = regexp.MustCompile(`^(?i:your[-_][a-z0-9_-]*[-_]here)$`)

// keyIsPathy reports whether a name itself says its value is a place and not a
// secret: _file, _path, _dir, _folder, _url, key_file, ssh_key. The list is short on
// purpose. A name that says only secret, token, key or password does not.
func keyIsPathy(key string) bool {
	k := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "-", "_"), ".", "_"))
	for _, s := range []string{"_file", "_path", "_dir", "_folder", "_url", "_uri"} {
		if strings.HasSuffix(k, s) {
			return true
		}
	}
	return k == "ssh_key" || k == "keyfile" || k == "pathtokey" || strings.HasSuffix(k, "keyfile") || strings.HasSuffix(k, "keypath")
}

// startsAsPath reports whether a value starts the way a place does: /, ./, ../,
// ~/, a drive letter, a share.
func startsAsPath(w string) bool {
	switch {
	case strings.HasPrefix(w, "/"), strings.HasPrefix(w, "./"), strings.HasPrefix(w, "../"), strings.HasPrefix(w, "~/"), strings.HasPrefix(w, `\\`):
		return true
	case len(w) >= 3 && isLetter(w[0]) && w[1] == ':' && (w[2] == '/' || w[2] == '\\'):
		return true
	}
	return false
}

func isLetter(c byte) bool { return isLower(c) || isUpper(c) }

// randomSegment reports whether any part of a path looks chosen at random: a long
// part with letters of both cases or digits in it that is not a name. A path with
// one is a secret that has slashes in it.
func randomSegment(w string) bool {
	for _, seg := range strings.FieldsFunc(w, func(r rune) bool { return r == '/' || r == '\\' }) {
		if len(seg) >= 12 && !nameLike(seg) && !identLike(seg) && mixedClasses(seg) {
			return true
		}
	}
	return false
}

// mountedSecret is a place secrets are mounted by the platform: /run/secrets/db_password
// names a file, and is where a password is read from, not the password.
func mountedSecret(w string) bool {
	for _, p := range []string{"/run/secrets/", "/var/run/secrets/", "/etc/secrets/", "/mnt/secrets/"} {
		if strings.HasPrefix(w, p) {
			return !randomSegment(w)
		}
	}
	return false
}

// notAValue reports whether the first word of the span is unambiguously not a value:
// a nothing, a boolean or a type name (nothingWords), a placeholder (your-token-here,
// a mask, a template, a call that reads the environment), a key on the next line of a
// YAML schema, a date under a name that is a time, a place that a name saying it is a
// place holds, or a mounted secret's file. Anything else under a secret's name is
// redacted, slashes, colons and all: when in doubt, redact.
func notAValue(work string, start, end int) bool {
	w := work[start:end]
	if i := strings.IndexAny(w, " \t\r\n"); i >= 0 {
		w = w[:i]
	}
	w = strings.Trim(w, "\"'`,;")
	lower := strings.ToLower(w)
	// One word: "Bearer xyz" is a credential and not the word bearer.
	single := !strings.ContainsAny(strings.TrimSpace(work[start:end]), " \t\r\n")
	key := keyBefore(work, start)
	klow := strings.ToLower(key)
	if single && nothingWords[lower] {
		return true
	}
	// A variable that has the name of its key and is handed on (Token: token, or
	// earlySecret: earlySecret,) is a reference, not a value: it is followed by what ends
	// an argument or a field.
	if single && key != "" && identOnly(w) && strings.EqualFold(strings.ReplaceAll(w, "_", ""), strings.ReplaceAll(key, "_", "")) && followedByArgumentEnd(work, end) {
		return true
	}
	if single && (lower == "bearer" || lower == "basic") && strings.HasSuffix(klow, "type") {
		return true
	}
	// A key on the line below a key (a schema: "password:" then "type: string"); on the
	// same line, "abc:" is a value that ends in a colon.
	if yamlKeyAsValue.MatchString(w) && startsLine(work, start) {
		return true
	}
	// An ERB tag that reads the environment, as the whole of the value, and a line of
	// amounts (input_tokens=1200 output_tokens=340) after the first of them.
	// (record's span stops at a quote or a space, so the whole word and the whole rest of
	// the line are looked at, not the span: a call that is more than the call is not one.)
	// Only lookAhead bytes of the line are read; one that goes on past that has no rest to
	// read, and so no exemption, which keeps a long line of many values linear.
	rest, inWindow := lookAheadWindow(work, start)
	if !inWindow {
		rest = ""
	}
	rest = strings.TrimSpace(rest)
	fw := rest
	if i := strings.IndexAny(fw, " \t"); i >= 0 {
		fw = fw[:i]
	}
	if erbRe.MatchString(rest) || callRe.MatchString(strings.Trim(fw, "\"'")) && !strings.Contains(work[start:end], " ") || pairsOfAmounts(strings.TrimSpace(work[start:end]), key) {
		return true
	}
	if !single {
		return false
	}
	if yourHere.MatchString(w) || maskRe.MatchString(w) {
		return true
	}
	timeKey := strings.HasSuffix(klow, "_at") || strings.Contains(klow, "expire") || strings.HasSuffix(klow, "_date") || strings.HasSuffix(klow, "_time")
	if timeKey && (isoDate.MatchString(w) || epochKept(key, w)) {
		return true
	}
	if key != "" && keyIsPathy(key) && !randomSegment(w) {
		if startsAsPath(w) {
			return true
		}
		if strings.HasSuffix(klow, "_url") || strings.HasSuffix(klow, "_uri") {
			if (strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")) && !strings.ContainsAny(w, "@=") {
				return true
			}
		}
	}
	return mountedSecret(w)
}

// identOnly is a plain identifier: letters, digits and underscores, not starting with a digit.
func identOnly(w string) bool {
	if w == "" || w[0] >= '0' && w[0] <= '9' {
		return false
	}
	for i := 0; i < len(w); i++ {
		c := w[i]
		if !(isLetter(c) || c >= '0' && c <= '9' || c == '_') {
			return false
		}
	}
	return true
}

// followedByArgumentEnd reports whether what comes after end (spaces aside) is a comma,
// a closing bracket or a brace.
func followedByArgumentEnd(work string, end int) bool {
	for i := end; i < len(work); i++ {
		switch work[i] {
		case ' ', '	':
			continue
		case ',', ')', '}':
			return true
		}
		return false
	}
	return false
}

// startsLine reports whether only white space is in front of start on its line.
func startsLine(work string, start int) bool {
	for i := start - 1; i >= 0; i-- {
		switch work[i] {
		case ' ', '\t':
			continue
		case '\n':
			return true
		default:
			return false
		}
	}
	return true
}

// ---- the command line ----

// dbCommand is a command whose password is on its command line; see dbSpans.
var dbCommand = regexp.MustCompile(`\b(mysql|mysqldump|mysqladmin|mysqlimport|mysqlshow|mariadb|mariadb-dump|sshpass|redis-cli|curl|htpasswd|docker[ \t]+login|podman[ \t]+login|helm[ \t]+registry[ \t]+login|vault[ \t]+login|az[ \t]+login|doctl|unzip|zip|zipcloak|gpg2?|7z[arz]?|aws[ \t]+configure[ \t]+set|https?|xhs?)\b`)

// closeQuote is where the quote that opens just before i closes, or the end of
// the line when it does not close within a few KiB. A quoted word may run over
// lines, so a password with a newline in it is one word.
func closeQuote(s string, i int, q byte, escapes bool) int {
	limit := min(len(s), i+4096)
	for j := i; j < limit; j++ {
		switch {
		case escapes && s[j] == '\\' && j+1 < len(s):
			j++
		case s[j] == q:
			return j + 1
		}
	}
	if nl := strings.IndexByte(s[i:], '\n'); nl >= 0 {
		return i + nl
	}
	return len(s)
}

// shellWordEnd is where the shell word that starts at i in s ends: at a space, a
// tab or a newline that is not in quotes. A word is the quoted and unquoted
// pieces run together, so 'it”s a pass' is one word, "a \"b\" c" is one, $'it\'s'
// (ANSI-C quoting) is one, and 'abc'def and "my pass"suffix go on past the closing
// quote. A backslash before a newline joins the lines.
func shellWordEnd(s string, i int) int {
	for i < len(s) {
		switch c := s[i]; {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			return i
		case c == '\'':
			i = closeQuote(s, i+1, '\'', false)
		case c == '$' && i+1 < len(s) && s[i+1] == '\'':
			i = closeQuote(s, i+2, '\'', true)
		case c == '"':
			i = closeQuote(s, i+1, '"', true)
		case c == '\\' && i+1 < len(s):
			i += 2
		case c == '[' && strings.HasPrefix(s[i:], "[REDACTED: "):
			// A mark has a space in it and is one word.
			if j := strings.IndexByte(s[i:], ']'); j >= 0 {
				i += j + 1
			} else {
				i++
			}
		default:
			i++
		}
	}
	return i
}

type word struct{ a, b int }

// commandWords reads the words of a command from pos to the end of the command:
// a newline that is not quoted or escaped, or a ; | & operator, or limit: the
// start of the next command, whose words are its own. That keeps a line of many
// commands linear and not quadratic.
func commandWords(s string, pos, limit int) []word {
	var words []word
	for i := pos; i < len(s) && i < limit && len(words) < 200; {
		for i < len(s) {
			if s[i] == ' ' || s[i] == '\t' {
				i++
			} else if s[i] == '\\' && i+1 < len(s) && s[i+1] == '\n' {
				i += 2
			} else if s[i] == '\\' && i+2 < len(s) && s[i+1] == '\r' && s[i+2] == '\n' {
				i += 3
			} else {
				break
			}
		}
		if i >= len(s) || s[i] == '\n' || s[i] == '\r' {
			break
		}
		e := shellWordEnd(s, i)
		t := s[i:e]
		switch t {
		case ";", "|", "||", "&&", "&", ";;":
			return words
		}
		words = append(words, word{i, e})
		if strings.HasSuffix(t, ";") {
			break
		}
		i = e
	}
	return words
}

// redirect reports whether a word is a redirection or a here-document or
// here-string operator: it is not an argument.
func redirect(w string) bool {
	return strings.HasPrefix(w, "<") || strings.HasPrefix(w, ">") || strings.HasPrefix(w, "2>") || strings.HasPrefix(w, "&>")
}

// dbSpans finds the passwords on the command lines of the clients that take one:
// mysql's attached -pSECRET, sshpass and docker login's -p (attached or after a
// space, even on the next line after a backslash), redis-cli's -a, curl's -u and
// --user (user:password), and htpasswd -b's password. The text is read as a shell
// reads it: a quoted value, with spaces, newlines and escaped or doubled quotes in
// it, or a quote followed by more characters, is one word, $'...' too, and every
// flag in the command is looked at, not the first. A capital -P is a port. With a
// space mysql reads -p's word as a database name, and it is kept: only the attached
// form is a password there.
func dbSpans(text string) []span {
	var out []span
	matches := dbCommand.FindAllStringSubmatchIndex(text, -1)
	// A command ends where the next one starts, which is what keeps a line of many
	// of them linear. A name that is part of a path or a file name (.htpasswd) is not
	// the start of one. The words read are counted, and past a budget every name ends
	// the command before it, so no text can make this quadratic.
	budget := len(text)/2 + 5000
	for i, m := range matches {
		limit := len(text)
		for _, n := range matches[i+1:] {
			if budget < 0 || startsCommand(text, n[0]) {
				limit = n[0]
				break
			}
		}
		cmd := strings.Join(strings.Fields(text[m[2]:m[3]]), " ")
		words := commandWords(text, m[1], limit)
		budget -= len(words)
		out = append(out, dbFlags(text, words, cmd, m[0])...)
	}
	return out
}

// startsCommand reports whether the program name at i begins a command: it is at
// the start of the text or after a space, a separator or a quote, and not after
// a slash, a dot or a dash, which is where a path or a file name has it.
func startsCommand(text string, i int) bool {
	return i == 0 || isCommandBoundary(text[i-1])
}

// dbFlags reads the words of a command for its password flags.
func dbFlags(text string, words []word, cmd string, start int) []span {
	var out []span
	tool, handled := toolFlags(text, words, cmd, start)
	if handled {
		return tool
	}
	out = append(out, tool...)
	add := func(a, b int) {
		if b > a {
			out = append(out, span{a, b, "db-password"})
		}
	}
	mysqlFamily := strings.HasPrefix(cmd, "mysql") || strings.HasPrefix(cmd, "mariadb")
	if cmd == "htpasswd" {
		return htpasswdSpans(text, words)
	}
	for i, w := range words {
		t := text[w.a:w.b]
		next := func() (word, bool) {
			if i+1 < len(words) {
				n := text[words[i+1].a:words[i+1].b]
				if !strings.HasPrefix(n, "-") && !redirect(n) {
					return words[i+1], true
				}
			}
			return word{}, false
		}
		switch {
		case (mysqlFamily || cmd == "sshpass" || cmd == "docker login") && strings.HasPrefix(t, "-p") && len(t) > 2:
			add(w.a+2, w.b)
		case mysqlFamily && t == "-p":
			// With a space mysql reads the word as a database name. It is taken
			// only when it reads like a password, which a database name seldom does.
			if n, ok := next(); ok && secretWord(text[n.a:n.b]) {
				add(n.a, n.b)
			}
		case (cmd == "sshpass" || cmd == "docker login") && t == "-p":
			if n, ok := next(); ok {
				add(n.a, n.b)
			}
		case cmd == "redis-cli" && t == "-a":
			if n, ok := next(); ok {
				add(n.a, n.b)
			}
		case cmd == "curl" && (t == "-u" || t == "--user"):
			// user:password; the user name is kept.
			if i+1 < len(words) {
				n := words[i+1]
				if k := colonOutsideMarks(text[n.a:n.b]); k >= 0 {
					end := n.b
					// A quoted argument keeps its closing quote.
					if q := text[n.a]; (q == '"' || q == '\'') && end-1 > n.a+k && text[end-1] == q {
						end--
					}
					add(n.a+k+1, end)
				}
			}
		}
	}
	return out
}

// secretWord reports whether a word reads like a password: eight characters or
// more, with a digit in it or both cases. A database name (mydb, production_db)
// seldom does.
func secretWord(w string) bool {
	if len(w) < 8 || strings.Contains(w, "[REDACTED: ") {
		return false
	}
	var lower, upper, digit bool
	for i := 0; i < len(w); i++ {
		switch c := w[i]; {
		case c >= 'a' && c <= 'z':
			lower = true
		case c >= 'A' && c <= 'Z':
			upper = true
		case c >= '0' && c <= '9':
			digit = true
		}
	}
	return digit || lower && upper
}

// htpasswdSpans takes the password of htpasswd -b: the third argument, or the
// second with -n (which prints to the terminal and has no file).
func htpasswdSpans(text string, words []word) []span {
	batch, noFile := false, false
	var args []word
	for _, w := range words {
		t := text[w.a:w.b]
		switch {
		case strings.HasPrefix(t, "--"):
		case strings.HasPrefix(t, "-") && len(t) > 1 && len(t) <= 5 && !strings.ContainsAny(t[1:], "0123456789_"):
			batch = batch || strings.Contains(t, "b")
			noFile = noFile || strings.Contains(t, "n")
		case redirect(t):
		default:
			args = append(args, w)
		}
	}
	at := 2
	if noFile {
		at = 1
	}
	if !batch || len(args) <= at {
		return nil
	}
	if w := args[at]; w.b > w.a {
		return []span{{w.a, w.b, "db-password"}}
	}
	return nil
}

// formRe is a pass= or pw= field of a form body or a query string; the longer
// names (passwd, pwd) are record's.
var formRe = regexp.MustCompile(`(?i)(?:^|[?&;,'"\s])(?:pass|pw)=([^&\s"';]{4,})`)

func formSpans(text string) []span {
	var out []span
	for _, m := range formRe.FindAllStringSubmatchIndex(text, -1) {
		v := strings.ToLower(text[m[2]:m[3]])
		if v == "true" || v == "false" || v == "none" || v == "null" || v[0] == '%' || placeholderAt(text[m[2]:]) {
			continue
		}
		out = append(out, span{m[2], m[3], "secret-value"})
	}
	return out
}

// ---- export and setx with a space ----

// exportRe is "export NAME value" and "setx NAME value": no =, the value is the
// rest of the line.
var exportRe = regexp.MustCompile(`(?m)(?:^|[;&|(])[ \t]*(export|setx)[ \t]+([A-Za-z_][A-Za-z0-9_]*)[ \t]+([^\n]*)`)

func exportSpans(text string) []span {
	var out []span
	for _, m := range exportRe.FindAllStringSubmatchIndex(text, -1) {
		name := strings.ToLower(text[m[4]:m[5]])
		if !record.SecretName(name) || !endsInSecretWord(name) {
			continue
		}
		a, b := m[6], m[7]
		for b > a && strings.IndexByte(" \t\r", text[b-1]) >= 0 {
			b--
		}
		if b-a >= 2 && (text[a] == '"' || text[a] == '\'') && text[b-1] == text[a] {
			a, b = a+1, b-1
		}
		if text[m[4]:m[5]] != strings.ToUpper(text[m[4]:m[5]]) && !lowerNameOK(text[m[2]:m[3]], text[a:b]) {
			continue
		}
		if b <= a || text[a] == '=' || strings.HasPrefix(text[a:b], "[REDACTED: ") || placeholderAt(text[a:]) {
			continue
		}
		out = append(out, span{a, b, "env-secret"})
	}
	return out
}

// ---- Kubernetes env entries ----

// k8sRe is a "- name: API_TOKEN" entry and the "value:" line under it.
var k8sRe = regexp.MustCompile(`(?m)^[ \t]*-[ \t]+name:[ \t]*["']?([A-Za-z_][A-Za-z0-9_.\-]*)["']?[ \t]*\r?\n[ \t]*value:[ \t]*([^\n]*)$`)

func k8sSpans(text string) []span {
	var out []span
	for _, m := range k8sRe.FindAllStringSubmatchIndex(text, -1) {
		name := strings.ToLower(text[m[2]:m[3]])
		if !record.SecretName(name) || !endsInSecretWord(name) {
			continue
		}
		a, b := m[4], m[5]
		for b > a && strings.IndexByte(" \t\r", text[b-1]) >= 0 {
			b--
		}
		if b-a >= 2 && (text[a] == '"' || text[a] == '\'') && text[b-1] == text[a] {
			a, b = a+1, b-1
		} else if i := strings.Index(text[a:b], " #"); i >= 0 {
			b = a + i
		}
		if b <= a || strings.HasPrefix(text[a:b], "[REDACTED: ") || placeholderAt(text[a:]) {
			continue
		}
		out = append(out, span{a, b, "env-secret"})
	}
	return out
}

// ---- Go composite literals ----

// goLitRe is an unquoted key (a struct field or a variable) with a Go composite
// literal as its value: password: []byte("x"), keys := []string{"a"}.
var goLitRe = regexp.MustCompile(`\b([A-Za-z_][A-Za-z0-9_]*)[ \t]*(?::=|=|:)[ \t]*(?:\[\]byte|\[\]string|string|map\[[A-Za-z0-9_.*]*\][A-Za-z0-9_.*]*)([({])`)

func goLitSpans(text string) []span {
	var out []span
	for _, m := range goLitRe.FindAllStringSubmatchIndex(text, -1) {
		name := strings.ToLower(text[m[2]:m[3]])
		if !record.SecretName(name) || !endsInSecretWord(name) {
			continue
		}
		if end := closingBracket(text, m[4]); end > 0 {
			out = append(out, jsonStrings(text, m[4]+1, end)...)
		}
	}
	return out
}

// goStruct reports whether the bracket that opens at open starts a Go
// composite literal of structs or a nested one ({{source: "x"}}), whose
// strings are not a secret's value.
func goStruct(text string, open int) bool {
	i := open + 1
	for i < len(text) && (text[i] == ' ' || text[i] == '\t' || text[i] == '\n' || text[i] == '\r') {
		i++
	}
	if i >= len(text) {
		return false
	}
	if text[i] == '{' {
		return true
	}
	j := i
	for j < len(text) && (text[j] == '_' || text[j] >= 'a' && text[j] <= 'z' || text[j] >= 'A' && text[j] <= 'Z' || text[j] >= '0' && text[j] <= '9') {
		j++
	}
	for k := j; k < len(text) && (text[k] == ' ' || text[k] == '\t'); k++ {
		if k+1 < len(text) && text[k+1] == ':' {
			return j > i
		}
	}
	return j > i && j < len(text) && text[j] == ':'
}

// ---- cookies ----

// cookieRe is a Cookie or Set-Cookie header: its value is up to a quote or the end
// of the line.
var cookieRe = regexp.MustCompile(`(?i)\b(?:set-)?cookie:[ \t]*([^\n'"]*)`)

// cookieAttrs are the attributes of Set-Cookie: they are not cookies.
var cookieAttrs = map[string]bool{"path": true, "domain": true, "expires": true, "max-age": true, "samesite": true, "secure": true, "httponly": true, "version": true, "comment": true}

func cookieSpans(text string) []span {
	var out []span
	for _, m := range cookieRe.FindAllStringSubmatchIndex(text, -1) {
		pos := m[2]
		for _, part := range strings.SplitAfter(text[m[2]:m[3]], ";") {
			at := pos
			pos += len(part)
			trimmed := strings.TrimLeft(part, " \t")
			at += len(part) - len(trimmed)
			trimmed = strings.TrimRight(trimmed, "; \t\r")
			eq := strings.IndexByte(trimmed, '=')
			if eq <= 0 {
				continue
			}
			name := strings.ToLower(trimmed[:eq])
			if cookieAttrs[name] {
				continue
			}
			v := trimmed[eq+1:]
			if v == "" || isHex(v) || digitsAndDots(v) || !opaque(v) || placeholderAt(v) || strings.HasPrefix(v, "[REDACTED: ") {
				continue
			}
			named := record.SecretName(name) || strings.Contains(name, "sess") || name == "sid" || strings.HasSuffix(name, "sid") || strings.Contains(name, "auth") || strings.Contains(name, "jwt")
			if named && len(v) >= 8 || len(v) >= 24 && !nameLike(v) && !identLike(v) {
				out = append(out, span{at + eq + 1, at + len(trimmed), "secret-value"})
			}
		}
	}
	return out
}

// opaque reports whether v is made of the characters of an opaque token: base64
// and its URL form, percent escapes, dots. A value with a bracket, a bar or a
// space in it is a structured one (utmcsr=a|utmccn=(b)), not a session id.
func opaque(v string) bool {
	for i := 0; i < len(v); i++ {
		c := v[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte("+/=_.-%~!#*$@^", c) >= 0) {
			return false
		}
	}
	return true
}

// digitsAndDots is a value of digits and dots, as analytics cookies are: 123.456.789.
func digitsAndDots(v string) bool {
	for i := 0; i < len(v); i++ {
		if !(v[i] >= '0' && v[i] <= '9' || v[i] == '.' || v[i] == '_' || v[i] == '-') {
			return false
		}
	}
	return true
}

// ---- the odd-space readings ----

// oddSpaceAt reports the width of the odd space (no-break, ideographic) that
// starts s, or 0.
func oddSpaceAt(s string) int {
	r, n := utf8.DecodeRuneInString(s)
	if spaceLike(r) {
		return n
	}
	return 0
}

// goNested is a Go composite literal inside another ({{source: "x"}}), which is
// code and not a template.
var goNested = regexp.MustCompile(`^\{\{[ \t]*[A-Za-z_]\w*[ \t]*:`)

// quotedAssignRe is a secret-named name set to a quoted literal: password := "x",
// const apiToken = "x", let secret_key = 'x', _auth = "x".
var quotedAssignRe = regexp.MustCompile("(?m)\\b([A-Za-z_][A-Za-z0-9_.]*)[ \\t]*(?::=|=|:)[ \\t]*([\"'`])([^\"'`\\n]{8,200})([\"'`])")

// npmAuthRe is _auth, _authToken or _password at the start of a line of an npmrc.
var npmAuthRe = regexp.MustCompile("(?mi)^[ \\t]*_(?:authToken|auth|password)[ \\t]*=[ \\t]*[\"']?([^\\s\"']+)")

// providerRe are keys that have a shape of their own and no name in front of them.
var providerRe = regexp.MustCompile(`\b(?:sk_(?:live|test)_[A-Za-z0-9]{16,}|shp(?:at|ca|pa|ss)_[a-f0-9]{32}|dop_v1_[a-f0-9]{64})`)

// mixedClasses reports whether v has characters of at least two of the classes
// lower case, upper case, digits and symbols, and is not a plain identifier or a
// path: a literal that looks chosen, not a label.
func mixedClasses(v string) bool {
	var lower, upper, digit, sym int
	for i := 0; i < len(v); i++ {
		switch c := v[i]; {
		case c >= 'a' && c <= 'z':
			lower = 1
		case c >= 'A' && c <= 'Z':
			upper = 1
		case c >= '0' && c <= '9':
			digit = 1
		case c == ' ':
		default:
			sym = 1
		}
	}
	return lower+upper+digit+sym >= 2 && (digit == 1 || upper == 1 && lower == 1)
}

func literalSpans(text string) []span {
	var out []span
	for _, m := range quotedAssignRe.FindAllStringSubmatchIndex(text, -1) {
		name := strings.ToLower(text[m[2]:m[3]])
		if m[4] < 0 || text[m[4]:m[5]] != text[m[8]:m[9]] {
			continue
		}
		v := text[m[6]:m[7]]
		if !secretLiteralName(name) || !mixedClasses(v) || placeholderAt(v) ||
			strings.HasPrefix(v, "[REDACTED: ") || strings.Contains(v, " ") && !strings.ContainsAny(v, "0123456789") {
			continue
		}
		out = append(out, span{m[6], m[7], "secret-value"})
	}
	for _, m := range npmAuthRe.FindAllStringSubmatchIndex(text, -1) {
		if !placeholderAt(text[m[2]:]) && !strings.HasPrefix(text[m[2]:m[3]], "[REDACTED: ") {
			out = append(out, span{m[2], m[3], "registry-token"})
		}
	}
	return out
}

// providerSpans finds the keys that have a shape of their own.
func providerSpans(text string) []span {
	if !strings.Contains(text, "sk_") && !strings.Contains(text, "shp") && !strings.Contains(text, "dop_v1_") {
		return nil
	}
	var out []span
	for _, m := range providerRe.FindAllStringIndex(text, -1) {
		out = append(out, span{m[0], m[1], "secret-value"})
	}
	return out
}

// secretLiteralName reports whether a name says what it holds is a secret, for a
// quoted literal assigned to it: one that ends in a secret word, or in "pass"
// (dbpass, rootpass) but is not a word that happens to (bypass, compass).
func secretLiteralName(name string) bool {
	if record.SecretName(name) && endsInSecretWord(name) {
		return true
	}
	n := strings.ToLower(name)
	if !strings.HasSuffix(n, "pass") || len(n) <= 4 {
		return false
	}
	switch n {
	case "bypass", "compass", "trespass", "overpass", "surpass", "underpass", "encompass", "impass", "tspass":
		return false
	}
	return true
}

// bareCredentialRe is user:password@host.tld with no scheme in front, as curl and
// git take it.
var bareCredentialRe = regexp.MustCompile(`(?:^|[\s"'=])([A-Za-z][A-Za-z0-9._-]{1,30}):([^\s:@/"']{6,64})@([A-Za-z0-9][A-Za-z0-9.-]*\.[A-Za-z]{2,})`)

// bareCredentialSpans finds the password in user:password@host.tld. The words that
// stand where a user would in an address (mailto:), and a "password" that is not
// chosen like one, are left.
func bareCredentialSpans(text string) []span {
	if !strings.Contains(text, "@") {
		return nil
	}
	var out []span
	for _, m := range bareCredentialRe.FindAllStringSubmatchIndex(text, -1) {
		switch strings.ToLower(text[m[2]:m[3]]) {
		case "mailto", "xmpp", "sip", "sips", "tel", "callto", "skype", "news", "from", "to", "cc", "email", "author", "by":
			continue
		}
		pw := text[m[4]:m[5]]
		if !mixedClasses(pw) || placeholderAt(pw) || strings.HasPrefix(pw, "[REDACTED: ") {
			continue
		}
		out = append(out, span{m[4], m[5], "url-credential"})
	}
	return out
}

// oddSpaceEndsAt reports whether an odd space (no-break, ideographic) ends at
// byte i of s.
func oddSpaceEndsAt(s string, i int) bool {
	r, n := utf8.DecodeLastRuneInString(s[:i])
	return n > 0 && r != utf8.RuneError && spaceLike(r)
}

// colonOutsideMarks is the index of the first ':' in s that is not inside a mark
// ("[REDACTED: kind]" has one), or -1.
func colonOutsideMarks(s string) int {
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == ':':
			return i
		case s[i] == '[' && strings.HasPrefix(s[i:], "[REDACTED: "):
			if j := strings.IndexByte(s[i:], ']'); j >= 0 {
				i += j
			}
		}
	}
	return -1
}

// isCommandBoundary is a character a command can follow: a space, a separator,
// a bracket or a quote.
func isCommandBoundary(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\r', ';', '|', '&', '(', '`', '\'', '"':
		return true
	}
	return false
}

// urlCredRe is the user and password of a URL: scheme://user:password@host. The
// password ends at the at sign that starts the host; a user name may itself end in a
// secret word (x-access-token, gitlab-ci-token, deploy-token), which is why the whole
// of "user:password" can look like a secret-named value to a pattern that does not
// know it is a URL.
var urlCredRe = regexp.MustCompile(`://([^\s:/@]+):([^\s@/]+)@`)

// urlCred is where the user starts and the password starts and ends in a text.
type urlCred struct{ user, pw, end int }

func urlCreds(text string) []urlCred {
	if !strings.Contains(text, "://") {
		return nil
	}
	var out []urlCred
	for _, m := range urlCredRe.FindAllStringSubmatchIndex(text, -1) {
		out = append(out, urlCred{m[2], m[4], m[5]})
	}
	return out
}

// urlCredSpans are the passwords of the URLs in text, and only the passwords: the
// scheme, the user name and the host stay.
func urlCredSpans(text string) []span {
	var out []span
	for _, u := range urlCreds(text) {
		if pw := text[u.pw:u.end]; pw != "" && !strings.HasPrefix(pw, "[REDACTED: ") && !placeholderAt(pw) {
			out = append(out, span{u.pw, u.end, "url-credential"})
		}
	}
	return out
}

// mergedWithURLUser reports whether a span found by a pattern takes in a URL's user
// name along with its password (x-access-token:secret): that span is not the
// password, and urlCredSpans has the password exactly.
func mergedWithURLUser(creds []urlCred, start, end int) bool {
	// The credentials are in order and do not overlap, so the only one a span can start
	// in is the first whose password does not start before it: found by searching, not by
	// looking at all of them for every span, which was quadratic for a text of many URLs.
	i := sort.Search(len(creds), func(i int) bool { return creds[i].pw >= start })
	if i == len(creds) {
		return false
	}
	u := creds[i]
	// Starting in the user name or at the password, and not just the password: a span
	// that takes in the user, or goes on past the password into the host.
	return start >= u.user && end > u.pw && !(start == u.pw && end == u.end)
}
