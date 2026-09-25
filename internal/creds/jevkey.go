package creds

import (
	"errors"
	"strings"
)

// JevID is the id the TypeSafe (Jev) API key is kept under in the store. It
// starts with "@" so it can never be an agent id: Names leaves it out of the
// list of agent keys, and Set and Clear refuse it, so the agent keys dialog
// and `flockdeck keys` neither show nor change it. It has its own
// functions below.
const JevID = "@typesafe"

func reserved(id string) bool { return strings.HasPrefix(strings.TrimSpace(id), "@") }

// JevKey returns the TypeSafe key set in Settings, or "". Like every read of
// the store, a store that cannot be read is no key. It is the one function
// that returns the value, for the client that sends it to api.typesafe.ai;
// nothing else is handed it.
func JevKey() string { return storedRaw(JevID) }

// HasJevKey reports whether a key is set in Settings, without returning it.
func HasJevKey() bool { return JevKey() != "" }

// SetJevKey stores the TypeSafe key, replacing any there. Errors never carry
// the value.
func SetJevKey(key string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return errors.New("the key is empty")
	}
	storeMu.Lock()
	defer storeMu.Unlock()
	keys, err := load()
	if err != nil {
		return err
	}
	keys[JevID] = key
	return save(keys)
}

// ClearJevKey removes the TypeSafe key and reports whether there was one.
func ClearJevKey() (bool, error) {
	storeMu.Lock()
	defer storeMu.Unlock()
	keys, err := load()
	if err != nil {
		return false, err
	}
	if _, ok := keys[JevID]; !ok {
		return false, nil
	}
	delete(keys, JevID)
	return true, save(keys)
}
