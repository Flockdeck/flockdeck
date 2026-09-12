package chat

import (
	"strings"
	"testing"
)

// A table is read down its columns, and a row wrapped like prose at the width
// of the pane turns it into a block of pipes. Rows are drawn as written.
func TestTableRowsAreDrawnAsWritten(t *testing.T) {
	var out strings.Builder
	p := newPrinter(&out, 20, false)
	table := "| model   | input | output |\n|---------|-------|--------|\n| opus    | $5    | $25    |\n"
	p.text("Prices:\n" + table + "That is all.\n")
	p.endMessage()

	for _, row := range strings.Split(strings.TrimSuffix(table, "\n"), "\n") {
		if !strings.Contains(out.String(), "\n"+row+"\n") {
			t.Errorf("row %q was not drawn as written:\n%s", row, out.String())
		}
	}
	if !strings.Contains(out.String(), "\nThat is all.") {
		t.Errorf("the prose after the table was not drawn as prose:\n%s", out.String())
	}
}
