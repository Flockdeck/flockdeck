package bundle

import (
	"bytes"
	"fmt"
)

// Strip removes what a browser does not need from JavaScript and keeps what it
// does: comments, the blank lines, the indentation and trailing spaces, and the
// runs of spaces between tokens, which become one. It never joins two lines and
// never touches a string, a template or a regular expression, so the program
// is the same (a line break can end a statement, so the breaks stay).
//
// It reads a script with a small scanner and refuses what it cannot tell: a
// string left open at the end of its line, a regular expression left open, and a
// "/" after a ")" or "}" that would have to be a regular expression for the rest
// to make sense. A refusal fails a test, not a page.
func Strip(src []byte) ([]byte, error) {
	s := &scanner{src: src}
	if err := s.run(); err != nil {
		return nil, err
	}
	return s.out.Bytes(), nil
}

type scanner struct {
	src  []byte
	i    int
	out  bytes.Buffer
	line int
	// prev says what came before, for "/": a word (identifier, number or
	// keyword), a closing bracket, or another mark.
	prev     byte   // 'w' word, ')' close, 0 none, or the mark itself
	prevWord string // the word, when prev is 'w'
	// braces counts { } in code inside ${ } of a template, one entry per template
	// being read; 0 on the stack top means the ${ is closed by the next }.
	stack []int
	space bool // whitespace was skipped since the last token on this line
}

// A "/" after one of these starts a regular expression and not a division.
var beforeRegex = map[string]bool{
	"return": true, "typeof": true, "case": true, "do": true, "else": true, "in": true, "of": true,
	"instanceof": true, "new": true, "delete": true, "void": true, "throw": true, "yield": true, "await": true,
}

func (s *scanner) fail(msg string) error {
	return fmt.Errorf("strip: line %d: %s", s.line+1, msg)
}

func isWord(c byte) bool {
	return c == '_' || c == '$' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= 0x80
}

// emit writes c, first the one space that stood between this token and the last
// on the line.
func (s *scanner) put(b ...byte) {
	if s.space && s.out.Len() > 0 {
		if last := s.out.Bytes()[s.out.Len()-1]; last != '\n' {
			s.out.WriteByte(' ')
		}
	}
	s.space = false
	s.out.Write(b)
}

func (s *scanner) newline() {
	s.line++
	s.space = false
	if n := s.out.Len(); n > 0 && s.out.Bytes()[n-1] != '\n' {
		s.out.WriteByte('\n')
	}
}

func (s *scanner) run() error {
	for s.i < len(s.src) {
		c := s.src[s.i]
		switch {
		case c == '\n':
			s.i++
			s.newline()
		case c == ' ' || c == '\t' || c == '\r':
			s.i++
			s.space = true
		case c == '/' && s.peek(1) == '/':
			for s.i < len(s.src) && s.src[s.i] != '\n' {
				s.i++
			}
			s.space = true
		case c == '/' && s.peek(1) == '*':
			end := bytes.Index(s.src[s.i+2:], []byte("*/"))
			if end < 0 {
				return s.fail("a comment is not closed")
			}
			body := s.src[s.i : s.i+2+end+2]
			s.i += len(body)
			if n := bytes.Count(body, []byte("\n")); n > 0 {
				for j := 0; j < n; j++ {
					s.newline()
				}
			} else {
				s.space = true
			}
		case c == '"' || c == '\'':
			if err := s.str(c); err != nil {
				return err
			}
			s.prev = 'w'
			s.prevWord = ""
		case c == '`':
			s.put('`')
			s.i++
			if err := s.template(); err != nil {
				return err
			}
			s.prev = 'w'
			s.prevWord = ""
		case c == '/':
			if s.prev == '}' {
				// A block can end here and a regular expression follow it, or an
				// object literal can and a division. Neither is written in this
				// front end; refuse to guess.
				return s.fail("a / after a } could be a division or a regular expression")
			}
			if s.regexAllowed() {
				if err := s.regex(); err != nil {
					return err
				}
				s.prev = 'w'
				s.prevWord = ""
			} else {
				s.put('/')
				s.i++
				s.prev = '/'
			}
		case isWord(c):
			j := s.i
			for j < len(s.src) && isWord(s.src[j]) {
				j++
			}
			s.put(s.src[s.i:j]...)
			s.prevWord = string(s.src[s.i:j])
			s.prev = 'w'
			s.i = j
		default:
			if len(s.stack) > 0 {
				if c == '{' {
					s.stack[len(s.stack)-1]++
				} else if c == '}' {
					if s.stack[len(s.stack)-1] == 0 {
						// The end of ${ }: back to the template's text.
						s.stack = s.stack[:len(s.stack)-1]
						s.put('}')
						s.i++
						if err := s.template(); err != nil {
							return err
						}
						s.prev = 'w'
						s.prevWord = ""
						continue
					}
					s.stack[len(s.stack)-1]--
				}
			}
			s.put(c)
			s.i++
			s.prev = c
		}
	}
	if len(s.stack) > 0 {
		return s.fail("a template is not closed")
	}
	return nil
}

func (s *scanner) peek(n int) byte {
	if s.i+n < len(s.src) {
		return s.src[s.i+n]
	}
	return 0
}

// regexAllowed says whether a "/" here starts a regular expression.
func (s *scanner) regexAllowed() bool {
	switch s.prev {
	case 0:
		return true
	case 'w':
		return beforeRegex[s.prevWord]
	case ')', ']':
		return false
	}
	return true
}

func (s *scanner) str(q byte) error {
	start := s.i
	s.i++
	for s.i < len(s.src) {
		switch s.src[s.i] {
		case '\\':
			s.i += 2
			continue
		case '\n':
			return s.fail("a string is left open at the end of its line")
		case q:
			s.i++
			s.put(s.src[start:s.i]...)
			return nil
		}
		s.i++
	}
	return s.fail("a string is not closed")
}

// template reads template text from s.i up to the closing backtick, or up to a
// "${", where it hands back to the scanner with the template pushed.
func (s *scanner) template() error {
	start := s.i
	flush := func() {
		s.out.Write(s.src[start:s.i])
		for _, b := range s.src[start:s.i] {
			if b == '\n' {
				s.line++
			}
		}
	}
	for s.i < len(s.src) {
		switch c := s.src[s.i]; {
		case c == '\\':
			s.i += 2
		case c == '`':
			s.i++
			flush()
			return nil
		case c == '$' && s.peek(1) == '{':
			s.i += 2
			flush()
			s.stack = append(s.stack, 0)
			return nil
		default:
			s.i++
		}
	}
	return s.fail("a template is not closed")
}

func (s *scanner) regex() error {
	start := s.i
	s.i++
	inClass := false
	for s.i < len(s.src) {
		switch s.src[s.i] {
		case '\\':
			s.i += 2
			continue
		case '\n':
			return s.fail("a regular expression is left open at the end of its line")
		case '[':
			inClass = true
		case ']':
			inClass = false
		case '/':
			if !inClass {
				s.i++
				for s.i < len(s.src) && isWord(s.src[s.i]) {
					s.i++
				}
				s.put(s.src[start:s.i]...)
				return nil
			}
		}
		s.i++
	}
	return s.fail("a regular expression is not closed")
}
