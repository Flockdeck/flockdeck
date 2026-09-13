// linkgone.go answers a window that arrives at a one-time link (see
// server.go's WindowURL) that has already been used, or has run out, with a
// page in the house style rather than a bare refusal -- and says which of
// the two it was, since they read very differently: a link used a moment
// too late is ordinary, where a link already used by the time its own
// window got to it is the one thing in R3.7.2 worth a person's attention.
package server

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/jmwri/flockdeck/internal/store"
)

// linkFate is what became of a link once it left links (see redeemLink and
// expireLink): used, by a window's own load of the page, or run out before
// anything did.
type linkFate int

const (
	fateUsed linkFate = iota
	fateExpired
)

// linkFateEntry is a link's fate, and when it was decided -- kept only long
// enough for a browser arriving at the same link to be told about it; see
// linkFateRetention.
type linkFateEntry struct {
	fate linkFate
	at   time.Time
}

// linkFateRetention is how long a link's fate is remembered after it leaves
// links. A window's own load follows a link within moments of it being
// handed out, so this is generous rather than tight; it exists only to bound
// how much this instance remembers, over however long it keeps running.
const linkFateRetention = 10 * time.Minute

// recordFate notes what became of link, sweeping any fate old enough that
// nothing will ask about it again on its way past.
func (s *Server) recordFate(link string, fate linkFate) {
	s.spentMu.Lock()
	defer s.spentMu.Unlock()
	now := time.Now()
	for l, e := range s.spent {
		if now.Sub(e.at) > linkFateRetention {
			delete(s.spent, l)
		}
	}
	s.spent[link] = linkFateEntry{fate: fate, at: now}
}

// fateOf reports what became of link, and whether anything is remembered
// about it at all: a link this instance never gave out, or whose fate has
// since been swept, comes back false.
func (s *Server) fateOf(link string) (linkFate, bool) {
	s.spentMu.Lock()
	defer s.spentMu.Unlock()
	e, ok := s.spent[link]
	return e.fate, ok
}

// serveLinkGone answers a load at link, which redeemLink has just refused,
// with a page explaining why: that the link was already used -- and that if
// the person reading it did not just open another Flockdeck window, somebody
// else on this computer may have -- that it had simply run out, or, where
// nothing is remembered of it at all, that it is not a link this instance
// recognises. Every case says the same thing to do about it: quit Flockdeck
// and start it again, which ends whatever session the link belonged to,
// along with any it may have been taken by. It is written to error.log too,
// since a window that never opened leaves nobody watching a console for it.
func (s *Server) serveLinkGone(w http.ResponseWriter, r *http.Request, link string) {
	fate, known := s.fateOf(link)
	heading, detail, logWord := linkGoneText(fate, known)
	logNote("window link " + logWord)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusForbidden)
	_, _ = w.Write([]byte(linkGoneHTML(heading, detail)))
}

// linkGoneText is the heading and body serveLinkGone shows for fate, and the
// word it is logged under, distinguishing a link already used from one that
// merely ran out, and either from a link this instance does not recognise at
// all.
func linkGoneText(fate linkFate, known bool) (heading, detail, logWord string) {
	if !known {
		return "This link isn't valid",
			"It may have come from an older run, or somewhere else entirely. " +
				"Quit Flockdeck and start it again to get a new one.",
			"not recognised"
	}
	if fate == fateExpired {
		return "This link has expired",
			"Links like this last about a minute, so that one lying around " +
				"unused for longer is no good to anybody. Quit Flockdeck and " +
				"start it again to open a new window.",
			"expired"
	}
	return "This link has already been used",
		"If you didn't just open another Flockdeck window yourself, someone " +
			"else on this computer may have opened this one first. Quit " +
			"Flockdeck and start it again -- that ends this session, and " +
			"whichever window got to it, along with it.",
		"already used"
}

// linkGoneHTML is the page serveLinkGone answers with, in the house style:
// the same dark palette and system font as the interface itself, since this
// is the one page in Flockdeck's own voice that a person may see before ever
// reaching that interface.
func linkGoneHTML(heading, detail string) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>%s - Flockdeck</title>
<style>
  :root { color-scheme: dark; }
  body {
    margin: 0; min-height: 100vh; display: flex; align-items: center; justify-content: center;
    background: #14161a; color: #d8dee9;
    font: 15px/1.5 "Segoe UI", -apple-system, BlinkMacSystemFont, Ubuntu, "Helvetica Neue", sans-serif;
  }
  main { max-width: 32rem; padding: 2.5rem; }
  h1 { margin: 0 0 .75rem; font-size: 1.25rem; color: #d8dee9; }
  p { margin: 0 0 .75rem; color: #8b93a1; }
  .accent { color: #4c9aff; }
</style>
</head>
<body>
<main>
<h1>%s</h1>
<p>%s</p>
<p class="accent">Flockdeck</p>
</main>
</body>
</html>
`, heading, heading, detail)
}

// logNote appends a timestamped line to error.log in the state directory, as
// logPanic does, and reports nothing back: a window that never opened has
// already been shown why, on the page itself, and this is only for whoever
// goes looking afterwards.
func logNote(line string) {
	dir, err := store.Dir()
	if err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, "error.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = fmt.Fprintf(f, "%s %s\n", time.Now().Format(time.RFC3339), line)
}
