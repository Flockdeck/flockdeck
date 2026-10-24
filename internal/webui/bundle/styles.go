package bundle

import (
	"bytes"
	"fmt"
	"io/fs"
	"regexp"
	"strings"
)

var (
	styleLink = regexp.MustCompile(`[ \t]*<link rel="stylesheet" href="assets/([^"]+)">[ \t]*\n?`)
	cssURL    = regexp.MustCompile(`url\(\s*["']?([^"')]+)`)
)

// Styles joins the stylesheets that index names, in the order it names them
// (the cascade depends on it), and returns them as one file together with the
// page rewritten to name that file, as combined, in place of the first of them.
// combined is a path in fsys, relative to the assets directory, such as
// "app.css"; every other stylesheet is left out of the page.
//
// A relative url() is read from the stylesheet's own folder, so one in a
// stylesheet that is not in the same folder as combined would point at another
// file once joined: that is refused. (Only app.css has any, and it is at the
// top.)
func Styles(fsys fs.FS, index []byte, combined string) (css, page []byte, err error) {
	links := styleLink.FindAllSubmatch(index, -1)
	if len(links) == 0 {
		return nil, nil, fmt.Errorf("bundle: the page links no stylesheet")
	}
	var out bytes.Buffer
	seen := false
	for _, l := range links {
		name := string(l[1])
		if name == combined {
			seen = true
		}
		b, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, nil, fmt.Errorf("bundle: %w", err)
		}
		if dir(name) != dir(combined) {
			for _, u := range cssURL.FindAllSubmatch(b, -1) {
				if ref := string(u[1]); !strings.HasPrefix(ref, "data:") && !strings.HasPrefix(ref, "#") {
					return nil, nil, fmt.Errorf("bundle: %s has url(%s), which would point elsewhere once joined into %s", name, ref, combined)
				}
			}
		}
		fmt.Fprintf(&out, "/* %s */\n", name)
		out.Write(b)
		if !bytes.HasSuffix(b, []byte("\n")) {
			out.WriteByte('\n')
		}
	}
	if !seen {
		return nil, nil, fmt.Errorf("bundle: the page does not link %s, so it cannot stand for the others", combined)
	}
	first := true
	page = styleLink.ReplaceAllFunc(index, func([]byte) []byte {
		if first {
			first = false
			return []byte(`<link rel="stylesheet" href="assets/` + combined + `">` + "\n")
		}
		return nil
	})
	return out.Bytes(), page, nil
}

func dir(name string) string {
	if i := strings.LastIndex(name, "/"); i >= 0 {
		return name[:i]
	}
	return ""
}
