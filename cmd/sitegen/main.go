// Command sitegen writes the public landing page.
//
// The page is generated rather than written by hand so that what it claims
// about Flockdeck comes from the repository it is built in: the agents it
// names and the install line sit next to the code that makes them true. The
// help itself stays in the application, behind F1, where it is rendered with
// the real key bindings substituted into it.
//
// The output is plain files with no build step, because the site is served by
// GitHub Pages out of a repository of its own:
//
//	go run ./cmd/sitegen -out ../flockdeck-site -shot shot.png
package main

import (
	"flag"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	out := flag.String("out", "site", "`directory` to write the site into")
	shot := flag.String("shot", "", "`screenshot` to copy in and show on the page")
	repo := flag.String("repo", "https://github.com/jmwri/flockdeck", "`url` of the source repository")
	flag.Parse()

	if err := run(*out, *shot, *repo); err != nil {
		fmt.Fprintln(os.Stderr, "sitegen:", err)
		os.Exit(1)
	}
}

func run(out, shot, repo string) error {
	if err := os.MkdirAll(out, 0o755); err != nil {
		return fmt.Errorf("create the output directory: %w", err)
	}

	s := site{repo: repo}
	if shot != "" {
		s.shot = filepath.Base(shot)
	}

	files := map[string]string{
		"index.html": s.landing(),
		"site.css":   stylesheet,
		// Pages runs what it is given through Jekyll unless it is told not to,
		// and Jekyll hides every path beginning with an underscore. There is
		// nothing here for it to do.
		".nojekyll": "",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(out, name), []byte(body), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", name, err)
		}
	}
	if shot != "" {
		data, err := os.ReadFile(shot)
		if err != nil {
			return fmt.Errorf("read the screenshot: %w", err)
		}
		if err := os.WriteFile(filepath.Join(out, s.shot), data, 0o644); err != nil {
			return fmt.Errorf("write the screenshot: %w", err)
		}
	}
	fmt.Printf("wrote the site to %s\n", out)
	return nil
}

// site is what the page needs to know about where it came from.
type site struct {
	repo string
	shot string
}

func (s site) landing() string {
	var b strings.Builder
	b.WriteString(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Flockdeck — run several coding agents at once</title>
<meta name="description" content="A desktop application for running several coding agents at once, each in a real terminal, with per-agent status, git worktrees and layout persistence.">
<link rel="stylesheet" href="site.css">
</head>
<body>
`)
	fmt.Fprintf(&b, `<header class="top">
  <span class="wordmark">Flockdeck</span>
  <nav><a href="%s">Source</a></nav>
</header>

<main class="wrap">
<section class="hero">
  <h1>Run several coding agents at once.</h1>
  <p class="lede">A desktop application that puts each agent in a real
     terminal — tabs, split panes, per-agent status, git worktrees — and tells
     you, at a glance, which one is waiting on you.</p>
  <p class="cta">
    <a class="button" href="#install">Install</a>
    <a class="button ghost" href="%s">Source on GitHub</a>
  </p>
</section>

`, html.EscapeString(s.repo), html.EscapeString(s.repo))

	if s.shot != "" {
		fmt.Fprintf(&b, `
<section class="shot">
  <img src="%s" alt="The Flockdeck window with the agent picker open over a pane. Claude Code is listed under Installed with its four models; Codex, Gemini, Aider, opencode, Cursor Agent and the API agents are listed below it, greyed, each with the command that installs it.">
  <p class="caption">Choosing the agent and model for a pane.</p>
</section>
`, html.EscapeString(s.shot))
	}

	b.WriteString(`
<section class="why">
  <h2>Why</h2>
  <p>Running one agent is easy. Running several is not: they finish at
     different times, they block on permission prompts, and you lose track of
     which one is waiting on you. Flockdeck answers one question at a glance —
     <strong>which agent needs me right now</strong> — and gives each agent its
     own branch to work on.</p>
</section>

<section class="features">
  <h2>What it gives you</h2>
  <div class="grid">
    <div class="card">
      <h3>Any agent, any model</h3>
      <p>Claude Code, Codex, Gemini, Aider, opencode or Cursor's agent — or a
         model API spoken to directly by the binary itself, including a local
         Ollama or any OpenAI-compatible endpoint. Picked per pane, remembered
         per project.</p>
    </div>
    <div class="card">
      <h3>One glance tells you who needs you</h3>
      <p>Per-pane status from each agent's own lifecycle where it reports one,
         tab and project markers, and a desktop notification when an agent
         blocks while you are looking elsewhere.</p>
    </div>
    <div class="card">
      <h3>Worktrees as a first-class thing</h3>
      <p>Create, inspect, occupy and remove them without leaving the app, so
         several agents work in parallel without touching each other's files.</p>
    </div>
    <div class="card">
      <h3>One plan becomes several agents</h3>
      <p>Fan out reads the list an agent just wrote, hands you an editable copy,
         and starts an agent per line — each in a worktree and branch of its
         own.</p>
    </div>
    <div class="card">
      <h3>Each agent knows where it is</h3>
      <p>Its own conversation, its own checkout, and a briefing at session start
         on which pane it is and who else is working — so being one of several
         is something it can act on.</p>
    </div>
    <div class="card">
      <h3>Everything comes back</h3>
      <p>Layouts, the set of projects you had open and each pane's conversation
         are restored. Agents can outlive the window: detach, close it, reattach
         later.</p>
    </div>
  </div>
</section>

<section class="install" id="install">
  <h2>Install</h2>
  <p>One binary, nothing beside it. It builds for Windows, macOS and Linux on
     both architectures with nothing but the Go toolchain.</p>
  <pre><code>go install github.com/jmwri/flockdeck@latest</code></pre>
  <p>Or from a clone:</p>
  <pre><code>make build      # a binary for this machine
make dist       # binaries for all six supported platforms</code></pre>
  <p>A CLI agent uses whatever login it already has, so Claude Code panes need
     no key and no separate account. Talking to a model API directly needs one,
     and <code>flockdeck keys</code> keeps it.</p>
  <p class="aside">The documentation lives in the application: press
     <kbd>F1</kbd> for the help, which is written against the key bindings your
     build actually has.</p>
</section>
</main>
`)
	fmt.Fprintf(&b, `<footer class="foot">
  <p><a href="%s">Source on GitHub</a></p>
</footer>
</body>
</html>
`, html.EscapeString(s.repo))
	return b.String()
}

const stylesheet = `/* Generated by cmd/sitegen in the flockdeck repository. */

:root {
  color-scheme: light dark;
  --bg: #fbfbfa;
  --fg: #1b1b1a;
  --dim: #5f5f5c;
  --rule: #e3e3df;
  --card: #ffffff;
  --accent: #1a5fb4;
  --code-bg: #f2f2ef;
  --shadow: 0 1px 2px rgba(0, 0, 0, .06), 0 8px 24px rgba(0, 0, 0, .04);
}

@media (prefers-color-scheme: dark) {
  :root {
    --bg: #16161a;
    --fg: #e7e7e4;
    --dim: #a0a09b;
    --rule: #2b2b31;
    --card: #1d1d22;
    --accent: #79b0f5;
    --code-bg: #111115;
    --shadow: none;
  }
}

* { box-sizing: border-box; }

body {
  margin: 0;
  background: var(--bg);
  color: var(--fg);
  font: 16px/1.65 ui-sans-serif, system-ui, -apple-system, "Segoe UI", Roboto, sans-serif;
  -webkit-font-smoothing: antialiased;
}

a { color: var(--accent); }
a:hover { text-decoration: none; }

.top {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 16px 24px;
  border-bottom: 1px solid var(--rule);
}
.wordmark { font-weight: 650; font-size: 1.05rem; letter-spacing: -.01em; }
.top nav a { color: var(--dim); text-decoration: none; font-size: .95rem; }
.top nav a:hover { color: var(--fg); }

.wrap { max-width: 860px; margin: 0 auto; padding: 0 24px 72px; }

.hero { padding: 72px 0 32px; }
.hero h1 {
  font-size: clamp(2rem, 5vw, 3rem);
  line-height: 1.1;
  letter-spacing: -.025em;
  margin: 0 0 20px;
}
.lede { font-size: 1.15rem; color: var(--dim); max-width: 60ch; margin: 0 0 28px; }
.cta { display: flex; flex-wrap: wrap; gap: 12px; margin: 0; }

.button {
  display: inline-block;
  padding: 10px 20px;
  border-radius: 8px;
  background: var(--accent);
  color: var(--bg);
  text-decoration: none;
  font-weight: 550;
  font-size: .95rem;
}
.button.ghost { background: transparent; color: var(--fg); border: 1px solid var(--rule); }

section { margin-top: 56px; }
h2 { font-size: 1.5rem; letter-spacing: -.015em; margin: 0 0 16px; }
h3 { font-size: 1.05rem; margin: 0 0 8px; }

.terminal pre, .install pre {
  background: var(--code-bg);
  border: 1px solid var(--rule);
  border-radius: 10px;
  padding: 18px;
  overflow-x: auto;
  font: 12.5px/1.5 ui-monospace, SFMono-Regular, "SF Mono", Menlo, Consolas, monospace;
}
.install pre { font-size: 13px; }
code {
  font-family: ui-monospace, SFMono-Regular, "SF Mono", Menlo, Consolas, monospace;
  font-size: .9em;
}
.install p code { background: var(--code-bg); padding: 1px 5px; border-radius: 4px; }
.install pre code { background: none; padding: 0; font-size: inherit; }

.shot img {
  max-width: 100%;
  border-radius: 10px;
  border: 1px solid var(--rule);
  box-shadow: var(--shadow);
  display: block;
}
.caption { color: var(--dim); font-size: .9rem; margin-top: 10px; }

.why p { max-width: 64ch; }

.grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(250px, 1fr));
  gap: 16px;
}
.card {
  background: var(--card);
  border: 1px solid var(--rule);
  border-radius: 10px;
  padding: 20px;
  box-shadow: var(--shadow);
}
.card p { margin: 0; color: var(--dim); font-size: .95rem; }

.aside { color: var(--dim); font-size: .95rem; }

kbd {
  font: 600 .8em ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  background: var(--card);
  border: 1px solid var(--rule);
  border-bottom-width: 2px;
  border-radius: 5px;
  padding: 1px 6px;
  white-space: nowrap;
}

.foot {
  border-top: 1px solid var(--rule);
  padding: 28px 24px 48px;
  color: var(--dim);
  font-size: .9rem;
  text-align: center;
}

@media (max-width: 640px) {
  .hero { padding: 48px 0 24px; }
}
`
