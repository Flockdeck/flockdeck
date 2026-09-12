package tool

import "testing"

// A line redrawn with carriage returns is kept as the terminal left it, and a
// Windows line ending is not taken for a redraw.
func TestAProgressBarIsKeptAsTheTerminalLeftIt(t *testing.T) {
	for in, want := range map[string]string{
		"downloading 10%\rdownloading 50%\rdownloading 100%\ndone\n": "downloading 100%\ndone\n",
		"line one\r\nline two\r\n":                                   "line one\r\nline two\r\n",
		"12%\r100%\r\nok\r\n":                                        "100%\r\nok\r\n",
		"no carriage returns at all":                                 "no carriage returns at all",
	} {
		if got := settleLines(in); got != want {
			t.Errorf("settleLines(%q) = %q, want %q", in, got, want)
		}
	}
}
