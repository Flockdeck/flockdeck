package bundle

import (
	"os"
	"strings"
	"testing"
	"testing/fstest"
)

func files(m map[string]string) fstest.MapFS {
	out := fstest.MapFS{}
	for k, v := range m {
		out[k] = &fstest.MapFile{Data: []byte(v)}
	}
	return out
}

func TestTheFrontEndJoinsIntoOneScript(t *testing.T) {
	out, err := Modules(os.DirFS("../ui/assets"), "main.js")
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, name := range []string{`"main.js"`, `"lib/store.js"`, `"shell/Live.js"`, `"vendor/preact.mjs"`, `"vendor/hooks.mjs"`, `"vendor/htm.mjs"`} {
		if !strings.Contains(s, "__defs["+name+"]") {
			t.Errorf("the bundle has no module %s", name)
		}
	}
	if strings.Contains(s, `"lib/load-script.js"`) == false {
		t.Error("the bundle has no load-script.js, which the terminal needs")
	}
}

func TestNamesAndShapesOfImportsAndExports(t *testing.T) {
	out, err := Modules(files(map[string]string{
		"main.js":  "import { a, b as c } from \"./lib/x.js\";\nimport * as ns from \"./lib/x.js\";\nimport D from \"./d.js\";\nexport { a as z } from \"./lib/x.js\";\nconsole.log(a, c, ns, D);\n",
		"lib/x.js": "const str = 1;\nexport const a = str;\nexport function b() {}\nexport async function e() {}\nexport class K {}\n",
		"d.js":     "const str = 2;\nexport default function d() { return str; }\n",
	}), "main.js")
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{
		"const { a, b: c } = __req(\"lib/x.js\");",
		"const ns = __req(\"lib/x.js\");",
		"const D = __req(\"d.js\").default;",
		"__x.z = __m.a;",
		"__x.a = a;", "__x.b = b;", "__x.e = e;", "__x.K = K;", "__x.default = d;",
		"function d()",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("no %q in\n%s", want, s)
		}
	}
}

func TestWhatItCannotReadIsRefused(t *testing.T) {
	cases := map[string]string{
		"a bare import":        "import { x } from \"preact\";\n",
		"an import cycle":      "import { x } from \"./b.js\";\n",
		"a changing export":    "export let n = 1;\n",
		"a dynamic import":     "const m = import(\"./b.js\");\n",
		"import.meta":          "const u = import.meta.url;\n",
		"a side-effect import": "import \"./b.js\";\n",
		"an export of a star":  "export * from \"./b.js\";\n",
		"an unreadable list":   "import { a b } from \"./b.js\";\n",
		"a missing module":     "import { x } from \"./nope.js\";\n",
	}
	for name, src := range cases {
		fsys := files(map[string]string{"main.js": src, "b.js": "import { y } from \"./main.js\";\nexport const x = 1;\n"})
		if _, err := Modules(fsys, "main.js"); err == nil {
			t.Errorf("%s was bundled", name)
		}
	}
}

func TestStylesJoinInTheOrderTheyAreLinkedAndThePageNamesOne(t *testing.T) {
	fsys := files(map[string]string{
		"vendor/a.css": "a{}",
		"app.css":      "@font-face{src:url(\"fonts/f.woff2\")}\nb{}\n",
		"shell/c.css":  "c{background:url(data:image/png;base64,AA)}",
	})
	page := "<head>\n<link rel=\"stylesheet\" href=\"assets/vendor/a.css\">\n<link rel=\"stylesheet\" href=\"assets/app.css\">\n<link rel=\"stylesheet\" href=\"assets/shell/c.css\">\n</head>\n"
	css, out, err := Styles(fsys, []byte(page), "app.css")
	if err != nil {
		t.Fatal(err)
	}
	s := string(css)
	if !(strings.Index(s, "a{}") < strings.Index(s, "b{}") && strings.Index(s, "b{}") < strings.Index(s, "c{")) {
		t.Errorf("the order is not the page's:\n%s", s)
	}
	if got := strings.Count(string(out), "<link"); got != 1 || !strings.Contains(string(out), `href="assets/app.css"`) {
		t.Errorf("the page links %d stylesheets: %s", got, out)
	}
	fsys["shell/c.css"] = &fstest.MapFile{Data: []byte("c{background:url(img/x.png)}")}
	if _, _, err := Styles(fsys, []byte(page), "app.css"); err == nil {
		t.Error("a relative url() in another folder was joined")
	}
}
