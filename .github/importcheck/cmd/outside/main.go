// Command outside reads changed file paths on standard input, one per line, and
// prints those a "ui:" pull request may not touch (anything outside ui/ and
// api/fixtures/). It exits 1 when there are any. The importcheck workflow feeds it
// git diff --name-only.
package main

import (
	"bufio"
	"fmt"
	"os"

	"importcheck"
)

func main() {
	var changed []string
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		changed = append(changed, sc.Text())
	}
	out := importcheck.PathsOutsideUI(changed)
	for _, p := range out {
		fmt.Println(p)
	}
	if len(out) > 0 {
		os.Exit(1)
	}
}
