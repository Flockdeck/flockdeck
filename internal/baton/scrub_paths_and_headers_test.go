package baton

import (
	"strings"
	"testing"
)

func TestEveryAuthorizationHeaderOnALineIsScrubbed(t *testing.T) {
	sc := NewScrubber()
	for _, c := range []struct {
		in    string
		leaks []string
		keeps []string
	}{
		{`curl -H 'Authorization: Token AAAA1111' -H 'Proxy-Authorization: Token BBBBBBBB2222' x`,
			[]string{"AAAA1111", "BBBBBBBB2222"}, []string{"Authorization: Token ", "Proxy-Authorization: Token ", " x"}},
		{`Authorization: Splunk AAAA1111 Authorization: Splunk BBBB2222`,
			[]string{"AAAA1111", "BBBB2222"}, []string{"Authorization: Splunk "}},
		{`Authorization: Bearer abcd;efgh1234`, []string{"efgh1234", "abcd"}, []string{"Authorization: Bearer "}},
		{`-H "Authorization: Digest username=\"bob\", response=\"cafe0123\"" -H "Authorization: Token CCCC3333"`,
			[]string{"cafe0123", "CCCC3333"}, []string{"Authorization: Digest ", "Authorization: Token "}},
		{`Authorization: ApiKey s3cr3t, then some words`, []string{"s3cr3t"}, []string{"then some words"}},
	} {
		got, _ := sc.Scrub(c.in)
		for _, leak := range c.leaks {
			if strings.Contains(got, leak) {
				t.Errorf("Scrub(%q) = %q, still holds %q", c.in, got, leak)
			}
		}
		for _, keep := range c.keeps {
			if !strings.Contains(got, keep) {
				t.Errorf("Scrub(%q) = %q, lost %q", c.in, got, keep)
			}
		}
	}
}

func TestQuotedDatabasePasswordsAreScrubbedWhole(t *testing.T) {
	sc := NewScrubber()
	for in, keep := range map[string]string{
		`mysql -u root -p'my pass' db`:       "mysql -u root -p",
		`mysql -u root -p"my pass" db`:       "mysql -u root -p",
		`mysqldump -p"my pass" db`:           "mysqldump -p",
		`sshpass -p 'my pass' ssh host`:      "sshpass -p ",
		`redis-cli -a 'my pass' ping`:        "redis-cli -a ",
		`docker login -u me -p 'my pass'`:    "docker login -u me -p ",
		`curl -u me:'my pass' https://x`:     "curl -u me:",
		`curl --user "me:my pass" https://x`: "curl --user ",
		`curl -u me:hunter2hunter https://x`: "curl -u me:",
	} {
		got, _ := sc.Scrub(in)
		if !strings.HasPrefix(got, keep) || strings.Contains(got, "my pass") || strings.Contains(got, "pass\"") || strings.Contains(got, "hunter2hunter") {
			t.Errorf("Scrub(%q) = %q", in, got)
		}
		if strings.Contains(in, "db") && !strings.HasSuffix(got, " db") {
			t.Errorf("Scrub(%q) = %q, lost the database name", in, got)
		}
	}
	// A capital P is a port, not a password.
	for _, in := range []string{"mysql -h db -P 3306 -u root", "mysql -P 33060 app"} {
		if got, _ := sc.Scrub(in); got != in {
			t.Errorf("Scrub(%q) = %q, took a port", in, got)
		}
	}
	got, _ := sc.Scrub("redis://:s3cretpw@cache:6379/0")
	if strings.Contains(got, "s3cretpw") || !strings.Contains(got, "@cache:6379/0") {
		t.Errorf("Scrub(redis url) = %q", got)
	}
}

// Paths keep their structure, whatever hash, GUID or odd name is in them; only a
// part that is itself a long random run goes.
func TestPathsWithHashesAndGUIDsAreKept(t *testing.T) {
	sc := NewScrubber()
	for _, p := range []string{
		`C:\Users\x\AppData\Local\Temp\8940aecf-4631\SomeFile_Name.go`,
		`C:\Users\someone\.claude\projects\C--Users-someone-Documents-repos-app\3b4c5d6e-aaaa.jsonl`,
		`D:\a\flockdeck\flockdeck\internal\baton\Scrub_Test_2A9F.go`,
		`C:\Windows\System32\DriverStore\FileRepository\nvlt.inf_amd64_3b4c5d6e7f8091a2\nvlt.inf`,
		`%LOCALAPPDATA%\Packages\Microsoft.WindowsTerminal_8wekyb3d8bbwe\LocalState\settings.json`,
		`/workspace/Project_2024-Q1/8940aecf-4631/Some_File_Name_Here.go`,
		`/var/lib/docker/overlay2/3b4c5d6e7f8091a23b4c5d6e7f8091a2/merged/etc/Some_Config_File.yaml`,
		`\\server\share\Team_Docs\Quarterly_Report_2026\Final_v2.docx`,
	} {
		for _, in := range []string{p, "see " + p + " now", "KEY=" + p} {
			if got, _ := sc.Scrub(in); got != in {
				t.Errorf("Scrub(%q) = %q", in, got)
			}
		}
	}
	// A path with a space in it: each word is looked at as a path.
	in := `C:\Users\John Smith\Documents\Quarterly_Report_2026\Final_Draft_v2.docx`
	if got, _ := sc.Scrub(in); got != in {
		t.Errorf("Scrub(%q) = %q", in, got)
	}
	// A part that is a secret still goes, and the rest of the path stays.
	got, _ := sc.Scrub(`C:\data\Zq8Xv2LmN9pR4tYw6KdH3sJf7BcA5eGu7Hn\file.txt`)
	if strings.Contains(got, "Zq8X") || !strings.HasPrefix(got, `C:\data\`) || !strings.HasSuffix(got, `\file.txt`) {
		t.Errorf("Scrub = %q", got)
	}
	// And the bare keys that contain slashes are still not paths.
	for _, secret := range []string{"wJalr" + "XUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", "/Zq8Xv2LmN9pR4tYw6KdH3sJf7BcA5eGu7Hn"} {
		if got, _ := sc.Scrub("key " + secret); strings.Contains(got, secret[:12]) {
			t.Errorf("%q survived: %q", secret, got)
		}
	}
}

func TestAKeySplitByAnyIgnorableCharacterIsScrubbed(t *testing.T) {
	sc := NewScrubber()
	runes := []rune{0x034F, 0x061C, 0x115F, 0x1160, 0x17B4, 0x17B5, 0x180B, 0x180C, 0x180D, 0x180E, 0x180F,
		0x200B, 0x200C, 0x200D, 0x200E, 0x200F, 0x202A, 0x202B, 0x202C, 0x202D, 0x202E,
		0x2060, 0x2061, 0x2062, 0x2063, 0x2064, 0x2066, 0x2067, 0x2068, 0x2069, 0x206A, 0x206F,
		0x2800, 0x3164, 0xFE00, 0xFE0F, 0xFEFF, 0xFFA0, 0xE0001, 0xE0020, 0xE0FFF, 0x00AD}
	for _, r := range runes {
		in := "see gh" + "p_01234" + string(r) + "56789abcdefghijklmnopqrstuvwxyz end"
		got, _ := sc.Scrub(in)
		if got != "see [REDACTED: github-token] end" {
			t.Errorf("U+%04X: Scrub = %q", r, got)
		}
	}
	// Odd spaces separate words as spaces do.
	for _, sp := range []rune{0x00A0, 0x2007, 0x202F, 0x3000} {
		in := "Authorization:" + string(sp) + "Token" + string(sp) + "ABCD1234efgh"
		if got, _ := sc.Scrub(in); strings.Contains(got, "ABCD1234efgh") {
			t.Errorf("U+%04X: Scrub = %q", sp, got)
		}
	}
}

// Joiners are part of how emoji, Persian and Hindi are written, so they stay in
// the text; they are only ignored when it is matched.
func TestJoinersStayInTheText(t *testing.T) {
	sc := NewScrubber()
	for name, in := range map[string]string{
		"emoji":   "family \U0001F468\u200d\U0001F469\u200d\U0001F467 here",
		"persian": "\u0645\u06CC\u200C\u062E\u0648\u0627\u0647\u0645 word",
		"hindi":   "\u0915\u094D\u200D\u0937 word",
		"vs16":    "heart \u2764\uFE0F ok",
	} {
		if got := CleanText(in); got != in {
			t.Errorf("%s: CleanText = %q, want it unchanged", name, got)
		}
		if got, _ := sc.Scrub(in); got != in {
			t.Errorf("%s: Scrub = %q, want it unchanged", name, got)
		}
	}
	// And a joiner in the middle of a key does not stop it being found, while
	// the joiner is kept in what is around it.
	got, _ := sc.Scrub("\U0001F468\u200d\U0001F469 key gh" + "p_01234\u200d56789abcdefghijklmnopqrstuvwxyz")
	if got != "\U0001F468\u200d\U0001F469 key [REDACTED: github-token]" {
		t.Errorf("Scrub = %q", got)
	}
}
