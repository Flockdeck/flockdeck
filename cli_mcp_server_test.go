package main

import "testing"

func TestLoopbackAPIOnlyAcceptsALoopbackHTTPAddress(t *testing.T) {
	for _, ok := range []string{"http://127.0.0.1:5000", "http://127.0.0.1:5000/", "http://[::1]:5000"} {
		if _, err := loopbackAPI(ok); err != nil {
			t.Errorf("%q refused: %v", ok, err)
		}
	}
	for _, bad := range []string{
		"", "127.0.0.1:5000", "https://127.0.0.1:5000", "http://example.com:5000", "http://localhost:5000",
		"http://10.0.0.5:5000", "http://127.0.0.1", "http://user:pw@127.0.0.1:5000", "http://127.0.0.1:5000/x",
		"http://127.0.0.1:5000?x=1", "http://0.0.0.0:5000", "ftp://127.0.0.1:5000", "http://127.0.0.1.evil.example:5000",
	} {
		if got, err := loopbackAPI(bad); err == nil {
			t.Errorf("%q accepted as %q", bad, got)
		}
	}
}

func TestMCPServerNeedsItsEnvironmentAndNoArguments(t *testing.T) {
	t.Setenv("FLOCKDECK_API", "http://127.0.0.1:5000")
	t.Setenv("FLOCKDECK_TOKEN", "")
	if err := runMCPServer(nil); err == nil {
		t.Error("started without a token")
	}
	t.Setenv("FLOCKDECK_TOKEN", "t")
	if err := runMCPServer([]string{"-pane", "p1"}); err == nil {
		t.Error("started with arguments")
	}
	t.Setenv("FLOCKDECK_API", "http://example.com:5000")
	if err := runMCPServer(nil); err == nil {
		t.Error("started with an address that is not loopback")
	}
}
