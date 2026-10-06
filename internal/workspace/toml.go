package workspace

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// This file reads the subset of TOML that a config file of a CLI agent uses, to find out
// where it sends what it is given. It is not a validator of every rule of TOML, but what it
// accepts it reads as TOML does: tables and arrays of tables, dotted and quoted keys,
// basic, literal and multi-line strings, arrays that run over lines, inline tables,
// comments, CRLF line ends and a byte order mark. Anything it cannot read is an error, and
// the caller takes an error to mean the company is not known.

// tomlRaw is a value that is not a string, a boolean, an array or a table: a number or a
// date. Only its text is kept.
type tomlRaw string

type tomlParser struct {
	s string
	i int
}

var errTOML = errors.New("not TOML that can be read")

func (p *tomlParser) fail(format string, args ...any) error {
	line := 1 + strings.Count(p.s[:min(p.i, len(p.s))], "\n")
	return fmt.Errorf("line %d: %s: %w", line, fmt.Sprintf(format, args...), errTOML)
}

// parseTOML reads a document into nested maps. A table that is an array of tables is a
// []any of maps.
func parseTOML(text string) (map[string]any, error) {
	text = strings.TrimPrefix(text, string([]byte{0xef, 0xbb, 0xbf}))
	if !utf8.ValidString(text) {
		return nil, fmt.Errorf("not valid UTF-8: %w", errTOML)
	}
	p := &tomlParser{s: text}
	root := map[string]any{}
	cur := root
	for {
		p.skipBlank()
		if p.i >= len(p.s) {
			return root, nil
		}
		switch {
		case strings.HasPrefix(p.s[p.i:], "[["):
			p.i += 2
			path, err := p.keyPath()
			if err != nil {
				return nil, err
			}
			if !strings.HasPrefix(p.s[p.i:], "]]") {
				return nil, p.fail("expected ]]")
			}
			p.i += 2
			if err := p.endOfLine(); err != nil {
				return nil, err
			}
			t, err := newArrayTable(root, path)
			if err != nil {
				return nil, p.fail("%v", err)
			}
			cur = t
		case p.s[p.i] == '[':
			p.i++
			path, err := p.keyPath()
			if err != nil {
				return nil, err
			}
			if p.i >= len(p.s) || p.s[p.i] != ']' {
				return nil, p.fail("expected ]")
			}
			p.i++
			if err := p.endOfLine(); err != nil {
				return nil, err
			}
			t, err := openTable(root, path)
			if err != nil {
				return nil, p.fail("%v", err)
			}
			cur = t
		default:
			path, err := p.keyPath()
			if err != nil {
				return nil, err
			}
			p.skipSpace()
			if p.i >= len(p.s) || p.s[p.i] != '=' {
				return nil, p.fail("expected =")
			}
			p.i++
			p.skipSpace()
			v, err := p.value(0)
			if err != nil {
				return nil, err
			}
			if err := p.endOfLine(); err != nil {
				return nil, err
			}
			if err := setPath(cur, path, v); err != nil {
				return nil, p.fail("%v", err)
			}
		}
	}
}

// openTable finds or makes the table [a.b.c] names. A segment that is an array of tables
// goes to its last element.
func openTable(root map[string]any, path []string) (map[string]any, error) {
	cur := root
	for _, seg := range path {
		switch x := cur[seg].(type) {
		case nil:
			t := map[string]any{}
			cur[seg] = t
			cur = t
		case map[string]any:
			cur = x
		case []any:
			if len(x) == 0 {
				return nil, errors.New("an empty array where a table is")
			}
			last, ok := x[len(x)-1].(map[string]any)
			if !ok {
				return nil, errors.New("an array that is not of tables where a table is")
			}
			cur = last
		default:
			return nil, fmt.Errorf("%q is a value and a table", seg)
		}
	}
	return cur, nil
}

// newArrayTable adds a table to the array of tables [[a.b]] names.
func newArrayTable(root map[string]any, path []string) (map[string]any, error) {
	parent, err := openTable(root, path[:len(path)-1])
	if err != nil {
		return nil, err
	}
	last := path[len(path)-1]
	t := map[string]any{}
	switch x := parent[last].(type) {
	case nil:
		parent[last] = []any{t}
	case []any:
		parent[last] = append(x, t)
	default:
		return nil, fmt.Errorf("%q is a table or a value and an array of tables", last)
	}
	return t, nil
}

// setPath sets a dotted key under cur, making the tables on the way.
func setPath(cur map[string]any, path []string, v any) error {
	for _, seg := range path[:len(path)-1] {
		switch x := cur[seg].(type) {
		case nil:
			t := map[string]any{}
			cur[seg] = t
			cur = t
		case map[string]any:
			cur = x
		default:
			return fmt.Errorf("%q is a value and a table", seg)
		}
	}
	last := path[len(path)-1]
	if _, dup := cur[last]; dup {
		return fmt.Errorf("%q is set twice", last)
	}
	cur[last] = v
	return nil
}

// skipSpace skips spaces and tabs.
func (p *tomlParser) skipSpace() {
	for p.i < len(p.s) && (p.s[p.i] == ' ' || p.s[p.i] == '\t') {
		p.i++
	}
}

// skipComment skips a comment to the end of the line, not the line end itself.
func (p *tomlParser) skipComment() {
	if p.i < len(p.s) && p.s[p.i] == '#' {
		for p.i < len(p.s) && p.s[p.i] != '\n' {
			p.i++
		}
	}
}

// skipBlank skips white space, line ends and comments.
func (p *tomlParser) skipBlank() {
	for p.i < len(p.s) {
		switch p.s[p.i] {
		case ' ', '\t', '\n', '\r':
			p.i++
		case '#':
			p.skipComment()
		default:
			return
		}
	}
}

// endOfLine takes what may follow a value or a header: spaces, a comment, then a line end.
func (p *tomlParser) endOfLine() error {
	p.skipSpace()
	p.skipComment()
	if p.i >= len(p.s) {
		return nil
	}
	if p.s[p.i] == '\n' {
		p.i++
		return nil
	}
	if strings.HasPrefix(p.s[p.i:], "\r\n") {
		p.i += 2
		return nil
	}
	return p.fail("expected the end of the line")
}

// keyPath reads a key, which may be dotted and quoted: a."b.c".d
func (p *tomlParser) keyPath() ([]string, error) {
	var path []string
	for {
		p.skipSpace()
		k, err := p.key()
		if err != nil {
			return nil, err
		}
		path = append(path, k)
		p.skipSpace()
		if p.i < len(p.s) && p.s[p.i] == '.' {
			p.i++
			continue
		}
		return path, nil
	}
}

func (p *tomlParser) key() (string, error) {
	if p.i >= len(p.s) {
		return "", p.fail("a key was expected")
	}
	switch c := p.s[p.i]; {
	case c == '"':
		return p.basicString()
	case c == '\'':
		return p.literalString()
	}
	start := p.i
	for p.i < len(p.s) {
		c := p.s[p.i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' {
			p.i++
			continue
		}
		break
	}
	if p.i == start {
		return "", p.fail("a key was expected")
	}
	return p.s[start:p.i], nil
}

const tomlMaxDepth = 64

// value reads a value: a string, an array, an inline table, a boolean, or a number or
// date kept as text.
func (p *tomlParser) value(depth int) (any, error) {
	if depth > tomlMaxDepth {
		return nil, p.fail("nested too deeply")
	}
	if p.i >= len(p.s) {
		return nil, p.fail("a value was expected")
	}
	switch {
	case strings.HasPrefix(p.s[p.i:], `"""`):
		return p.multiString('"')
	case strings.HasPrefix(p.s[p.i:], `'''`):
		return p.multiString('\'')
	case p.s[p.i] == '"':
		return p.basicString()
	case p.s[p.i] == '\'':
		return p.literalString()
	case p.s[p.i] == '[':
		return p.array(depth)
	case p.s[p.i] == '{':
		return p.inlineTable(depth)
	}
	start := p.i
	for p.i < len(p.s) && !strings.ContainsRune(" \t\r\n,]}#", rune(p.s[p.i])) {
		p.i++
	}
	tok := p.s[start:p.i]
	switch tok {
	case "":
		return nil, p.fail("a value was expected")
	case "true":
		return true, nil
	case "false":
		return false, nil
	}
	// A date has a space in it, "1979-05-27 07:32:00": the time that follows is part of it.
	if len(tok) == 10 && tok[4] == '-' && tok[7] == '-' && p.i+1 < len(p.s) && p.s[p.i] == ' ' && p.s[p.i+1] >= '0' && p.s[p.i+1] <= '9' {
		for p.i < len(p.s) && !strings.ContainsRune("\r\n,]}#", rune(p.s[p.i])) {
			p.i++
		}
		tok = strings.TrimSpace(p.s[start:p.i])
	}
	if tok[0] != '+' && tok[0] != '-' && tok[0] != '.' && (tok[0] < '0' || tok[0] > '9') && tok != "inf" && tok != "nan" {
		return nil, p.fail("%q is not a value", tok)
	}
	return tomlRaw(tok), nil
}

func (p *tomlParser) array(depth int) (any, error) {
	p.i++ // [
	out := []any{}
	for {
		p.skipBlank()
		if p.i >= len(p.s) {
			return nil, p.fail("an array was not closed")
		}
		if p.s[p.i] == ']' {
			p.i++
			return out, nil
		}
		v, err := p.value(depth + 1)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
		p.skipBlank()
		if p.i >= len(p.s) {
			return nil, p.fail("an array was not closed")
		}
		switch p.s[p.i] {
		case ',':
			p.i++
		case ']':
		default:
			return nil, p.fail("expected , or ]")
		}
	}
}

func (p *tomlParser) inlineTable(depth int) (any, error) {
	p.i++ // {
	out := map[string]any{}
	for {
		p.skipBlank()
		if p.i >= len(p.s) {
			return nil, p.fail("an inline table was not closed")
		}
		if p.s[p.i] == '}' {
			p.i++
			return out, nil
		}
		path, err := p.keyPath()
		if err != nil {
			return nil, err
		}
		p.skipSpace()
		if p.i >= len(p.s) || p.s[p.i] != '=' {
			return nil, p.fail("expected =")
		}
		p.i++
		p.skipSpace()
		v, err := p.value(depth + 1)
		if err != nil {
			return nil, err
		}
		if err := setPath(out, path, v); err != nil {
			return nil, p.fail("%v", err)
		}
		p.skipBlank()
		if p.i >= len(p.s) {
			return nil, p.fail("an inline table was not closed")
		}
		switch p.s[p.i] {
		case ',':
			p.i++
		case '}':
		default:
			return nil, p.fail("expected , or }")
		}
	}
}

func (p *tomlParser) literalString() (string, error) {
	p.i++
	start := p.i
	for p.i < len(p.s) && p.s[p.i] != '\'' && p.s[p.i] != '\n' {
		p.i++
	}
	if p.i >= len(p.s) || p.s[p.i] != '\'' {
		return "", p.fail("a string was not closed")
	}
	s := p.s[start:p.i]
	p.i++
	if strings.ContainsRune(s, '\r') {
		return "", p.fail("a line end in a string")
	}
	return s, nil
}

func (p *tomlParser) basicString() (string, error) {
	p.i++
	var b strings.Builder
	for p.i < len(p.s) {
		c := p.s[p.i]
		switch {
		case c == '"':
			p.i++
			return b.String(), nil
		case c == '\n' || c == '\r':
			return "", p.fail("a line end in a string")
		case c == '\\':
			if err := p.escape(&b, false); err != nil {
				return "", err
			}
		default:
			b.WriteByte(c)
			p.i++
		}
	}
	return "", p.fail("a string was not closed")
}

// escape reads the backslash sequence at p.i into b. In a multi-line string a backslash at
// the end of a line takes the white space that follows it.
func (p *tomlParser) escape(b *strings.Builder, multi bool) error {
	p.i++ // the backslash
	if p.i >= len(p.s) {
		return p.fail("an escape was cut off")
	}
	c := p.s[p.i]
	p.i++
	switch c {
	case 'b':
		b.WriteByte('\b')
	case 't':
		b.WriteByte('\t')
	case 'n':
		b.WriteByte('\n')
	case 'f':
		b.WriteByte('\f')
	case 'r':
		b.WriteByte('\r')
	case '"':
		b.WriteByte('"')
	case '\\':
		b.WriteByte('\\')
	case 'u', 'U':
		n := 4
		if c == 'U' {
			n = 8
		}
		if p.i+n > len(p.s) {
			return p.fail("an escape was cut off")
		}
		r, err := strconv.ParseUint(p.s[p.i:p.i+n], 16, 32)
		if err != nil || !utf8.ValidRune(rune(r)) {
			return p.fail("a bad escape")
		}
		b.WriteRune(rune(r))
		p.i += n
	case ' ', '\t', '\r', '\n':
		if !multi {
			return p.fail("a bad escape")
		}
		// a line-ending backslash: only white space then a line end may follow it
		j := p.i - 1
		for j < len(p.s) && (p.s[j] == ' ' || p.s[j] == '\t') {
			j++
		}
		if j < len(p.s) && p.s[j] == '\r' {
			j++
		}
		if j >= len(p.s) || p.s[j] != '\n' {
			return p.fail("a bad escape")
		}
		for j < len(p.s) && strings.ContainsRune(" \t\r\n", rune(p.s[j])) {
			j++
		}
		p.i = j
	default:
		return p.fail("a bad escape")
	}
	return nil
}

// multiString reads """...""" or ”'...”': a line end straight after the opening quotes
// is dropped, and up to two quotes may be just before the closing three.
func (p *tomlParser) multiString(q byte) (string, error) {
	p.i += 3
	if strings.HasPrefix(p.s[p.i:], "\r\n") {
		p.i += 2
	} else if p.i < len(p.s) && p.s[p.i] == '\n' {
		p.i++
	}
	var b strings.Builder
	for p.i < len(p.s) {
		c := p.s[p.i]
		if c == q {
			run := 0
			for p.i+run < len(p.s) && p.s[p.i+run] == q {
				run++
			}
			if run >= 3 {
				if run > 5 {
					return "", p.fail("too many quotes")
				}
				b.WriteString(strings.Repeat(string(q), run-3))
				p.i += run
				return b.String(), nil
			}
			b.WriteString(strings.Repeat(string(q), run))
			p.i += run
			continue
		}
		if c == '\\' && q == '"' {
			if err := p.escape(&b, true); err != nil {
				return "", err
			}
			continue
		}
		b.WriteByte(c)
		p.i++
	}
	return "", p.fail("a string was not closed")
}
