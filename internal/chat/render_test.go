package chat

import (
	"strings"
	"testing"
)

func TestPrinterDrawsAnAnswer(t *testing.T) {
	tests := []struct {
		name   string
		text   string
		width  int
		colour bool
		want   string
	}{
		{
			name:  "wraps to the width",
			text:  "one two three four five six\n",
			width: 20,
			want:  "one two three four\nfive six\n",
		},
		{
			name:  "a word longer than the line is left whole",
			text:  "aaaaaaaaaaaaaaaaaaaaaaaaa tail\n",
			width: 20,
			want:  "aaaaaaaaaaaaaaaaaaaaaaaaa\ntail\n",
		},
		{
			name:  "a heading loses its marker",
			text:  "## Title\nbody\n",
			width: 40,
			want:  "Title\nbody\n",
		},
		{
			name:   "a heading is bold",
			text:   "# Title\n",
			width:  40,
			colour: true,
			want:   "\x1b[1mTitle\x1b[0m\n",
		},
		{
			name:  "a hash that is part of a word stays",
			text:  "#include <stdio.h>\n",
			width: 40,
			want:  "#include <stdio.h>\n",
		},
		{
			name:  "code keeps its own line breaks and loses its fence",
			text:  "```go\nif x {\n\treturn 1\n}\n```\ndone\n",
			width: 10,
			want:  "if x {\n\treturn 1\n}\ndone\n",
		},
		{
			name:   "emphasis spans the words it opened on",
			text:   "**two words** after\n",
			width:  40,
			colour: true,
			want:   "\x1b[1mtwo\x1b[0m \x1b[1mwords\x1b[0m after\n",
		},
		{
			name:   "inline code is picked out",
			text:   "run `make check` now\n",
			width:  40,
			colour: true,
			want:   "run \x1b[36mmake\x1b[0m \x1b[36mcheck\x1b[0m now\n",
		},
		{
			name:  "runs of blank lines become one",
			text:  "a\n\n\n\nb\n",
			width: 40,
			want:  "a\n\nb\n",
		},
		{
			name:  "a leading blank line draws nothing",
			text:  "\n\nfirst\n",
			width: 40,
			want:  "first\n",
		},
		{
			name:  "carriage returns are not columns",
			text:  "a\r\nb\r\n",
			width: 40,
			want:  "a\nb\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf strings.Builder
			p := newPrinter(&buf, tc.width, tc.colour)
			p.text(tc.text)
			p.endMessage()
			if got := buf.String(); got != tc.want {
				t.Errorf("drew %q, want %q", got, tc.want)
			}
		})
	}
}

// TestPrinterDrawsTheSameTextHoweverItArrives is the property that matters most
// for something fed by a stream: the answer must not depend on where the
// network happened to split it.
func TestPrinterDrawsTheSameTextHoweverItArrives(t *testing.T) {
	const text = "## Plan\n\nFirst do **this**, then run `make check` in a very long line " +
		"that has to wrap at least once.\n\n```sh\nmake check\n```\n\nThen say so.\n"

	var whole strings.Builder
	p := newPrinter(&whole, 30, true)
	p.text(text)
	p.endMessage()

	for _, size := range []int{1, 2, 3, 7, 13} {
		var buf strings.Builder
		p := newPrinter(&buf, 30, true)
		for i := 0; i < len(text); i += size {
			end := i + size
			if end > len(text) {
				end = len(text)
			}
			p.text(text[i:end])
		}
		p.endMessage()
		if buf.String() != whole.String() {
			t.Errorf("in chunks of %d it drew\n%q\nwant\n%q", size, buf.String(), whole.String())
		}
	}
}

func TestPrinterClosesAFenceTheModelLeftOpen(t *testing.T) {
	var buf strings.Builder
	p := newPrinter(&buf, 40, false)
	p.text("```\nhalf a line")
	p.endMessage()
	if got := buf.String(); got != "half a line\n" {
		t.Errorf("drew %q, want the unfinished line", got)
	}
}

func TestPrinterNeverLeavesAStyleOpen(t *testing.T) {
	var buf strings.Builder
	p := newPrinter(&buf, 40, true)
	p.text("**bold and then the stream stops")
	p.endMessage()
	got := buf.String()
	if !strings.HasSuffix(strings.TrimRight(got, "\n"), ansiReset) {
		t.Errorf("drew %q, which leaves the terminal styled", got)
	}
}
