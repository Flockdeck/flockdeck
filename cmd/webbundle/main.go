// Command webbundle writes the front end as the server sends it: the modules
// joined into main.js, the stylesheets joined into app.css and the page
// rewritten to match. It exists to look at the result and to measure it
// (scripts/firstload/measure.mjs --bundled); the server makes the same files
// itself, in memory, with internal/webui/bundle.
//
// Usage: go run ./cmd/webbundle -o <empty folder>
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jmwri/flockdeck/internal/webui"
)

func main() {
	out := flag.String("o", "", "folder to write main.js, app.css and index.html into")
	flag.Parse()
	if *out == "" {
		fmt.Fprintln(os.Stderr, "webbundle: -o is required")
		os.Exit(2)
	}
	files, err := webui.Bundled()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(*out, name), data, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
}
