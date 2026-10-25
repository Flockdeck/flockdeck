package changes

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
)

// The check file is written by hand, but it sits in a folder an agent on the
// machine may be able to write to. A pin is the hash of the commands the person
// last looked at in the window and confirmed; a check runs only while the file
// still has that hash. It is a tripwire for a file that changed after the person
// read it. It is not proof against an agent that can also write the pin file or
// send the confirm itself: that needs a credential of the window's own.

const pinFileLimit = 4 << 10

var hashRe = regexp.MustCompile(`^[0-9a-f]{64}$`)

// HashChecks is the hash of the commands that would run: the cleaned list
// LoadChecks returns, in order. A change to a name or a command, or to which
// entries survive the limits, changes it; spacing in the file does not.
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

// PinPath is where the pin for the check file at checksPath is kept.
func PinPath(checksPath string) string {
	ext := filepath.Ext(checksPath)
	return checksPath[:len(checksPath)-len(ext)] + ".pin" + ext
}

// LoadPin reads the confirmed hash. A file that is missing, too large or not a
// hash is no pin, so a damaged pin never lets a check run.
func LoadPin(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, pinFileLimit+1))
	if err != nil || len(data) > pinFileLimit {
		return ""
	}
	var doc struct {
		Hash string `json:"hash"`
	}
	if json.Unmarshal(data, &doc) != nil || !hashRe.MatchString(doc.Hash) {
		return ""
	}
	return doc.Hash
}

// SavePin records hash as the confirmed one, atomically.
func SavePin(path, hash string) error {
	if !hashRe.MatchString(hash) {
		return errors.New("not a hash")
	}
	data, err := json.MarshalIndent(struct {
		Hash string `json:"hash"`
	}{hash}, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(path, data)
}
