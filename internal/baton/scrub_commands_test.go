package baton

import (
	"math/rand"
	"strings"
	"testing"
)

// taken checks that none of the leaks survive scrubbing in, and that every keep does.
func taken(t *testing.T, in string, leaks []string, keeps []string) {
	t.Helper()
	got, _ := NewScrubber().Scrub(in)
	for _, l := range leaks {
		if strings.Contains(got, l) {
			t.Errorf("Scrub(%q) = %q, still holds %q", in, got, l)
		}
	}
	for _, k := range keeps {
		if !strings.Contains(got, k) {
			t.Errorf("Scrub(%q) = %q, lost %q", in, got, k)
		}
	}
}

func untouched(t *testing.T, in string) {
	t.Helper()
	if got, _ := NewScrubber().Scrub(in); got != in {
		t.Errorf("Scrub(%q) = %q, want it unchanged", in, got)
	}
}

func TestHtpasswdTakesThePassword(t *testing.T) {
	taken(t, "htpasswd -b /etc/nginx/.htpasswd alice Sw0rdf1shPw", []string{"Sw0rdf1shPw"}, []string{"alice", ".htpasswd"})
	taken(t, "htpasswd -nb bob Pa55w0rdHere", []string{"Pa55w0rdHere"}, []string{"bob"})
	taken(t, "htpasswd -Bb file carol Tr0ub4dor", []string{"Tr0ub4dor"}, []string{"carol"})
}

func TestSetxAndExportWithASpace(t *testing.T) {
	taken(t, "setx GITHUB_TOKEN my token words", []string{"my token words"}, []string{"setx GITHUB_TOKEN"})
	taken(t, "export API_SECRET hunter2hunter2", []string{"hunter2hunter2"}, []string{"export API_SECRET"})
	untouched(t, "export PATH /usr/bin")
	untouched(t, "setx EDITOR code")
}

func TestCookieValuesAreTaken(t *testing.T) {
	taken(t, "Cookie: session=Zm9vYmFyYmF6cXV4MTIzNDU2Nzg5MDEyMzQ; theme=dark", []string{"Zm9vYmFy"}, []string{"theme=dark"})
	taken(t, "Set-Cookie: sid=AbCdEfGhIjKlMnOpQrStUvWxYz012345; Path=/; HttpOnly", []string{"AbCdEfGhIj"}, []string{"Path=/", "HttpOnly"})
	untouched(t, "Set-Cookie: lang=en; Path=/; Secure")
	untouched(t, "Cookie: theme=dark; sidebar=collapsed")
	// A bare hex session id is not recognised, on purpose: see the help page.
	untouched(t, "Cookie: a=0123456789abcdef0123456789abcdef")
}

func TestYAMLBlockQuotedKeyAndComment(t *testing.T) {
	taken(t, "\"password\": |\n  line one secret\n  line two secret\nother: 1", []string{"line one secret", "line two secret"}, []string{"other: 1"})
	taken(t, "password: | # keep\n  line one secret\n  line two secret\nnext: 1", []string{"line one secret"}, []string{"next: 1"})
}

func TestKubernetesEnvNameThenValue(t *testing.T) {
	taken(t, "env:\n  - name: API_TOKEN\n    value: abc 123 def\n  - name: LOG\n    value: debug", []string{"abc 123 def"}, []string{"value: debug"})
	taken(t, "- name: \"DB_PASSWORD\"\n  value: \"hunter two\"", []string{"hunter two"}, nil)
	untouched(t, "- name: API_TOKEN\n  value: ${API_TOKEN}")
}

func TestMysqlSpacedPFlagIsTheDatabase(t *testing.T) {
	taken(t, "mysql -u root -p mydb", nil, []string{"mydb"})
	taken(t, "mysql -u root -pHunter99x mydb", []string{"Hunter99x"}, []string{"mydb"})
	taken(t, "sshpass -p Secret99x ssh host", []string{"Secret99x"}, []string{"ssh host"})
}

func TestGoCompositeLiteralsUnderASecretKey(t *testing.T) {
	untouched(t, "\t\t\"passwd\": {{source: \"compat\"}},")
	taken(t, "\"password\": []string{\"hunter2hunter2\"},", []string{"hunter2hunter2"}, nil)
	taken(t, "password: []byte(\"hunter2hunter2\"),", []string{"hunter2hunter2"}, nil)
}

func TestAcronymNamesAreNames(t *testing.T) {
	untouched(t, "x := getHTTPResponseBodyAsString1(req)")
	untouched(t, "func ParseHTTPSRequestAndValidateInput2(r *Request) error {")
}

func TestPlaceholdersAreNotSecrets(t *testing.T) {
	for _, in := range []string{
		"Authorization: Bearer <token>",
		"Authorization: Bearer $TOKEN",
		"Authorization: Bearer ${TOKEN}",
		"curl -H 'Authorization: Bearer $TOKEN' https://x.example/api",
		"password: ${DB_PASSWORD}",
		"password: {{ .Values.password }}",
		"token: <your-token>",
		"password=%DB_PASSWORD%",
	} {
		untouched(t, in)
	}
	taken(t, "Authorization: Bearer ${TOKEN:-realsecretvalue1}", nil, nil)
}

func TestPublicKeyFileIsNotASecret(t *testing.T) {
	untouched(t, "cat ~/.ssh/id_rsa.pub")
	taken(t, "cat ~/.ssh/id_rsa", []string{"id_rsa"}, nil)
}

func TestCodeLinesAreNotMarked(t *testing.T) {
	for _, in := range []string{
		"if token == \"\" {",
		"const maxTokens = 4096",
		"flockdeck run --max-tokens 1024",
		"TOKEN_BUCKET_SIZE=100",
		"\ttoken := next()",
	} {
		untouched(t, in)
	}
	taken(t, "password=hunter2hunter2", []string{"hunter2hunter2"}, nil)
}

func TestMarkFollowedByAWordIsStable(t *testing.T) {
	sc := NewScrubber()
	for _, in := range []string{
		"password=[REDACTED: secret-value]word",
		"x [REDACTED: secret-value]-tail ok",
		"AK" + "IAIOSFODNN7EXAMPLE_more and token=abc123def456ghi",
		"+AK" + "IAIOSFODNN7EXAMPLE-{:AK" + "IAIOSFODNN7EXAMPLE\n+token_:word",
	} {
		once, _ := sc.Scrub(in)
		twice, _ := sc.Scrub(once)
		if once != twice {
			t.Errorf("Scrub(%q): once %q, twice %q", in, once, twice)
		}
	}
}

func TestScrubIsIdempotentOnRandomText(t *testing.T) {
	sc := NewScrubber()
	frags := []string{"password", "=", ":", " ", "\n", "token", "AK" + "IAIOSFODNN7EXAMPLE", "abc123", "word", "-", "_", "+", "[REDACTED: secret-value]",
		"[REDACTED: high-entropy]", "Bearer ", "Authorization: ", "secret", "x9Zq7Lm2Vb8Nc4Rt6Yh1Jk3", "/", ".", "mysql -p ", "\"", "'", "{", "}", "export ", "A", "b", "1"}
	rng := rand.New(rand.NewSource(5))
	for i := 0; i < 3000; i++ {
		var b strings.Builder
		for n := rng.Intn(12) + 1; n > 0; n-- {
			b.WriteString(frags[rng.Intn(len(frags))])
		}
		in := b.String()
		once, _ := sc.Scrub(in)
		twice, _ := sc.Scrub(once)
		if once != twice {
			t.Fatalf("Scrub(%q): once %q, twice %q", in, once, twice)
		}
	}
}

func FuzzScrubIsIdempotent(f *testing.F) {
	for _, s := range []string{"password=[REDACTED: secret-value]word", "token: abc def\nx", "mysql -p x", "Bearer abcdef123456"} {
		f.Add(s)
	}
	sc := NewScrubber()
	f.Fuzz(func(t *testing.T, in string) {
		once, _ := sc.Scrub(in)
		twice, _ := sc.Scrub(once)
		if once != twice {
			t.Fatalf("Scrub(%q): once %q, twice %q", in, once, twice)
		}
	})
}

func TestPathPartsOf24To31Bytes(t *testing.T) {
	taken(t, "open /var/lib/app/x9Zq7Lm2Vb8Nc4Rt6Yh1Jk3Pq/data", []string{"x9Zq7Lm2Vb8Nc4Rt6Yh1Jk3Pq"}, []string{"/var/lib/app/"})
}

func TestOddSpaceDoesNotTakeTheNextWord(t *testing.T) {
	taken(t, "Authorization: Bearer abc123def456 keepme here", []string{"abc123def456"}, []string{"keepme"})
	taken(t, "run --token abc123def456　keepme", []string{"abc123def456"}, []string{"keepme"})
	taken(t, "Authorization: Bearer abc123def456 x9y8z7w6", []string{"x9y8z7w6"}, nil)
}

func TestShellQuotingForms(t *testing.T) {
	taken(t, "mysql -u root -p$'my pass\\'word' mydb", []string{"my pass", "word'"}, []string{"mydb"})
	taken(t, "sshpass -p 'multi\nline pw' ssh host", []string{"line pw"}, nil)
	taken(t, "docker login -u me -p\nhunter2hunter2 registry.example.com", nil, nil)
}

func TestKeyValueIndentedDashedAndTrailingComma(t *testing.T) {
	taken(t, "    - db_password: my secret value here", []string{"secret value"}, nil)
	taken(t, "  \"db_password\": my secret value here,", []string{"secret value"}, nil)
	taken(t, "- api_token = a b c d", []string{"b c d"}, nil)
}

func TestFormFieldsAndDockerLoginNextLine(t *testing.T) {
	taken(t, "curl --data 'user=me&pass=hunter2hunter2&x=1' https://x.example", []string{"hunter2hunter2"}, []string{"user=me", "x=1"})
	taken(t, "curl https://x.example/login?pwd=hunter2hunter2&a=b", []string{"hunter2hunter2"}, []string{"a=b"})
	taken(t, "curl -d passwd=hunter2hunter2 https://x.example", []string{"hunter2hunter2"}, nil)
	taken(t, "docker login -u me -p \\\n  hunter2hunter2 registry.example.com", []string{"hunter2hunter2"}, nil)
	untouched(t, "pass=true")
}
