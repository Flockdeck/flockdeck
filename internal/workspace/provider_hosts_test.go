package workspace

import "testing"

// Lower-casing must not fold other characters into ASCII: each of these looks like the
// vendor's host and is not it.
func TestLookalikeHostsAreNotTheVendorsOwn(t *testing.T) {
	isolateEnv(t)
	for name, host := range map[string]string{
		"dotted capital I":    "api.anthropİc.com",
		"dotless i":           "api.anthropıc.com",
		"kelvin sign":         "api.anthropic.cK.com",
		"long s":              "api.ſanthropic.com",
		"fullwidth letters":   "api.ａnthropic.com",
		"cyrillic a":          "api.аnthropic.com",
		"capital I with dot2": "İ.anthropic.com",
	} {
		t.Setenv("ANTHROPIC_BASE_URL", "https://"+host+"/v1")
		if p, _ := BatonProviderDetail(claudeSpec, ""); p == "anthropic" {
			t.Errorf("%s: %q was taken for the vendor's own host", name, host)
		}
	}
	for name, host := range map[string]string{"openai dotted I": "api.openaİ.com"} {
		t.Setenv("OPENAI_BASE_URL", "https://"+host+"/v1")
		if p, _ := BatonProviderDetail(codexSpec, ""); p == "openai" {
			t.Errorf("%s: %q was taken for the vendor's own host", name, host)
		}
	}
	if h := HostOf("https://api.anthropİc.com"); h == "api.anthropic.com" {
		t.Errorf("HostOf folded U+0130 to i: %q", h)
	}
}

// Genuine ASCII hosts stay official whatever the case, with a trailing dot, a port or userinfo
// that is not the host.
func TestGenuineHostsStayOfficial(t *testing.T) {
	isolateEnv(t)
	for _, u := range []string{
		"https://API.ANTHROPIC.COM", "https://api.anthropic.com.", "https://api.anthropic.com:443/v1", "https:" + "//user:pw@api.anthropic.com/x", "https://Api.Anthropic.Com",
	} {
		t.Setenv("ANTHROPIC_BASE_URL", u)
		if p, why := BatonProviderDetail(claudeSpec, ""); p != "anthropic" {
			t.Errorf("%q: provider %q, %q", u, p, why)
		}
	}
	// A userinfo that names the vendor is not the host.
	t.Setenv("ANTHROPIC_BASE_URL", "https://api.anthropic.com@evil.example/")
	if p, _ := BatonProviderDetail(claudeSpec, ""); p == "anthropic" {
		t.Error("userinfo was taken for the host")
	}
}

// isOfficial refuses a host with a byte that is not ASCII whatever it is compared with: this
// reaches it with a non-ASCII host that equals the entry it is given, which only that check
// refuses (the hosts that are folded, in the tests above, never get here equal).
func TestIsOfficialRefusesANonASCIIHostThatEqualsTheEntry(t *testing.T) {
	host := "api.anthrop\u0131c.com"
	if isOfficial(host, []string{host}) {
		t.Error("a host with a byte that is not ASCII was taken for the vendor's own")
	}
	if !isOfficial("api.anthropic.com", []string{"api.anthropic.com"}) {
		t.Error("the vendor's own host was refused")
	}
}
