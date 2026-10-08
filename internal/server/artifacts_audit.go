package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/jmwri/flockdeck/internal/store"
)

// The artifacts audit log: one JSON line for each connection to the artifacts
// socket, each refusal, and each change the desk makes to what may be viewed.
// It holds who, what kind, and how many bytes; never what was shown. A name,
// once files are served, is a path relative to the pane's project.
//
// It is kept in the state folder with owner-only permissions and rotated by
// size, so it cannot grow without bound: auditFile, then auditFile.1 and
// auditFile.2, which together are the three files the design allows.
const (
	artifactAuditFile = "remote-artifacts.log"
	auditMaxBytes     = 1 << 20
	auditFiles        = 3
	// auditFieldMax caps every text field, so that a hostile device name or a
	// long path cannot fill the log.
	auditFieldMax = 200
)

// auditEvent is one line of the log.
type auditEvent struct {
	Time       time.Time `json:"time"`
	Event      string    `json:"event"`
	Device     string    `json:"device,omitempty"`
	DeviceName string    `json:"deviceName,omitempty"`
	Kind       string    `json:"kind,omitempty"`
	Pane       string    `json:"pane,omitempty"`
	Name       string    `json:"name,omitempty"`
	Bytes      int64     `json:"bytes,omitempty"`
	Reason     string    `json:"reason,omitempty"`
	// Suppressed is how many like refusals were left out since the last one
	// written, when a device is refused faster than they are worth recording.
	Suppressed int `json:"suppressed,omitempty"`
}

// auditLog appends to the log. The zero value is ready to use; dir and max
// are overridden by tests.
type auditLog struct {
	mu  sync.Mutex
	dir func() (string, error)
	max int64
}

func (a *auditLog) folder() (string, error) {
	if a.dir != nil {
		return a.dir()
	}
	return store.Dir()
}

// write appends e, rotating first when the file would pass its size. An error
// is returned for the caller to decide on: a connection is refused if it
// cannot be recorded.
func (a *auditLog) write(e auditEvent) error {
	if e.Time.IsZero() {
		e.Time = time.Now().UTC()
	}
	e.Event = tidy(e.Event)
	e.Device = tidy(e.Device)
	e.DeviceName = tidy(e.DeviceName)
	e.Kind = tidy(e.Kind)
	e.Pane = tidy(e.Pane)
	e.Name = tidy(e.Name)
	e.Reason = tidy(e.Reason)
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	line = append(line, '\n')

	a.mu.Lock()
	defer a.mu.Unlock()
	dir, err := a.folder()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(dir, artifactAuditFile)
	limit := a.max
	if limit <= 0 {
		limit = auditMaxBytes
	}
	if fi, err := os.Stat(path); err == nil && fi.Size()+int64(len(line)) > limit {
		rotateAudit(path)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, werr := f.Write(line)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	return werr
}

// rotateAudit moves the log to .1 and .1 to .2, dropping the old .2.
func rotateAudit(path string) {
	for i := auditFiles - 1; i >= 1; i-- {
		from := path
		if i > 1 {
			from = path + "." + strconv.Itoa(i-1)
		}
		_ = os.Remove(path + "." + strconv.Itoa(i))
		_ = os.Rename(from, path+"."+strconv.Itoa(i))
	}
}

// tidy makes a string safe to put in the log and in a notice: control and
// format characters (which could fake a line or reorder text) go, and it is
// cut to auditFieldMax runes.
func tidy(s string) string {
	var b strings.Builder
	n := 0
	for _, r := range s {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == '\u2028' || r == '\u2029' {
			r = ' '
		}
		if n++; n > auditFieldMax {
			break
		}
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}
