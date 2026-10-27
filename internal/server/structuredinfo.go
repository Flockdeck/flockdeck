package server

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/jmwri/flockdeck/internal/structured"
)

// structuredInfo answers with the agent behind a structured pane as plain text:
// who it says it is, how it signs in, what it can do, the session, mode and
// model, the names of the MCP servers the session was given, the directory and
// the adapter. It holds names only. An MCP server's command, arguments and
// environment, the agent's environment and any key never reach it, and the
// agent's own wording is cleaned and cut. It is for a window on this machine
// (structuredCommand refuses the relay), and the answer is a teamResult with the
// text in Info.

const (
	// structuredInfoMax bounds the whole text; infoLine bounds one value.
	structuredInfoMax  = 6000
	structuredInfoLine = 200
)

func (s *Server) structuredInfo(c *controlClient, cmd command) {
	rt := s.ws.StructuredRuntime(cmd.ID)
	if rt == nil {
		refuse(c, cmd, errStructured(structuredNotPane))
		return
	}
	text, _ := clipStructured(infoText(rt.Info()), structuredInfoMax)
	c.sendJSON(teamResultMsg{Type: "teamResult", Req: cmd.Req, Op: "structuredInfo", OK: true, Info: text})
}

// infoText is the view, one fact to a line. Every value is one line of at most
// structuredInfoLine bytes. The fixed rows come first and the agent's settings
// last, and a setting that would take the text past structuredInfoMax is left
// out, with a "Cut" row saying so.
func infoText(i structured.Info) string {
	var b strings.Builder
	row := func(name, v string) {
		v = infoValue(v)
		if v == "" {
			v = "not reported"
		}
		fmt.Fprintf(&b, "%s: %s\n", name, v)
	}
	list := func(name string, vs []string) {
		if len(vs) == 0 {
			row(name, "none")
			return
		}
		row(name, strings.Join(vs, ", "))
	}
	agent := i.Name
	if agent != "" && i.Version != "" {
		agent += " " + i.Version
	}
	row("Agent", agent)
	row("State", string(i.Phase))
	row("Catalog entry", i.Catalog)
	row("Adapter", i.Adapter)
	row("Directory", i.Cwd)
	row("Session", i.SessionID)
	list("Sign-in methods", i.Auth)
	if st := i.Setup; st != nil {
		row("Sign-in needed", st.Message)
		row("How to sign in", st.Login)
	}
	ss := i.Session
	if m := ss.Modes; m != nil {
		row("Mode", choiceName(m.Current, func(yield func(id, name string)) {
			for _, a := range m.Available {
				yield(a.ID, a.Name)
			}
		}))
	}
	if m := ss.Models; m != nil {
		row("Model", choiceName(m.Current, func(yield func(id, name string)) {
			for _, a := range m.Available {
				yield(a.ID, a.Name)
			}
		}))
	}
	list("MCP servers", i.MCP)
	c := ss.Capabilities
	var caps []string
	for _, f := range []struct {
		on   bool
		name string
	}{
		{c.LoadSession, "load session"}, {c.Resume, "resume session"}, {c.List, "list sessions"}, {c.Fork, "fork session"},
		{c.Prompt.Image, "images"}, {c.Prompt.Audio, "audio"}, {c.Prompt.EmbeddedContext, "embedded context"},
		{c.MCP.HTTP, "MCP over http"}, {c.MCP.SSE, "MCP over sse"},
	} {
		if f.on {
			caps = append(caps, f.name)
		}
	}
	list("Agent can", caps)
	row("Slash commands", fmt.Sprint(len(ss.Commands)))
	// The settings: each row is made apart and added only while the text, with
	// room kept for the Cut row, stays within the limit.
	const cutRow = "Cut: some settings are left out\n"
	cut := false
	for n, o := range ss.ConfigOptions {
		if n == structuredConfigMax {
			cut = true
			break
		}
		name, _ := clipLine(strings.ReplaceAll(infoPlain(o.ID), ": ", " "), 60)
		cur := infoValue(choiceName(o.Current, func(yield func(id, name string)) {
			for _, v := range o.Values {
				yield(v.Value, v.Name)
			}
		}))
		if cur == "" {
			cur = "not reported"
		}
		line := "Setting " + name + ": " + cur + "\n"
		if b.Len()+len(line)+len(cutRow) > structuredInfoMax {
			cut = true
			break
		}
		b.WriteString(line)
	}
	if cut {
		b.WriteString(cutRow)
	}
	return b.String()
}

// infoPlain removes what makes text more than letters: control characters (also
// the C1 ones), format characters such as the bidi overrides, and the line and
// paragraph separators. A line break becomes a space.
func infoPlain(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == 0x0A || r == 0x0D || r == 0x09 || r == 0x2028 || r == 0x2029:
			return ' '
		case unicode.IsControl(r) || unicode.Is(unicode.Cf, r):
			return -1
		}
		return r
	}, strings.ToValidUTF8(s, "?"))
}

// infoValue is infoPlain cut to one value's length.
func infoValue(s string) string {
	v, _ := clipLine(infoPlain(s), structuredInfoLine)
	return v
}

// choiceName is the current id with its name when the list of choices has one.
func choiceName(current string, each func(func(id, name string))) string {
	if current == "" {
		return ""
	}
	out := current
	each(func(id, name string) {
		if id == current && name != "" && name != id {
			out = name + " (" + id + ")"
		}
	})
	return out
}
