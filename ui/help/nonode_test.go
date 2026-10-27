package help

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Flockdeck does not use Node and does not install agents, so an agent that
// cannot start is only ever described as unavailable. No text a person can read
// says otherwise: not a help page, and not a string in the interface.

// helpNode matches Node as a whole word in prose, and an npm install line.
// \b counts an underscore as part of a word, so node_modules, NodePort and
// nodes do not match, while "node modules", "node-fetch" and "Node." do. A page
// may still name npm as a command the reader runs themselves.
var helpNode = regexp.MustCompile(`(?i)\bnode(\.?js)?\b|\bnpm (i|install)\b`)

// uiNode is narrower, because the interface code is full of graph nodes and a
// module called Node.js: it matches the ways Node would be named to a person.
var uiNode = regexp.MustCompile(`(?i)\bnodejs\b|\bnode\.js\b|\bnpm (i|install)\b|\bnpx\b|\bnode (\d|or (newer|later)|required|is (needed|missing))`)

// agentInstall matches the ways the text about an agent that cannot start used
// to tell somebody to go and get it. It is applied to the files that write that
// text, in full, comments included.
var agentInstall = regexp.MustCompile(`(?i)not installed|\binstall (it|the agent|node)\b|where to get|how to get it|, see https?://|\bnpm (i|install)\b`)

func TestTheGuardPatternsMatchWhatTheyShould(t *testing.T) {
	for in, want := range map[string]bool{
		"Install Node 20 or newer": true, "needs Node.js": true, "node was not found": true, "see nodejs.org": true,
		"npm install -g x": true, "the node.": true, "node modules": true,
		"node_modules": false, "NodePort": false, "nodes": false, "the graph nodes": false, "tree-node": true,
	} {
		if got := helpNode.MatchString(in); got != want {
			t.Errorf("helpNode(%q) = %v, want %v", in, got, want)
		}
	}
	for in, want := range map[string]bool{
		"Install Node 20": true, "needs Node.js": true, "see nodejs.org": true, "npm i -g codex": true, "Node required": true,
		"const node = tree.node": false, "nodes.map": false, "node_modules": false, "clip(node)": false,
	} {
		if got := uiNode.MatchString(in); got != want {
			t.Errorf("uiNode(%q) = %v, want %v", in, got, want)
		}
	}
	for in, want := range map[string]bool{
		"Codex (not installed, see https://x)": true, "install it from the vendor": true, "where to get it": true,
		"Codex (unavailable)": false, "Flockdeck does not install agents": false,
	} {
		if got := agentInstall.MatchString(in); got != want {
			t.Errorf("agentInstall(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestNoHelpPageMentionsNode(t *testing.T) {
	pages, err := Pages()
	if err != nil {
		t.Fatalf("Pages: %v", err)
	}
	for _, p := range pages {
		for _, text := range []string{p.Title, p.Summary, p.Text} {
			if m := helpNode.FindString(text); m != "" {
				i := strings.Index(text, m)
				t.Errorf("%s: mentions %q: ...%s...", p.Slug, m, text[max(0, i-40):min(len(text), i+40)])
			}
		}
	}
}

// The pages that write about agents that cannot start do not send the reader
// off to install one.
func TestAgentHelpPagesDoNotTellYouToInstall(t *testing.T) {
	for _, name := range []string{"agents.md", "troubleshooting.md"} {
		b, err := os.ReadFile(filepath.Join("pages", name))
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			if m := agentInstall.FindString(line); m != "" {
				t.Errorf("%s:%d: says %q", name, i+1, m)
			}
		}
	}
}

func TestNoInterfaceStringMentionsNode(t *testing.T) {
	root := filepath.Join("..", "src")
	checked := 0
	// The files that write the text about an agent that cannot start.
	agentFiles := map[string]bool{"lib/lifecycle.js": true, "lib/baton.js": true}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		ext := filepath.Ext(path)
		if ext != ".js" && ext != ".json" {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		checked++
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		for i, line := range strings.Split(string(b), "\n") {
			if agentFiles[rel] {
				if m := agentInstall.FindString(line); m != "" {
					t.Errorf("%s:%d: says %q", rel, i+1, m)
				}
			}
			if ext == ".json" {
				// Data, all of it shown or searched: no line is skipped.
				if m := helpNode.FindString(line); m != "" {
					t.Errorf("%s:%d: mentions %q", rel, i+1, m)
				}
				continue
			}
			l := strings.TrimSpace(line)
			// Imports name the Node.js module of the graph; comments are not shown.
			if strings.HasPrefix(l, "import ") || strings.HasPrefix(l, "} from ") ||
				strings.HasPrefix(l, "//") || strings.HasPrefix(l, "/*") || strings.HasPrefix(l, "*") {
				continue
			}
			if m := uiNode.FindString(l); m != "" {
				t.Errorf("%s:%d: mentions %q", rel, i+1, m)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 50 {
		t.Fatalf("only %d interface files were read; the walk is looking in the wrong place", checked)
	}
}
