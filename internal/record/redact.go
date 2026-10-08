package record

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// Redacted is what a secret is replaced with.
const Redacted = "[redacted]"

// Withheld is what the output of a read of a secret file is replaced with.
const Withheld = "[withheld: a secret file]"

// Clip cuts s to at most n bytes on a rune boundary and says how much it cut,
// so a reader of the transcript knows what they are looking at is not all of
// it.
func Clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return fmt.Sprintf("%s…[clipped %d bytes]", s[:cut], len(s)-cut)
}

// nameIsSecret is the idea of a secret's name the guard in internal/review has,
// widened from file names to variable and field names: whatever a value of this
// name holds is taken to be one. These are matched in any case, anywhere in the
// name:
//
//	secret, token, password, passwd, pwd, credential, authorization,
//	api_key, apikey, access_key, private_key (the underscore may be a dash or
//	missing), auth_token, auth_key, auth_header
//
// With wide set the names also are passphrase and pass as the last word of a
// compound name (DB_PASS, my.pass; not pass alone, bypass or compass).
//
// It is the pattern (?i)(secret|token|passw(or)?d|passwd|pwd|api[_-]?key|apikey|
// access[_-]?key|private[_-]?key|credential|authorization|auth[_-](token|key|
// header)), written by hand because a pattern is read byte by byte and a name can
// be as long as the text around it.
func nameIsSecret(name string, wide bool) bool {
	l := strings.ToLower(name)
	if strings.Contains(l, "ſ") { // folds to s in a pattern with (?i)
		l = strings.ReplaceAll(l, "ſ", "s")
	}
	return nameIsSecretLower(l, wide)
}

// nameIsSecretLower is nameIsSecret for a name that is in lower case already.
func nameIsSecretLower(l string, wide bool) bool {
	for i := 0; i < len(l); i++ {
		rest := l[i:]
		switch l[i] {
		case 's':
			if strings.HasPrefix(rest, "secret") {
				return true
			}
		case 't':
			if strings.HasPrefix(rest, "token") {
				return true
			}
		case 'c':
			if strings.HasPrefix(rest, "credential") {
				return true
			}
		case 'p':
			if strings.HasPrefix(rest, "password") || strings.HasPrefix(rest, "passwd") || strings.HasPrefix(rest, "pwd") {
				return true
			}
			if strings.HasPrefix(rest, "private") && keyAfter(rest[len("private"):]) {
				return true
			}
			if wide && (strings.HasPrefix(rest, "passphrase") || len(rest) == 4 && rest == "pass" && compoundBefore(l, i)) {
				return true
			}
		case 'a':
			switch {
			case strings.HasPrefix(rest, "apikey"), strings.HasPrefix(rest, "authorization"):
				return true
			case strings.HasPrefix(rest, "api") && keyAfter(rest[len("api"):]):
				return true
			case strings.HasPrefix(rest, "access") && keyAfter(rest[len("access"):]):
				return true
			case strings.HasPrefix(rest, "auth") && len(rest) > len("auth") && (rest[4] == '_' || rest[4] == '-'):
				r := rest[5:]
				if strings.HasPrefix(r, "token") || strings.HasPrefix(r, "key") || strings.HasPrefix(r, "header") {
					return true
				}
			}
		}
	}
	return false
}

// keyAfter reports whether s starts with "key" or with a dash or underscore and
// then "key".
func keyAfter(s string) bool {
	if s != "" && (s[0] == '_' || s[0] == '-') {
		s = s[1:]
	}
	return strings.HasPrefix(s, "key")
}

var (
	// user:password@ in a URL; group 1 is the scheme and user.
	urlCredRe = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.\-]*://[^\s/:@]+):[^\s/@]+@`)

	// Authorization headers: the scheme is kept, the credential is not.
	bearerRe = regexp.MustCompile(`(?i)\b(bearer|basic)(\s+)[A-Za-z0-9._~+/=\-]{12,}`)
)

func isAlnum(b byte) bool {
	return '0' <= b && b <= '9' || 'a' <= b && b <= 'z' || 'A' <= b && b <= 'Z'
}

// shape is a token with a recognisable shape, whatever it is called, and the
// kind of secret a caller can say it was.
type shape struct {
	kind string
	re   *regexp.Regexp
}

var tokenShapes = []shape{
	{"anthropic-key", regexp.MustCompile(`sk-ant-[A-Za-z0-9_\-]{10,}`)},
	{"api-key", regexp.MustCompile(`sk-[A-Za-z0-9_\-]{20,}`)},
	{"github-token", regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{30,}`)},
	{"github-token", regexp.MustCompile(`github_pat_[A-Za-z0-9_]{20,}`)},
	{"gitlab-token", regexp.MustCompile(`glpat-[A-Za-z0-9_\-]{20,}`)},
	{"aws-key", regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`)},
	{"slack-token", regexp.MustCompile(`xox[abprs]-[A-Za-z0-9\-]{10,}`)},
	{"google-key", regexp.MustCompile(`AIza[0-9A-Za-z_\-]{35}`)},
	{"npm-token", regexp.MustCompile(`npm_[A-Za-z0-9]{36}`)},
	{"sendgrid-key", regexp.MustCompile(`\bSG\.[A-Za-z0-9_\-]{16,}\.[A-Za-z0-9_\-]{16,}`)},
	{"jwt", regexp.MustCompile(`\beyJ[A-Za-z0-9_\-]{8,}\.[A-Za-z0-9_\-]{8,}\.[A-Za-z0-9_\-]{8,}`)},
}

// Kinds lists every kind of secret Find names: the kinds of the shaped tokens
// and the four it gives to the rest. It is for a caller that marks what it
// removes and must know the marks it can have made.
func Kinds() []string {
	kinds := []string{"private-key", "bearer-token", "url-credential", "secret-value"}
	seen := map[string]bool{}
	for _, k := range kinds {
		seen[k] = true
	}
	for _, t := range tokenShapes {
		if !seen[t.kind] {
			seen[t.kind] = true
			kinds = append(kinds, t.kind)
		}
	}
	return kinds
}

// SecretName reports whether a variable or field name says that what it holds
// is a secret.
func SecretName(name string) bool { return nameIsSecret(name, false) }

// Span is a stretch of a string that Find took for a secret: the bytes from
// Start up to End, and the kind of secret it looks like.
type Span struct {
	Start, End int
	Kind       string
}

// FindTokens reports the tokens in s that have a recognisable shape (a
// provider's key prefix, a JWT) and nothing that is only named as a secret.
// It is the part of Find that is safe to run on text that has been joined
// across line breaks.
func FindTokens(s string) []Span {
	var out []Span
	for _, t := range tokenShapes {
		if !mayMatch(s, t.kind) {
			continue
		}
		for _, m := range t.re.FindAllStringIndex(s, -1) {
			out = append(out, Span{m[0], m[1], t.kind})
		}
	}
	return merge(out)
}

// Find reports what Redact would replace in s, as spans of s, without
// replacing it. Spans that overlap are merged into one.
func Find(s string) []Span {
	return merge(append(findOthers(s, false), FindTokens(s)...))
}

// findOthers is Find without the shaped tokens: private keys, credentials after
// Bearer and Basic and in URLs, and values assigned to a secret's name.
//
// With wide set it also takes what Redact takes and Find, which callers use to
// mark spans of text they have scrubbed by other rules too, does not: a quoted
// value with no closing quote on its line, or with a line break in it; the names
// passphrase and pass; the flag form --password value; the credential after an
// Authorization scheme word; and the passwords on the command lines of curl,
// mysql and sshpass.
//
// Every step is linear in the length of s. The ones that would otherwise run a
// pattern over all of it first look for the few bytes the pattern needs.
func findOthers(s string, wide bool) []Span {
	if s == "" {
		return nil
	}
	ls, ascii := foldASCII(s)
	var out []Span
	if strings.Contains(s, "-----BEGIN ") {
		out = findPEM(s, out)
	}
	if !ascii || strings.Contains(ls, "bearer") || strings.Contains(ls, "basic") {
		// The scheme and the space after it stay; the credential goes.
		for _, m := range bearerRe.FindAllStringSubmatchIndex(s, -1) {
			out = append(out, Span{m[5], m[1], "bearer-token"})
		}
	}
	if strings.Contains(s, "://") {
		// From after the colon that follows the user, up to the "@".
		for _, m := range urlCredRe.FindAllStringSubmatchIndex(s, -1) {
			out = append(out, Span{m[3] + 1, m[1] - 1, "url-credential"})
		}
	}
	out = findAssigned(s, ls, ascii, wide, out)
	if wide {
		if !ascii || strings.Contains(ls, "curl") {
			out = appendGroup(out, s, curlUserRe)
		}
		if !ascii || strings.Contains(ls, "mysql") || strings.Contains(ls, "mariadb") {
			out = appendGroup(out, s, mysqlPassRe)
		}
		if !ascii || strings.Contains(ls, "sshpass") {
			out = appendGroup(out, s, sshpassRe)
		}
	}
	return merge(out)
}

// appendGroup adds the first group of every match of re as a secret value.
func appendGroup(out []Span, s string, re *regexp.Regexp) []Span {
	for _, m := range re.FindAllStringSubmatchIndex(s, -1) {
		for k := 2; k+1 < len(m); k += 2 {
			if m[k] >= 0 && !strings.Contains(s[m[k]:m[k+1]], Redacted) {
				out = append(out, Span{m[k], m[k+1], "secret-value"})
			}
		}
	}
	return out
}

var (
	// curl -u user:password and curl --user user:password. The user stays, the
	// password goes.
	curlUserRe = regexp.MustCompile(`(?i)\bcurl\b[^\n]*?[ \t](?:-u|--user)(?:[ \t]+|=)(?:"[^":\n]*:([^"\n]+)"|'[^':\n]*:([^'\n]+)'|["']?[^\s:"']*:([^\s"']+))`)
	// mysql -pPASSWORD. The password is attached; with a space after -p mysql
	// asks for it instead.
	mysqlPassRe = regexp.MustCompile(`(?i)\b(?:mysql|mysqldump|mysqladmin|mariadb)\b[^\n]*?[ \t]-p("[^"\n]+"|'[^'\n]+'|[^\s"'-][^\s"']*)`)
	// sshpass -p PASSWORD and sshpass -pPASSWORD.
	sshpassRe = regexp.MustCompile(`(?i)\bsshpass\b[^\n]*?[ \t]-p[ \t]*("[^"\n]+"|'[^'\n]+'|[^\s"'-][^\s"']*)`)
)

// maxQuotedValue is how far past an opening quote a value is looked for its
// closing one when it may run over lines. A value not closed within it is taken
// to the end of its line.
const maxQuotedValue = 4096

// foldASCII returns s in lower case when s is ASCII, and says whether it was.
// For other text the second result is false and the first is not to be used.
func foldASCII(s string) (string, bool) {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return "", false
		}
	}
	return strings.ToLower(s), true
}

// nameStems are words every secret name contains; a run of name characters
// without one of them is not looked at any further.
var nameStems = []string{"secret", "token", "pass", "pwd", "key", "cred", "auth"}

func hasStem(lower string) bool {
	for _, st := range nameStems {
		if strings.Contains(lower, st) {
			return true
		}
	}
	return false
}

// nameByte reports whether b can be part of a variable, field or flag name.
// The bytes of non-ASCII characters are, so that a name with an accent in it is
// one name, as it is to a reader.
func nameByte(b byte) bool {
	return b >= utf8.RuneSelf || b == '_' || b == '.' || b == '-' ||
		'0' <= b && b <= '9' || 'a' <= b && b <= 'z' || 'A' <= b && b <= 'Z'
}

func isSpace(b byte) bool { return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\f' }

// valueStop ends a value that is not quoted.
func valueStop(b byte) bool {
	return isSpace(b) || b == '"' || b == '\'' || b == ',' || b == ';' || b == '&' || b == ')' || b == '}' || b == ']'
}

// schemes are the words that can start an Authorization header's value.
var schemes = map[string]bool{
	"bearer": true, "basic": true, "token": true, "apikey": true, "api-key": true,
	"negotiate": true, "ntlm": true, "ssws": true, "bot": true,
}

// scan is the state of one findAssigned.
type scan struct {
	s       string
	wide    bool
	budget  int // bytes quoted values may still be looked through
	lineEnd int // the end of the line the last unclosed value was cut at
	next    int // where the scan goes on after the name last looked at
}

// findAssigned finds NAME=value, NAME: value, "name": "value" and, when wide,
// the flag form --name value, where the name says it is a secret. It walks the
// runs of name characters once, so a long run of them costs what it is long.
func findAssigned(s, ls string, ascii, wide bool, out []Span) []Span {
	if ascii && !hasStem(ls) {
		return out
	}
	sc := &scan{s: s, wide: wide, budget: 4*len(s) + 1<<16, lineEnd: -1}
	for i := 0; i < len(s); {
		if !nameByte(s[i]) {
			i++
			continue
		}
		j := i + 1
		for j < len(s) && nameByte(s[j]) {
			j++
		}
		var secret bool
		if ascii {
			secret = nameIsSecretLower(ls[i:j], wide)
		} else {
			secret = nameIsSecret(s[i:j], wide)
		}
		if secret {
			out = sc.assigned(out, i, j)
			j = max(j, sc.next)
		}
		i = j
	}
	return out
}

// assigned looks at what follows the secret name s[start:end]. It sets sc.next to
// where the scan goes on: after the value if there was one, as a pattern that
// matches a name and its value goes on after both, so that every byte is read as
// part of a value at most once.
func (sc *scan) assigned(out []Span, start, end int) []Span {
	s := sc.s
	sc.next = end
	p := end
	if p < len(s) && (s[p] == '"' || s[p] == '\'') {
		p++
	}
	for p < len(s) && isSpace(s[p]) {
		p++
	}
	switch {
	case p < len(s) && (s[p] == ':' || s[p] == '='):
		p++
		for p < len(s) && isSpace(s[p]) {
			p++
		}
	case sc.wide && s[start] == '-' && end < len(s) && (s[end] == ' ' || s[end] == '\t') && flagTakesSecret(s[start:end]):
		// A flag: --password hunter2.
		p = end
		for p < len(s) && (s[p] == ' ' || s[p] == '\t') {
			p++
		}
		if p >= len(s) || s[p] == '-' || s[p] == '<' || s[p] == '>' || s[p] == '|' || s[p] == '\n' || s[p] == '\r' {
			return out
		}
		if shortWord(s[p:]) {
			// "--token is still taken": prose that names the flag, not a value.
			return out
		}
	default:
		return out
	}
	if p >= len(s) {
		return out
	}
	if q := s[p]; q == '"' || q == '\'' {
		e := sc.quotedEnd(p)
		if e > p {
			sc.next = e
			if !strings.Contains(s[p:e], Redacted) {
				out = append(out, Span{p, e, "secret-value"})
			}
		}
		return out
	}
	e := p
	for e < len(s) && !valueStop(s[e]) {
		e++
	}
	if e == p {
		return out
	}
	sc.next = e
	if strings.Contains(s[p:e], Redacted) || sc.wide && strings.HasPrefix(s[p:], Redacted) {
		return out
	}
	value := s[p:e]
	name := strings.ToLower(strings.Trim(s[start:p], " \t'\":="))
	if sc.wide {
		name = strings.ToLower(s[start:end])
	}
	if name == "authorization" || name == "proxy-authorization" {
		if !sc.wide && (strings.EqualFold(value, "bearer") || strings.EqualFold(value, "basic")) {
			return out
		}
		if sc.wide && schemes[strings.ToLower(value)] {
			// The scheme word is not the secret; the credential after it is.
			c := e
			for c < len(s) && (s[c] == ' ' || s[c] == '\t') {
				c++
			}
			d := c
			for d < len(s) && !valueStop(s[d]) {
				d++
			}
			if d > c && c > e && !strings.HasPrefix(s[c:], Redacted) && !placeholder(s[c]) {
				out = append(out, Span{c, d, "bearer-token"})
				sc.next = d
			}
			return out
		}
	}
	if sc.wide && strings.HasSuffix(name, "pass") && boolish(value) {
		// "vault_pass": true is a setting, not a password.
		return out
	}
	return append(out, Span{p, e, "secret-value"})
}

// flagTakesSecret reports whether a flag, with its dashes, is one of the few that
// are followed by a secret: exactly --password and its kin, not --password-stdin,
// --no-password, --token-file or a flag that only has one of these words in it.
func flagTakesSecret(flag string) bool {
	name := strings.ToLower(strings.TrimLeft(flag, "-"))
	if len(flag)-len(name) > 2 {
		return false
	}
	switch name {
	case "password", "passwd", "pwd", "passphrase", "token", "secret", "api-key", "apikey", "api_key",
		"access-token", "auth-token", "client-secret":
		return true
	}
	return false
}

// quotedEnd is where the quoted value that starts with the quote at s[p] ends:
// after its closing quote, or 0 when there is none that counts. A backslash
// escapes the byte after it. A value is closed on its own line unless wide, when
// it may run over lines for maxQuotedValue bytes and one with no closing quote
// ends at the end of its line. budget bounds the bytes that all the looks of one
// call may read, so a text of many openers with no closer costs a few passes over
// it and no more.
func (sc *scan) quotedEnd(p int) int {
	s := sc.s
	q := s[p]
	limit := len(s)
	if sc.wide {
		limit = min(limit, p+1+maxQuotedValue)
	}
	if sc.budget > 0 {
		sc.budget -= limit - p
		for i := p + 1; i < limit; i++ {
			switch s[i] {
			case '\\':
				if !sc.wide && (i+1 >= len(s) || s[i+1] == '\n') {
					return 0
				}
				i++
			case '\n':
				if !sc.wide {
					return 0
				}
			case q:
				return i + 1
			}
		}
	}
	if !sc.wide {
		return 0
	}
	if sc.lineEnd < p {
		if n := strings.IndexByte(s[p:], '\n'); n >= 0 {
			sc.lineEnd = p + n
		} else {
			sc.lineEnd = len(s)
		}
	}
	return max(sc.lineEnd, p+1)
}

// findPEM finds private key blocks: from BEGIN to the matching END, or to the end
// of the text for one with none (a clipped output).
func findPEM(s string, out []Span) []Span {
	const begin, end = "-----BEGIN ", "-----END "
	for at := 0; at < len(s); {
		i := strings.Index(s[at:], begin)
		if i < 0 {
			break
		}
		i += at
		h, ok := pemLabel(s, i+len(begin))
		if !ok {
			at = i + len(begin)
			continue
		}
		stop := len(s)
		for from := h; from < len(s); {
			k := strings.Index(s[from:], end)
			if k < 0 {
				break
			}
			k += from
			if e, ok := pemLabel(s, k+len(end)); ok {
				stop = e
				break
			}
			from = k + len(end)
		}
		out = append(out, Span{i, stop, "private-key"})
		at = stop
	}
	return out
}

// pemLabel matches [A-Z0-9 ]*PRIVATE KEY----- at s[from:] and returns the offset
// after it.
func pemLabel(s string, from int) (int, bool) {
	const label = "PRIVATE KEY"
	j := from
	for j < len(s) && (s[j] == ' ' || '0' <= s[j] && s[j] <= '9' || 'A' <= s[j] && s[j] <= 'Z') {
		j++
	}
	// The class stops at the first dash, so the label has to end the run.
	if !strings.HasPrefix(s[j:], "-----") || j-from < len(label) || s[j-len(label):j] != label {
		return 0, false
	}
	return j + len("-----"), true
}

// merge sorts spans and joins the ones that overlap, keeping the kind of the
// first.
func merge(spans []Span) []Span {
	if len(spans) < 2 {
		return spans
	}
	sort.Slice(spans, func(i, j int) bool {
		if spans[i].Start != spans[j].Start {
			return spans[i].Start < spans[j].Start
		}
		return spans[i].End > spans[j].End
	})
	out := spans[:1]
	for _, sp := range spans[1:] {
		last := &out[len(out)-1]
		if sp.Start < last.End {
			last.End = max(last.End, sp.End)
			continue
		}
		out = append(out, sp)
	}
	return out
}

// MaxRedactBytes is the most of a text Redact reads. A text over it is cut there
// (on a character boundary) and the rest is replaced by one mark: a text too long
// to read is not passed on unread.
const MaxRedactBytes = 1 << 20

// Redact removes what looks like a secret from s. It is best effort, by
// patterns: a secret with no recognisable shape, in text that does not name it
// as one, is kept as it was said. Its time is linear in the length of s, up to
// MaxRedactBytes.
func Redact(s string) string {
	for range 4 {
		next := redactOnce(s)
		if next == s {
			break
		}
		s = next
	}
	return s
}

// redactOnce is one round of Redact. Text that one round has changed is read again,
// because what it left can make a pattern that did not match before: a mark that
// ends a quoted value, two pieces of a flag and its value brought together.
func redactOnce(s string) string {
	if len(s) > MaxRedactBytes {
		cut := MaxRedactBytes
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		return redactOnce(s[:cut]) + Redacted
	}
	spans := findOthers(s, true)
	if len(spans) > 0 {
		var b strings.Builder
		at := 0
		for _, sp := range spans {
			b.WriteString(s[at:sp.Start])
			b.WriteString(Redacted)
			at = sp.End
		}
		b.WriteString(s[at:])
		s = b.String()
	}
	// The shaped tokens go one shape after another on what is left, as they always
	// have. Finding them all on the original text and merging (Find does that, for
	// callers that need spans of the text they have) is not the same: a shape that
	// starts or ends at a word boundary, an AWS key or a JWT, sees a boundary where
	// another token that was replaced before it stood glued to it, and does not on
	// the original.
	for _, t := range tokenShapes {
		// ReplaceAllString copies its text even when nothing matches.
		if mayMatch(s, t.kind) && t.re.MatchString(s) {
			s = t.re.ReplaceAllString(s, Redacted)
		}
	}
	return s
}

// RedactValue is Redact for a decoded JSON value: every string in it is
// redacted, and a value under a key that names a secret is replaced whole
// whatever it holds.
func RedactValue(v any) any {
	switch x := v.(type) {
	case string:
		return Redact(x)
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = RedactValue(e)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			if nameIsSecret(k, true) {
				out[k] = Redacted
				continue
			}
			out[k] = RedactValue(e)
		}
		return out
	}
	return v
}

// ClipValue is Clip for every string in a decoded JSON value.
func ClipValue(v any, n int) any {
	switch x := v.(type) {
	case string:
		return Clip(x, n)
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = ClipValue(e, n)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = ClipValue(e, n)
		}
		return out
	}
	return v
}

// mayMatch is a cheap look for the bytes a shape starts with, for the shapes
// whose pattern begins with a word boundary and so is read byte by byte.
func mayMatch(s, kind string) bool {
	switch kind {
	case "aws-key":
		return strings.Contains(s, "AKIA") || strings.Contains(s, "ASIA")
	case "sendgrid-key":
		return strings.Contains(s, "SG.")
	case "jwt":
		return strings.Contains(s, "eyJ")
	}
	return true
}

// KeyIsSecret reports whether Redact and RedactValue replace the value under a
// key of this name whole: SecretName, and also the names passphrase and pass.
func KeyIsSecret(name string) bool { return nameIsSecret(name, true) }

// RedactSpans reports what Redact would replace in s, as spans of s. It is Find
// with the forms Redact takes and Find does not (see findOthers), for a caller
// that has to replace in a text other than the one it looked at.
func RedactSpans(s string) []Span {
	return merge(append(findOthers(s, true), FindTokens(s)...))
}

// compoundBefore reports whether the name l[:i+4] that ends in "pass" has another
// word before it, joined by a separator: DB_PASS, mysql.pass. A bare "pass" is
// too often a word (a test result, a variable) to say what follows it is a secret.
func compoundBefore(l string, i int) bool {
	if i == 0 || isAlnum(l[i-1]) {
		return false
	}
	for j := i - 1; j >= 0; j-- {
		if isAlnum(l[j]) {
			return true
		}
	}
	return false
}

// shortWord reports whether s starts with a word of one to four lower-case
// letters and then a space or the end: "is", "the", "for". A flag followed by one
// is a flag named in a sentence, and a secret that short is not worth the words it
// would cost.
func shortWord(s string) bool {
	n := 0
	for n < len(s) && 'a' <= s[n] && s[n] <= 'z' {
		n++
	}
	return n >= 1 && n <= 4 && (n == len(s) || s[n] == ' ' || s[n] == '\t' || s[n] == '\n')
}

// placeholder reports whether b starts a stand-in for a value and not a value: a
// format verb, a variable or a template field.
func placeholder(b byte) bool {
	return b == '%' || b == '$' || b == '<' || b == '{' || b == '[' || b == '('
}

// boolish reports whether v is a yes or no, a number or an empty value.
func boolish(v string) bool {
	switch strings.ToLower(v) {
	case "true", "false", "null", "nil", "none", "yes", "no", "on", "off":
		return true
	}
	for i := 0; i < len(v); i++ {
		if v[i] < '0' || v[i] > '9' {
			return false
		}
	}
	return true
}
