package artifacts

import (
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// Threat: this package, or anything it imports, growing a way to change the
// machine or reach the network. It is meant to be read-only and offline for
// good. Two things are held to that:
//
//   - What it can reach. Every package this one imports, directly or not,
//     for each of the three platforms a release is built for, is walked, and
//     none may be one that starts a process or opens a connection. (A direct
//     import list is not enough: the package once imported internal/review,
//     which imports internal/sysproc, which imports os/exec.)
//   - What its own sources call. No call, by any receiver, of a function that
//     writes, removes, renames, links, creates or changes a file or a folder,
//     or that starts a process; and no open for writing.
//
// A later change that needs one has to change this test, and so be seen.
//
// This is a tripwire, not a sandbox. It catches the ordinary ways a change
// would grow a write, a process or a connection, and a careless or confused one
// that reaches for them by another name. It cannot stop code that is written to
// get round it (reflection, a table of function values built from a package
// that is allowed, a dependency that is added to go.mod and imported under a
// name it does not list). The security of the package rests on its design and
// on review of every change to it; this test makes such a change visible.

// forbiddenImports are packages this package may not import, directly or by
// anything it imports, on any platform.
var forbiddenImports = map[string]bool{
	"os/exec": true, "net": true, "net/http": true, "net/rpc": true, "net/smtp": true, "net/mail": true,
	"net/textproto": true, "plugin": true, "os/signal": true, "os/user": true, "log/syslog": true,
	"crypto/tls": true, "database/sql": true, "C": true,
}

// everyPlatform are all the systems Go builds for, with the architecture that
// has them. The walk covers all of them: a file for one of them is not built on
// the machine that runs this test.
var everyPlatform = [][2]string{
	{"windows", "amd64"}, {"linux", "amd64"}, {"darwin", "arm64"}, {"freebsd", "amd64"}, {"netbsd", "amd64"},
	{"openbsd", "amd64"}, {"dragonfly", "amd64"}, {"solaris", "amd64"}, {"illumos", "amd64"}, {"aix", "ppc64"},
	{"plan9", "amd64"}, {"android", "arm64"}, {"ios", "arm64"}, {"js", "wasm"}, {"wasip1", "wasm"},
}

func TestPackageIsReadOnlyAndOffline(t *testing.T) {
	forbidden := forbiddenImports
	root := moduleRoot(t)
	for _, p := range everyPlatform {
		goos := p[0]
		ctx := build.Default
		ctx.GOOS, ctx.GOARCH, ctx.CgoEnabled = goos, p[1], false
		deps := map[string]bool{}
		walkDeps(t, &ctx, root, "github.com/jmwri/flockdeck/internal/artifacts", deps)
		var names []string
		for d := range deps {
			names = append(names, d)
		}
		sort.Strings(names)
		for _, d := range names {
			if forbidden[d] {
				t.Errorf("on %s the package can reach %s", goos, d)
			}
			if strings.HasPrefix(d, "github.com/jmwri/flockdeck/") &&
				d != "github.com/jmwri/flockdeck/internal/artifacts" &&
				d != "github.com/jmwri/flockdeck/internal/secretname" {
				t.Errorf("on %s the package imports %s: only the leaf internal/secretname is allowed", goos, d)
			}
		}
		if len(deps) < 5 {
			t.Fatalf("on %s only %d packages were found; the walk is not looking where it should", goos, len(deps))
		}
	}
	sourceChecks(t)
}

// moduleRoot is this module's directory: the nearest one up with a go.mod.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test's directory")
		}
		dir = parent
	}
}

const modulePath = "github.com/jmwri/flockdeck"

// walkDeps adds path, and everything it imports, to seen, reading only source
// (the standard library from GOROOT, this module from its own tree) and running
// nothing.
func walkDeps(t *testing.T, ctx *build.Context, modRoot, path string, seen map[string]bool) {
	t.Helper()
	if path == "C" || path == "unsafe" || seen[path] {
		return
	}
	seen[path] = true
	var pkg *build.Package
	var err error
	if rest, ok := strings.CutPrefix(path, modulePath+"/"); ok {
		pkg, err = ctx.ImportDir(filepath.Join(modRoot, filepath.FromSlash(rest)), 0)
	} else if path == modulePath {
		pkg, err = ctx.ImportDir(modRoot, 0)
	} else {
		pkg, err = ctx.Import(path, filepath.Join(runtime.GOROOT(), "src"), 0)
		if err == nil {
			// The standard library's own vendored copies are named
			// vendor/golang.org/...; seen under that name.
			seen[pkg.ImportPath] = true
		}
	}
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	for _, imp := range pkg.Imports {
		walkDeps(t, ctx, modRoot, imp, seen)
	}
}

// writes are names refused whatever they are called on: os.Remove and
// root.Remove, a method called Mkdir and a function called Mkdir.
var writes = map[string]bool{
	"WriteFile": true, "Create": true, "CreateTemp": true, "Remove": true, "RemoveAll": true,
	"Rename": true, "Mkdir": true, "MkdirAll": true, "MkdirTemp": true, "Chmod": true, "Chown": true,
	"Lchown": true, "Chtimes": true, "Truncate": true, "Symlink": true, "Link": true,
	"Setenv": true, "Unsetenv": true, "Clearenv": true, "Chdir": true, "Mkfifo": true, "Mknod": true,
	// a file made from a descriptor, and writes to one
	"NewFile": true, "Write": true, "WriteAt": true, "Pwrite": true, "Ftruncate": true, "Fchmod": true, "Fchown": true,
	// syscall, by the names the platforms give them
	"Unlink": true, "Rmdir": true, "StartProcess": true, "ForkExec": true, "Exec": true,
	"CreateProcess": true, "DeleteFile": true, "MoveFile": true, "SetEndOfFile": true, "SetFileTime": true,
	"Renameat": true, "Unlinkat": true, "Mkdirat": true, "Symlinkat": true, "Linkat": true, "Mknodat": true,
	// more ways to write to a file or to change one
	"WriteString": true, "ReadFrom": true, "Utimes": true, "UtimesNano": true, "Futimes": true, "Lutimes": true,
	"Fchdir": true, "Setuid": true, "Setgid": true, "Fsync": true,
	// raw system calls, sockets, signals, and leaving
	"Syscall": true, "Syscall6": true, "Syscall9": true, "RawSyscall": true, "RawSyscall6": true,
	"Socket": true, "Connect": true, "Bind": true, "Listen": true, "Accept": true, "Sendto": true, "Dial": true,
	"Kill": true, "FindProcess": true, "Exit": true,
}

// writeIdents may not be named at all in this package's sources: not in an open,
// not in a constant or a variable that is later passed to one.
var writeIdents = map[string]bool{
	"O_WRONLY": true, "O_RDWR": true, "O_CREATE": true, "O_CREAT": true, "O_TRUNC": true, "O_APPEND": true, "O_EXCL": true,
	"O_SYNC": true, "O_DSYNC": true, "O_TMPFILE": true,
	// Windows access rights and dispositions that write or create
	"GENERIC_WRITE": true, "GENERIC_ALL": true, "FILE_WRITE_DATA": true, "FILE_APPEND_DATA": true,
	"FILE_WRITE_ATTRIBUTES": true, "FILE_WRITE_EA": true, "DELETE": true, "WRITE_DAC": true, "WRITE_OWNER": true,
	"CREATE_NEW": true, "CREATE_ALWAYS": true, "OPEN_ALWAYS": true, "TRUNCATE_EXISTING": true,
}

// readFlags are the only names an open's flags may be made of, and 0.
var readFlags = map[string]bool{
	"O_RDONLY": true, "O_NONBLOCK": true, "O_NOFOLLOW": true, "O_NOCTTY": true, "O_CLOEXEC": true, "O_DIRECTORY": true,
	"openFlags": true,
}

// readAccess are the only names a Windows CreateFile's access and disposition
// may be, besides 0.
var readAccess = map[string]bool{
	"GENERIC_READ": true, "FILE_READ_ATTRIBUTES": true, "FILE_LIST_DIRECTORY": true, "SYNCHRONIZE": true,
	"OPEN_EXISTING": true,
}

// onlyNames is whether e is built from names in allowed (with any package
// prefix), "|", parentheses and the literal 0 -- and from nothing else: not a
// variable, a call, another literal. That is what makes the flags of an open
// something this test can read.
func onlyNames(e ast.Expr, allowed map[string]bool) bool {
	switch x := e.(type) {
	case *ast.Ident:
		return allowed[x.Name]
	case *ast.SelectorExpr:
		_, isPkg := x.X.(*ast.Ident)
		return isPkg && allowed[x.Sel.Name]
	case *ast.BasicLit:
		return x.Kind == token.INT && x.Value == "0"
	case *ast.ParenExpr:
		return onlyNames(x.X, allowed)
	case *ast.BinaryExpr:
		return x.Op == token.OR && onlyNames(x.X, allowed) && onlyNames(x.Y, allowed)
	}
	return false
}

// unsafeAllowed are the only files that may import "unsafe": the Windows file
// that asks the system for a handle's final path.
var unsafeAllowed = map[string]bool{"root_windows.go": true}

// checkSource returns what a source file does that this package must not: import
// a package that starts processes or dials (or "C", or "unsafe" outside the file
// that needs it), use anything that changes a file, a folder or the environment,
// makes a file from a descriptor, makes a raw system call or leaves the process
// -- as a call or as a value that is called later -- open a file with flags that
// are not read-only names, name a write flag at all, or link to a runtime
// function with a go:linkname.
func checkSource(fset *token.FileSet, name string, f *ast.File) []string {
	var bad []string
	add := func(n ast.Node, format string, args ...any) {
		bad = append(bad, fset.Position(n.Pos()).String()+": "+fmt.Sprintf(format, args...))
	}
	for _, imp := range f.Imports {
		p, _ := strconv.Unquote(imp.Path.Value)
		if forbiddenImports[p] || strings.HasPrefix(p, "net/") && p != "net/url" && !strings.HasPrefix(p, "net/netip") {
			add(imp, "imports %s", p)
		}
		if p == "unsafe" && !unsafeAllowed[filepath.Base(name)] {
			add(imp, "imports unsafe")
		}
	}
	for _, cg := range f.Comments {
		for _, c := range cg.List {
			if strings.HasPrefix(c.Text, "//go:linkname") || strings.HasPrefix(c.Text, "//go:cgo") {
				add(c, "has a %s directive", strings.Fields(c.Text)[0])
			}
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Ident:
			if writeIdents[x.Name] {
				add(x, "names %s", x.Name)
			}
			if writes[x.Name] {
				add(x, "uses %s, which changes files or starts processes", x.Name)
			}
		case *ast.SelectorExpr:
			if writeIdents[x.Sel.Name] {
				add(x, "names %s", x.Sel.Name)
			}
			// As a value too, not only as a call: rm := os.Remove; rm(path).
			if writes[x.Sel.Name] {
				add(x, "uses %s, which changes files or starts processes", x.Sel.Name)
			}
		case *ast.ValueSpec:
			// The one constant the open flags may be spelled with is itself held to
			// being read-only names.
			for i, id := range x.Names {
				if id.Name == "openFlags" && i < len(x.Values) && !onlyNames(x.Values[i], readFlags) {
					add(x, "openFlags is not made of read-only flag names")
				}
			}
		case *ast.CallExpr:
			var fn, recv string
			switch c := x.Fun.(type) {
			case *ast.SelectorExpr:
				fn = c.Sel.Name
				if id, ok := c.X.(*ast.Ident); ok {
					recv = id.Name
				}
			case *ast.Ident:
				fn = c.Name
			}
			flagArg := -1
			switch {
			case fn == "OpenFile":
				flagArg = 1
			case fn == "Open" && (recv == "syscall" || recv == "unix"):
				flagArg = 1
			case fn == "Openat" && (recv == "syscall" || recv == "unix"):
				flagArg = 2
			}
			if flagArg >= 0 && (len(x.Args) <= flagArg || !onlyNames(x.Args[flagArg], readFlags)) {
				add(x, "opens a file with flags that are not read-only names")
			}
			if fn == "CreateFile" && recv == "syscall" {
				if len(x.Args) < 5 || !onlyNames(x.Args[1], readAccess) || !onlyNames(x.Args[4], readAccess) {
					add(x, "CreateFile with an access or disposition that is not read-only")
				}
			}
		}
		return true
	})
	return bad
}

// sourceChecks parses this package's own non-test sources, for every platform's
// files, and refuses what checkSource finds.
func sourceChecks(t *testing.T) {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	checked := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		// Parsed for every platform at once (no build constraints applied), so
		// a Windows or Unix file is read wherever this runs.
		f, err := parser.ParseFile(fset, name, nil, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		checked++
		for _, b := range checkSource(fset, name, f) {
			t.Error(b)
		}
	}
	if checked < 6 {
		t.Fatalf("only %d source files were read; the check is not looking where it should", checked)
	}
}

func parseSample(t *testing.T, name, src string) (*token.FileSet, *ast.File) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, parser.ParseComments)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return fset, f
}

// The check itself must be able to fail: run over source that does each thing it
// forbids, it says so; run over source that opens a file the way this package
// does, it does not. (A guard that cannot find anything is not a guard.)
func TestReadOnlyCheckCatchesWhatItForbids(t *testing.T) {
	bad := map[string]string{
		"remove":           `os.Remove("a")`,
		"method mkdir":     `r.Mkdir("d", 0)`,
		"exec call":        `exec.Command("x")`,
		"write flag":       `os.OpenFile("a", os.O_WRONLY, 0)`,
		"flags in var":     `flags := 0x41; os.OpenFile("a", flags, 0)`,
		"flags in const":   `const w = 0x41; os.OpenFile("a", w, 0)`,
		"flags from func":  `os.OpenFile("a", flagsFor("a"), 0)`,
		"flags literal":    `os.OpenFile("a", 0x41, 0)`,
		"flags missing":    `os.OpenFile("a")`,
		"write const":      `const c = syscall.O_CREAT; _ = c`,
		"write ident":      `x := O_TRUNC; _ = x`,
		"syscall open w":   `syscall.Open("a", syscall.O_WRONLY, 0)`,
		"syscall open var": `syscall.Open("a", fl, 0)`,
		"unix openat":      `unix.Openat(1, "a", fl, 0)`,
		"new file":         `os.NewFile(3, "x")`,
		"write call":       `f.Write(nil)`,
		"createfile w":     `syscall.CreateFile(n, syscall.GENERIC_WRITE, 0, nil, syscall.OPEN_EXISTING, 0, 0)`,
		"createfile disp":  `syscall.CreateFile(n, 0, 0, nil, syscall.CREATE_ALWAYS, 0, 0)`,
		"createfile var":   `syscall.CreateFile(n, acc, 0, nil, syscall.OPEN_EXISTING, 0, 0)`,
		"function value":   `rm := os.Remove; rm("a")`,
		"var value":        `var rm = os.Rename; _ = rm`,
		"write string":     `f.WriteString("x")`,
		"read from":        `f.ReadFrom(nil)`,
		"raw syscall":      `syscall.Syscall(1, 2, 3, 4)`,
		"socket":           `syscall.Socket(1, 2, 3)`,
		"connect":          `syscall.Connect(1, nil)`,
		"kill":             `syscall.Kill(1, 9)`,
		"process kill":     `p.Kill()`,
		"utimes":           `syscall.Utimes("a", nil)`,
		"exit":             `os.Exit(1)`,
	}
	for name, body := range bad {
		src := "package x\nimport (\"os\"; \"os/exec\"; \"syscall\")\nfunc f(r *os.Root, n *uint16, acc uint32, fl int) {\n" + body + "\n}\n"
		fset, f := parseSample(t, name, src)
		if len(checkSource(fset, "x.go", f)) == 0 {
			t.Errorf("%s: the check did not object to %s", name, body)
		}
	}
	for name, src := range map[string]string{
		"exec import":     "package x\nimport \"os/exec\"\n",
		"smtp import":     "package x\nimport \"net/smtp\"\n",
		"net sub import":  "package x\nimport \"net/http/httputil\"\n",
		"cgo import":      "package x\nimport \"C\"\n",
		"unsafe import":   "package x\nimport \"unsafe\"\n",
		"linkname":        "package x\n//go:linkname f runtime.f\nfunc f()\n",
		"openFlags write": "package x\nconst openFlags = 0x41\n",
		"openFlags mixed": "package x\nconst openFlags = syscall.O_NONBLOCK | 0x1\n",
	} {
		fset, f := parseSample(t, name, src)
		if len(checkSource(fset, "x.go", f)) == 0 {
			t.Errorf("%s: the check did not object", name)
		}
	}
	good := `package x
import ("os"; "syscall")
const openFlags = syscall.O_NONBLOCK | syscall.O_NOFOLLOW | syscall.O_NOCTTY
func f(r *os.Root, n *uint16) {
	r.OpenFile("a", os.O_RDONLY|openFlags, 0)
	r.Lstat("a")
	syscall.CreateFile(n, 0, syscall.FILE_SHARE_READ, nil, syscall.OPEN_EXISTING, syscall.FILE_FLAG_BACKUP_SEMANTICS, 0)
	syscall.Open("a", syscall.O_RDONLY, 0)
}
`
	fset, f := parseSample(t, "good", good)
	if got := checkSource(fset, "x.go", f); len(got) != 0 {
		t.Errorf("the check objected to read-only opens: %v", got)
	}
	// And the walk of imports reaches os/exec through a package that uses it.
	ctx := build.Default
	ctx.CgoEnabled = false
	deps := map[string]bool{}
	walkDeps(t, &ctx, moduleRoot(t), "github.com/jmwri/flockdeck/internal/review", deps)
	if !deps["os/exec"] {
		t.Error("the walk did not find os/exec under internal/review, which does import it by way of sysproc")
	}
}
