package help

import "strings"

// Every action the interface offers is described exactly once, here. The
// command palette, the shortcut tables in the help pages and the README table
// are all rendered from this list, so a binding cannot be changed in one of
// them and left stale in the others.

// Key is one action, its binding if it has one, and where it belongs in the
// shortcut tables.
type Key struct {
	// ID is stable and is what the front end hangs its implementation off;
	// it is also what `[[key:id]]` in a help page refers to.
	ID string `json:"id"`
	// Keys is the binding as it is written for a reader, or "" for actions
	// that are only reachable from the command palette.
	Keys string `json:"keys"`
	// Label is the one line shown in the palette and in the shortcut tables.
	Label string `json:"label"`
	// Section groups the action in the shortcut tables.
	Section string `json:"section"`
	// Page is the help page that explains the action, if one does.
	Page string `json:"page,omitempty"`
	// NoPalette keeps navigation that only makes sense from the keyboard out
	// of the palette — opening the palette from the palette, most of all.
	NoPalette bool `json:"noPalette,omitempty"`
	// Confirm is the question to ask before running, for the actions that
	// stop other people's work.
	Confirm string `json:"confirm,omitempty"`
}

// Sections are the groups the shortcut tables are drawn in, in order.
var Sections = []string{
	"Panes",
	"Tabs",
	"Agents",
	"Git",
	"Finding your way",
	"The window",
}

// Keys is every action, in the order the palette and the tables show them.
var Keys = []Key{
	// --- panes -------------------------------------------------------------
	{ID: "splitRight", Keys: "Ctrl+Shift+D", Label: "Split right (agent)", Section: "Panes", Page: "panes"},
	{ID: "splitDown", Keys: "Ctrl+Shift+E", Label: "Split down (agent)", Section: "Panes", Page: "panes"},
	// The plain splits and the plain new tab take the default agent and stay one
	// keystroke; these two are the same thing with the picker in front of it, and
	// they have no binding of their own because they are reached by the caret
	// beside New tab and from the palette.
	{ID: "splitRightChoose", Label: "Split right (choose agent)…", Section: "Panes", Page: "agents"},
	{ID: "splitRightShell", Label: "Split right (shell)", Section: "Panes", Page: "panes"},
	{ID: "movePaneLeft", Keys: "Ctrl+Shift+←", Label: "Move pane left", Section: "Panes", Page: "rearranging"},
	{ID: "movePaneRight", Keys: "Ctrl+Shift+→", Label: "Move pane right", Section: "Panes", Page: "rearranging"},
	{ID: "movePaneUp", Keys: "Ctrl+Shift+↑", Label: "Move pane up", Section: "Panes", Page: "rearranging"},
	{ID: "movePaneDown", Keys: "Ctrl+Shift+↓", Label: "Move pane down", Section: "Panes", Page: "rearranging"},
	{ID: "movePaneToNewTab", Label: "Move pane to a tab of its own", Section: "Panes", Page: "rearranging"},
	{ID: "tilePanes", Label: "Tile these panes evenly", Section: "Panes", Page: "rearranging"},
	{ID: "zoomPane", Keys: "Ctrl+Shift+Z", Label: "Zoom pane", Section: "Panes", Page: "panes"},
	{ID: "restartPane", Label: "Restart pane", Section: "Panes", Page: "panes"},
	{ID: "closePane", Keys: "Ctrl+Shift+W", Label: "Close pane", Section: "Panes", Page: "panes"},

	// --- tabs --------------------------------------------------------------
	{ID: "newAgentTab", Keys: "Ctrl+Shift+T", Label: "New agent tab", Section: "Tabs", Page: "panes"},
	{ID: "newAgentTabChoose", Label: "New agent tab (choose agent)…", Section: "Tabs", Page: "agents"},
	{ID: "newShellTab", Keys: "Ctrl+Shift+N", Label: "New shell tab", Section: "Tabs", Page: "panes"},
	{ID: "nextTab", Keys: "Ctrl+Tab", Label: "Next tab", Section: "Tabs", NoPalette: true},
	{ID: "prevTab", Keys: "Ctrl+Shift+Tab", Label: "Previous tab", Section: "Tabs", NoPalette: true},
	{ID: "selectTab", Keys: "Alt+1 … Alt+9", Label: "Select tab by number", Section: "Tabs", NoPalette: true},
	{ID: "mergeAllTabs", Label: "Merge every tab into this one", Section: "Tabs", Page: "rearranging"},

	// --- agents ------------------------------------------------------------
	{ID: "toggleBroadcast", Keys: "Ctrl+Shift+B", Label: "Toggle broadcast", Section: "Agents", Page: "broadcast"},
	{ID: "promptAll", Keys: "Ctrl+Shift+P", Label: "Prompt all panes", Section: "Agents", Page: "broadcast"},
	{ID: "fanout", Keys: "Ctrl+Shift+X", Label: "Fan out — turn this pane's plan into agents", Section: "Agents", Page: "fanout"},
	{ID: "agents", Keys: "Ctrl+Shift+A", Label: "All agents across projects", Section: "Agents", Page: "status"},
	{ID: "apiKeys", Label: "API keys…", Section: "Agents", Page: "agents"},

	// --- git ---------------------------------------------------------------
	{ID: "worktrees", Keys: "Ctrl+Shift+G", Label: "Worktrees", Section: "Git", Page: "worktrees"},
	{ID: "changes", Keys: "Ctrl+Shift+S", Label: "Review changes, commit and push", Section: "Git", Page: "changes"},

	// --- finding your way --------------------------------------------------
	{ID: "palette", Keys: "Ctrl+Shift+K", Label: "Command palette", Section: "Finding your way", NoPalette: true},
	{ID: "findInTerminal", Keys: "Ctrl+Shift+F", Label: "Find in terminal", Section: "Finding your way"},
	{ID: "history", Keys: "Ctrl+Shift+R", Label: "Resume a past conversation", Section: "Finding your way", Page: "history"},
	{ID: "projects", Keys: "Ctrl+Shift+O", Label: "Projects", Section: "Finding your way", Page: "projects"},
	{ID: "help", Keys: "F1", Label: "Help", Section: "Finding your way", Page: "getting-started"},

	// --- the window --------------------------------------------------------
	{ID: "fontUp", Keys: "Ctrl+=", Label: "Increase font size", Section: "The window"},
	{ID: "fontDown", Keys: "Ctrl+-", Label: "Decrease font size", Section: "The window"},
	{ID: "fontReset", Keys: "Ctrl+0", Label: "Reset font size", Section: "The window"},
	{ID: "remote", Label: "Remote access…", Section: "The window", Page: "remote"},
	{ID: "detach", Label: "Detach — close the window, leave agents running", Section: "The window", Page: "persistence"},
	{ID: "quit", Label: "Quit — stop every agent in every project", Section: "The window",
		Confirm: "Stop every agent in every open project?"},
}

// byID indexes the table for placeholder expansion and lookups.
var byID = func() map[string]Key {
	m := make(map[string]Key, len(Keys))
	for _, k := range Keys {
		m[k.ID] = k
	}
	return m
}()

// Name is the action as prose calls it: the label up to the dash that
// introduces the gloss the palette needs and a sentence does not. "Detach —
// close the window, leave agents running" is the palette entry; "Detach" is
// what a page says.
func (k Key) Name() string {
	if i := strings.Index(k.Label, " — "); i > 0 {
		return k.Label[:i]
	}
	return k.Label
}

// Lookup returns the action with the given id.
func Lookup(id string) (Key, bool) {
	k, ok := byID[id]
	return k, ok
}

// InSection returns the actions of one section, in table order.
func InSection(section string) []Key {
	var out []Key
	for _, k := range Keys {
		if k.Section == section {
			out = append(out, k)
		}
	}
	return out
}
