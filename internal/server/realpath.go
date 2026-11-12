package server

import "path/filepath"

// realPath is where path really is, every link in it followed. A path that does
// not exist yet is resolved through its longest part that does, and the missing
// rest is put back on the end: proj/link/new, with link leading out of proj and
// new not made, is where link leads, plus new. Returning the path as typed
// would call it inside proj, and whatever is made there lands outside.
func realPath(path string) string {
	path = filepath.Clean(path)
	var tail []string
	for p := path; ; {
		if r, ok := resolveExisting(p); ok {
			for i := len(tail) - 1; i >= 0; i-- {
				r = filepath.Join(r, tail[i])
			}
			return r
		}
		parent := filepath.Dir(p)
		if parent == p {
			return path
		}
		tail = append(tail, filepath.Base(p))
		p = parent
	}
}
