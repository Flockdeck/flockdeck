package record

import "github.com/jmwri/flockdeck/internal/review"

// secretFile is review's name check for a secret file.
func secretFile(arg string) bool { return review.SecretPath(arg) }
