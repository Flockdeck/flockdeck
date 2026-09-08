package help

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer/html"
)

// The help pages are Markdown so they can be written as prose rather than as
// DOM building code. They are compiled into the binary, so the HTML they
// produce is our own and raw HTML in the source is safe to pass through — it
// is what makes `[[key:…]]` able to expand to a real <kbd>.
var md = goldmark.New(
	goldmark.WithExtensions(extension.Table, extension.Strikethrough),
	goldmark.WithRendererOptions(html.WithUnsafe()),
)

// expand replaces the placeholders that keep a page's shortcuts tied to the
// key table. Anything naming an action that does not exist is an error rather
// than a silent gap, so a renamed action cannot leave stale prose behind.
func expand(src string) (string, error) {
	out, err := expandKeyTables(src)
	if err != nil {
		return "", err
	}
	return expandInlineKeys(out)
}

// expandKeyTables replaces `{{keys}}` with every section and `{{keys:Name}}`
// with one of them.
func expandKeyTables(src string) (string, error) {
	var b strings.Builder
	rest := src
	for {
		i := strings.Index(rest, "{{keys")
		if i < 0 {
			b.WriteString(rest)
			return b.String(), nil
		}
		end := strings.Index(rest[i:], "}}")
		if end < 0 {
			return "", fmt.Errorf("unterminated {{keys placeholder")
		}
		end += i
		b.WriteString(rest[:i])

		arg := strings.TrimPrefix(rest[i+len("{{keys"):end], ":")
		if arg == "" {
			for _, s := range Sections {
				b.WriteString("### " + s + "\n\n")
				b.WriteString(keyTable(InSection(s)))
				b.WriteString("\n")
			}
		} else {
			rows := InSection(arg)
			if len(rows) == 0 {
				return "", fmt.Errorf("{{keys:%s}}: no such section", arg)
			}
			b.WriteString(keyTable(rows))
		}
		rest = rest[end+2:]
	}
}

// expandInlineKeys replaces `[[key:id]]` with the binding for that action.
func expandInlineKeys(src string) (string, error) {
	return expandInlineKeysWith(src, func(keys string) string { return "<kbd>" + keys + "</kbd>" })
}

// expandInlineKeysWith does the same with the binding wrapped however the
// destination wants it: <kbd> for the rendered page, the bare keys for the
// contents summary, which the interface shows as text.
func expandInlineKeysWith(src string, wrap func(string) string) (string, error) {
	var b strings.Builder
	rest := src
	for {
		i := strings.Index(rest, "[[key:")
		if i < 0 {
			b.WriteString(rest)
			return b.String(), nil
		}
		end := strings.Index(rest[i:], "]]")
		if end < 0 {
			return "", fmt.Errorf("unterminated [[key: placeholder")
		}
		end += i
		b.WriteString(rest[:i])

		id := rest[i+len("[[key:") : end]
		k, ok := Lookup(id)
		if !ok {
			return "", fmt.Errorf("[[key:%s]]: no such action", id)
		}
		if k.Keys == "" {
			return "", fmt.Errorf("[[key:%s]]: %q has no binding; name the palette instead", id, k.Label)
		}
		b.WriteString(wrap(k.Keys))
		rest = rest[end+2:]
	}
}

// keyTable renders one group of actions as a Markdown table. Actions with no
// binding are still listed: "in the palette" is where they are, and leaving
// them out is how they came to be undocumented in the first place.
func keyTable(rows []Key) string {
	return keyTableWith(rows, func(keys string) string { return "<kbd>" + keys + "</kbd>" })
}

// keyTableWith renders the same table with the binding wrapped however the
// destination wants it: <kbd> for the help pages, backticks for the README.
func keyTableWith(rows []Key, wrap func(string) string) string {
	var b strings.Builder
	b.WriteString("| Keys | Action |\n| --- | --- |\n")
	for _, k := range rows {
		keys := "Command palette"
		if k.Keys != "" {
			keys = wrap(k.Keys)
		}
		b.WriteString("| " + keys + " | " + escapeCell(k.Label) + " |\n")
	}
	return b.String()
}

// ShortcutsMarkdown renders the whole key table as the README carries it. The
// README is the one copy of this that is not rendered at run time, so a test
// compares the two and can rewrite it.
func ShortcutsMarkdown() string {
	var b strings.Builder
	for i, s := range Sections {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString("### " + s + "\n\n")
		b.WriteString(keyTableWith(InSection(s), func(keys string) string { return "`" + keys + "`" }))
	}
	return b.String()
}

// escapeCell keeps a label containing a pipe from breaking out of its cell.
func escapeCell(s string) string { return strings.ReplaceAll(s, "|", `\|`) }

// toHTML renders expanded Markdown.
func toHTML(src string) (string, error) {
	var buf bytes.Buffer
	if err := md.Convert([]byte(src), &buf); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// plainText reduces rendered HTML to the words in it, which is what the help
// search matches against. It is a reader, not a parser: the input is our own
// rendered output, so tags are well formed and unbalanced angle brackets in
// the prose have already been escaped to entities.
func plainText(htmlText string) string {
	var b strings.Builder
	depth := 0
	for _, r := range htmlText {
		switch {
		case r == '<':
			depth++
			b.WriteByte(' ')
		case r == '>':
			if depth > 0 {
				depth--
			}
		case depth == 0:
			b.WriteRune(r)
		}
	}
	return strings.Join(strings.Fields(unescapeEntities(b.String())), " ")
}

// unescapeEntities undoes the handful of entities the renderer emits, so that
// searching for "don't" or "&" finds the prose that contains them.
func unescapeEntities(s string) string {
	return strings.NewReplacer(
		"&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`, "&#39;", "'", "&#34;", `"`,
	).Replace(s)
}
