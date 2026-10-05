package workspace

import (
	"math/rand"
	"strings"
	"testing"
)

// The shapes of config.toml that send Codex somewhere else, and the ones that look as if
// they do and do not.
func TestCodexConfigOverrideShapes(t *testing.T) {
	cases := []struct {
		name, text string
		over       bool
	}{
		// real overrides
		{"profile with another provider", "profile = \"w\"\n[profiles.w]\nmodel_provider = \"ollama\"\n", true},
		{"profile with an oss provider", "profile = \"w\"\n[profiles.w]\noss_provider = \"ollama\"\n", true},
		{"a profile that is not defined", "profile = \"w\"\n", true},
		{"a profile that is not a string", "profile = 3\n", true},
		{"a quoted key", "\"model_provider\" = \"azure\"\n", true},
		{"a literal quoted key", "'model_provider' = 'azure'\n", true},
		{"after a multi-line array of arrays", "notify = [\n  [\"a\", \"b\"],\n  [\"c\"],\n]\nmodel_provider = \"azure\"\n", true},
		{"after a multi-line array that ends with a bracket line", "x = [\n[1],\n]\n[t]\nk = 1\n", false},
		{"dotted base_url of the openai provider", "model_provider = \"openai\"\nmodel_providers.openai.base_url = \"https://gw.example/v1\"\n", true},
		{"dotted base_url in a table", "model_provider = \"openai\"\n[model_providers]\nopenai.base_url = \"https://gw.example/v1\"\n", true},
		{"an inline table of providers", "model_provider = \"openai\"\nmodel_providers = { openai = { base_url = \"https://gw.example\" } }\n", true},
		{"a table of the openai provider with a gateway", "[model_providers.openai]\nbase_url = \"https://gw.example\"\nname = \"x\"\n", true},
		{"a comment after the value", "model_provider = 'azure' # work\n", true},
		{"CRLF line ends", "model = \"x\"\r\nmodel_provider = \"azure\"\r\n", true},
		{"a byte order mark", string([]byte{0xef, 0xbb, 0xbf}) + "model_provider = \"azure\"\n", true},
		{"a provider with a hash in its name", "model_provider = \"a#b\"\n", true},
		{"a userinfo trick", "openai_base_url = \"https://api.openai.com@evil.example/v1\"\n", true},
		{"the official host in the path", "openai_base_url = \"https://evil.example/api.openai.com\"\n", true},
		{"a top level openai_base_url", "openai_base_url = \"https://gw.example/v1\"\n", true},
		{"a base url that has no host", "openai_base_url = \"//\"\n", true},
		{"a model_provider that is not a string", "model_provider = 5\n", true},
		{"an unparseable file", "model_provider = \n", true},
		{"something that is not TOML", "this is not toml\n", true},
		{"an unclosed string", "model = \"abc\nmodel_provider = \"azure\"\n", true},
		{"a duplicate key", "model_provider = \"openai\"\nmodel_provider = \"azure\"\n", true},
		// not overrides
		{"nothing", "", false},
		{"comments only", "# model_provider = \"azure\"\n", false},
		{"openai", "model_provider = \"openai\"\n", false},
		{"openai with a comment that has quotes", "model_provider = \"openai\" # a \"b\n", false},
		{"the official host in upper case", "openai_base_url = \"https://API.OPENAI.COM/v1\"\n", false},
		{"an empty base url", "openai_base_url = \"\"\n", false},
		{"base_url of an MCP server", "[mcp_servers.s]\nbase_url = \"https://mcp.example/x\"\ncommand = \"x\"\n", false},
		{"text in a multi-line string", "instructions = \"\"\"\nmodel_provider = \"azure\"\nopenai_base_url = \"https://gw.example\"\n\"\"\"\n", false},
		{"text in a multi-line literal string", "instructions = '''\nmodel_provider = \"azure\"\n'''\n", false},
		{"a profile that is not selected", "[profiles.x]\nmodel_provider = \"azure\"\n", false},
		{"a profile that puts openai back", "model_provider = \"azure\"\nprofile = \"w\"\n[profiles.w]\nmodel_provider = \"openai\"\n", false},
		{"an empty profile", "profile = \"\"\n[profiles.x]\nmodel_provider = \"azure\"\n", false},
		{"a model_provider in another table", "[tui]\nmodel_provider = \"azure\"\n", false},
		{"an array of tables", "[[skills]]\nname = \"a\"\nbase_url = \"https://x.example\"\n", false},
	}
	for _, c := range cases {
		if _, over := codexOverrideFrom(c.text); over != c.over {
			t.Errorf("%s: over = %v, want %v for %q", c.name, over, c.over, c.text)
		}
	}
}

func TestTOMLStringsAndValues(t *testing.T) {
	got, err := parseTOML("a = \"x\\ty\\u00e9\"\nb = 'l\\iteral'\nc = \"\"\"\nline one \\\n   joined\nline two\"\"\"\nd = '''\nraw \\n\n'''\ne = [1, 2,\n 3, # c\n]\nf = { g = 1, h.i = \"j\" }\nk = 1979-05-27 07:32:00\nl = true\n[[m]]\nn = 1\n[[m]]\nn = 2\n[o.\"p.q\"]\nr = 0x1F\n")
	if err != nil {
		t.Fatal(err)
	}
	if got["a"] != "x\tyé" || got["b"] != "l\\iteral" || got["c"] != "line one joined\nline two" || got["d"] != "raw \\n\n" {
		t.Errorf("strings: %q %q %q %q", got["a"], got["b"], got["c"], got["d"])
	}
	if e, _ := got["e"].([]any); len(e) != 3 {
		t.Errorf("array: %v", got["e"])
	}
	if f, _ := got["f"].(map[string]any); f == nil || f["h"].(map[string]any)["i"] != "j" {
		t.Errorf("inline table: %v", got["f"])
	}
	if got["l"] != true {
		t.Errorf("bool: %v", got["l"])
	}
	if m, _ := got["m"].([]any); len(m) != 2 {
		t.Errorf("array of tables: %v", got["m"])
	}
	if o, _ := got["o"].(map[string]any); o == nil || o["p.q"] == nil {
		t.Errorf("quoted table key: %v", got["o"])
	}
}

func TestTOMLThingsThatAreErrors(t *testing.T) {
	for _, in := range []string{
		"a = ", "a = \"x", "a = 'x", "a = [1, 2", "a = { b = 1", "a b = 1", "[a", "[[a]", "a = 1 b = 2", "a = 1\na = 2", "a = \"\\q\"",
		"a = \"\"\"x", "= 1", "a.b = 1\na = 2", "[a]\n[a.b]\nc = 1\n[a]\nb = 1\nb.c = 2", "a = nope", "a = \"\xff\"",
		strings.Repeat("[", 80) + strings.Repeat("]", 80),
	} {
		if _, err := parseTOML(in); err == nil {
			t.Errorf("parseTOML(%q) did not fail", in)
		}
	}
}

// Documents that are built from valid pieces, with a provider line put where it is a real
// setting or where it is only text: a real one is found wherever the pieces put it, text
// in a string or a table that is not a setting never counts.
func TestCodexConfigGeneratedDocuments(t *testing.T) {
	top := []string{
		"model = \"gpt-5\"\n", "notify = [\n  [\"a\", \"b\"],\n  [\"c\"],\n]\n", "x = [\n[1],\n[2, 3],\n]\n", "# model_provider = \"azure\"\n",
		"s = \"\"\"\n[profiles.q]\nmodel_provider = \"azure\"\nbase_url = \"https://gw.example\"\n\"\"\"\n", "s2 = '''\nmodel_provider = \"azure\"\n'''\n",
		"inline = { a = 1, b = { c = \"d # e\" } }\n", "n = 1979-05-27 07:32:00\n", "name = \"a#b\" # \"c\n", "approval_policy = 'never'\n\n",
		"dotted.key.here = 1\n", "\"quoted key\" = 2\n",
	}
	tables := []string{
		"[tui]\nmodel_provider = \"azure\"\n", "[mcp_servers.s]\nbase_url = \"https://mcp.example\"\ncommand = \"x\"\n", "[[skills]]\nbase_url = \"https://x.example\"\n",
		"[profiles.other]\nmodel_provider = \"azure\"\n", "[model_providers.unused]\nbase_url = \"https://gw.example\"\n",
	}
	r := rand.New(rand.NewSource(10))
	// Each piece at most once: a key set twice is not TOML.
	pick := func(from []string, n int) string {
		var b strings.Builder
		for _, i := range r.Perm(len(from))[:min(n, len(from))] {
			b.WriteString(from[i])
		}
		return b.String()
	}
	for i := 0; i < 400; i++ {
		eol := []string{"\n", "\r\n"}[r.Intn(2)]
		build := func(withProvider bool) string {
			order := r.Perm(len(top))[:r.Intn(7)]
			cut := r.Intn(len(order) + 1)
			var before, after string
			for k, i := range order {
				if k < cut {
					before += top[i]
				} else {
					after += top[i]
				}
			}
			doc := before
			if withProvider {
				doc += "model_provider = \"azure\"\n"
			}
			doc += after + pick(tables, r.Intn(3))
			return strings.ReplaceAll(doc, "\n", eol)
		}
		if _, over := codexOverrideFrom(build(true)); !over {
			t.Fatalf("a real provider was missed in %q", build(true))
		}
		doc := build(false)
		if why, over := codexOverrideFrom(doc); over {
			t.Fatalf("%s for text that sets no provider: %q", why, doc)
		}
	}
}

func FuzzParseTOML(f *testing.F) {
	for _, s := range []string{
		"", "a = 1", "model_provider = \"azure\"\n", "[a.b]\nc = [1,\n2]\n", "x = \"\"\"\n\\\n\"\"\"", "a = { b = { c = [ { d = 1 } ] } }", "[[a]]\n[[a]]\nb.c = 'x'\n",
		"\xef\xbb\xbfa = 1\r\n", "a = \"\\u00e9\\U0001F600\"", "k = 1979-05-27T07:32:00Z",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		a, errA := parseTOML(s)
		b, errB := parseTOML(s)
		if (errA == nil) != (errB == nil) || len(a) != len(b) {
			t.Fatal("the same text read two ways")
		}
		why1, over1 := codexOverrideFrom(s)
		why2, over2 := codexOverrideFrom(s)
		if why1 != why2 || over1 != over2 {
			t.Fatal("the same text decided two ways")
		}
		if errA != nil && !over1 {
			t.Fatalf("a file that cannot be read was not taken for an override: %q", s)
		}
	})
}

// A surrogate is not a character: an escape that names one is an error, not a replacement
// character in its place.
func TestTOMLAnEscapeNamingASurrogateIsAnError(t *testing.T) {
	for _, in := range []string{`a = "\ud800"`, `a = "\U0000dfff"`, `a = "\U00110000"`} {
		if _, err := parseTOML(in); err == nil {
			t.Errorf("parseTOML(%q) did not fail", in)
		}
	}
	if got, err := parseTOML(`a = "\u00e9"`); err != nil || got["a"] != "é" {
		t.Errorf("a valid escape: %v %v", got, err)
	}
}

// Values nested deeper than 64 are refused (a stack of brackets cannot be made to run away);
// 60 deep is read.
func TestTOMLValuesNestedPastTheCapAreRefused(t *testing.T) {
	deep := func(n int) string { return "a = " + strings.Repeat("[", n) + strings.Repeat("]", n) }
	if _, err := parseTOML(deep(60)); err != nil {
		t.Errorf("60 deep: %v", err)
	}
	if _, err := parseTOML(deep(70)); err == nil {
		t.Error("70 deep was read")
	}
}
