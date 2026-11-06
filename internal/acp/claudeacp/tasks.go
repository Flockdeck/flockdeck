package claudeacp

import (
	"encoding/json"
	"regexp"
	"strings"
)

// claude -p does not list a todo tool unless CLAUDE_CODE_ENABLE_TODO_TOOLS is
// set; with it, claude 2.1.319 lists TaskCreate, TaskGet, TaskList and
// TaskUpdate. They send changes (one new task, one task's new status), so the
// session keeps the list and sends all of it as a plan after each change.

// withTodoTools returns env with CLAUDE_CODE_ENABLE_TODO_TOOLS set, unless the
// caller already set it.
func withTodoTools(env []string) []string {
	for _, kv := range env {
		if strings.HasPrefix(strings.ToUpper(kv), "CLAUDE_CODE_ENABLE_TODO_TOOLS=") {
			return env
		}
	}
	out := make([]string, 0, len(env)+1)
	out = append(out, env...)
	return append(out, "CLAUDE_CODE_ENABLE_TODO_TOOLS=1")
}

type taskItem struct {
	id      string // claude's id, known once the TaskCreate result arrives
	useID   string // the TaskCreate tool use that made it
	subject string
	status  string
}

type taskList struct {
	items []*taskItem
}

func (l *taskList) plan() []map[string]any {
	out := []map[string]any{}
	for _, t := range l.items {
		out = append(out, map[string]any{"content": t.subject, "priority": "medium", "status": t.status})
	}
	return out
}

func (l *taskList) find(id string) int {
	for i, t := range l.items {
		if t.id != "" && t.id == id {
			return i
		}
	}
	return -1
}

func taskStatus(s string) (string, bool) {
	switch s {
	case "pending", "in_progress", "completed":
		return s, true
	}
	return "", false
}

// applyTask applies a Task tool use to the session's list and sends the list.
// TaskList and TaskGet only read.
func (ss *session) applyTask(useID, name string, input json.RawMessage) {
	var in struct {
		TaskID  string  `json:"taskId"`
		Subject *string `json:"subject"`
		Status  *string `json:"status"`
	}
	_ = json.Unmarshal(input, &in)
	ss.mu.Lock()
	changed := false
	switch name {
	case "TaskCreate":
		subject := ""
		if in.Subject != nil {
			subject = *in.Subject
		}
		ss.tasks.items = append(ss.tasks.items, &taskItem{useID: useID, subject: subject, status: "pending"})
		changed = true
	case "TaskUpdate":
		if i := ss.tasks.find(in.TaskID); i >= 0 {
			t := ss.tasks.items[i]
			if in.Subject != nil && *in.Subject != "" {
				t.subject = *in.Subject
			}
			if in.Status != nil {
				if *in.Status == "deleted" {
					ss.tasks.items = append(ss.tasks.items[:i], ss.tasks.items[i+1:]...)
				} else if st, ok := taskStatus(*in.Status); ok {
					t.status = st
				}
			}
			changed = true
		}
	}
	var plan []map[string]any
	if changed {
		plan = ss.tasks.plan()
	}
	ss.mu.Unlock()
	if changed {
		ss.srv.update(ss.id, map[string]any{"sessionUpdate": "plan", "entries": plan})
	}
}

var taskCreatedRe = regexp.MustCompile(`Task #(\S+?) created`)

// taskCreated records the id claude gave a new task ("Task #1 created
// successfully: alpha"). A failed TaskCreate takes its row out again.
func (ss *session) taskCreated(useID, result string, failed bool) {
	ss.mu.Lock()
	idx := -1
	for i, t := range ss.tasks.items {
		if t.useID == useID {
			idx = i
		}
	}
	if idx < 0 {
		ss.mu.Unlock()
		return
	}
	resend := false
	if m := taskCreatedRe.FindStringSubmatch(result); !failed && m != nil {
		ss.tasks.items[idx].id = m[1]
	} else if failed {
		ss.tasks.items = append(ss.tasks.items[:idx], ss.tasks.items[idx+1:]...)
		resend = true
	}
	var plan []map[string]any
	if resend {
		plan = ss.tasks.plan()
	}
	ss.mu.Unlock()
	if resend {
		ss.srv.update(ss.id, map[string]any{"sessionUpdate": "plan", "entries": plan})
	}
}
