package server

import (
	"os"
	"strings"

	"github.com/jmwri/flockdeck/internal/creds"
	"github.com/jmwri/flockdeck/internal/jev"
)

// jevKeyMsg answers the TypeSafe key field in Settings. It says whether a key
// is set and where it would come from, and has nowhere to put the key: not the
// value, and not its last characters.
type jevKeyMsg struct {
	Type string `json:"type"`
	// Set is true when a key is set in Settings.
	Set bool `json:"set"`
	// Env is true when TYPESAFE_API_KEY is set in the environment, which is
	// used when nothing is set in Settings.
	Env bool `json:"env"`
}

func jevKeyState() jevKeyMsg {
	return jevKeyMsg{
		Type: "jevKey",
		Set:  creds.HasJevKey(),
		Env:  strings.TrimSpace(os.Getenv(jev.KeyEnv)) != "",
	}
}

// jevKeyAtTheDesk refuses a window reached through the relay that asked to
// read, set or clear the TypeSafe key, and reports whether it did. The relay
// decrypts what passes through it, so a key typed on a phone would be read on
// its way; like the agent keys, this one is handled on the machine that uses
// it.
func jevKeyAtTheDesk(c *controlClient) bool {
	if !c.remote {
		return false
	}
	c.notify("the TypeSafe key is set on the machine flockdeck runs on — one typed in a window reached through the relay passes through the relay, which can read it", true)
	return true
}

// jevKey handles the "jevKey" command: kind "get" reports the state, "set"
// stores cmd text as the key, "clear" forgets it. Setting a key turns nothing
// on: the two switches that send anything to TypeSafe are separate.
func (s *Server) jevKey(c *controlClient, kind, text string) {
	if jevKeyAtTheDesk(c) {
		return
	}
	go func() {
		defer s.surviveFor(c, "the TypeSafe key")
		switch kind {
		case "set":
			keyWrites.Lock()
			err := creds.SetJevKey(text)
			keyWrites.Unlock()
			if err != nil {
				c.notify("could not save the TypeSafe key: "+err.Error(), true)
			} else {
				c.notify("saved the TypeSafe key. Nothing is sent to TypeSafe until you turn on a setting that uses it", false)
			}
		case "clear":
			keyWrites.Lock()
			had, err := creds.ClearJevKey()
			keyWrites.Unlock()
			switch {
			case err != nil:
				c.notify("could not clear the TypeSafe key: "+err.Error(), true)
			case had:
				c.notify("cleared the TypeSafe key", false)
			default:
				c.notify("there was no TypeSafe key set in Settings to clear", false)
			}
		}
		c.sendJSON(jevKeyState())
	}()
}
