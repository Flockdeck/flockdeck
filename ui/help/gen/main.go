// Command gen writes the files the interface reads its help and key table
// from: src/help.json and src/keys.json, from the pages and the table in
// package help. Run it with `go generate ./help` from ui/.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/jmwri/flockdeck/ui/help"
)

func main() {
	dir := "src"
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	for name, make := range map[string]func() ([]byte, error){
		help.HelpFile: func() ([]byte, error) { return help.JSON(true) },
		help.KeysFile: help.KeysJSON,
	} {
		b, err := make()
		if err != nil {
			fmt.Fprintln(os.Stderr, "gen:", name+":", err)
			os.Exit(1)
		}
		if err := os.WriteFile(filepath.Join(dir, name), append(b, '\n'), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "gen:", err)
			os.Exit(1)
		}
	}
}
