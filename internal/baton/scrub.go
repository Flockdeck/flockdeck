package baton

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jmwri/flockdeck/internal/gitx"
	"github.com/jmwri/flockdeck/internal/record"
)

// Mark is what a removed value is replaced with, and MarkRe finds one again in
// text that has been edited since. The kind is kept so the reader knows a value
// was there and roughly what it was.
func Mark(kind string) string { return "[REDACTED: " + kind + "]" }

// ownKinds are the kinds of mark the baton's own patterns make, beside the ones
// internal/record names.
var ownKinds = []string{
	"known-secret", "secret-flag", "key-file-path", "high-entropy",
	"auth-credential", "db-password", "registry-token", "block-secret", "env-secret",
	tooLargeKind,
}

// neutralKind is the kind of a span that is not a secret and is not counted: an
// opener, "[REDACTED: ", with no mark to close it. It is written as the same number
// of characters, with a bracket in place of the square one, "(REDACTED: ", which is no mark and is not
// taken again, so repeating it does not grow a text.
const (
	neutralKind = "neutral"
	neutralMark = "(REDACTED: "
)

// forgedMarkRe is anything shaped like a mark, whether or not its kind is one
// the scrubber makes.
var forgedMarkRe = regexp.MustCompile(`\[REDACTED: [^\]\n]*\]`)

// realMarkRe is a whole string that is a mark the scrubber makes.
var realMarkRe = regexp.MustCompile("^" + markRe.String() + "$")

// MarkKinds is every kind of mark the scrubber makes, and so the only kinds a
// mark in a text can have. A mark with another kind is text that looks like
// one, and is scrubbed as text.
func MarkKinds() []string { return append(record.Kinds(), ownKinds...) }

// markRe finds a mark the scrubber could have made.
var markRe = func() *regexp.Regexp {
	var alt []string
	for _, k := range MarkKinds() {
		alt = append(alt, regexp.QuoteMeta(k))
	}
	return regexp.MustCompile(`\[REDACTED: (?:` + strings.Join(alt, "|") + `)\]`)
}()

// CountMarks is how many removed values a text shows. It counts the marks in
// the text as it stands, so a mark the person deleted from the draft is not
// counted, and one the scrubber made on the final pass is.
func CountMarks(text string) int { return len(findMarks(text)) }

// minExact is the shortest value matched by exact value. A shorter one is a
// word, a port or a flag, and removing it everywhere would wreck the text
// without hiding anything.
const minExact = 8

// Scrubber removes secrets from text in three layers: values it was told are
// secret, tokens with the shape of a provider's key or credential, and long
// high-entropy strings. It is a filter, not a guarantee: a secret it was not
// told, that has no known shape and that does not look random gets through.
type Scrubber struct {
	exact []string
	// oddValues says a told value has an odd space in it, which reads one way as
	// a space and the other as nothing, so both readings of the text are looked at
	// even when the text itself has none.
	oddValues bool
	// deadline, when it is set, is when scrubbing stops: text not reached by then is replaced
	// by a mark and not passed on (see Scrub).
	deadline time.Time
}

// NewScrubber returns a scrubber for the layer-one values given, longest
// first. Values shorter than minExact are ignored.
func NewScrubber(exact ...string) *Scrubber {
	seen := map[string]bool{}
	var vals []string
	for _, v := range exact {
		v = strings.TrimSpace(v)
		if len(v) < minExact || seen[v] {
			continue
		}
		seen[v] = true
		vals = append(vals, v)
	}
	sort.Slice(vals, func(i, j int) bool { return len(vals[i]) > len(vals[j]) })
	sc := &Scrubber{exact: vals}
	for _, v := range vals {
		for _, r := range v {
			if spaceLike(r) {
				sc.oddValues = true
			}
		}
	}
	return sc
}

// EnvValues picks the values of the secret-named variables out of an
// environment in KEY=value form.
func EnvValues(env []string) []string {
	var out []string
	for _, kv := range env {
		name, val, ok := strings.Cut(kv, "=")
		if ok && record.SecretName(name) {
			out = append(out, val)
		}
	}
	return out
}

// EnvFileValues reads the values out of the .env files in dir and in the root of
// the repository dir is in, if that is somewhere else: an agent works in a
// subfolder or a worktree, and the file with the secrets is at the top. They
// are read here to be matched and are never stored: the scrubber keeps them in
// memory for the one run. It runs git, so it must not run on the workspace
// goroutine.
func EnvFileValues(dir string) []string {
	dirs := []string{dir}
	if root, err := gitx.Root(dir); err == nil && filepath.Clean(root) != filepath.Clean(dir) {
		dirs = append(dirs, root)
	}
	return EnvFileValuesIn(dirs...)
}

// EnvFileValuesIn is EnvFileValues for the folders it is given. A trailing
// "# comment" is not part of a value, and a value that is a plain word or a
// number is left out: NODE_ENV=development is not a secret, and removing the
// word from every sentence is a false positive that costs more than it hides.
// Plain here is lower-case letters only up to 20 characters, or digits only.
func EnvFileValuesIn(dirs ...string) []string {
	var out []string
	for _, dir := range dirs {
		files, _ := filepath.Glob(filepath.Join(dir, ".env*"))
		for _, f := range files {
			fi, err := os.Lstat(f)
			if err != nil || !fi.Mode().IsRegular() || fi.Size() > 1<<20 {
				continue
			}
			data, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			for _, line := range strings.Split(string(data), "\n") {
				line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "export "))
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				_, val, ok := strings.Cut(line, "=")
				if !ok {
					continue
				}
				if v := envValue(val); !plainValue(v) {
					out = append(out, v)
				}
			}
		}
	}
	return out
}

// envValue is the value on the right of an assignment in a .env file: what is
// inside the quotes if it is quoted, otherwise up to a "#" that starts a comment.
func envValue(val string) string {
	val = strings.TrimSpace(val)
	if val != "" && (val[0] == '"' || val[0] == '\'') {
		if end := strings.IndexByte(val[1:], val[0]); end >= 0 {
			return val[1 : 1+end]
		}
		return strings.Trim(val, "\"'")
	}
	if i := strings.IndexAny(val, " \t"); i >= 0 {
		if rest := strings.TrimLeft(val[i:], " \t"); strings.HasPrefix(rest, "#") {
			val = val[:i]
		}
	}
	return val
}

// plainValue reports whether a value is too ordinary to remove everywhere.
func plainValue(v string) bool {
	if len(v) < minExact {
		return true
	}
	digits, lower := true, len(v) <= 20
	for i := 0; i < len(v); i++ {
		c := v[i]
		if c < '0' || c > '9' {
			digits = false
		}
		if c < 'a' || c > 'z' {
			lower = false
		}
	}
	return digits || lower
}

// flat is s with its line breaks, and the indent after them, taken out, so a
// key that a terminal or an editor wrapped over two lines is one token again.
// pos[i] is where flat byte i came from in s.
type flat struct {
	text string
	pos  []int
}

func flatten(s string) flat {
	buf := make([]byte, 0, len(s))
	pos := make([]int, 0, len(s))
	afterBreak := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\n' || c == '\r':
			// A backslash that ends a line is a continuation, not part of the
			// value.
			if !afterBreak && len(buf) > 0 && buf[len(buf)-1] == '\\' {
				buf, pos = buf[:len(buf)-1], pos[:len(pos)-1]
			}
			afterBreak = true
			continue
		case afterBreak && (c == ' ' || c == '\t'):
			continue
		}
		afterBreak = false
		buf = append(buf, c)
		pos = append(pos, i)
	}
	return flat{string(buf), pos}
}

// span maps a stretch of the flattened text back to s.
func (f flat) span(start, end int) (int, int) {
	return f.pos[start], f.pos[end-1] + 1
}

// flagRe is a command-line flag that names a secret, with its value after a
// space: --password hunter2, -token abc.
var flagRe = regexp.MustCompile(`(?i)(?:^|\s)(--?[A-Za-z0-9_\-]+)(\s+)("[^"\n]*"|'[^'\n]*'|[^\s"'-][^\s"']*)`)

// keyPathRe is a path to a private key or certificate store.
var keyPathRe = regexp.MustCompile(`(?i)(?:[A-Za-z]:)?[~./\\\w\-]*(?:\bid_(?:rsa|dsa|ecdsa|ed25519)\b|\.(?:pem|key|p12|pfx|jks)\b)`)

// glued is what counts as part of a word stuck to the end of a mark with no
// space between: password=[REDACTED: x]realsecret is one secret, not a mark and
// a word. A slash is not: a URL whose random piece was removed goes on with
// /file after the mark, and scrubbing that again must change nothing.
func glued(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '+' || c == '-'
}

// Limits on what is scrubbed in one call. A text over scrubMaxText has its head and tail
// scrubbed and its middle replaced by a line that says so. The rest is scrubbed as one text:
// in pieces, a key wrapped over a line that is cut between two pieces is not seen whole and is
// kept. What a baton is built from is cut to its own maximum first, so that a text of that
// size is only a baton that was stored or edited.
const (
	scrubMaxText = 1 << 20
	tooLargeKind = "too-large-to-scrub"
	tooLargeMark = "[REDACTED: " + tooLargeKind + "]"
)

// WithDeadline is a scrubber like s that scrubs no text it is given after the time given: the
// text is replaced by "[REDACTED: too-large-to-scrub]", never passed through.
func (s *Scrubber) WithDeadline(t time.Time) *Scrubber {
	c := *s
	c.deadline = t
	return &c
}

func (s *Scrubber) expired() bool { return !s.deadline.IsZero() && time.Now().After(s.deadline) }

// Scrub removes secrets from text and reports what it removed.
//
// It looks at the text as it is matched (see normalise), not as it is written:
// invisible characters are not there, and full-width letters are ASCII. The odd
// spaces (no-break, ideographic) are read both ways, as spaces and as nothing,
// and what either finds is taken. What is found is taken out of the original.
//
// Scrubbing what was scrubbed changes nothing: if one pass leaves something the
// next would take (a mark that now sits next to a word, say), the passes go on to
// a fixed point, at most a few, and the marks are counted in what is returned.
func (s *Scrubber) Scrub(text string) (string, []Redaction) {
	if text == "" {
		return "", nil
	}
	// The deadline is looked at before a text is begun, not during it: a text, at most
	// scrubMaxText, is scrubbed whole.
	if s.expired() {
		return tooLargeMark, []Redaction{{tooLargeKind, 1}}
	}
	if len(text) > scrubMaxText {
		head, tail := text[:scrubMaxText/2], text[len(text)-scrubMaxText/2:]
		if k := strings.LastIndexByte(head, '\n'); k > 0 {
			head = head[:k]
		}
		if k := strings.IndexByte(tail, '\n'); k >= 0 {
			tail = tail[k+1:]
		}
		text = head + "\n[clipped]\n" + tail
	}
	return s.scrubText(text)
}

// scrubText is Scrub for a text that is not too large to scrub whole.
func (s *Scrubber) scrubText(text string) (string, []Redaction) {
	out, reds := s.scrubOnce(text)
	if out == text || !needsSettling(out) {
		return out, reds
	}
	changed := false
	for range 4 {
		next, more := s.scrubOnce(out)
		if next == out {
			break
		}
		_ = more
		out, changed = next, true
	}
	if changed {
		counts := map[string]int{}
		for _, m := range findMarks(out) {
			counts[out[m[0]+len("[REDACTED: "):m[1]-1]]++
		}
		reds = reds[:0]
		for k, n := range counts {
			reds = append(reds, Redaction{k, n})
		}
		SortRedactions(reds)
	}
	return out, reds
}

// needsSettling reports whether a second pass can find what the first left, which
// it can only where a mark touches something: a word stuck to it (a word is read
// as part of what the mark stands for), a word stuck in front of it (a key path
// cut by a token), a command in the text whose words shifted when a mark took one
// of them, or characters that are read two ways (odd spaces, invisible and
// full-width ones). Marks that stand alone between spaces, brackets and line ends,
// which is nearly all of them, need none, so a large text costs one pass.
func needsSettling(s string) bool {
	if strings.Contains(s, neutralMark) {
		return true
	}
	// A text of ordinary size is settled with care; a large one only where a mark
	// plainly touches something, so that an input of megabytes costs one pass.
	careful := len(s) <= carefulLimit
	marks := findMarks(s)
	for _, m := range marks {
		// An odd space, an invisible or a full-width character against a mark is read
		// two ways, and what is read the other way can differ.
		if m[1] < len(s) && s[m[1]] >= 0x80 || m[0] > 0 && s[m[0]-1] >= 0x80 {
			return true
		}
		after := m[1] < len(s) && !quietAfter(s[m[1]])
		// A word in front of a mark, but not a one-letter flag (-p) with its value.
		before := m[0] > 0 && !quietBefore(s[m[0]-1]) && !(s[m[0]-1] != '-' && m[0] >= 2 && s[m[0]-2] == '-')
		if careful {
			// Anything but white space against a mark, a bracket included.
			after = m[1] < len(s) && !isSpaceByte(s[m[1]])
			before = m[0] > 0 && !isSpaceByte(s[m[0]-1]) && s[m[0]-1] != '=' && s[m[0]-1] != ':' && !(s[m[0]-1] != '-' && m[0] >= 2 && s[m[0]-2] == '-')
		}
		if after || before {
			return true
		}
	}
	if len(marks) == 0 {
		return false
	}
	if careful {
		// Where a command reads its words by position (htpasswd), or by the colon in
		// one (curl -u), a mark that took one of them moves the others.
		if containsAny(s, "htpasswd", "curl", "mysql", "sshpass", "redis-cli", "docker", "id_", ".pem", ".key", ".p12", ".pfx", ".jks") {
			return true
		}
		// A mark on a line that names a secret: what is left of the line is read in
		// the light of the mark (Authorization: Authorization: x).
		return markBesideASecretWord(s)
	}
	return containsAny(s, "htpasswd", "id_", ".pem", ".key")
}

// carefulLimit is how long a text may be to get the careful settling.
const carefulLimit = 64 << 10

// markBesideASecretWord reports whether a line that has a mark in it has a word that
// names a secret outside the marks (the kind inside a mark, db-password, does not count).
func markBesideASecretWord(s string) bool {
	for _, line := range strings.Split(s, "\n") {
		if !strings.Contains(line, "[REDACTED") {
			continue
		}
		if containsAny(strings.ToLower(withoutMarks(line)), "pass", "pwd", "secret", "token", "key", "auth", "cred", "bearer") {
			return true
		}
	}
	return false
}

// scrubOnce is one pass of Scrub.
func (s *Scrubber) scrubOnce(text string) (string, []Redaction) {
	if text == "" {
		return text, nil
	}
	// The values the scrubber was told, as they were given, against the text as
	// it was written: a value that has odd characters in it matches text that has
	// the same.
	spans := exactSpans(text, s.exact, "known-secret")
	seen, first := "", true
	var joined, spaced []span
	for _, asSpaces := range []bool{false, true} {
		work, idx := normalise(text, asSpaces)
		if !first && work == seen && !s.oddValues {
			continue // no odd spaces: the second reading is the first
		}
		first, seen = false, work
		orig := func(i int) int {
			if idx == nil {
				return i
			}
			return idx[i]
		}
		// Where a word sits straight after an odd space in the text as written: the
		// reading with those spaces removed puts it straight after what came before.
		afterOdd := func(i int) bool {
			o := orig(i)
			return o > 0 && o <= len(text) && oddSpaceEndsAt(text, o)
		}
		for _, sp := range s.find(work, asSpaces, afterOdd) {
			o := span{orig(sp.start), orig(sp.end), sp.kind}
			if asSpaces {
				spaced = append(spaced, o)
			} else {
				joined = append(joined, o)
			}
		}
	}
	spans = append(spans, withoutRunOns(text, joined, spaced)...)
	spans = append(spans, spaced...)
	if len(spans) == 0 {
		return text, nil
	}
	sort.SliceStable(spans, func(i, j int) bool {
		if spans[i].start != spans[j].start {
			return spans[i].start < spans[j].start
		}
		return spans[i].end > spans[j].end
	})
	var out strings.Builder
	counts := map[string]int{}
	at := 0
	for i := 0; i < len(spans); {
		sp := spans[i]
		end := sp.end
		j := i + 1
		kind := sp.kind
		// A mark is never left with a word stuck to it, whatever the span was: the
		// next scrub would take the word, and scrubbing is meant to be done in one.
		// (An unclosed opener that is only made harmless has nothing stuck to it.)
		for {
			for ; j < len(spans) && spans[j].start < end; j++ {
				end = max(end, spans[j].end)
				if kind == neutralKind {
					kind = spans[j].kind
				}
			}
			if kind == neutralKind {
				break
			}
			e := end
			for e < len(text) && glued(text[e]) {
				e++
			}
			if e == end {
				break
			}
			end = e
		}
		out.WriteString(text[at:sp.start])
		if kind == neutralKind {
			out.WriteString(neutralMark)
		} else {
			out.WriteString(Mark(kind))
			counts[kind]++
		}
		at = end
		i = j
	}
	out.WriteString(text[at:])
	var reds []Redaction
	for k, n := range counts {
		reds = append(reds, Redaction{k, n})
	}
	SortRedactions(reds)
	return out.String(), reds
}

// withoutRunOns drops what the reading with the odd spaces taken out found, when
// the reading with them as spaces ended the same span earlier at an odd space and
// what follows it is a plain word: a word next to a no-break space is a word, and
// not the rest of the token. A tail that has a digit or a symbol in it is kept, as
// a secret split with a no-break space would have.
func withoutRunOns(text string, joined, spaced []span) []span {
	var out []span
	for _, a := range joined {
		runOn := false
		for _, b := range spaced {
			if b.start != a.start || b.end >= a.end {
				continue
			}
			n := oddSpaceAt(text[b.end:])
			if n == 0 || b.end+n > a.end {
				continue
			}
			if tail := text[b.end+n : a.end]; len(tail) >= 2 && len(tail) <= 20 && isLetters(tail) {
				runOn = true
			}
		}
		if !runOn {
			out = append(out, a)
		}
	}
	return out
}

func isLetters(s string) bool {
	for i := 0; i < len(s); i++ {
		if !isLower(s[i]) && !isUpper(s[i]) {
			return false
		}
	}
	return s != ""
}

// exactSpans finds the given values in text, whole, or wrapped over lines.
func exactSpans(text string, values []string, kind string) []span {
	var out []span
	f := flatten(text)
	for _, v := range values {
		for at := 0; ; {
			i := strings.Index(text[at:], v)
			if i < 0 {
				break
			}
			out = append(out, span{at + i, at + i + len(v), kind})
			at += i + len(v)
		}
		fv := strings.Join(strings.Fields(v), "")
		if fv == "" {
			continue
		}
		for at := 0; ; {
			i := strings.Index(f.text[at:], fv)
			if i < 0 {
				break
			}
			a, b := f.span(at+i, at+i+len(fv))
			out = append(out, span{a, b, kind})
			at += i + len(fv)
		}
	}
	return out
}

// normalisedValues is the values the scrubber was told, as they read once they are
// normalised the way text is: a value typed with full-width letters, or with a
// zero width joiner in it, is the value the text has after the same treatment.
func (s *Scrubber) normalisedValues(asSpaces bool) []string {
	var out []string
	for _, v := range s.exact {
		if n, _ := normalise(v, asSpaces); n != v && len(n) >= minExact {
			out = append(out, n)
		}
	}
	return out
}

// find looks for secrets in work, which is text already normalised, and returns
// where they are in work.
func (s *Scrubber) find(work string, asSpaces bool, afterOdd func(int) bool) []span {
	var spans []span
	// What an earlier pass marked is not looked at again, so scrubbing text
	// that was already scrubbed changes nothing. Only the marks this scrubber
	// makes count: anything else shaped like one is text, and is scrubbed.
	marks := findMarks(work)
	add := func(start, end int, kind string) {
		if end <= start {
			return
		}
		// A span that is only the inside of a mark is the mark again. One that
		// is larger than a mark and takes it in is a secret that contains text
		// shaped like a mark, which could have been put there to hide it, and is
		// kept whole.
		// (The marks are in order, so the ones the span touches are found by
		// searching, not by looking at all of them: a text with many marks is not
		// quadratic.)
		for changed := true; changed; {
			changed = false
			k := sort.Search(len(marks), func(i int) bool { return marks[i][1] > start })
			for ; k < len(marks) && marks[k][0] < end; k++ {
				m := marks[k]
				if start >= m[1] || end <= m[0] {
					continue
				}
				if start >= m[0] && end <= m[1] {
					return
				}
				if start > m[0] || end < m[1] {
					start, end = min(start, m[0]), max(end, m[1])
					changed = true
				}
			}
		}
		spans = append(spans, span{start, end, kind})
	}
	// Something shaped like a mark that this scrubber could not have made was
	// put there by somebody: it is a secret, and so is the word stuck to it.
	for _, m := range forgedMarkRe.FindAllStringIndex(work, -1) {
		if realMarkRe.MatchString(work[m[0]:m[1]]) {
			continue
		}
		e := m[1]
		for e < len(work) && glued(work[e]) {
			e++
		}
		spans = append(spans, span{m[0], e, "secret-value"})
	}
	// A "[REDACTED: " that is not the start of a mark this scrubber makes is text
	// somebody wrote. It is taken, so that what is left next to another mark is not
	// read as a mark of a kind it never was the next time round.
	for at := 0; ; {
		i := strings.Index(work[at:], "[REDACTED: ")
		if i < 0 {
			break
		}
		i += at
		at = i + len("[REDACTED: ")
		real := false
		for _, m := range marks {
			if m[0] == i {
				real = true
				break
			}
		}
		if !real {
			spans = append(spans, span{i, at, neutralKind})
		}
	}
	// A word glued to the end of a mark was the rest of what the mark stands for.
	for _, m := range marks {
		// With an odd space between them, the word is next to the mark only in the
		// reading that removed the space, and is not the rest of it.
		if !asSpaces && afterOdd(m[1]) {
			continue
		}
		e := m[1]
		for e < len(work) && glued(work[e]) {
			e++
		}
		add(m[1], e, "secret-value")
	}

	// Layer one: values the scrubber was told, as they read once normalised.
	// (As they were given, they are looked for in the text as written, in Scrub.)
	for _, sp := range exactSpans(work, s.normalisedValues(asSpaces), "known-secret") {
		add(sp.start, sp.end, sp.kind)
	}
	// Plain values need no normalising and are found in the text as written.
	for _, sp := range exactSpans(work, s.exact, "known-secret") {
		add(sp.start, sp.end, sp.kind)
	}

	// Layer two: shapes. Provider-shaped tokens and long random runs are also
	// looked for across a line break; a name that merely suggests a secret is
	// not, since two lines joined can make one that was never there.
	extra, keep := extraSpans(work)
	creds := urlCreds(work)
	for _, sp := range record.Find(work) {
		if skipRules["record"] {
			break
		}
		if mergedWithURLUser(creds, sp.Start, sp.End) {
			continue // the password of the URL is found exactly by urlCredSpans
		}
		if dropped(keep, sp.Start, sp.End) || coversKept(keep, sp.Start, sp.End) || blockIndicator.MatchString(work[sp.Start:sp.End]) || notSecret(work, sp.Start, sp.End, sp.Kind) {
			continue
		}
		add(sp.Start, sp.End, sp.Kind)
	}
	for _, sp := range extra {
		if notSecret(work, sp.start, sp.end, sp.kind) {
			continue
		}
		add(sp.start, sp.end, sp.kind)
	}
	for _, w := range wrappedTokens(work) {
		if skipRules["wrapped"] {
			break
		}
		add(w.start, w.end, w.kind)
	}
	for _, w := range wrappedRuns(work) {
		if skipRules["wrappedruns"] {
			break
		}
		add(w.start, w.end, w.kind)
	}
	for _, m := range flagRe.FindAllStringSubmatchIndex(work, -1) {
		if skipRules["flag"] {
			break
		}
		// A single-dash flag is a short word (-token, -password); a longer one is a
		// flag with its value stuck to it, like -pSecret99, whose value is not the
		// next word.
		name := work[m[2]:m[3]]
		// A flag that takes nothing from the line after it (--password-stdin), or
		// whose value is a file (--password-file) or a variable's name.
		if strings.Contains(work[m[3]:m[6]], "\n") || flagWithoutSecret(name) {
			continue
		}
		if record.SecretName(name) && (strings.HasPrefix(name, "--") || len(name) <= 12) && !notSecret(work, m[6], m[7], "secret-flag") && !(numericValue.MatchString(work[m[6]:m[7]]) && quantityKey(name)) {
			add(m[6], m[7], "secret-flag")
		}
	}
	for _, m := range keyPathRe.FindAllStringIndex(work, -1) {
		if skipRules["keypath"] {
			break
		}
		if strings.HasPrefix(work[m[1]:], ".pub") {
			continue // id_rsa.pub is the public half
		}
		if strings.HasPrefix(work[m[1]:], "[REDACTED: ") {
			continue // id_rsa with a token cut out after it: scrubbed already
		}
		add(m[0], m[1], "key-file-path")
	}

	// Layer three: strings that look random.
	for _, m := range entropySpans(work) {
		if skipRules["entropy"] {
			break
		}
		add(m[0], m[1], "high-entropy")
	}
	return spans
}

// coversKept reports whether [start, end) takes in a range that a baton pattern has
// said to keep (an Authorization scheme word): that pattern is more exact than the
// one that found this span, and takes the credential on its own.
func coversKept(keep [][2]int, start, end int) bool {
	for _, k := range keep {
		if k[0] >= start && k[1] <= end && !(k[0] == start && k[1] == end) {
			return true
		}
	}
	return false
}

// flagWithoutSecret reports whether a flag that names a secret takes no secret: it
// reads it from stdin or a file or the environment.
func flagWithoutSecret(name string) bool {
	n := strings.ToLower(name)
	for _, s := range []string{"-stdin", "-file", "-env", "-prompt", "-path"} {
		if strings.HasSuffix(n, s) {
			return true
		}
	}
	return false
}

// blockIndicator is what opens a YAML block scalar ("password: >" has no value on
// that line, and the > is not one) or the bracket that opens a JSON array or
// object, whose strings are looked at by jsonSpans: record's pattern takes the
// bracket itself for the value.
var blockIndicator = regexp.MustCompile(`^(?:[>|][+\-0-9]*|[\[{])$`)

// dropped reports whether [start, end) is one of the spans to keep.
func dropped(keep [][2]int, start, end int) bool {
	for _, k := range keep {
		if k[0] == start && k[1] == end {
			return true
		}
	}
	return false
}

// ScrubBaton scrubs every field of a baton that carries text (its title, its
// header fields, its lineage and each section), strips terminal escapes from
// them, and recounts what the text shows. It is run when a baton is built and
// again on the final text just before it is delivered, because a person's edits
// and pastes can bring a secret back.
func (s *Scrubber) ScrubBaton(b Baton) Baton {
	one := func(t string) string {
		t, _ = s.Scrub(CleanText(t))
		return t
	}
	// The header is short and bounded (each field is cut to 4 KB first), and is scrubbed with
	// no deadline: the id, the folder and the branch must not become a mark because the
	// sections took the time.
	hs := s.WithDeadline(time.Time{})
	head := func(t string) string {
		t, _ = hs.Scrub(CleanText(clipRaw(t, 4096)))
		return t
	}
	b.ID = head(b.ID)
	b.Title = head(b.Title)
	b.FromPane, b.FromAgent, b.FromModel = head(b.FromPane), head(b.FromAgent), head(b.FromModel)
	b.Cwd, b.Branch, b.BaseCommit = head(b.Cwd), head(b.Branch), head(b.BaseCommit)
	if b.Derived != nil {
		d := make([]string, len(b.Derived))
		for i, x := range b.Derived {
			d[i] = head(x)
		}
		b.Derived = d
	}
	m := make(map[Section]string, len(b.Sections))
	for sec, text := range b.Sections {
		m[sec] = one(text)
	}
	b.Sections = m
	b.Redactions = countKinds(b)
	return b
}

// countKinds tallies the marks in a baton's text by kind.
func countKinds(b Baton) []Redaction {
	counts := map[string]int{}
	tally := func(t string) {
		for _, m := range markRe.FindAllString(t, -1) {
			counts[strings.TrimSuffix(strings.TrimPrefix(m, "[REDACTED: "), "]")]++
		}
	}
	for _, t := range []string{b.ID, b.Title, b.FromPane, b.FromAgent, b.FromModel, b.Cwd, b.Branch, b.BaseCommit} {
		tally(t)
	}
	for _, t := range b.Derived {
		tally(t)
	}
	for _, t := range b.Sections {
		tally(t)
	}
	var out []Redaction
	for k, n := range counts {
		out = append(out, Redaction{k, n})
	}
	SortRedactions(out)
	return out
}

func isSpaceByte(c byte) bool { return c == ' ' || c == '\n' || c == '\t' || c == '\r' }

// withoutMarks is s with its marks taken out.
func withoutMarks(s string) string {
	marks := findMarks(s)
	if len(marks) == 0 {
		return s
	}
	var b strings.Builder
	at := 0
	for _, m := range marks {
		b.WriteString(s[at:m[0]])
		at = m[1]
	}
	b.WriteString(s[at:])
	return b.String()
}

// markKinds is MarkKinds as a set.
var markKinds = func() map[string]bool {
	m := map[string]bool{}
	for _, k := range MarkKinds() {
		m[k] = true
	}
	return m
}()

// findMarks finds the marks the scrubber makes in s, as markRe would, but by
// looking for the opener and reading the kind up to the bracket: the pattern with
// every kind in it is slow on a text that has many marks.
func findMarks(s string) [][2]int {
	var out [][2]int
	const open = "[REDACTED: "
	for at := 0; at < len(s); {
		i := strings.Index(s[at:], open)
		if i < 0 {
			break
		}
		i += at
		from := i + len(open)
		end := from
		for end < len(s) && end-from <= 32 && (s[end] >= 'a' && s[end] <= 'z' || s[end] >= '0' && s[end] <= '9' || s[end] == '-') {
			end++
		}
		if end < len(s) && s[end] == ']' && markKinds[s[from:end]] {
			out = append(out, [2]int{i, end + 1})
			at = end + 1
			continue
		}
		at = i + 1
	}
	return out
}

// quietAfter is what may follow a mark without a second look: white space, another
// mark, and punctuation that ends a value.
func quietAfter(c byte) bool {
	return isSpaceByte(c) || c == '[' || c == '"' || c == '\x27' || c == ')' || c == ']' || c == '}' || c == '>' || c == ',' || c == ';' || c == '@' || c == '/'
}

// quietBefore is what may sit in front of a mark without a second look: white space
// and the characters that introduce a value.
func quietBefore(c byte) bool {
	return isSpaceByte(c) || c == '=' || c == ':' || c == '(' || c == '[' || c == '{' || c == '<' || c == '"' || c == '\x27' || c == ',' || c == '/' || c == '@' || c == ']'
}
