package hooks

import (
	"regexp"
	"strings"
)

// A task notification is how Claude Code tells the model that a background
// task has ended: a run_in_background command finishing or being stopped, a
// background subagent finishing. It submits one as a prompt, which fires
// UserPromptSubmit, and stores it in the conversation as a user turn or as
// a queued command. As 2.1.28x writes it:
//
//	<task-notification>
//	<task-id>bmxcupw20</task-id>
//	<tool-use-id>toolu_...</tool-use-id>
//	<output-file>...</output-file>
//	<status>completed</status>
//	<summary>Background command "..." completed (exit code 0)</summary>
//	</task-notification>
//
// The same reading serves the hook (backgroundOf) and the check against the
// stored conversation (internal/workspace), so the two agree on what counts.

// TaskNotification is the task a notification names and the status it gives.
type TaskNotification struct {
	ID     string
	Status string
}

// Ended reports whether the status says the task is no longer running.
func (n TaskNotification) Ended() bool { return stoppedStatuses[n.Status] }

// stoppedStatuses are the <status> values that say a task has stopped.
// Real notifications use "completed" and "stopped"; the rest are accepted
// should Claude Code use them.
var stoppedStatuses = map[string]bool{
	"completed": true, "stopped": true, "killed": true, "failed": true, "cancelled": true, "canceled": true, "error": true,
}

// taskIDPattern is the shape of an id Claude Code gives a background task or
// subagent: no whitespace, no markup, short.
var taskIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// ValidTaskID reports whether id has the shape of a background task's id.
// Nothing else is put into a path or taken as naming a task.
func ValidTaskID(id string) bool { return taskIDPattern.MatchString(id) }

// notificationHead is the order Claude Code writes a notification's first
// elements in: its <task-id>, then <tool-use-id> and <output-file> where it
// has them, then <status>. The summary, result, note and usage that follow
// carry a command's or a monitor's output, so a <status> among them could
// have been put there by anyone.
var notificationHead = []string{"task-id", "tool-use-id", "output-file", "status"}

// ParseTaskNotification reads the <task-notification> a message is. The
// message must be exactly one block with only whitespace around it, or it is
// not read: Claude Code does not escape the text in a <summary> or
// <result>, so a summary can close the real block and open a forged one, and
// two blocks in one message cannot be told from that. Within the block only
// its own top-level <task-id> and <status> count: the block must be nothing
// but elements, start with a <task-id> of ValidTaskID's shape, and reach its
// <status> through only the elements notificationHead allows, each once. A
// block with no such status, a monitor's event say, is not read either.
func ParseTaskNotification(text string) (TaskNotification, bool) {
	const open, close = "<task-notification>", "</task-notification>"
	var n TaskNotification
	after, ok := strings.CutPrefix(strings.TrimSpace(text), open)
	if !ok {
		return n, false
	}
	body, tail, ok := strings.Cut(after, close)
	if !ok || strings.Contains(body, open) || strings.TrimSpace(tail) != "" {
		return n, false
	}
	head := 0 // the next place in notificationHead an element may take
	ids, statuses := 0, 0
	rest := strings.TrimSpace(body)
	for rest != "" {
		if !strings.HasPrefix(rest, "<") {
			return n, false
		}
		end := strings.IndexByte(rest, '>')
		if end < 2 {
			return n, false
		}
		name := rest[1:end]
		if !elementName(name) {
			return n, false
		}
		content, tail, ok := strings.Cut(rest[end+1:], "</"+name+">")
		if !ok {
			return n, false
		}
		if statuses == 0 {
			// Still in the head: each element must come later in it than
			// the one before, and the first must be the id.
			at := -1
			for i := head; i < len(notificationHead); i++ {
				if notificationHead[i] == name {
					at = i
					break
				}
			}
			if at < 0 || (head == 0 && at != 0) {
				return n, false
			}
			head = at + 1
		}
		switch name {
		case "task-id":
			ids++
			n.ID = strings.TrimSpace(content)
		case "status":
			statuses++
			n.Status = strings.ToLower(strings.TrimSpace(content))
		}
		rest = strings.TrimSpace(tail)
	}
	if ids != 1 || statuses != 1 || !ValidTaskID(n.ID) {
		return TaskNotification{}, false
	}
	return n, true
}

// elementName reports whether s is a plain tag name: lower-case letters,
// digits, '-' and '_'.
func elementName(s string) bool {
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return s != ""
}
