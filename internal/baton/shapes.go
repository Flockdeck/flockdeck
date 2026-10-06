package baton

import (
	"regexp"
	"strings"

	"github.com/jmwri/flockdeck/internal/record"
)

// This file is the part of the scrubber's second layer that is the baton's own
// and not internal/record's: forms that matter in a command line or a config
// file pasted into a conversation. They are kept here so that what record
// redacts in a recording stays what docs/recording-format.md says.

// span is a stretch of text to remove, and what kind of thing it was.
type span struct {
	start, end int
	kind       string
}

// authRe is an Authorization header up to the first character of its credential:
// the name, a separator, the scheme word, and the space after it. The scheme
// stays and the credential goes, whatever the scheme is.
//
// Any word after the name is the scheme when the rest looks like a scheme and a
// credential: letters and hyphens only (Token, Custom-Scheme, Zoho-oauthtoken,
// Client-ID), or a known one with digits in it (AWS4-HMAC-SHA256). A single word
// with digits in it ("Authorization: abc123 more") is a credential with no
// scheme, and is left to record's own pattern.
//
// Only what follows the scheme as one run is taken, to a space: a second header
// on the same line is found by its own name, not eaten by the first, and a quote
// inside the credential does not end it. The schemes whose credential is a list
// (see listSchemes) go to the end of the line or to the next header name,
// whichever comes first.
var authRe = regexp.MustCompile(`(?i)\b(?:proxy-)?authorization["']?[ \t]*[:=][ \t]*["']?([A-Za-z][A-Za-z0-9\-]*)([ \t]+)(\S)`)

// authName is the name of an Authorization header, to find where one ends.
var authName = regexp.MustCompile(`(?i)\b(?:proxy-)?authorization\b`)

// nextCredential is a second scheme and credential after a comma:
// "Bearer a, Bearer b".
var nextCredential = regexp.MustCompile(`^[ \t]*([A-Za-z][A-Za-z\-]*)[ \t]+(\S)`)

// schemes are the words with digits in them that an Authorization header puts
// before its credential; any word of letters and hyphens is taken for one too.
var schemes = map[string]bool{"aws4-hmac-sha256": true}

// commonSchemes are the schemes that may follow a comma after a credential.
var commonSchemes = map[string]bool{"bearer": true, "basic": true, "token": true, "digest": true, "apikey": true, "negotiate": true, "ntlm": true, "bot": true}

// listSchemes are the schemes whose credential is a list of name=value pairs,
// and not one token.
var listSchemes = map[string]bool{"digest": true, "hawk": true, "aws4-hmac-sha256": true, "oauth": true, "mac": true}

// schemeWord reports whether a word after Authorization: is a scheme.
func schemeWord(w string) bool {
	if schemes[strings.ToLower(w)] {
		return true
	}
	for i := 0; i < len(w); i++ {
		c := w[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '-') {
			return false
		}
	}
	return w != ""
}

var (
	// redisURL is a redis://:password@host URL, whose empty user name is why
	// record's own pattern, which wants one, does not take it.
	redisURL = regexp.MustCompile(`://:([^@\s/]+)@`)
)

// npmRe is an npm registry credential: //registry.npmjs.org/:_authToken=value,
// or the same with a space, as `npm config set` takes it.
var npmRe = regexp.MustCompile(`(?i):_(?:authToken|auth|password)(?:=|[ \t]+)([^\s"']+)`)

// yamlBlockRe is a YAML key that opens a block scalar (password: > or |).
var yamlBlockRe = regexp.MustCompile(`(?m)^([ \t]*)(?:-[ \t]+)?["']?([\w.\-]+)["']?[ \t]*:[ \t]*([>|][+\-0-9]*)[ \t]*(?:#[^\n]*)?$`)

// envLineRe is a NAME=value line whose value is not quoted, so that its value
// can have spaces in it.
var envLineRe = regexp.MustCompile(`(?m)^[ \t]*(?:export[ \t]+)?([A-Za-z_][A-Za-z0-9_.\-]*)=([^\n"'][^\n]*)$`)

// kvLineRe is a line that is a secret-named key, then : or =, then the rest of the
// line: password: my secret value, secret_key = correct horse battery staple.
var kvLineRe = regexp.MustCompile(`(?m)^[ \t]*(?:-[ \t]+)?(?:export[ \t]+)?["']?([A-Za-z_][A-Za-z0-9_.\-]*)["']?[ \t]*([:=])[ \t]*([^\n]*)$`)

// extraSpans finds what this file's patterns take out of text, and the spans
// of record's own that should not be taken: the scheme words above.
func extraSpans(text string) (found []span, keep [][2]int) {
	// Most text mentions none of what these shapes are about, and each shape is a
	// pass over all of it: a cheap look at the text first keeps the cost where the
	// words are.
	// (The words in a mark, db-password and secret-value, are not words of the text.)
	lower := strings.ToLower(withoutMarks(text))
	named := containsAny(lower, "pass", "pwd", "secret", "token", "key", "auth", "cred", "pw")
	found = append(found, rule("auth", authSpans(text, &keep))...)
	found = append(found, rule("urlcred", urlCredSpans(text))...)
	found = append(found, rule("dbcmd", dbSpans(text))...)
	if strings.Contains(text, "://:") {
		for _, m := range redisURL.FindAllStringSubmatchIndex(text, -1) {
			found = append(found, span{m[2], m[3], "url-credential"})
		}
	}
	if strings.Contains(lower, ":_auth") || strings.Contains(lower, ":_password") {
		for _, m := range npmRe.FindAllStringSubmatchIndex(text, -1) {
			found = append(found, span{m[2], m[3], "registry-token"})
		}
	}
	if !named {
		if strings.Contains(lower, "cookie:") {
			found = append(found, rule("cookie", cookieSpans(text))...)
		}
		found = append(found, rule("provider", providerSpans(text))...)
		found = append(found, rule("barecred", bareCredentialSpans(text))...)
		return found, keep
	}
	found = append(found, rule("json", jsonSpans(text))...)
	blocks, indicators := yamlBlocks(text)
	found, keep = append(found, blocks...), append(keep, indicators...)
	for _, m := range envLineRe.FindAllStringSubmatchIndex(text, -1) {
		if skipRules["envline"] {
			break
		}
		if !record.SecretName(text[m[2]:m[3]]) || strings.HasPrefix(text[m[4]:m[5]], "[REDACTED: ") {
			continue
		}
		end := m[5]
		for end > m[4] && (text[end-1] == ' ' || text[end-1] == '\t' || text[end-1] == '\r') {
			end--
		}
		found = append(found, span{m[4], end, "env-secret"})
	}
	found = append(found, rule("kv", kvSpans(text))...)
	found = append(found, rule("passname", passNameSpans(text))...)
	found = append(found, rule("call", callSpans(text))...)
	found = append(found, rule("form", formSpans(text))...)
	if strings.Contains(text, "export") || strings.Contains(text, "setx") {
		found = append(found, rule("export", exportSpans(text))...)
	}
	if strings.Contains(text, "name:") {
		found = append(found, rule("k8s", k8sSpans(text))...)
	}
	found = append(found, rule("golit", goLitSpans(text))...)
	if strings.Contains(lower, "cookie:") {
		found = append(found, rule("cookie", cookieSpans(text))...)
	}
	found = append(found, rule("literal", literalSpans(text))...)
	found = append(found, rule("provider", providerSpans(text))...)
	found = append(found, rule("barecred", bareCredentialSpans(text))...)
	return found, keep
}

// containsAny reports whether s has any of the words in it.
func containsAny(s string, words ...string) bool {
	for _, w := range words {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}

// authSpans finds the credentials in Authorization headers, and notes the scheme
// words to keep.
func authSpans(text string, keep *[][2]int) []span {
	var found []span
	for _, m := range authRe.FindAllStringSubmatchIndex(text, -1) {
		scheme := text[m[2]:m[3]]
		if !schemeWord(scheme) {
			continue
		}
		*keep = append(*keep, [2]int{m[2], m[3]})
		start := m[6]
		if listSchemes[strings.ToLower(scheme)] {
			end := len(text)
			if nl := strings.IndexByte(text[start:], '\n'); nl >= 0 {
				end = start + nl
			}
			if next := authName.FindStringIndex(text[start:end]); next != nil {
				end = start + next[0]
			}
			end = start + len(strings.TrimRight(text[start:end], " \t\r\"'"))
			found = append(found, span{start, end, "auth-credential"})
			continue
		}
		// One run to a space, with a closing quote that is the header's own left
		// off the end. "Bearer a, Bearer b" is two credentials: after a comma
		// another scheme and credential follow.
		for {
			run := text[start : start+runEnd(text[start:])]
			// Closing quotes and brackets of what the header sits in are not the credential.
			trimmed := strings.TrimRight(run, `"'}])`)
			if trimmed == "" {
				break
			}
			found = append(found, span{start, start + len(trimmed), "auth-credential"})
			if !strings.HasSuffix(trimmed, ",") {
				break
			}
			rest := text[start+len(run):]
			n := nextCredential.FindStringSubmatchIndex(rest)
			// After a comma only the same scheme again, or one that is well known,
			// is a second credential: "then some words" is not.
			if n == nil || !(strings.EqualFold(rest[n[2]:n[3]], scheme) || commonSchemes[strings.ToLower(rest[n[2]:n[3]])]) {
				break
			}
			*keep = append(*keep, [2]int{start + len(run) + n[2], start + len(run) + n[3]})
			start += len(run) + n[4]
		}
	}
	return found
}

// runEnd is where the first run of non-space characters in s ends.
func runEnd(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r' {
			return i
		}
	}
	return len(s)
}

// jsonKeyRe is a JSON key followed by the start of an array or an object.
var jsonKeyRe = regexp.MustCompile(`"((?:[^"\\\n]|\\.)*)"[ \t]*:[ \t]*(?:(?:\[\]|map\[[A-Za-z0-9_.*]*\])[A-Za-z0-9_.*]*)?([\[{(])`)

// jsonPairRe is a two-string array: ["X-Api-Key","value"], as a list of headers
// is written.
var jsonPairRe = regexp.MustCompile(`\[[ \t]*"((?:[^"\\\n]|\\.)*)"[ \t]*,[ \t]*"((?:[^"\\\n]|\\.)*)"[ \t]*\]`)

// jsonLimit bounds how far a bracket is followed to its end.
const jsonLimit = 8192

// jsonSpans finds the strings in an array or object that is the value of a
// secret-named key ({"password":["a","b"]}, {"token":{"value":"x"}}), and the value
// in a [name, value] pair whose name is secret-named. record's pattern takes the
// opening bracket for the value and leaves everything inside.
func jsonSpans(text string) []span {
	var out []span
	for _, m := range jsonKeyRe.FindAllStringSubmatchIndex(text, -1) {
		if !record.SecretName(text[m[2]:m[3]]) || goStruct(text, m[4]) {
			continue
		}
		if end := closingBracket(text, m[4]); end > 0 {
			out = append(out, jsonStrings(text, m[4]+1, end)...)
		}
	}
	for _, m := range jsonPairRe.FindAllStringSubmatchIndex(text, -1) {
		if record.SecretName(text[m[2]:m[3]]) && m[5] > m[4] {
			out = append(out, span{m[4], m[5], "secret-value"})
		}
	}
	return out
}

// closingBracket is where the bracket that opens at open is closed, or -1 if it
// is not within jsonLimit bytes. Strings are skipped, with their escapes.
func closingBracket(text string, open int) int {
	depth := 0
	for i := open; i < len(text) && i < open+jsonLimit; i++ {
		switch text[i] {
		case '"':
			for i++; i < len(text) && text[i] != '"'; i++ {
				if text[i] == '\\' {
					i++
				}
			}
		case '[', '{', '(':
			depth++
		case ']', '}', ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// jsonStrings finds the string values (not the keys) between from and to, and
// returns what is inside each one's quotes.
func jsonStrings(text string, from, to int) []span {
	var out []span
	for i := from; i < to; i++ {
		if text[i] != '"' {
			continue
		}
		start := i + 1
		j := start
		for j < to && text[j] != '"' {
			if text[j] == '\\' {
				j++
			}
			j++
		}
		after := j + 1
		for after < to && (text[after] == ' ' || text[after] == '\t') {
			after++
		}
		isKey := after < to && text[after] == ':'
		if !isKey && j > start {
			out = append(out, span{start, min(j, to), "secret-value"})
		}
		i = j
	}
	return out
}

// secretWords are what a key ends in to be taken by kvSpans.
var secretWords = []string{"password", "passwd", "pwd", "passphrase", "secret", "secret_key", "secretkey", "token", "auth_token",
	"api_key", "apikey", "access_key", "private_key", "auth_key", "credential", "credentials"}

// endsInSecretWord reports whether a key, lower case, ends in one of secretWords:
// the word is the last part of the name and not just somewhere in it.
func endsInSecretWord(name string) bool {
	name = strings.ReplaceAll(name, "-", "_")
	for _, w := range secretWords {
		if strings.HasSuffix(name, w) {
			return true
		}
	}
	return false
}

// kvSpans finds a secret-named key at the start of a line followed by : or = and a
// value of more than one word, which runs to the end of the line or to an
// unquoted " #" that starts a comment: password: my secret value here, token = a b c.
// A value of one word is record's to take. A line that is code is not taken: a
// value that begins with a quote, a bracket, $, = (so ==, := and =>), or is a call
// or a block opener or a statement, is left alone, and so is any line that does
// not start with the key itself (if token == "" {).
func kvSpans(text string) []span {
	var out []span
	for _, m := range kvLineRe.FindAllStringSubmatchIndex(text, -1) {
		name := strings.ToLower(text[m[2]:m[3]])
		// An Authorization header keeps its scheme; authSpans takes its credential.
		// The key has to end in the secret word (db_password, secretKey), so that
		// privateKeySize and masterSecretLength, which only contain one, are not.
		if !record.SecretName(name) || name == "authorization" || name == "proxy-authorization" || !endsInSecretWord(name) {
			continue
		}
		start, val := m[6], text[m[6]:m[7]]
		if i := strings.Index(val, " #"); i >= 0 {
			val = val[:i]
		}
		if i := strings.Index(val, "\t#"); i >= 0 {
			val = val[:i]
		}
		val = strings.TrimRight(val, " \t\r")
		val = strings.TrimRight(strings.TrimSuffix(val, ","), " \t")
		if val == "" || !strings.ContainsAny(val, " \t") || strings.HasPrefix(val, "[REDACTED: ") {
			continue
		}
		if strings.IndexByte("\"'[{<>$=*&|!%", val[0]) >= 0 || strings.ContainsAny(val, "()<>*") ||
			strings.Contains(val, " + ") || strings.Contains(val, "//") || strings.Contains(val, "&&") || strings.Contains(val, "||") ||
			strings.HasSuffix(val, "{") || strings.HasSuffix(val, ";") || strings.HasSuffix(val, ",") {
			continue
		}
		out = append(out, span{start, start + len(val), "env-secret"})
	}
	return out
}

// indentOf is the width of a line's leading space and tabs.
func indentOf(line string) int {
	return len(line) - len(strings.TrimLeft(line, " \t"))
}

// yamlBlocks finds the bodies of block scalars under a secret-named key: the
// indented lines after "password: >" or "private_key: |", to the first line
// that is not indented deeper than the key.
func yamlBlocks(text string) (out []span, indicators [][2]int) {
	covered := 0 // the end of the last block taken
	for _, m := range yamlBlockRe.FindAllStringSubmatchIndex(text, -1) {
		if !record.SecretName(text[m[4]:m[5]]) {
			continue
		}
		// A block inside one already taken is taken with it, and reading it again
		// is what made a deepening run of them quadratic.
		if m[0] < covered {
			indicators = append(indicators, [2]int{m[6], m[7]})
			continue
		}
		indent := m[3] - m[2]
		pos := m[1]
		if pos < len(text) && text[pos] == '\n' {
			pos++
		}
		start, end := -1, -1
		for pos < len(text) {
			nl := strings.IndexByte(text[pos:], '\n')
			lineEnd := len(text)
			if nl >= 0 {
				lineEnd = pos + nl
			}
			line := text[pos:lineEnd]
			if strings.TrimSpace(line) != "" {
				if indentOf(line) <= indent {
					break
				}
				if start < 0 {
					start = pos
				}
				end = lineEnd
			}
			if nl < 0 {
				break
			}
			pos = lineEnd + 1
		}
		if start >= 0 {
			out = append(out, span{start, end, "block-secret"})
			covered = end
			// The > or | is not the secret, whatever record makes of the key.
			indicators = append(indicators, [2]int{m[6], m[7]})
		}
	}
	return out, indicators
}
