package changes

import (
	"context"
	"sort"
	"sync"
)

// readers is how many checkouts are read at once.
const readers = 4

// PaneRef is one agent pane whose checkout is to be read.
type PaneRef struct {
	ID  string
	Cwd string
	// TaskID is the task the pane works on, from the board, or empty.
	TaskID string
}

// ReadTeam reads the checkouts of a project's panes, in the order given, a few at
// a time, and marks the files that other branches of the same repository change
// too. A checkout that cannot be read is an entry with an Error, and Partial says
// how many were read. rev is the number of the push.
func ReadTeam(ctx context.Context, root, project string, panes []PaneRef, rev int) Team {
	t := Team{Root: root, Project: project, Rev: rev, Branches: make([]Branch, len(panes))}
	commons := make([]string, len(panes))
	var wg sync.WaitGroup
	sem := make(chan struct{}, readers)
	for i, p := range panes {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			b, common := read(ctx, p.ID, p.Cwd, MaxFiles)
			b.TaskID = p.TaskID
			t.Branches[i], commons[i] = b, common
		}()
	}
	wg.Wait()
	markConflicts(t.Branches, commons)
	failed := 0
	for _, b := range t.Branches {
		if b.Error != "" {
			failed++
		}
	}
	if failed > 0 {
		t.Partial = &Partial{Read: len(panes) - failed, Total: len(panes)}
	}
	return t
}

// markConflicts sets ConflictWith on the files of each branch that another branch
// of the same repository changes too: the same path, or a path one side renamed
// to or from.
func markConflicts(bs []Branch, commons []string) {
	type owner struct {
		branch int
		path   string
	}
	// For each repository, which branches touch which path (by either name).
	touched := map[string]map[string][]owner{}
	for i, b := range bs {
		if b.Error != "" || commons[i] == "" {
			continue
		}
		m := touched[commons[i]]
		if m == nil {
			m = map[string][]owner{}
			touched[commons[i]] = m
		}
		for _, f := range b.AllFiles() {
			m[f.Path] = append(m[f.Path], owner{i, f.Path})
			if f.OldPath != "" {
				m[f.OldPath] = append(m[f.OldPath], owner{i, f.Path})
			}
		}
	}
	for i := range bs {
		if bs[i].Error != "" || commons[i] == "" {
			continue
		}
		m := touched[commons[i]]
		mark := func(files []File) {
			for k := range files {
				f := &files[k]
				seen := map[string]bool{}
				var cw []ConflictWith
				for _, name := range []string{f.Path, f.OldPath} {
					if name == "" {
						continue
					}
					for _, o := range m[name] {
						key := bs[o.branch].Pane + "\x00" + o.path
						if o.branch == i || seen[key] {
							continue
						}
						seen[key] = true
						cw = append(cw, ConflictWith{Pane: bs[o.branch].Pane, Path: o.path})
					}
				}
				sort.Slice(cw, func(a, b int) bool {
					if cw[a].Pane != cw[b].Pane {
						return cw[a].Pane < cw[b].Pane
					}
					return cw[a].Path < cw[b].Path
				})
				f.ConflictWith = cw
			}
		}
		mark(bs[i].Files)
		if bs[i].all != nil {
			// The listed files are the head of all, which shares its array.
			mark(bs[i].all)
		}
	}
}

// Page cuts a page of files out of a branch's full list.
func Page(files []File, offset, limit int) []File {
	if offset < 0 {
		offset = 0
	}
	if offset >= len(files) {
		return []File{}
	}
	end := offset + limit
	if end > len(files) || limit <= 0 {
		end = len(files)
	}
	return files[offset:end]
}
