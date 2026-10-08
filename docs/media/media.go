// Package media holds the recordings that the README and the site show.
//
// The files live here, under docs, so that the repository keeps one copy of
// each: the README links them by path, and cmd/sitegen embeds them from this
// package, because go:embed cannot reach a file outside its own directory.
package media

import _ "embed"

// Demo is flockdeck-demo.gif: about 90 seconds of three Claude Code agents on
// three git worktrees of a small demo project, recorded live in one take. The
// two stretches where the agents are working are played 3x faster; typing and
// everything else is real time.
//
//go:embed flockdeck-demo.gif
var Demo []byte
