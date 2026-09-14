package workspace

import "testing"

// BenchmarkSelectProjectAlternating measures what a person actually does
// when flipping between two open projects: SelectProject(other),
// SelectProject(back), and so on. Before touchRecent, this ran
// store.TouchRecent inline -- a synchronous, fsync'd rewrite of the recent
// projects list, benchmarked in internal/store at several milliseconds per
// switch on Windows -- on the same goroutine that every other window's
// commands and every pane's PTY output wait behind (see server.runLoop).
// SelectProject itself should now cost microseconds; the disk write still
// happens, just off that goroutine.
func BenchmarkSelectProjectAlternating(b *testing.B) {
	dir := b.TempDir()
	b.Setenv("APPDATA", dir)
	b.Setenv("XDG_CONFIG_HOME", dir)
	b.Setenv("HOME", dir)

	first, second := b.TempDir(), b.TempDir()
	ws, err := New(Options{Root: first})
	if err != nil {
		b.Fatalf("new workspace: %v", err)
	}
	defer ws.Close()
	if err := ws.OpenProject(second); err != nil {
		b.Fatalf("open second: %v", err)
	}

	roots := [2]string{first, second}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ws.SelectProject(roots[i%2])
	}
}
