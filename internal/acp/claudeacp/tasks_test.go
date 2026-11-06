package claudeacp

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestWithTodoToolsSetsTheVariableOnce(t *testing.T) {
	got := withTodoTools([]string{"A=1"})
	if !reflect.DeepEqual(got, []string{"A=1", "CLAUDE_CODE_ENABLE_TODO_TOOLS=1"}) {
		t.Errorf("env = %v", got)
	}
	set := []string{"claude_code_enable_todo_tools=0"}
	if got := withTodoTools(set); !reflect.DeepEqual(got, set) {
		t.Errorf("a value the caller set was changed: %v", got)
	}
	if got := withTodoTools(nil); len(got) != 1 {
		t.Errorf("env = %v", got)
	}
}

func TestDescribeToolKnowsTheTaskTools(t *testing.T) {
	for _, name := range []string{"TaskCreate", "TaskUpdate", "TaskList", "TaskGet"} {
		if d := describeTool(name, json.RawMessage(`{}`), "", ""); !d.task || d.plan != nil {
			t.Errorf("%s: %+v", name, d)
		}
	}
	if d := describeTool("Task", json.RawMessage(`{"description":"look"}`), "", ""); d.task {
		t.Error("the subagent tool Task is not a task list tool")
	}
}

// claude's Task tools send changes; the plan sent to the client is the whole list.
func TestTaskToolsBecomeAPlan(t *testing.T) {
	use := func(id, name, input string) string {
		return `{"type":"assistant","message":{"id":"m` + id + `","content":[{"type":"tool_use","id":"` + id + `","name":"` + name + `","input":` + input + `}]},"parent_tool_use_id":null}`
	}
	res := func(id, text string) string {
		return `{"type":"user","message":{"role":"user","content":[{"tool_use_id":"` + id + `","type":"tool_result","content":"` + text + `"}]},"parent_tool_use_id":null}`
	}
	h := newHarness(t, []step{expect(`"role":"user"`),
		emit(use("t1", "TaskCreate", `{"subject":"alpha","description":"alpha"}`)),
		emit(res("t1", "Task #1 created successfully: alpha")),
		emit(use("t2", "TaskCreate", `{"subject":"beta","description":"beta"}`)),
		emit(res("t2", "Task #2 created successfully: beta")),
		emit(use("t3", "TaskUpdate", `{"taskId":"1","status":"completed"}`)),
		emit(res("t3", "Updated task #1 status")),
		emit(use("t4", "TaskUpdate", `{"taskId":"2","status":"in_progress"}`)),
		emit(res("t4", "Updated task #2 status")),
		emit(use("t5", "TaskList", `{}`)),
		emit(res("t5", "#1 [completed] alpha")),
		emit(use("t6", "TaskUpdate", `{"taskId":"2","status":"deleted"}`)),
		emit(res("t6", "Updated task #2 deleted")),
		emit(lResultOK)})
	sid := h.start()
	stopReason(t, h.await(h.promptText(sid, "plan")))
	var plans [][]string
	for _, u := range h.updates() {
		switch u["sessionUpdate"] {
		case "tool_call", "tool_call_update":
			t.Errorf("a Task tool drew a tool card: %v", u)
		case "plan":
			var row []string
			for _, e := range u["entries"].([]any) {
				m := e.(map[string]any)
				row = append(row, m["content"].(string)+":"+m["status"].(string))
			}
			plans = append(plans, row)
		}
	}
	want := [][]string{
		{"alpha:pending"},
		{"alpha:pending", "beta:pending"},
		{"alpha:completed", "beta:pending"},
		{"alpha:completed", "beta:in_progress"},
		{"alpha:completed"},
	}
	if !reflect.DeepEqual(plans, want) {
		t.Errorf("plans = %v, want %v", plans, want)
	}
}
