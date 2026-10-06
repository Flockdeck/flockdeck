package baton

import "os"

// slowRun says wall-clock limits do not apply: the race detector, or a shared
// CI runner whose speed varies too much, makes a timing assertion fail for
// reasons that have nothing to do with the code. The leak and correctness
// assertions in the same tests still run.
func slowRun() bool { return raceEnabled || os.Getenv("CI") != "" }
