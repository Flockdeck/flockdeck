//go:build !windows

package appwindow

// workArea reports nothing known, which leaves the window its default size.
// Chromium was seen placing an oversized window as asked on Windows; elsewhere
// it has not been checked, so the size there is left as it always was.
func workArea() (int, int) { return 0, 0 }
