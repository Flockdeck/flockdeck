package chat

import "strings"

// commandNames are the commands command() knows, kept beside it: a name added
// there and not here is still run, only never suggested.
var commandNames = []string{"exit", "quit", "help", "model", "output", "history", "retry", "clear", "status", "forget"}

// nearestCommand is the command a mistyped name was most likely meant to be --
// /modle for /model, /stauts for /status -- or "" when none is within two
// edits of it. A slip of a letter is answered with the command meant, rather
// than with a trip through /help to find it.
func nearestCommand(name string) string {
	name = strings.ToLower(name)
	best, bestDist := "", 3
	for _, c := range commandNames {
		if d := editDistance(name, c); d < bestDist {
			best, bestDist = c, d
		}
	}
	return best
}

// editDistance is how many single-character insertions, deletions and
// substitutions turn a into b.
func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur := make([]int, len(rb)+1)
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(rb)]
}
