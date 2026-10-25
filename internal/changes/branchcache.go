package changes

import (
	"context"
	"sync"
	"time"
)

// branchCacheTTL is how long a read of a branch is kept for diffHunks and
// diffContext. A window asks for the diff of a file it has just been shown in the
// list, and several of those come one after another; reading the whole branch,
// with every line count, for each would cost more than the diff. A file that
// appeared in the last few seconds is not found until the entry has expired.
var branchCacheTTL = 3 * time.Second

const branchCacheMax = 64

type cachedRead struct {
	b  Branch
	at time.Time
}

var (
	branchCacheMu sync.Mutex
	branchCache   = map[string]cachedRead{}
)

// cachedBranch is Read of the whole branch, reused for branchCacheTTL.
func cachedBranch(ctx context.Context, pane, cwd string) Branch {
	key := pane + "\x00" + cwd
	now := time.Now()
	if branchCacheTTL > 0 {
		branchCacheMu.Lock()
		e, ok := branchCache[key]
		branchCacheMu.Unlock()
		if ok && now.Sub(e.at) < branchCacheTTL {
			return e.b
		}
	}
	b := Read(ctx, pane, cwd, 0)
	if branchCacheTTL > 0 && b.Error == "" {
		branchCacheMu.Lock()
		for k, e := range branchCache {
			if now.Sub(e.at) >= branchCacheTTL {
				delete(branchCache, k)
			}
		}
		if len(branchCache) < branchCacheMax {
			branchCache[key] = cachedRead{b: b, at: now}
		}
		branchCacheMu.Unlock()
	}
	return b
}
