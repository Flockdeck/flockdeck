package boundary

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// tree writes files under a temp root and returns it.
func tree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func rules(vs []Violation) []string {
	var out []string
	for _, v := range vs {
		out = append(out, v.Rule+":"+v.File)
	}
	slices.Sort(out)
	return out
}

func TestNoUIDirectoryIsNoViolations(t *testing.T) {
	root := tree(t, map[string]string{"main.go": "package main\n", "internal/server/s.go": "package server\n"})
	got, err := Check(root)
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestACleanTreeHasNone(t *testing.T) {
	root := tree(t, map[string]string{
		"main.go":                    "package main\nimport _ \"github.com/jmwri/flockdeck/ui\"\nimport _ \"github.com/jmwri/flockdeck/internal/server\"\n",
		"cmd/webbundle/main.go":      "package main\nimport _ \"github.com/jmwri/flockdeck/ui/build\"\n",
		"internal/server/s.go":       "package server\n",
		"ui/embed.go":                "package ui\nimport \"embed\"\nvar _ embed.FS\n",
		"ui/src/a.js":                "import { b } from \"./b.js\";\nexport { c } from \"../lib/c.js\";\n",
		"ui/test/a.test.mjs":         "import x from \"../src/a.js\";\nimport f from \"../../api/fixtures/v1/hello.json\";\n",
		"api/catalog.json":           "{}",
		"api/fixtures/v1/hello.json": "{}",
	})
	got, err := Check(root)
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestEachCrossingIsFound(t *testing.T) {
	root := tree(t, map[string]string{
		"ui/embed.go":          "package ui\nimport _ \"github.com/jmwri/flockdeck/internal/server\"\n",
		"internal/server/s.go": "package server\nimport _ \"github.com/jmwri/flockdeck/ui\"\n",
		"ui/src/a.js":          "import { x } from \"../../internal/webui/x.js\";\nconst y = await import(\"../../../outside.js\");\n",
		"api/helper.go":        "package api\n",
		"api/tool.mjs":         "export {};\n",
	})
	got, err := Check(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"api-is-data:api/helper.go", "api-is-data:api/tool.mjs",
		"server-imports-ui:internal/server/s.go",
		"ui-imports-server:ui/embed.go",
		"ui-reaches-out:ui/src/a.js", "ui-reaches-out:ui/src/a.js",
	}
	if g := rules(got); !slices.Equal(g, want) {
		t.Fatalf("got %v\nwant %v", g, want)
	}
	for _, v := range got {
		if v.Line == 0 {
			t.Errorf("no line for %+v", v)
		}
	}
}

func TestTestdataAndNodeModulesAreNotRead(t *testing.T) {
	root := tree(t, map[string]string{
		"ui/embed.go":                "package ui\n",
		"ui/testdata/bad.go":         "package x\nimport _ \"github.com/jmwri/flockdeck/internal/server\"\n",
		"ui/node_modules/p/index.js": "import x from \"../../../../secret.js\";\n",
	})
	if got, _ := Check(root); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}

func TestAUIPullRequestMayTouchOnlyUIAndFixtures(t *testing.T) {
	got := PathsOutsideUI([]string{"ui/src/a.js", "api/fixtures/v1/x.json", "api/catalog.json", "internal/server/s.go", "", " main.go "})
	want := []string{"api/catalog.json", "internal/server/s.go", "main.go"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v", got)
	}
}

// TestThisRepository runs the check on the real tree. Until ui/ exists it finds
// nothing to check; once it does, it is the check CI runs.
func TestThisRepository(t *testing.T) {
	got, err := Check(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range got {
		t.Errorf("%s:%d %s: %s", v.File, v.Line, v.Rule, v.Text)
	}
}
