package server

import (
	"path/filepath"
	"strconv"

	"github.com/jmwri/flockdeck/internal/artifacts"
	"github.com/jmwri/flockdeck/internal/record"
	"github.com/jmwri/flockdeck/internal/recordview"
	"github.com/jmwri/flockdeck/internal/store"
)

// The recordings kind of /ws/artifacts: the transcripts this machine's own
// recorder wrote, listed and read page by page.
//
// A device is never given a path or file bytes. It is given a list of ids this
// socket issued, and asks for a page of an id. Each request looks the id up in
// this socket's own registry, opens the folder again as a root, vets the file
// through that root (no link, no second hard link, no secret name, regular, inside
// the folder), and only then hands the path to recordview, which parses the
// lines, redacts them again and clips them. Only the host's own recordings
// folder is ever a root: it is built from the state directory, never from
// anything a device or a file said.

// recordingsKind is the kind name on the wire.
const recordingsKind = "recordings"

const (
	// recListPage is how many items one list reply carries; the whole list is
	// at most artifacts.MaxListItems.
	recListPage = 100
	// recScanMax is how many recordings on disk one listing looks at, so a
	// folder holding a very large number cannot make a list slow. They are
	// newest first, so what is left out is the oldest.
	recScanMax = 2000
	// recFieldMax clips the text fields of a list item.
	recFieldMax = 120
	// recCursorMax is the longest list cursor read.
	recCursorMax = 8
)

// recItem is one recording as a listing found it. path is the host's and is
// never sent.
type recItem struct {
	path    string
	name    string
	project string
	agent   string
	started string
	size    int64
	mtime   int64
}

// recordingsRoot opens the host's recordings folder as a root. It fails if the
// folder is not there, which is an empty list rather than an error.
func recordingsRoot() (*artifacts.Root, error) {
	dir, err := record.Root(store.Dir)
	if err != nil {
		return nil, err
	}
	return artifacts.NewRoot(dir)
}

// scanRecordings is the recordings on disk this device may be told of, newest
// first. A file that fails the root's checks is left out, so a list is not a way
// to learn that a link or a secret-named file is there.
func scanRecordings() []recItem {
	root, err := recordingsRoot()
	if err != nil {
		return nil
	}
	defer root.Close()
	infos, err := record.List(store.Dir)
	if err != nil {
		return nil
	}
	var out []recItem
	for i, in := range infos {
		if i >= recScanMax || len(out) >= artifacts.MaxListItems {
			break
		}
		// A first line that is not the start of a transcript is not one.
		if in.Started == "" {
			continue
		}
		f, err := root.Open(in.Path)
		if err != nil {
			continue
		}
		f.Close()
		out = append(out, recItem{
			path:    in.Path,
			name:    recText(filepath.Base(in.Path)),
			project: recText(in.Project),
			agent:   recText(in.Agent),
			started: recText(in.Started),
			size:    in.Size,
			mtime:   in.Modified.UnixMilli(),
		})
	}
	return out
}

// recText is a field of a list item as it may be sent: no control characters,
// redacted, clipped.
func recText(s string) string {
	return record.Clip(record.Redact(tidy(s)), recFieldMax)
}

// listRecordings answers a list request for the recordings kind. A request
// without a cursor takes a fresh look at the disk and keeps the result on the
// socket; a request with one reads the next page of that look, so paging does
// not rescan, and a cursor that this socket did not make is not served.
func (s *Server) listRecordings(sock *artifactSock, req artifactRequest) map[string]any {
	if req.Pane != "" {
		return map[string]any{"op": "error", "code": artifactErrUnavailable}
	}
	start := 0
	if req.After == "" {
		sock.recs = scanRecordings()
	} else {
		n, ok := parseRecCursor(req.After, len(sock.recs))
		if !ok {
			return map[string]any{"op": "error", "code": artifactErrUnavailable}
		}
		start = n
	}
	end := min(start+recListPage, len(sock.recs))
	items := make([]map[string]any, 0, end-start)
	for _, it := range sock.recs[start:end] {
		id, err := sock.reg.Issue(artifacts.Entry{Kind: recordingsKind, Path: it.path})
		if err != nil {
			return map[string]any{"op": "error", "code": artifactErrUnavailable}
		}
		items = append(items, map[string]any{
			"id": id, "kind": recordingsKind, "name": it.name, "project": it.project,
			"agent": it.agent, "started": it.started, "size": it.size, "mtime": it.mtime,
			"viewable": true,
		})
	}
	next := ""
	if end < len(sock.recs) {
		next = strconv.Itoa(end)
	}
	return map[string]any{"op": "list", "kind": recordingsKind, "items": items, "next": next}
}

// parseRecCursor reads a list cursor: plain digits, no more than the listing
// held when it was taken.
func parseRecCursor(s string, held int) (int, bool) {
	if s == "" || len(s) > recCursorMax {
		return 0, false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(s)
	if err != nil || n > held {
		return 0, false
	}
	return n, true
}

// openRecording answers an open request for an id that this socket issued: one
// page of the parsed transcript. Every failure is the same reply, and says
// nothing about why.
func (s *Server) openRecording(device, deviceName string, e artifacts.Entry, req artifactRequest) map[string]any {
	fail := map[string]any{"op": "error", "code": artifactErrUnavailable}
	if e.Kind != recordingsKind || req.Cursor < 0 || req.Cursor > recordview.MaxFileBytes {
		return fail
	}
	root, err := recordingsRoot()
	if err != nil {
		return fail
	}
	defer root.Close()
	// The folder's own checks come first and are made again on every request:
	// the file may have been replaced by a link since it was listed.
	f, err := root.Open(e.Path)
	if err != nil {
		return fail
	}
	rel, size := f.Rel, f.Size
	f.Close()

	rd, err := recordview.Open(e.Path)
	if err != nil {
		return fail
	}
	page, err := rd.Page(int(req.Cursor), req.Max)
	if err != nil {
		return fail
	}
	// The first page of a recording is the view being recorded: who, which file,
	// how large, never what it said. If it cannot be recorded, nothing is shown.
	if req.Cursor == 0 {
		if err := s.artifacts.audit.write(auditEvent{Event: "open", Device: device, DeviceName: deviceName, Kind: recordingsKind, Name: rel, Bytes: size}); err != nil {
			return fail
		}
	}
	return map[string]any{
		"op": "data", "id": req.ID, "kind": recordingsKind,
		"header": rd.Header(), "entries": page.Entries,
		"next": page.Next, "done": page.Done, "skipped": page.Skipped,
	}
}
