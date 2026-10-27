package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jmwri/flockdeck/internal/gitx"
)

// structuredFiles answers the @ picker of a structured pane's prompt box with
// file names under the pane's working directory. It reads names only, never
// contents. In a git working tree the list is what git lists as tracked or not
// ignored, so .gitignore is honoured as git honours it; elsewhere, or when that
// list is too large to hold, a bounded walk of the directory is used.
//
// What a hostile directory can do is bounded here: how many entries are read,
// how much git may write, how long it takes, how many names go back and how long
// each is. A symlink is never followed or listed, and a name is sent only if it
// resolves to a regular file inside the pane's directory.

const (
	// structuredFilesMax is how many names one answer carries.
	structuredFilesMax = 50
	// structuredFilesGitBytes is the most of git's listing that is held.
	structuredFilesGitBytes = 4 << 20
	// structuredFilesQueryCut and structuredFilesPathMax bound the query and one name, in bytes.
	structuredFilesQueryCut = 200
	structuredFilesPathMax  = 300
	// structuredFilesTimeout bounds the whole listing.
	structuredFilesTimeout = 5 * time.Second

	structuredFilesRateLimit  = 40
	structuredFilesRateWindow = 10 * time.Second

	structuredFilesBusy  = "Too many file lists asked for too quickly"
	structuredFilesNoDir = "The pane's directory could not be read"
)

// structuredFilesScanMax is how many entries are looked at, from git or from a
// walk. A variable so a test can lower it.
var structuredFilesScanMax = 200000

// structuredFilesHitsMax is how many matches are collected to be ranked before the scan stops.
const structuredFilesHitsMax = 500

// structuredFilesMsg answers structuredFiles. Files are slash-separated paths
// relative to the pane's directory, best match first. More says the list was
// cut: by the limit on names, or because the directory has more entries than
// are read.
type structuredFilesMsg struct {
	Type  string   `json:"type"`
	ID    string   `json:"id"`
	Req   string   `json:"req,omitempty"`
	Q     string   `json:"q"`
	Files []string `json:"files"`
	More  bool     `json:"more,omitempty"`
	Error string   `json:"error,omitempty"`
}

// structuredFiles lists the files of the structured pane cmd.ID that match
// cmd.Q. It must run on the workspace goroutine; the listing itself does not.
func (s *Server) structuredFiles(c *controlClient, cmd command) {
	p := s.ws.Pane(cmd.ID)
	if p == nil || s.ws.StructuredRuntime(cmd.ID) == nil {
		refuse(c, cmd, errStructured(structuredNotPane))
		return
	}
	dir, id, req := p.Cwd, cmd.ID, cmd.Req
	q := cleanFilesQuery(cmd.Q)
	if !c.filesLimit.allow(structuredFilesRateLimit, structuredFilesRateWindow) {
		c.sendJSON(structuredFilesMsg{Type: "structuredFiles", ID: id, Req: req, Q: q, Files: []string{}, Error: structuredFilesBusy})
		return
	}
	go func() {
		defer s.surviveFor(c, "listing files")
		files, more, err := listPaneFiles(dir, q)
		m := structuredFilesMsg{Type: "structuredFiles", ID: id, Req: req, Q: q, Files: files, More: more}
		if err != nil {
			m.Files, m.More, m.Error = []string{}, false, structuredFilesNoDir
		}
		c.sendJSON(m)
	}()
}

// cleanFilesQuery is the query as it is matched: cut to a length, without
// control characters, with backslashes as slashes.
func cleanFilesQuery(q string) string {
	if len(q) > structuredFilesQueryCut {
		q = q[:structuredFilesQueryCut]
	}
	q = strings.ToValidUTF8(q, "")
	q = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, q)
	return strings.ReplaceAll(strings.TrimSpace(q), `\`, "/")
}

// listPaneFiles is up to structuredFilesMax paths under dir matching q.
func listPaneFiles(dir, q string) ([]string, bool, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, false, errors.New("no directory")
	}
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, false, err
	}
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		return nil, false, errors.New("not a directory")
	}
	ctx, cancel := context.WithTimeout(context.Background(), structuredFilesTimeout)
	defer cancel()

	cands, over := gitCandidates(ctx, root)
	if cands == nil {
		cands, over = walkCandidates(ctx, root)
	}
	files, cut := pickFiles(ctx, root, cands, q)
	return files, over || cut, nil
}

// gitCandidates is the files git lists in root (tracked, and not ignored), or
// nil when root is not in a working tree, git is missing or its list is too
// large to hold. All of what git wrote (at most structuredFilesGitBytes) is returned, so the query is
// applied to every name and a file late in git's order is found.
func gitCandidates(ctx context.Context, root string) ([]string, bool) {
	if !gitx.Available() {
		return nil, false
	}
	out, err := gitx.ReadOnlyCapped(ctx, root, structuredFilesGitBytes, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return nil, false
	}
	list := []string{}
	for _, one := range strings.Split(out, "\x00") {
		if one == "" {
			continue
		}
		list = append(list, one)
	}
	return list, false
}

// skippedDirs are directories a walk without git does not enter: they hold
// what a person does not mention by hand.
var skippedDirs = map[string]bool{"node_modules": true, "__pycache__": true}

// walkCandidates reads root breadth first, so shallow files come before deep
// ones, until structuredFilesScanMax entries have been read. It does not enter
// a symlink or a directory whose name starts with a dot.
func walkCandidates(ctx context.Context, root string) ([]string, bool) {
	list := []string{}
	queue := []string{""}
	seen := 0
	for len(queue) > 0 {
		rel := queue[0]
		queue = queue[1:]
		f, err := os.Open(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			continue
		}
		for {
			if ctx.Err() != nil {
				_ = f.Close()
				return list, true
			}
			ents, err := f.ReadDir(256)
			for _, e := range ents {
				if seen == structuredFilesScanMax {
					_ = f.Close()
					return list, true
				}
				seen++
				name := e.Name()
				p := name
				if rel != "" {
					p = rel + "/" + name
				}
				switch t := e.Type(); {
				case t&os.ModeSymlink != 0:
				case t.IsDir():
					if !strings.HasPrefix(name, ".") && !skippedDirs[name] {
						queue = append(queue, p)
					}
				case t.IsRegular():
					list = append(list, p)
				}
			}
			if err != nil {
				break
			}
		}
		_ = f.Close()
	}
	return list, false
}

// pickFiles looks at every candidate, collects up to structuredFilesHitsMax that match q, ranks them
// and returns the best structuredFilesMax that are regular files inside root. cut says there were more
// matches than that or the context ended first.
func pickFiles(ctx context.Context, root string, cands []string, q string) ([]string, bool) {
	lq := strings.ToLower(q)
	type hit struct {
		path string
		rank int
	}
	var hits []hit
	cut := false
	for i, p := range cands {
		if i%256 == 0 && ctx.Err() != nil {
			cut = true
			break
		}
		if !plainPath(p) {
			continue
		}
		if r, ok := rankPath(strings.ToLower(p), lq); ok {
			if len(hits) == structuredFilesHitsMax {
				cut = true
				break
			}
			hits = append(hits, hit{p, r})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		a, b := hits[i], hits[j]
		if a.rank != b.rank {
			return a.rank < b.rank
		}
		if len(a.path) != len(b.path) {
			return len(a.path) < len(b.path)
		}
		return a.path < b.path
	})
	out := []string{}
	for _, h := range hits {
		if len(out) == structuredFilesMax {
			return out, true
		}
		if ctx.Err() != nil {
			return out, true
		}
		if insideRegular(root, h.path) {
			out = append(out, h.path)
		}
	}
	return out, cut
}

// plainPath says a name is one a window may be sent as text: relative, with no
// "." or ".." element, valid UTF-8, no control character, and not long.
func plainPath(p string) bool {
	if p == "" || len(p) > structuredFilesPathMax || !utf8.ValidString(p) {
		return false
	}
	if strings.HasPrefix(p, "/") || strings.ContainsAny(p, `\:`) || filepath.VolumeName(p) != "" {
		return false
	}
	if strings.IndexFunc(p, unicode.IsControl) >= 0 {
		return false
	}
	for _, el := range strings.Split(p, "/") {
		if el == "" || el == "." || el == ".." || strings.HasSuffix(el, ".") || strings.HasSuffix(el, " ") || reservedName(el) {
			return false
		}
	}
	return true
}

// reservedName says el is a name Windows treats as a device, with or without an extension. The names are
// refused on every system, so a listing made on one is safe to use on another.
func reservedName(el string) bool {
	base := strings.ToUpper(el)
	if i := strings.IndexByte(base, '.'); i >= 0 {
		base = base[:i]
	}
	base = strings.TrimRight(base, " ")
	switch base {
	case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$":
		return true
	}
	return len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9'
}

// rankPath scores a lower-cased path against a lower-cased query: 0 when the
// file name starts with it, 1 when the name has it, 2 when the path has it, 3
// when its letters appear in order in the path, 4 for an empty query.
func rankPath(p, q string) (int, bool) {
	if q == "" {
		return 4, true
	}
	name := p[strings.LastIndexByte(p, '/')+1:]
	switch {
	case strings.HasPrefix(name, q):
		return 0, true
	case strings.Contains(name, q):
		return 1, true
	case strings.Contains(p, q):
		return 2, true
	}
	i := 0
	for _, r := range q {
		j := strings.IndexRune(p[i:], r)
		if j < 0 {
			return 0, false
		}
		i += j + utf8.RuneLen(r)
	}
	return 3, true
}

// insideRegular says rel names a regular file that, with every link on its way
// followed, is still inside root. A symlink as the last element is not a
// regular file here, so it is refused.
func insideRegular(root, rel string) bool {
	full := filepath.Join(root, filepath.FromSlash(rel))
	fi, err := os.Lstat(full)
	if err != nil || !fi.Mode().IsRegular() {
		return false
	}
	real, err := filepath.EvalSymlinks(full)
	if err != nil {
		return false
	}
	back, err := filepath.Rel(root, real)
	return err == nil && back != ".." && !strings.HasPrefix(back, ".."+string(filepath.Separator)) && !filepath.IsAbs(back)
}
