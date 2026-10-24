package help

import "encoding/json"

// The help pages and the key table as the interface reads them: two files in
// the interface's own source tree, written by `go generate` (see gen), so the
// page can be drawn from its own files and the server need not serve them.
// A test fails when either is out of date.
const (
	// HelpFile is the pages as nodes the interface builds elements from.
	HelpFile = "help.json"
	// KeysFile is the table of actions with their default bindings.
	KeysFile = "keys.json"
)

// JSON is what /help.json answers with: every page, as HTML text, or with tree
// the same pages as nodes with no HTML in them. The program hands it to the
// server (server.Config.HelpJSON).
func JSON(tree bool) ([]byte, error) {
	if tree {
		pages, err := TreePages()
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"pages": pages})
	}
	pages, err := Pages()
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"pages": pages})
}

// KeysJSON is the key table, as a JSON array in table order.
func KeysJSON() ([]byte, error) { return json.Marshal(Keys) }
