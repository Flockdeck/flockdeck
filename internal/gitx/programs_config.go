package gitx

import (
	"bytes"
	"strings"
)

// parseSimpleConfig reads a git configuration file that is plain: sections
// written [name] or [name "subsection"], and assignments written name = value,
// with whole-line comments and blank lines between. That is all a submodule's
// git directory holds, and reading it here saves starting git for each of
// hundreds of them.
//
// It reads nothing it is not sure of. Anything else (an [include], an old-style
// [section.subsection], a quote, a backslash, a comment after a value, a value
// continued on the next line, a byte order mark, a NUL) returns false, and the
// caller has git read the file, which understands all of that. What it returns
// is what `git config --list --file` prints: section and name lower-cased, the
// subsection as written, the value without the spaces round it.
func parseSimpleConfig(data []byte, origin string) ([]configEntry, bool) {
	if bytes.IndexByte(data, 0) >= 0 || bytes.HasPrefix(data, []byte("\xef\xbb\xbf")) {
		return nil, false
	}
	var entries []configEntry
	section := ""
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimRight(line, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || trimmed[0] == '#' || trimmed[0] == ';' {
			continue
		}
		if trimmed[0] == '[' {
			sec, ok := parseSection(trimmed)
			if !ok {
				return nil, false
			}
			section = sec
			continue
		}
		if section == "" {
			return nil, false
		}
		name, value, hasValue := strings.Cut(trimmed, "=")
		name = strings.TrimSpace(name)
		if !validName(name) {
			return nil, false
		}
		value = strings.TrimSpace(value)
		if strings.ContainsAny(value, "\"\\#;") {
			return nil, false
		}
		if !hasValue {
			value = ""
		}
		entries = append(entries, configEntry{key: section + "." + strings.ToLower(name), value: value, origin: origin})
	}
	return entries, true
}

// parseSection reads "[name]" or `[name "subsection"]` and returns the key
// prefix git prints for it.
func parseSection(s string) (string, bool) {
	if !strings.HasSuffix(s, "]") {
		return "", false
	}
	inner := s[1 : len(s)-1]
	name, sub, hasSub := strings.Cut(inner, " ")
	if !validName(name) {
		return "", false
	}
	lower := strings.ToLower(name)
	if lower == "include" || lower == "includeif" {
		return "", false
	}
	if !hasSub {
		return lower, true
	}
	sub = strings.TrimSpace(sub)
	if len(sub) < 2 || sub[0] != '"' || sub[len(sub)-1] != '"' {
		return "", false
	}
	sub = sub[1 : len(sub)-1]
	if sub == "" || strings.ContainsAny(sub, "\"\\\n") {
		return "", false
	}
	return lower + "." + sub, true
}

// validName is a section or variable name: letters, digits and dashes, starting
// with a letter.
func validName(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
		case i > 0 && (c >= '0' && c <= '9' || c == '-'):
		default:
			return false
		}
	}
	return true
}
