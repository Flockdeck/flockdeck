package claudeacp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// session/list names the sessions claude has stored for a folder, from the
// files under <config dir>/projects/<cwd as dashes>/<session id>.jsonl, the same
// files session/load replays. claude documents no format for them, so this is
// best effort like the replay: a file that does not parse is left out.
//
// Only the folder's own directory is read. session/load also searches the other
// project directories for an id, so the client must not take an id it did not
// get from this list for the same folder (internal/structured checks that).
const (
	// listMax is the most sessions an answer holds.
	listMax = 30
	// listMaxFiles is how many files, newest first, are opened to fill the list.
	// Files that hold no prompt of the person's are left out and do not count.
	listMaxFiles = 200
	// listHeadBytes is how much of the start of a file is read to find the
	// first prompt and the folder it names.
	listHeadBytes = 256 << 10
	// listHeadLines bounds the lines looked at.
	listHeadLines = 2000
	// listTitleMax is the longest title, in bytes.
	listTitleMax = 200
)

var uuidName = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type listedSession struct {
	ID, Cwd, Title string
	Updated        time.Time
}

// listSessions reads the stored sessions of cwd, newest first.
func listSessions(root, cwd string) []listedSession {
	if root == "" || cwd == "" {
		return nil
	}
	dir := filepath.Join(root, nonWord.ReplaceAllString(cwd, "-"))
	if fi, err := os.Lstat(dir); err != nil || !fi.IsDir() {
		return nil
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	type file struct {
		id  string
		mod time.Time
	}
	var files []file
	for _, e := range ents {
		name, ok := strings.CutSuffix(e.Name(), ".jsonl")
		// A symbolic link or junction is not followed: Type is the link's own.
		if !ok || !uuidName.MatchString(name) || !e.Type().IsRegular() {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, file{name, fi.ModTime()})
		if len(files) >= historyMaxDirs {
			break
		}
	}
	sort.Slice(files, func(i, j int) bool {
		if !files[i].mod.Equal(files[j].mod) {
			return files[i].mod.After(files[j].mod)
		}
		return files[i].id > files[j].id
	})
	var out []listedSession
	for i, f := range files {
		if i >= listMaxFiles || len(out) >= listMax {
			break
		}
		title, fileCwd := readHead(filepath.Join(dir, f.id+".jsonl"))
		if title == "" {
			continue
		}
		if fileCwd == "" {
			fileCwd = cwd
		}
		out = append(out, listedSession{ID: f.id, Cwd: fileCwd, Title: title, Updated: f.mod})
	}
	return out
}

// readHead finds the first prompt the person typed, as one line, and the cwd
// claude wrote in the file. Either is empty when the start of the file has none.
func readHead(path string) (title, cwd string) {
	f, err := os.Open(path)
	if err != nil {
		return "", ""
	}
	defer f.Close()
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() {
		return "", ""
	}
	r := bufio.NewReaderSize(&limitedReader{f: f, left: listHeadBytes}, 64<<10)
	for n := 0; n < listHeadLines && (title == "" || cwd == ""); n++ {
		line, _, _, skipped, err := readLine(r)
		if !skipped && len(line) > 0 && bytes.Contains(line, []byte(`"cwd"`)) && cwd == "" {
			var c struct {
				Cwd string `json:"cwd"`
			}
			if json.Unmarshal(line, &c) == nil {
				cwd = c.Cwd
			}
		}
		if title == "" && !skipped && bytes.Contains(line, []byte(`"user"`)) {
			var l histLine
			if json.Unmarshal(line, &l) == nil && l.Type == "user" &&
				!l.IsSidechain && !l.IsMeta && !l.IsCompactSummary && l.Origin.human() {
				title = titleLine(promptText(l.Message.Content), listTitleMax)
			}
		}
		if err != nil {
			break
		}
	}
	return title, cwd
}

// limitedReader reads at most left bytes of f.
type limitedReader struct {
	f    *os.File
	left int64
}

func (l *limitedReader) Read(p []byte) (int, error) {
	if l.left <= 0 {
		return 0, os.ErrClosed
	}
	if int64(len(p)) > l.left {
		p = p[:l.left]
	}
	n, err := l.f.Read(p)
	l.left -= int64(n)
	return n, err
}

// titleLine is the first non-empty line of s with its runs of white space made
// single spaces, cut to at most n bytes on a rune boundary.
func titleLine(s string, n int) string {
	for _, ln := range strings.Split(s, "\n") {
		if ln = strings.Join(strings.Fields(ln), " "); ln != "" {
			if len(ln) > n {
				for n > 0 && !utf8.RuneStart(ln[n]) {
					n--
				}
				ln = ln[:n]
			}
			return ln
		}
	}
	return ""
}

// listSessionsCall answers session/list for the cwd in the request. It has no
// pages: the answer is at most listMax sessions.
func (s *server) listSessionsCall(m rpcMsg) {
	var p struct {
		Cwd string `json:"cwd"`
	}
	if err := json.Unmarshal(m.Params, &p); err != nil || !filepath.IsAbs(p.Cwd) {
		s.replyErr(m.ID, &rpcErr{Code: codeInvalidParams, Message: "cwd must be an absolute path"})
		return
	}
	list := []map[string]any{}
	for _, l := range listSessions(s.opts.historyRoot(), p.Cwd) {
		list = append(list, map[string]any{"sessionId": l.ID, "cwd": l.Cwd, "title": l.Title,
			"updatedAt": l.Updated.UTC().Format(time.RFC3339)})
	}
	s.reply(m.ID, map[string]any{"sessions": list})
}
