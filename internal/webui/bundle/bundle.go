// Package bundle joins the front end's ES modules into one file, and its
// stylesheets into one, so a first load is a handful of requests and not a
// hundred. On a phone's link every request costs round trips, and that, not the
// bytes, is what a first load spends (docs/first-load.md).
//
// It is not a general bundler. It reads the module syntax this front end uses
// and nothing else, and it refuses what it does not read, so a file written in
// another style fails a test and not a page. It reads relative imports of names
// (import { a, b as c }), of a namespace (import * as ns) and of a default,
// export of a function, class or const, export of a list, and re-export of a
// list. It refuses a bare import, import() and import.meta, export of a let or
// var (a binding that changes would be copied, not shared), and an import
// cycle. It also strips comments and spaces (Strip). The sources are never changed; the node tests run on them.
//
// Each module becomes a function of its own, so two modules that both have a
// top-level name such as str do not meet, and the function runs the first time
// something requires it, which is the order the browser would have run the
// modules in.
package bundle

import (
	"bytes"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"strconv"
	"strings"
)

const ident = `[A-Za-z_$][\w$]*`

// A statement starts at the start of a line or, in minified code, after a ; or }.
const start = `(?m)(^|[;}])[ \t]*`

var (
	importNS      = regexp.MustCompile(start + `import\s*\*\s*as\s+(` + ident + `)\s*from\s*"([^"]+)"\s*;?`)
	importNames   = regexp.MustCompile(start + `import\s*\{([^}]*)\}\s*from\s*"([^"]+)"\s*;?`)
	importDefault = regexp.MustCompile(start + `import\s+(` + ident + `)\s*from\s*"([^"]+)"\s*;?`)
	exportFrom    = regexp.MustCompile(start + `export\s*\{([^}]*)\}\s*from\s*"([^"]+)"\s*;?`)
	exportList    = regexp.MustCompile(start + `export\s*\{([^}]*)\}\s*;?`)
	exportDefault = regexp.MustCompile(start + `export\s+default\s+(async\s+)?function\s*\*?\s*(` + ident + `)`)
	exportAnon    = regexp.MustCompile(start + `export\s+default\s+function\s*\(`)
	exportDecl    = regexp.MustCompile(start + `export\s+(async\s+function\s*\*?|function\s*\*?|const|let|var|class)\s+(` + ident + `)`)
	leftover      = regexp.MustCompile(start + `(import|export)\b[^\n]{0,40}`)
	// A dynamic import or import.meta would be the page's own, not the module's.
	dynamic = regexp.MustCompile(`\bimport\s*\(|\bimport\.meta\b`)
)

type module struct {
	name string
	body string
	deps []string
}

// Modules joins the module graph that starts at entry (a slash path in fsys,
// such as "main.js") into one script. The script is an ES module itself, with
// no import of its own.
func Modules(fsys fs.FS, entry string) ([]byte, error) {
	b := &builder{fsys: fsys, done: map[string]bool{}, active: map[string]bool{}}
	if err := b.load(entry, ""); err != nil {
		return nil, err
	}
	var out bytes.Buffer
	out.WriteString("// The front end's modules, joined by internal/webui/bundle. The source is the\n// files this was made from; the node tests run on those.\n")
	out.WriteString("const __defs = {};\nconst __made = {};\n")
	out.WriteString("function __req(name) {\n  if (name in __made) return __made[name];\n  const exports = {};\n  __made[name] = exports;\n  __defs[name](exports, __req);\n  return exports;\n}\n")
	for _, m := range b.order {
		fmt.Fprintf(&out, "__defs[%s] = function (__x, __req) {\n\"use strict\";\n%s\n};\n", strconv.Quote(m.name), m.body)
	}
	fmt.Fprintf(&out, "__req(%s);\n", strconv.Quote(entry))
	return out.Bytes(), nil
}

type builder struct {
	fsys   fs.FS
	done   map[string]bool
	active map[string]bool
	order  []*module
}

func (b *builder) load(name, from string) error {
	if b.active[name] {
		return fmt.Errorf("bundle: %s is in an import cycle (reached again from %s)", name, from)
	}
	if b.done[name] {
		return nil
	}
	b.active[name] = true
	src, err := fs.ReadFile(b.fsys, name)
	if err != nil {
		if from == "" {
			return fmt.Errorf("bundle: %w", err)
		}
		return fmt.Errorf("bundle: %s imports %s: %w", from, name, err)
	}
	m, err := transform(name, string(src))
	if err != nil {
		return err
	}
	for _, d := range m.deps {
		if err := b.load(d, name); err != nil {
			return err
		}
	}
	delete(b.active, name)
	b.done[name] = true
	b.order = append(b.order, m)
	return nil
}

// resolve is the path of the module a specifier names, relative to the module
// that imports it.
func resolve(from, spec string) (string, error) {
	if !strings.HasPrefix(spec, "./") && !strings.HasPrefix(spec, "../") {
		return "", fmt.Errorf("bundle: %s imports %q, which is not a relative path", from, spec)
	}
	p := path.Join(path.Dir(from), spec)
	if strings.HasPrefix(p, "../") {
		return "", fmt.Errorf("bundle: %s imports %q, which leaves the assets", from, spec)
	}
	return p, nil
}

// pairs reads "a, b as c" as the pairs (a, a) and (b, c): the name in the
// other module, and the name here.
func pairs(list string) ([][2]string, error) {
	var out [][2]string
	for _, item := range strings.Split(list, ",") {
		f := strings.Fields(item)
		switch {
		case len(f) == 0:
		case len(f) == 1:
			out = append(out, [2]string{f[0], f[0]})
		case len(f) == 3 && f[1] == "as":
			out = append(out, [2]string{f[0], f[2]})
		default:
			return nil, fmt.Errorf("cannot read %q", strings.TrimSpace(item))
		}
	}
	return out, nil
}

func transform(name, src string) (*module, error) {
	m := &module{name: name}
	var failure error
	fail := func(err error) string {
		if failure == nil {
			failure = fmt.Errorf("bundle: %s: %w", name, err)
		}
		return ""
	}
	dep := func(spec string) string {
		p, err := resolve(name, spec)
		if err != nil {
			if failure == nil {
				failure = err
			}
			return `""`
		}
		m.deps = append(m.deps, p)
		return strconv.Quote(p)
	}
	var exports []string // run at the end, once the names exist
	sub := func(re *regexp.Regexp, s string, fn func(g []string) string) string {
		return re.ReplaceAllStringFunc(s, func(match string) string {
			g := re.FindStringSubmatch(match)
			return g[1] + fn(g)
		})
	}
	body := src
	body = sub(importNS, body, func(g []string) string {
		return "const " + g[2] + " = __req(" + dep(g[3]) + ");"
	})
	body = sub(importNames, body, func(g []string) string {
		ps, err := pairs(g[2])
		if err != nil {
			return fail(err)
		}
		parts := make([]string, len(ps))
		for i, p := range ps {
			if p[0] == p[1] {
				parts[i] = p[0]
			} else {
				parts[i] = p[0] + ": " + p[1]
			}
		}
		return "const { " + strings.Join(parts, ", ") + " } = __req(" + dep(g[3]) + ");"
	})
	body = sub(importDefault, body, func(g []string) string {
		return "const " + g[2] + " = __req(" + dep(g[3]) + ").default;"
	})
	body = sub(exportFrom, body, func(g []string) string {
		ps, err := pairs(g[2])
		if err != nil {
			return fail(err)
		}
		var sb strings.Builder
		sb.WriteString("{ const __m = __req(" + dep(g[3]) + ");")
		for _, p := range ps {
			sb.WriteString(" __x." + p[1] + " = __m." + p[0] + ";")
		}
		sb.WriteString(" }")
		return sb.String()
	})
	body = sub(exportList, body, func(g []string) string {
		ps, err := pairs(g[2])
		if err != nil {
			return fail(err)
		}
		for _, p := range ps {
			exports = append(exports, "__x."+p[1]+" = "+p[0]+";")
		}
		return ""
	})
	body = sub(exportDefault, body, func(g []string) string {
		exports = append(exports, "__x.default = "+g[3]+";")
		return strings.TrimSpace(g[2]+" function ") + " " + g[3]
	})
	body = sub(exportAnon, body, func(g []string) string {
		return "__x.default = function("
	})
	body = sub(exportDecl, body, func(g []string) string {
		if g[2] == "let" || g[2] == "var" {
			return fail(fmt.Errorf("export %s %s: a binding that can change would be copied, not shared; use const", g[2], g[3]))
		}
		exports = append(exports, "__x."+g[3]+" = "+g[3]+";")
		return g[2] + " " + g[3]
	})
	if failure != nil {
		return nil, failure
	}
	if dynamic.MatchString(body) {
		return nil, fmt.Errorf("bundle: %s uses import() or import.meta, which the bundle does not carry", name)
	}
	if loc := leftover.FindStringIndex(body); loc != nil {
		return nil, fmt.Errorf("bundle: %s has an import or export it cannot read: %q", name, strings.TrimSpace(body[loc[0]:loc[1]]))
	}
	small, err := Strip([]byte(body + "\n" + strings.Join(exports, "\n")))
	if err != nil {
		return nil, fmt.Errorf("bundle: %s: %w", name, err)
	}
	m.body = string(small)
	return m, nil
}
