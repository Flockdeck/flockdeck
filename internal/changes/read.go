package changes

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jmwri/flockdeck/internal/gitx"
)

const (
	// MaxFiles is how many files a branch lists in a push; the rest are counted in
	// Omitted and read with filesPage.
	MaxFiles = 2000
	// maxUntracked is the most untracked files read for a branch.
	maxUntracked = 5000
	// maxCountBytes is the largest untracked file whose lines are counted.
	maxCountBytes = 1 << 20
	// maxBinarySizes is how many binary files have their old size looked up.
	maxBinarySizes = 50
)

// Read reads one pane's checkout. maxFiles caps the files listed (the rest are
// counted in Omitted); zero lists them all, for filesPage. A checkout that cannot
// be read is a Branch with only Pane, Cwd and Error.
func Read(ctx context.Context, pane, cwd string, maxFiles int) Branch {
	b, _ := read(ctx, pane, cwd, maxFiles)
	return b
}

// read is Read that also returns the repository the checkout belongs to, for
// telling which branches can share a path.
func read(ctx context.Context, pane, cwd string, maxFiles int) (Branch, string) {
	fail := func(msg string) (Branch, string) { return Branch{Pane: pane, Cwd: cwd, Error: msg}, "" }
	if fi, err := os.Stat(cwd); err != nil || !fi.IsDir() {
		return fail("the directory is gone")
	}
	if !gitx.Available() {
		return fail("git is not installed")
	}
	if _, err := gitx.Root(cwd); err != nil {
		if strings.Contains(err.Error(), "or any of the parent directories") {
			return fail("not a git repository")
		}
		return fail("could not read the repository: " + firstLine(err.Error()))
	}
	common, _ := gitx.CommonDir(cwd)
	st := gitx.StatusOf(cwd)
	if st.Unborn || st.Commit == "" {
		return fail("the branch has no commits yet")
	}
	b := Branch{Pane: pane, Cwd: cwd, HeadSha: st.Commit, Upstream: st.Upstream}
	if st.Detached {
		b.Detached = true
	} else {
		b.Branch = st.Branch
	}
	dirty, untracked := st.Dirty, st.Untracked
	b.Uncommitted, b.Untracked = &dirty, &untracked

	base := gitx.BaseOf(ctx, cwd)
	if base == "" {
		return fail("no base branch was found to compare against")
	}
	b.Base = base
	baseSha, err := git(ctx, cwd, "merge-base", "--end-of-options", base, "HEAD")
	if err != nil || strings.TrimSpace(baseSha) == "" {
		return fail("the branch shares no history with " + base)
	}
	b.BaseSha = strings.TrimSpace(baseSha)

	ahead, behind := st.Ahead, st.Behind
	if st.Upstream == "" {
		if out, err := git(ctx, cwd, "rev-list", "--left-right", "--count", "--end-of-options", base+"...HEAD"); err == nil {
			f := strings.Fields(out)
			if len(f) == 2 {
				behind, _ = strconv.Atoi(f[0])
				ahead, _ = strconv.Atoi(f[1])
			}
		}
	}
	b.Ahead, b.Behind = &ahead, &behind

	if out, err := git(ctx, cwd, "log", "-1", "--format=%H%x1f%s%x1f%cI"); err == nil {
		if f := strings.Split(strings.TrimRight(out, "\r\n"), "\x1f"); len(f) == 3 {
			b.LastCommit = &LastCommit{Sha: f[0], Subject: f[1], At: f[2]}
		}
	}

	files, err := filesOf(ctx, cwd, b.BaseSha)
	if err != nil {
		return fail("could not read the changed files: " + firstLine(err.Error()))
	}
	sort.SliceStable(files, func(i, j int) bool {
		a, c := files[i].Added+files[i].Removed, files[j].Added+files[j].Removed
		if a != c {
			return a > c
		}
		return files[i].Path < files[j].Path
	})
	for i := range files {
		files[i].Owner = pane
	}
	b.Files = files
	none := []struct{}{}
	b.Checks, b.Reviews, b.Comments = &none, &none, &none
	if maxFiles > 0 && len(files) > maxFiles {
		b.Omitted = len(files) - maxFiles
		b.Files = files[:maxFiles]
		b.all = files
	}
	return b, common
}

// filesOf lists what differs from baseSha in the working tree: the committed
// changes since the merge base and the uncommitted ones together, a file in both
// once, and the untracked files.
func filesOf(ctx context.Context, cwd, baseSha string) ([]File, error) {
	raw, err := git(ctx, cwd, "diff", "--raw", "-z", "-M", "--no-color", "--no-ext-diff", "--end-of-options", baseSha)
	if err != nil {
		return nil, err
	}
	files := parseRaw(raw)
	byPath := map[string]int{}
	for i, f := range files {
		byPath[f.Path] = i
	}

	num, err := git(ctx, cwd, "diff", "--numstat", "-z", "-M", "--no-color", "--no-ext-diff", "--end-of-options", baseSha)
	if err != nil {
		return nil, err
	}
	for path, n := range parseNumstat(num) {
		if i, ok := byPath[path]; ok {
			if n.binary {
				files[i].Binary = true
			} else {
				files[i].Added, files[i].Removed = n.added, n.removed
			}
		}
	}

	if cached, err := git(ctx, cwd, "diff", "--cached", "--name-only", "-z", "--no-ext-diff"); err == nil {
		for _, p := range splitZ(cached) {
			if i, ok := byPath[p]; ok {
				files[i].Staged = true
			}
		}
	}

	if out, err := git(ctx, cwd, "ls-files", "--others", "--exclude-standard", "-z"); err == nil {
		for n, p := range splitZ(out) {
			if n >= maxUntracked {
				break
			}
			if _, tracked := byPath[p]; tracked {
				continue
			}
			f := File{Path: p, Status: "A"}
			countUntracked(filepath.Join(cwd, filepath.FromSlash(p)), &f)
			byPath[p] = len(files)
			files = append(files, f)
		}
	}

	looked := 0
	for i := range files {
		f := &files[i]
		f.Generated = Generated(f.Path)
		if !f.Binary {
			continue
		}
		f.Added, f.Removed = 0, 0
		if f.Status != "D" {
			if fi, err := os.Lstat(filepath.Join(cwd, filepath.FromSlash(f.Path))); err == nil && fi.Mode().IsRegular() {
				n := fi.Size()
				f.SizeAfter = &n
			}
		}
		if f.Status != "A" && looked < maxBinarySizes {
			looked++
			old := f.Path
			if f.OldPath != "" {
				old = f.OldPath
			}
			if out, err := git(ctx, cwd, "cat-file", "-s", "--end-of-options", baseSha+":"+old); err == nil {
				if n, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64); err == nil {
					f.SizeBefore = &n
				}
			}
		}
	}
	return files, nil
}

// parseRaw reads `git diff --raw -z -M`: ":oldmode newmode oldsha newsha S\0path\0",
// and for a rename or copy ":... R087\0old\0new\0".
func parseRaw(out string) []File {
	var files []File
	toks := splitZ(out)
	for i := 0; i < len(toks); i++ {
		meta := toks[i]
		if !strings.HasPrefix(meta, ":") {
			continue
		}
		f := strings.Fields(meta[1:])
		if len(f) < 5 {
			continue
		}
		oldMode, newMode, code := f[0], f[1], f[4]
		kind := code[:1]
		file := File{Status: kind}
		switch kind {
		case "R", "C":
			if i+2 >= len(toks) {
				return files
			}
			file.OldPath, file.Path = toks[i+1], toks[i+2]
			i += 2
			file.Similarity, _ = strconv.Atoi(code[1:])
		case "A", "M", "D", "U", "T":
			if i+1 >= len(toks) {
				return files
			}
			file.Path = toks[i+1]
			i++
			if kind == "T" {
				file.Status = "M"
			}
		default:
			continue
		}
		if oldMode != newMode && oldMode != "000000" && newMode != "000000" {
			file.ModeChange = oldMode + " " + newMode
		}
		files = append(files, file)
	}
	return files
}

type count struct {
	added, removed int
	binary         bool
}

// parseNumstat reads `git diff --numstat -z -M`: "A\tR\tpath\0", and for a rename
// "A\tR\t\0old\0new\0". A binary file has "-" for both. It is keyed by the path
// after the change.
func parseNumstat(out string) map[string]count {
	res := map[string]count{}
	toks := splitZ(out)
	for i := 0; i < len(toks); i++ {
		parts := strings.SplitN(toks[i], "\t", 3)
		if len(parts) != 3 {
			continue
		}
		path := parts[2]
		if path == "" {
			if i+2 >= len(toks) {
				return res
			}
			path = toks[i+2]
			i += 2
		}
		var c count
		if parts[0] == "-" || parts[1] == "-" {
			c.binary = true
		} else {
			c.added, _ = strconv.Atoi(parts[0])
			c.removed, _ = strconv.Atoi(parts[1])
		}
		res[path] = c
	}
	return res
}

func splitZ(s string) []string {
	var out []string
	for _, t := range strings.Split(s, "\x00") {
		if t != "" {
			out = append(out, t)
		}
	}
	return out
}

// countUntracked fills in the lines of a new file, or marks it binary. A link or
// a file over maxCountBytes is listed with no count.
func countUntracked(path string, f *File) {
	fi, err := os.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() {
		return
	}
	if fi.Size() > maxCountBytes {
		n := fi.Size()
		f.SizeAfter = &n
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	head := data
	if len(head) > 8000 {
		head = head[:8000]
	}
	for _, c := range head {
		if c == 0 {
			f.Binary = true
			return
		}
	}
	lines := strings.Count(string(data), "\n")
	if len(data) > 0 && data[len(data)-1] != '\n' {
		lines++
	}
	f.Added = lines
}

// Generated says the path rules mark a file as generated: lockfiles, vendored
// directories, minified bundles and generated protobuf code.
func Generated(path string) bool {
	slash := strings.ReplaceAll(path, "\\", "/")
	for _, part := range strings.Split(slash, "/") {
		if part == "vendor" || part == "node_modules" {
			return true
		}
	}
	base := slash[strings.LastIndex(slash, "/")+1:]
	switch base {
	case "go.sum", "package-lock.json", "yarn.lock", "pnpm-lock.yaml", "Cargo.lock":
		return true
	}
	return strings.HasSuffix(base, ".min.js") || strings.HasSuffix(base, ".min.css") || strings.HasSuffix(base, ".pb.go")
}

// git runs a read-only git command in dir, with the protections of every other
// command Flockdeck runs, and a limit on how long it may take.
func git(ctx context.Context, dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	return gitx.ReadOnly(ctx, dir, args...)
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}
