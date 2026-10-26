package changes

import (
	"context"
	"strconv"
	"strings"
	"sync"

	"github.com/jmwri/flockdeck/internal/gitx"
)

// GitFacts are the two things the gate needs from git that the branch read does not
// hold.
type GitFacts struct {
	// OwnCommits is the number of commits beyond the base; -1 when git could not say.
	OwnCommits int
	// Unmergeable is true only when git said the head and the base's tip conflict.
	Unmergeable bool
}

// FactCache remembers GitFacts by the commits they were worked out for, so that a
// poll of a branch that has not moved costs no merge. A fact depends only on the
// commits named in its key.
type FactCache struct {
	mu    sync.Mutex
	own   map[string]int
	merge map[string]bool // true when the commits conflict
}

const factCacheMax = 1000

// Facts works out the facts of a branch read without error. It is best effort:
// whatever git cannot say is left unknown, and unknown never blocks.
func (c *FactCache) Facts(ctx context.Context, b Branch) GitFacts {
	f := GitFacts{OwnCommits: -1}
	if b.HeadSha == "" || b.BaseSha == "" {
		return f
	}
	ownKey := b.BaseSha + ".." + b.HeadSha
	c.mu.Lock()
	n, ok := c.own[ownKey]
	c.mu.Unlock()
	if !ok {
		out, err := git(ctx, b.Cwd, "rev-list", "--count", "--end-of-options", b.HeadSha, "^"+b.BaseSha)
		if cnt, cerr := strconv.Atoi(strings.TrimSpace(out)); err == nil && cerr == nil {
			n, ok = cnt, true
			c.mu.Lock()
			if c.own == nil || len(c.own) >= factCacheMax {
				c.own = map[string]int{}
			}
			c.own[ownKey] = n
			c.mu.Unlock()
		}
	}
	if ok {
		f.OwnCommits = n
	}
	if f.OwnCommits == 0 || b.Base == "" {
		return f
	}
	tip, err := git(ctx, b.Cwd, "rev-parse", "--verify", "-q", "--end-of-options", b.Base+"^{commit}")
	tip = strings.TrimSpace(tip)
	if err != nil || tip == "" {
		return f
	}
	key := tip + "+" + b.HeadSha
	c.mu.Lock()
	conflict, seen := c.merge[key]
	c.mu.Unlock()
	if !seen {
		clean, known := gitx.MergesCleanly(ctx, b.Cwd, tip, b.HeadSha)
		if !known {
			return f
		}
		conflict = !clean
		c.mu.Lock()
		if c.merge == nil || len(c.merge) >= factCacheMax {
			c.merge = map[string]bool{}
		}
		c.merge[key] = conflict
		c.mu.Unlock()
	}
	f.Unmergeable = conflict
	return f
}

// MergedInto says whether the head of b is already in its base.
func MergedInto(ctx context.Context, b Branch) bool {
	if b.HeadSha == "" || b.Base == "" {
		return false
	}
	_, err := git(ctx, b.Cwd, "merge-base", "--is-ancestor", b.HeadSha, b.Base)
	return err == nil
}
