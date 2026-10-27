package claudeacp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type toolDesc struct {
	title     string
	kind      string
	locations []map[string]any
	content   []any
	plan      []map[string]any // set for TodoWrite, which is sent as a plan
}

// maxDiffRead is the most of an existing file read to show a Write as a diff.
const maxDiffRead = 1 << 20

func str(m map[string]json.RawMessage, key string) string {
	var s string
	_ = json.Unmarshal(m[key], &s)
	return s
}

func firstLine(s string, n int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i] + " …"
	}
	if len([]rune(s)) > n {
		s = string([]rune(s)[:n]) + "…"
	}
	return s
}

func diffItem(path string, old *string, new string) map[string]any {
	d := map[string]any{"type": "diff", "path": path, "newText": new}
	if old != nil {
		d["oldText"] = *old
	} else {
		d["oldText"] = nil
	}
	return d
}

// describeTool maps a claude tool and its input to the title, kind, locations
// and content of an ACP tool call. display is claude's own label for the tool,
// used when none of the cases below knows it.
func describeTool(name string, input json.RawMessage, cwd, display string) toolDesc {
	var in map[string]json.RawMessage
	_ = json.Unmarshal(input, &in)
	abs := func(p string) string {
		if p == "" || filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(cwd, p)
	}
	loc := func(p string, line int) []map[string]any {
		if p == "" {
			return nil
		}
		l := map[string]any{"path": abs(p)}
		if line > 0 {
			l["line"] = line
		}
		return []map[string]any{l}
	}
	d := toolDesc{kind: "other", title: name}
	if display != "" {
		d.title = display
	}
	switch name {
	case "Read":
		p := str(in, "file_path")
		var off int
		_ = json.Unmarshal(in["offset"], &off)
		d.kind, d.title, d.locations = "read", "Read "+p, loc(p, off)
	case "Write":
		p := str(in, "file_path")
		d.kind, d.title, d.locations = "edit", "Write "+p, loc(p, 0)
		var old *string
		if b, err := readCapped(abs(p)); err == nil {
			s := string(b)
			old = &s
		}
		d.content = []any{diffItem(abs(p), old, str(in, "content"))}
	case "Edit":
		p := str(in, "file_path")
		d.kind, d.title, d.locations = "edit", "Edit "+p, loc(p, 0)
		old := str(in, "old_string")
		d.content = []any{diffItem(abs(p), &old, str(in, "new_string"))}
	case "MultiEdit":
		p := str(in, "file_path")
		d.kind, d.title, d.locations = "edit", "Edit "+p, loc(p, 0)
		var edits []struct {
			Old string `json:"old_string"`
			New string `json:"new_string"`
		}
		_ = json.Unmarshal(in["edits"], &edits)
		for _, e := range edits {
			old := e.Old
			d.content = append(d.content, diffItem(abs(p), &old, e.New))
		}
	case "NotebookEdit":
		p := str(in, "notebook_path")
		d.kind, d.title, d.locations = "edit", "Edit "+p, loc(p, 0)
	case "Bash", "PowerShell":
		d.kind = "execute"
		d.title = firstLine(str(in, "command"), 120)
		if d.title == "" {
			d.title = name
		}
	case "BashOutput", "KillShell", "KillBash":
		d.kind, d.title = "execute", name
	case "Glob":
		d.kind, d.title = "search", "Find "+str(in, "pattern")
		d.locations = loc(str(in, "path"), 0)
	case "Grep":
		d.kind, d.title = "search", "Search "+str(in, "pattern")
		d.locations = loc(str(in, "path"), 0)
	case "LS":
		d.kind, d.title = "search", "List "+str(in, "path")
		d.locations = loc(str(in, "path"), 0)
	case "WebFetch":
		d.kind, d.title = "fetch", "Fetch "+str(in, "url")
	case "WebSearch":
		d.kind, d.title = "fetch", "Search the web: "+str(in, "query")
	case "Task", "Agent":
		d.kind = "think"
		d.title = firstLine(str(in, "description"), 120)
		if d.title == "" {
			d.title = name
		}
	case "ExitPlanMode":
		d.kind, d.title = "switch_mode", "Exit plan mode"
		// The plan is what is being approved: it goes with the request, as text.
		if plan := str(in, "plan"); strings.TrimSpace(plan) != "" {
			d.content = []any{map[string]any{"type": "content", "content": map[string]any{"type": "text", "text": capText(plan)}}}
		}
	case "TodoWrite":
		var todos []struct {
			Content string `json:"content"`
			Status  string `json:"status"`
		}
		_ = json.Unmarshal(in["todos"], &todos)
		d.plan = []map[string]any{}
		for _, t := range todos {
			status := t.Status
			if status != "pending" && status != "in_progress" && status != "completed" {
				status = "pending"
			}
			d.plan = append(d.plan, map[string]any{"content": t.Content, "priority": "medium", "status": status})
		}
	default:
		if strings.HasPrefix(name, "mcp__") {
			parts := strings.SplitN(name, "__", 3)
			if len(parts) == 3 {
				d.title = parts[1] + ": " + parts[2]
			}
		}
	}
	if strings.TrimSpace(d.title) == "" {
		d.title = name
	}
	return d
}

func readCapped(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if st.IsDir() || st.Size() > maxDiffRead {
		return nil, fmt.Errorf("not shown")
	}
	b := make([]byte, st.Size())
	n, err := f.Read(b)
	if err != nil && n == 0 && st.Size() > 0 {
		return nil, err
	}
	return b[:n], nil
}

// maxResultText is the most of a tool result sent as a tool call's content.
const maxResultText = 64 << 10

// resultText is the text of a tool_result's content: a string, or an array of
// blocks of which the text is kept and an image is named.
func resultText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return capText(s)
	}
	var blocks []block
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		switch b.Type {
		case "text":
			parts = append(parts, b.Text)
		case "image":
			parts = append(parts, "[image]")
		}
	}
	return capText(strings.Join(parts, "\n"))
}

func capText(s string) string {
	if len(s) <= maxResultText {
		return s
	}
	return s[:maxResultText] + "\n[output cut at 64 KiB]"
}
