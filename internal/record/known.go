package record

import (
	"strings"
	"sync"
	"sync/atomic"
)

// A stored API key has no shape Redact can know: a vendor's new key format, a
// gateway's token, a pasted string. The workspace registers each key it hands
// to an agent, and Redact then replaces that exact value wherever it appears,
// so an agent that echoes its environment cannot put the key into a recording
// or an export.

// minKnownSecret is the shortest value kept. A shorter one would blot out
// ordinary words.
const minKnownSecret = 8

// maxKnownSecrets bounds the registry; the oldest value goes first.
const maxKnownSecrets = 64

var (
	knownMu   sync.Mutex
	knownList []string
	known     atomic.Pointer[[]string]
)

// RegisterSecret has Redact replace v wherever it occurs from now on. A value
// under minKnownSecret bytes, or one already registered, is ignored.
func RegisterSecret(v string) {
	v = strings.TrimSpace(v)
	if len(v) < minKnownSecret {
		return
	}
	knownMu.Lock()
	defer knownMu.Unlock()
	for _, k := range knownList {
		if k == v {
			return
		}
	}
	knownList = append(knownList, v)
	if len(knownList) > maxKnownSecrets {
		knownList = knownList[len(knownList)-maxKnownSecrets:]
	}
	snap := append([]string(nil), knownList...)
	known.Store(&snap)
}

// redactKnown replaces every registered value in s.
func redactKnown(s string) string {
	p := known.Load()
	if p == nil {
		return s
	}
	for _, v := range *p {
		if strings.Contains(s, v) {
			s = strings.ReplaceAll(s, v, Redacted)
		}
	}
	return s
}
