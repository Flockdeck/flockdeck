package changes

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
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
	// maxGitOutput is the most of one git command's output that is kept. A
	// repository can make a listing of changes as large as it likes, so past this
	// the checkout is reported as having too many changes to list.
	maxGitOutput = 16 << 20
	// maxCountTotal is the most bytes of untracked files read to count lines in
	// one read of a checkout; files past it are listed without a count.
	maxCountTotal = 64 << 20
	// gitReads is how many git commands run at once for all the windows.
	gitReads = 6
)

// tooMany is said of a checkout whose listing of changes is past the limit.
const tooMany = "too many changes to list"

// gitSlots limits the git processes the changes view has running at once, across
// every window and every watch.
var gitSlots = make(chan struct{}, gitReads)

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

	base, baseID, err := baseOf(ctx, cwd, st.Branch)
	if err != nil {
		return fail(err.Error())
	}
	b.Base = base
	baseSha, err := git(ctx, cwd, "merge-base", "--end-of-options", baseID, "HEAD")
	if err != nil || strings.TrimSpace(baseSha) == "" {
		return fail("the branch shares no history with " + base)
	}
	b.BaseSha = strings.TrimSpace(baseSha)

	ahead, behind := st.Ahead, st.Behind
	if st.Upstream == "" {
		if out, err := git(ctx, cwd, "rev-list", "--left-right", "--count", "--end-of-options", baseID+"...HEAD"); err == nil {
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
		if errors.Is(err, gitx.ErrTooLarge) {
			return fail(tooMany)
		}
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
	checks, reviews, comments := []Check{}, []Review{}, []Comment{}
	b.Checks, b.Reviews, b.Comments = &checks, &reviews, &comments
	if maxFiles > 0 && len(files) > maxFiles {
		b.Omitted = len(files) - maxFiles
		b.Files = files[:maxFiles]
		b.all = files
	}
	return b, common
}

// baseOf finds the branch a checkout's changes are measured from: of the
// candidates (the main worktree's branch, origin's default, main and master) the
// nearest that shares history, as the radar chooses. The checkout's own branch is
// left out, since a pane in the main worktree on a feature branch would otherwise
// be measured against itself and show nothing; it is used only when it is the one
// candidate. name is the branch as a person writes it; id is its commit.
func baseOf(ctx context.Context, cwd, own string) (name, id string, err error) {
	bases, err := gitx.ResolveBases(ctx, cwd)
	if err != nil {
		if errors.Is(err, gitx.ErrNoBase) {
			return "", "", errors.New("no base branch was found to compare against")
		}
		return "", "", errors.New("could not find the base branch: " + firstLine(err.Error()))
	}
	pick := *bases
	if own != "" {
		var others []gitx.Base
		for _, c := range bases.List {
			if c.Ref != "refs/heads/"+own {
				others = append(others, c)
			}
		}
		if len(others) > 0 {
			pick.List = others
		}
	}
	id, err = gitx.ChooseBase(ctx, cwd, "", &pick)
	if err != nil {
		return "", "", errors.New("no base branch shares history with this one")
	}
	for _, c := range pick.List {
		if c.ID == id {
			name = strings.TrimPrefix(strings.TrimPrefix(c.Ref, "refs/heads/"), "refs/remotes/")
			return name, id, nil
		}
	}
	return id, id, nil
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

	budget := int64(maxCountTotal)
	out, err := git(ctx, cwd, "ls-files", "--others", "--exclude-standard", "-z")
	if errors.Is(err, gitx.ErrTooLarge) {
		return nil, err
	}
	if err == nil {
		for n, p := range splitZ(out) {
			if n >= maxUntracked {
				break
			}
			if _, tracked := byPath[p]; tracked {
				continue
			}
			f := File{Path: p, Status: "A"}
			countUntracked(filepath.Join(cwd, filepath.FromSlash(p)), &f, &budget)
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

// countCache remembers the line count of an untracked file by its size and
// modification time, so that a poll every few seconds reads a file again only
// when it changed.
var countCache = struct {
	sync.Mutex
	m map[string]counted
}{m: map[string]counted{}}

type counted struct {
	size   int64
	mod    int64
	added  int
	binary bool
}

const maxCountCache = 20000

// countUntracked fills in the lines of a new file, or marks it binary. A link, a
// file over maxCountBytes, or one that would take a read past the budget (the
// bytes still allowed for this read of the checkout) is listed with no count. The
// file is opened and checked to be the one that was looked at, so a link swapped
// in between is not followed.
func countUntracked(path string, f *File, budget *int64) {
	fi, err := os.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() {
		return
	}
	size := fi.Size()
	if size > maxCountBytes {
		f.SizeAfter = &size
		return
	}
	mod := fi.ModTime().UnixNano()
	countCache.Lock()
	c, ok := countCache.m[path]
	countCache.Unlock()
	if ok && c.size == size && c.mod == mod {
		f.Added, f.Binary = c.added, c.binary
		return
	}
	if *budget < size {
		f.SizeAfter = &size
		return
	}
	*budget -= size
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()
	if open, err := file.Stat(); err != nil || !open.Mode().IsRegular() || !os.SameFile(fi, open) {
		return
	}
	data, err := io.ReadAll(io.LimitReader(file, maxCountBytes+1))
	if err != nil || len(data) > maxCountBytes {
		return
	}
	head := data
	if len(head) > 8000 {
		head = head[:8000]
	}
	for _, ch := range head {
		if ch == 0 {
			f.Binary = true
		}
	}
	if !f.Binary {
		lines := bytes.Count(data, []byte{'\n'})
		if len(data) > 0 && data[len(data)-1] != '\n' {
			lines++
		}
		f.Added = lines
	}
	countCache.Lock()
	if len(countCache.m) >= maxCountCache {
		clear(countCache.m)
	}
	countCache.m[path] = counted{size: size, mod: mod, added: f.Added, binary: f.Binary}
	countCache.Unlock()
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
	select {
	case gitSlots <- struct{}{}:
		defer func() { <-gitSlots }()
	case <-ctx.Done():
		return "", ctx.Err()
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	// A path is a path and not a pattern: a file named "*.go" or ":(top)x" is that file.
	args = append([]string{"--literal-pathspecs"}, args...)
	return gitx.ReadOnlyCapped(ctx, dir, maxGitOutput, args...)
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
