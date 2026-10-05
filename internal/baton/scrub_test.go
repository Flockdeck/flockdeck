package baton

import (
	"flag"
	"os"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// leaks are the fake secrets in testdata/scrub_input.txt. None may appear in
// the output, however the golden file reads.
var leaks = []string{
	"AK" + "IAIOSFODNN7EXAMPLE",
	"wJalr" + "XUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
	"gh" + "p_0123456789abcdefghij",
	"klmnopqrstuvwxyz",
	"sk" + "-proj-abcdefghijklmnopqrstuvwxyz0123456789",
	"s3cretpw0rd",
	"ABCDEFGHIJ",
	"KLMNOP1234567890",
	"correct-horse",
	"battery-staple",
	"abcdefghijklmnop123456",
	"hunter2hunter",
	"ey" + "JhbGciOiJIUzI1NiJ9",
	"MIIEowIBAAKCAQEA",
	"id_rsa",
	"swordfish99",
	"abc123def456ghi789",
	"Zq8Xv2LmN9pR4tYw6KdH3sJf7BcA5eGu",
	"/Zq8Xv2LmN9pR4tYw6KdH3sJf7BcA5eGu7Hn",
	"9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",
	"SG" + ".abcdefghijklmnopqrstuv",
	"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopq",
	"my pass",
	"more words",
	"Zq8Xv2LmN9pR4tYw6KdH3sJf7BcA5eGu7Hn/file",
	"b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
	"K7MDENG",
	"bPxRfiCYEXAMPLEKEY",
	"abcdef123456ghij",
	"deadbeefcafe0123",
	"s3cr3tvalue99",
	"SuperSecret99",
	"hunter2hunter ssh",
	"tok-abcdef-12345-xyz",
	"abcdefgh12345678",
	"line one of the hidden block",
	"line two of the hidden block",
	"pa ss word",
}

// stays is text that must come through untouched: a false positive costs a
// person an edit, but these are what a baton is for.
var stays = []string{
	"HOME=/home/dev",
	"PATH=/usr/bin:/bin",
	"3f2c9a1d8e7b6a5f4e3d2c1b0a9f8e7d6c5b4a39",
	"123e4567-e89b-12d3-a456-426614174000",
	"internal/session/transcript/claude_stream.go",
	"TestParseTranscriptWithLongNameForThing",
	"PORT=8080 and DEBUG=true",
	"--verbose",
	"the path /usr/local/share/node_modules/typescript/lib/tsc is not a token",
	"and then some words",
	"https://example.com/docs/GettingStarted/Installation_Guide_V2/section-12",
	"github.com/aws/aws-sdk-go-v2/service/S3Control/types",
	"pkg/mod/github.com/Azure/azure-sdk-for-go/sdk/Storage2Blob",
	"refs/heads/Feature_Branch_ABC123_Final_v2",
	`C:\Users\someone\Documents\Project2\FooBarBaz\Quarterly_Report_2026`,
	"https://github.com/Flockdeck/flockdeck/blob/main/internal/baton/Scrub_Test_Data",
	"Authorization: Token ",
	"Authorization: ApiKey ",
	"mysql -u root ",
	"other: value stays",
	"after the block",
	"docker pull registry.example.com/team/app@sha256:",
	`C:\Users\x\AppData\Local\Temp\8940aecf-4631\SomeFile_Name.go`,
	`C:\Users\someone\.claude\projects\C--Users-someone-Documents-repos-app\3b4c5d6e-aaaa.jsonl`,
	`D:\a\flockdeck\flockdeck\internal\baton\Scrub_Test_2A9F.go`,
	`C:\Windows\System32\DriverStore\FileRepository\nvlt.inf_amd64_3b4c5d6e7f8091a2\nvlt.inf`,
	`%LOCALAPPDATA%\Packages\Microsoft.WindowsTerminal_8wekyb3d8bbwe\LocalState\settings.json`,
	`/workspace/Project_2024-Q1/8940aecf-4631/Some_File_Name_Here.go`,
	`C:\Users\John Smith\Documents\Quarterly_Report_2026\Final_Draft_v2.docx`,
	"https://example.com/download/",
	"https://example.org",
}

func TestScrubCorpus(t *testing.T) {
	in := corpusInput(t)
	// The scrubber is told one value, which layer one has to find whole and
	// wrapped over a line.
	got, reds := NewScrubber("correct-horse-battery-staple").Scrub(in)
	for _, s := range leaks {
		if strings.Contains(got, s) {
			t.Errorf("output still holds %q", s)
		}
	}
	for _, s := range stays {
		if !strings.Contains(got, s) {
			t.Errorf("output lost %q", s)
		}
	}
	if CountMarks(got) != RedactionCount(reds) {
		t.Errorf("%d marks in the text, %d counted", CountMarks(got), RedactionCount(reds))
	}
	const golden = "testdata/scrub_golden.txt"
	if *update {
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if got != strings.ReplaceAll(string(want), "\r\n", "\n") {
		t.Errorf("scrubbed corpus differs from %s (run with -update to see why):\n%s", golden, got)
	}
	// A second pass over text that has been scrubbed removes nothing more.
	again, more := NewScrubber("correct-horse-battery-staple").Scrub(got)
	if again != got || len(more) != 0 {
		t.Errorf("scrubbing scrubbed text changed it: %v", more)
	}
}

func TestScrubKindsAreNamed(t *testing.T) {
	_, reds := NewScrubber().Scrub("key AK" + "IAIOSFODNN7EXAMPLE and gh" + "p_0123456789abcdefghijklmnopqrstuvwxyz and gh" + "p_0123456789abcdefghijklmnopqrstuvwxy9")
	want := map[string]int{"aws-key": 1, "github-token": 2}
	for _, r := range reds {
		if want[r.Kind] != r.Count {
			t.Errorf("%+v, want %v", reds, want)
		}
		delete(want, r.Kind)
	}
	if len(want) > 0 {
		t.Errorf("missing %v in %+v", want, reds)
	}
}

func TestExactValuesIgnoreShortOnes(t *testing.T) {
	got, _ := NewScrubber("true", "8080", "short").Scrub("PORT=8080 DEBUG=true short")
	if got != "PORT=8080 DEBUG=true short" {
		t.Errorf("a short value was removed: %q", got)
	}
}

func TestEnvValuesTakeOnlySecretNames(t *testing.T) {
	got := EnvValues([]string{"HOME=/home/dev", "GITHUB_TOKEN=tok-value-123", "DB_PASSWORD=pw-value-456", "NOEQUALS"})
	if len(got) != 2 || got[0] != "tok-value-123" || got[1] != "pw-value-456" {
		t.Errorf("EnvValues = %v", got)
	}
}

func TestEnvFileValuesAreMatchedAndNotKept(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/.env.local", []byte("# comment\nexport SERVICE=\"internal-value-123\"\nBLANK=\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	vals := EnvFileValues(dir)
	got, reds := NewScrubber(vals...).Scrub("it said internal-value-123 in the log")
	if strings.Contains(got, "internal-value-123") || len(reds) != 1 || reds[0].Kind != "known-secret" {
		t.Errorf("got %q %+v", got, reds)
	}
}

func TestScrubBatonRecountsFromTheText(t *testing.T) {
	b := Baton{Title: "t", Sections: map[Section]string{
		Goal:      "use AK" + "IAIOSFODNN7EXAMPLE",
		Decisions: "an edit pasted gh" + "p_0123456789abcdefghijklmnopqrstuvwxyz back in, and [REDACTED: jwt] is already here",
	}}
	got := NewScrubber().ScrubBaton(b)
	if n := RedactionCount(got.Redactions); n != 3 {
		t.Errorf("redactions = %+v, want 3", got.Redactions)
	}
	if strings.Contains(got.Section(Decisions), "ghp_") {
		t.Error("a pasted token survived the final pass")
	}
}

func TestEntropyLeavesCommonLongRunsAlone(t *testing.T) {
	for _, s := range []string{
		"3f2c9a1d8e7b6a5f4e3d2c1b0a9f8e7d6c5b4a39",
		"123e4567-e89b-12d3-a456-426614174000",
		"internal/workspace/fanout_prompt_test.go",
		"TestRestartWithBatonStartsANewConversation",
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	} {
		if looksRandom(s) {
			t.Errorf("%q was taken for a secret", s)
		}
	}
	if !looksRandom("Zq8Xv2LmN9pR4tYw6KdH3sJf7BcA5eGu") {
		t.Error("a random 32 character token was not taken for a secret")
	}
}

// corpusMarker splits the provider prefixes in testdata/scrub_input.txt so the
// file holds no complete secret shape. corpusInput removes it again, which
// gives the scrubber the whole fake secrets.
const corpusMarker = "<~>"

func corpusInput(t *testing.T) string {
	t.Helper()
	in, err := os.ReadFile("testdata/scrub_input.txt")
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(in), corpusMarker, "")
}
