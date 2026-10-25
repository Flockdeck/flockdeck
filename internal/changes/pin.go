package changes

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// HashChecks is the hash of the commands that would run: the cleaned list
// LoadChecks returns, in order. A change to a name or a command, or to which
// entries survive the limits, changes it; spacing in the file does not. The
// server keeps the hash the person confirmed in memory (docs/changes-api.md,
// section 6).
func HashChecks(defs []CheckDef) string {
	if defs == nil {
		defs = []CheckDef{}
	}
	data, err := json.Marshal(defs)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
