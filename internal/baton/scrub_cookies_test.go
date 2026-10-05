package baton

import "testing"

func TestStrictlyReadsAnsiQuotesAndHeredocs(t *testing.T) {
	for in, want := range map[string]string{
		`FOO=$'secret\' more words' deploy`: "deploy",
		`API=$'a b' make test`:              "make test",
		"cat <<EOF":                         "cat ...",
		"cat <<-'EOF' > /tmp/x":             "cat ...",
		"tr a b <<< \"secret words\"":       "tr ...",
		`echo $'it\'s a secret'`:            "echo ...",
	} {
		if got := Strictly(in); got != want {
			t.Errorf("Strictly(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAnalyticsCookiesAndFormatVerbsAreNotSecrets(t *testing.T) {
	untouched(t, "COOKIE: __utma=000000000.0000000000.0000000000.0000000000.0000000000.00; __utmb=000000000.0.00.0000000000")
	untouched(t, `return fmt.Sprintf("user=%#v, pass=%#v, host=%q", u, p, h)`)
}

func TestStructuredCookieValuesAreNotSecrets(t *testing.T) {
	untouched(t, "COOKIE: __utmz=000000000.0000000000.00.0.utmcsr=code.google.com|utmccn=(referral)|utmcmd=referral|utmcct=/p/go/issues/detail")
}
