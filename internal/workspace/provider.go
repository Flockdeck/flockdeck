package workspace

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"github.com/jmwri/flockdeck/internal/agent"
)

// BatonProvider names the company an agent sends what it is given to, so a baton
// handed from one agent to another can say when that changes. It is the vendor's
// wire for an API agent (with the endpoint when the agent talks to one of its own)
// and the program for a CLI, and "" when it is not known: a CLI that has been
// pointed at a gateway or a cloud's copy of a model sends what it is given to
// whoever runs that.
func BatonProvider(spec agent.Spec) string {
	p, _ := BatonProviderDetail(spec, "")
	return p
}

// BatonProviderDetail is BatonProvider for an agent that runs in cwd, whose project
// settings (the folders from cwd up to the git root) are read, and, for a folder that
// does not exist yet (a worktree about to be made), also from the folders in also:
// the checkout it is cut from. It says why the company is not known when it is not:
// "through a gateway or proxy (host)", a settings file that could not be read, or
// "not known".
func BatonProviderDetail(spec agent.Spec, cwd string, also ...string) (provider, why string) {
	return BatonProviderWith(spec, cwd, Sources{Dirs: also})
}

// Sources are where besides the folder itself a CLI's settings are read from: other
// folders (the checkout a worktree is cut from), and settings files as text (what a
// branch holds in .claude/settings.json, which is not in any folder yet).
type Sources struct {
	Dirs     []string
	Settings [][]byte
	// Unknown, when it is not empty, says that a settings file was meant to be read from
	// the object store and could not be: the company is not known.
	Unknown string
}

// BatonProviderWith is BatonProviderDetail with Sources.
func BatonProviderWith(spec agent.Spec, cwd string, src Sources) (provider, why string) {
	if spec.Runner == agent.RunnerAPI {
		if spec.API.BaseURL != "" {
			return spec.API.Wire + " via " + spec.API.BaseURL, ""
		}
		return spec.API.Wire, ""
	}
	vendor := vendorOf(spec)
	if why, over := gatewayOverride(vendor, spec, append([]string{cwd}, src.Dirs...), src.Settings, src.Unknown); over {
		return "", why
	}
	switch vendor {
	case "claude":
		return "anthropic", ""
	case "codex":
		return "openai", ""
	case "gemini":
		return "gemini", ""
	}
	if p := firstNonEmpty(exeName(spec), spec.ID); p != "" {
		return p, ""
	}
	return "", "not known"
}

// BatonProviderShown is a provider as a window may be told it: an endpoint is
// reduced to its host, since a base URL can carry a credential. "" stays "".
func BatonProviderShown(spec agent.Spec, cwd string) string {
	p, _ := BatonProviderDetail(spec, cwd)
	return ProviderHost(p)
}

// ProviderHost is a provider with any URL in it reduced to its host.
func ProviderHost(p string) string {
	if i := strings.Index(p, " via "); i >= 0 {
		if h := HostOf(p[i+len(" via "):]); h != "" {
			return p[:i] + " via " + h
		}
		return p[:i] + " via a custom endpoint"
	}
	return p
}

// asciiLower lower-cases the ASCII letters of s and nothing else. strings.ToLower folds
// other characters to ASCII (U+0130 to i), which would make api.anthropİc.com read as
// api.anthropic.com.
func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}

// HostOf is the host of a URL or a host[:port], with no user, password, path, query
// or trailing dot, or "" when there is none to find.
func HostOf(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return ""
	}
	h := strings.TrimSuffix(asciiLower(u.Hostname()), ".")
	if len(h) > 80 {
		h = h[:80]
	}
	return h
}

// exeName is the program an agent runs, without its folder or extension.
func exeName(spec agent.Spec) string {
	// Either separator, whatever this runs on: a catalog is written on one
	// machine and can be carried to another.
	exe := spec.Exe
	if i := strings.LastIndexAny(exe, `/\`); i >= 0 {
		exe = exe[i+1:]
	}
	exe = asciiLower(exe)
	for _, ext := range []string{".exe", ".cmd", ".bat", ".com", ".ps1"} {
		if name, ok := strings.CutSuffix(exe, ext); ok {
			return name
		}
	}
	return exe
}

// vendorOf is the vendor a CLI agent belongs to: by its program (claude, codex,
// gemini), and where a wrapper runs it (claude-code, node, a script) by what the
// agent's id says.
func vendorOf(spec agent.Spec) string {
	exe := exeName(spec)
	for _, v := range []string{"claude", "codex", "gemini"} {
		if exe == v {
			return v
		}
	}
	id := asciiLower(spec.ID)
	for _, v := range []string{"claude", "codex", "gemini"} {
		if id == v || strings.HasPrefix(id, v+"-") || strings.HasPrefix(id, v+"_") || strings.HasPrefix(id, v+".") {
			return v
		}
	}
	return ""
}

// gatewayVar is a setting that sends one vendor's CLI somewhere else: a base URL,
// which is a gateway when its host is not the vendor's own, or a switch that turns
// on a cloud's copy of the model.
type gatewayVar struct {
	name     string
	official []string // hosts that are the vendor's own
	flag     bool     // a switch, not an address
}

// gatewayVars are scoped to the CLI each one affects. A variable that names one
// vendor does nothing to another vendor's CLI.
var gatewayVars = map[string][]gatewayVar{
	"claude": {
		{name: "ANTHROPIC_BASE_URL", official: []string{"api.anthropic.com"}},
		{name: "ANTHROPIC_BEDROCK_BASE_URL"}, {name: "ANTHROPIC_VERTEX_BASE_URL"},
		{name: "ANTHROPIC_FOUNDRY_BASE_URL"}, {name: "ANTHROPIC_FOUNDRY_RESOURCE"},
		{name: "CLAUDE_CODE_USE_BEDROCK", flag: true}, {name: "CLAUDE_CODE_USE_VERTEX", flag: true},
		{name: "CLAUDE_CODE_USE_FOUNDRY", flag: true},
	},
	"codex": {
		{name: "OPENAI_BASE_URL", official: []string{"api.openai.com"}},
		{name: "OPENAI_API_BASE", official: []string{"api.openai.com"}},
		{name: "AZURE_OPENAI_ENDPOINT"}, {name: "CODEX_OSS_BASE_URL"},
	},
	"gemini": {
		{name: "GOOGLE_GEMINI_BASE_URL", official: []string{"generativelanguage.googleapis.com"}},
		{name: "GEMINI_API_BASE_URL", official: []string{"generativelanguage.googleapis.com"}},
		{name: "GOOGLE_VERTEX_BASE_URL"}, {name: "GOOGLE_GENAI_USE_VERTEXAI", flag: true},
	},
}

// gatewayOverride reports whether a setting that applies to this CLI sends it
// somewhere other than its vendor, and says how, for a notice. The settings are read
// from the agent's own environment block, from Flockdeck's environment, for Claude Code
// from the env of its settings.json and settings.local.json (the user's, and the
// project's from each folder in dirs up to the git root), and for Codex from
// ~/.codex/config.toml. An empty value, 0, false, and an address whose host is the
// vendor's own (a trailing dot too) are not an override. An agent's own env entry that
// is empty does not hide the same setting from the environment. A Claude Code settings
// file that cannot be read is taken for an override, because it may hold one.
func gatewayOverride(vendor string, spec agent.Spec, dirs []string, blobs [][]byte, unknown string) (why string, over bool) {
	vars := gatewayVars[vendor]
	if len(vars) == 0 {
		return "", false
	}
	sources := []func(string) string{
		func(name string) string {
			for _, kv := range spec.Env {
				if k, v, ok := strings.Cut(kv, "="); ok && strings.EqualFold(k, name) {
					return v
				}
			}
			return ""
		},
		environFold,
	}
	switch vendor {
	case "claude":
		envs, unreadable := claudeSettingsEnv(dirs)
		if unreadable == "" {
			unreadable = unknown
		}
		for _, b := range blobs {
			env, ok := parseSettingsEnv(b)
			if !ok {
				unreadable = "the settings.json a branch holds"
				continue
			}
			if len(env) > 0 {
				envs = append(envs, env)
			}
		}
		if unreadable != "" {
			return "its Claude Code settings could not be read (" + unreadable + ")", true
		}
		for _, env := range envs {
			e := env
			sources = append(sources, func(name string) string { return lookupFold(e, name) })
		}
	case "codex":
		if why, over := codexConfigOverride(); over {
			return why, true
		}
	}
	for _, gv := range vars {
		for _, get := range sources {
			v := strings.TrimSpace(get(gv.name))
			switch asciiLower(v) {
			case "", "0", "false", "no", "off":
				continue
			}
			if gv.flag {
				return "through a cloud's copy of the model (" + gv.name + ")", true
			}
			h := HostOf(v)
			if h != "" && isOfficial(h, gv.official) {
				continue
			}
			if h != "" {
				return "through a gateway or proxy (" + h + ")", true
			}
			return "through a gateway or proxy", true
		}
	}
	return "", false
}

func isOfficial(host string, official []string) bool {
	for i := 0; i < len(host); i++ {
		if host[i] >= 0x80 {
			return false // a host that is not plain ASCII is never the vendor's own
		}
	}
	for _, o := range official {
		if host == o {
			return true
		}
	}
	return false
}

// projectDirs are a folder and its parents up to the git root (the first with a .git in
// it, within eight levels), or the folder alone when there is no repository.
func projectDirs(cwd string) []string {
	if cwd == "" {
		return nil
	}
	// With no repository on the way up there is no project: only the folder itself, not
	// whatever folders above it hold.
	inRepo := false
	for d, i := filepath.Clean(cwd), 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			inRepo = true
			break
		}
		if parent := filepath.Dir(d); parent == d {
			break
		} else {
			d = parent
		}
	}
	if !inRepo {
		return []string{filepath.Clean(cwd)}
	}
	var out []string
	dir := filepath.Clean(cwd)
	for i := 0; i < 8 && dir != "" && dir != "." && cwd != ""; i++ {
		out = append(out, dir)
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return out
}

// managedSettingsPaths are where Claude Code's managed settings file is on this system
// (the paths its documentation gives, which Flockdeck has not been able to check against
// a managed machine); a test replaces it.
var managedSettingsPaths = func() []string {
	switch runtime.GOOS {
	case "windows":
		return []string{filepath.Join(envOr("ProgramFiles", `C:\Program Files`), "ClaudeCode", "managed-settings.json")}
	case "darwin":
		return []string{"/Library/Application Support/ClaudeCode/managed-settings.json"}
	}
	return []string{"/etc/claude-code/managed-settings.json"}
}

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

// settingsFileMax is the largest settings file that is read.
const settingsFileMax = 1 << 20

// claudeSettingsEnv reads the env block of Claude Code's settings files: the user's, the
// managed one (and the .json files in the managed-settings.d folder beside it), then, for
// each project folder, settings.json and settings.local.json. A file that is not there has
// none. A file that is there and cannot be read fails closed: one that cannot be opened (a
// permission refused, a folder where it should be), is over 1 MB (it is not read at all),
// or cannot be read as JSON (once a byte order mark, comments and trailing commas are taken
// out) is named in unreadable, and the company is not known.
func claudeSettingsEnv(dirs []string) (envs []map[string]string, unreadable string) {
	var paths []string
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		paths = append(paths, filepath.Join(dir, "settings.json"))
	} else if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, ".claude", "settings.json"))
	}
	bad := func(what string) {
		if unreadable == "" {
			unreadable = what
		}
	}
	// Settings an organisation manages, which win over the others: the file, and the
	// drop-in files of the folder beside it. The folder is not known to exist on every
	// system, so a missing one is nothing, and one that is there and cannot be listed
	// is not known.
	for _, m := range managedSettingsPaths() {
		paths = append(paths, m)
		d := filepath.Join(filepath.Dir(m), "managed-settings.d")
		entries, err := os.ReadDir(d)
		switch {
		case err == nil:
			for _, e := range entries {
				if !e.IsDir() && strings.HasSuffix(asciiLower(e.Name()), ".json") && !editorFile(e.Name()) {
					paths = append(paths, filepath.Join(d, e.Name()))
				}
			}
		case !absentErr(err):
			bad(d + " could not be listed")
		}
	}
	seen := map[string]bool{}
	for _, d := range dirs {
		for _, dir := range settingsDirs(d) {
			if seen[dir] {
				continue
			}
			seen[dir] = true
			paths = append(paths, filepath.Join(dir, ".claude", "settings.json"), filepath.Join(dir, ".claude", "settings.local.json"))
		}
	}
	for _, p := range paths {
		data, absent, why := readSettingsFile(p)
		if absent {
			continue
		}
		if why != "" {
			// The whole path, so that the user's own file, the project's and the managed one can
			// be told apart.
			bad(p + " " + why)
			continue
		}
		env, ok := parseSettingsEnv(data)
		if !ok {
			bad(p + " is not JSON that can be read")
			continue
		}
		if len(env) > 0 {
			envs = append(envs, env)
		}
	}
	return envs, unreadable
}

// settingsDirs are the folders whose .claude settings apply to a place. A place that is
// there is a project (see projectDirs). A place that is not there yet, the worktree that
// is planned and not made, is only itself: the folders above it are read for the checkout
// it is cut from, which is in the list of folders beside it, and not for it. The worktree
// when it is made has its own .git, so projectDirs stops at it too, and what is read when
// the question is asked is what is read when the helper starts.
func settingsDirs(d string) []string {
	if d == "" {
		return nil
	}
	if _, err := os.Stat(d); err != nil {
		return []string{filepath.Clean(d)}
	}
	return projectDirs(d)
}

// absentErr reports whether an error from looking at a file says it is not there: it is
// missing, or a folder on the way to it is a file.
func absentErr(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR)
}

// parseSettingsEnv reads the env block of a Claude Code settings file; ok is false when
// the text cannot be read as JSON (a byte order mark, comments and trailing commas
// excepted).
func parseSettingsEnv(data []byte) (env map[string]string, ok bool) {
	// A file with nothing in it (one that was only touched) holds no settings.
	clean := cleanJSONC(decodeUTF16(data))
	if len(bytes.TrimSpace(clean)) == 0 {
		return map[string]string{}, true
	}
	// The top-level keys are read one by one, with their case: Go's decoder matches "env"
	// with "ENV" and "Env" and keeps the last of two keys, so a file could say a gateway and
	// then take it back. A key that is "env" in another case, or "env" twice, makes the file
	// unreadable, which is to say the company is not known.
	dec := json.NewDecoder(bytes.NewReader(clean))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil, false
	}
	var rawEnv json.RawMessage
	seen := false
	for dec.More() {
		kt, err := dec.Token()
		key, isStr := kt.(string)
		if err != nil || !isStr {
			return nil, false
		}
		var val json.RawMessage
		if dec.Decode(&val) != nil {
			return nil, false
		}
		if strings.EqualFold(key, "env") {
			if key != "env" || seen {
				return nil, false
			}
			seen, rawEnv = true, val
		}
	}
	if _, err := dec.Token(); err != nil {
		return nil, false
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, false
	}
	env = map[string]string{}
	// An env that is not an object (null, a list, a number) sets nothing.
	raw := bytes.TrimSpace(rawEnv)
	if len(raw) == 0 || raw[0] != '{' {
		return env, true
	}
	edec := json.NewDecoder(bytes.NewReader(raw))
	if _, err := edec.Token(); err != nil {
		return nil, false
	}
	for edec.More() {
		kt, err := edec.Token()
		k, isStr := kt.(string)
		if err != nil || !isStr {
			return nil, false
		}
		var v any
		if edec.Decode(&v) != nil {
			return nil, false
		}
		var text string
		switch x := v.(type) {
		case string:
			text = x
		case float64:
			text = strconv.FormatFloat(x, 'f', -1, 64)
		case bool:
			text = strconv.FormatBool(x)
		default:
			continue
		}
		// A name set twice keeps a value that says something: one that takes it back does not
		// hide it.
		if env[k] == "" {
			env[k] = text
		}
	}
	return env, true
}

// lookupFold is the value of a variable by name in a map, whatever the case of the name: Claude
// Code on Windows reads them that way, and over-detecting elsewhere is safe. A value that
// says something is preferred to one that is empty.
func lookupFold(m map[string]string, name string) string {
	if v := m[name]; v != "" {
		return v
	}
	for k, v := range m {
		if v != "" && strings.EqualFold(k, name) {
			return v
		}
	}
	return ""
}

// environFold is the environment variable called name in any case.
func environFold(name string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok && v != "" && strings.EqualFold(k, name) {
			return v
		}
	}
	return ""
}

// editorFile reports whether a name in a folder of drop-in files is what an editor leaves
// (a hidden file, a backup with ~, a swap or temporary file) and not a file of settings.
func editorFile(name string) bool {
	n := asciiLower(name)
	return strings.HasPrefix(n, ".") || strings.HasSuffix(n, "~") || strings.HasSuffix(n, ".swp") || strings.HasSuffix(n, ".tmp")
}

// cleanJSONC takes a byte order mark, // and /* */ comments and trailing commas out of
// JSON, as an editor lets a settings file have them and a JSON parser does not.
func cleanJSONC(data []byte) []byte {
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	out := make([]byte, 0, len(data))
	inStr := false
	for i := 0; i < len(data); i++ {
		c := data[i]
		switch {
		case inStr:
			out = append(out, c)
			if c == '\\' && i+1 < len(data) {
				i++
				out = append(out, data[i])
			} else if c == '"' {
				inStr = false
			}
		case c == '"':
			inStr = true
			out = append(out, c)
		case c == '/' && i+1 < len(data) && data[i+1] == '/':
			for i < len(data) && data[i] != '\n' {
				i++
			}
			if i < len(data) {
				out = append(out, '\n')
			}
		case c == '/' && i+1 < len(data) && data[i+1] == '*':
			i += 2
			for i+1 < len(data) && !(data[i] == '*' && data[i+1] == '/') {
				i++
			}
			i++
		case c == '}' || c == ']':
			// A comma before the closing bracket is dropped.
			j := len(out) - 1
			for j >= 0 && (out[j] == ' ' || out[j] == '\t' || out[j] == '\n' || out[j] == '\r') {
				j--
			}
			if j >= 0 && out[j] == ',' {
				out = append(out[:j], out[j+1:]...)
			}
			out = append(out, c)
		default:
			out = append(out, c)
		}
	}
	return out
}

// codexConfigOverride reads Codex's config.toml, from $CODEX_HOME or ~/.codex. A file that
// is not there has no override. One that is there and cannot be read, or is over 1 MB,
// is taken for an override, because it may hold one: the company is not known.
func codexConfigOverride() (why string, over bool) {
	dir := os.Getenv("CODEX_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", false
		}
		dir = filepath.Join(home, ".codex")
	}
	path := filepath.Join(dir, "config.toml")
	data, absent, why := readSettingsFile(path)
	if absent {
		return "", false
	}
	if why != "" {
		return "its Codex config.toml " + why, true
	}
	return codexOverrideFrom(string(data))
}

// codexOverrideFrom reads a Codex config.toml and works out where the active settings
// send what the agent is given. The file is parsed as TOML (see parseTOML), and one that
// cannot be parsed is an override, because it may hold one. The settings that are active
// are the top level ones, with the ones of the profile that "profile" selects on top.
// Not read, and said so in the help: what a command line gives Codex (-c, --profile,
// --oss) and what its environment holds beyond the variables gatewayOverride reads.
//
// A model_provider that is not "openai" (the one built-in id of this vendor) is another
// company, an oss_provider is a local or other model, and a base URL whose host is not
// OpenAI's is a gateway: openai_base_url, and the base_url of the table of the provider
// that is active. A base_url in any other table (an MCP server's) is not read.
func codexOverrideFrom(text string) (why string, over bool) {
	cfg, err := parseTOML(text)
	if err != nil {
		return "its Codex config.toml could not be read as TOML", true
	}
	active := map[string]any{}
	for k, v := range cfg {
		active[k] = v
	}
	if pv, ok := cfg["profile"]; ok {
		name, isStr := pv.(string)
		if !isStr {
			return "its Codex config.toml names a profile that is not a string", true
		}
		if name != "" {
			profiles, _ := cfg["profiles"].(map[string]any)
			prof, ok := profiles[name].(map[string]any)
			if !ok {
				return "its Codex config.toml selects the profile " + name + ", which it does not define", true
			}
			for k, v := range prof {
				active[k] = v
			}
		}
	}
	if v, ok := active["oss_provider"]; ok {
		if name, isStr := v.(string); !isStr || name != "" {
			return "through an open-source model provider", true
		}
	}
	provider := "openai"
	if v, ok := active["model_provider"]; ok {
		name, isStr := v.(string)
		if !isStr {
			return "its Codex config.toml has a model_provider that is not a string", true
		}
		if name != "" {
			provider = name
		}
	}
	if provider != "openai" {
		return "through a model provider other than OpenAI (" + provider + ")", true
	}
	if v, ok := active["openai_base_url"]; ok {
		if why, over := codexURLOverride(v); over {
			return why, true
		}
	}
	if tables, ok := cfg["model_providers"].(map[string]any); ok {
		if t, ok := tables[provider].(map[string]any); ok {
			if v, ok := t["base_url"]; ok {
				if why, over := codexURLOverride(v); over {
					return why, true
				}
			}
		}
	}
	return "", false
}

// codexURLOverride says whether a base URL value sends Codex somewhere other than
// OpenAI: a host that is not api.openai.com, a value that is not a string, or one that has
// no host to be read.
func codexURLOverride(v any) (why string, over bool) {
	u, isStr := v.(string)
	if !isStr {
		return "its Codex config.toml has a base URL that is not a string", true
	}
	if strings.TrimSpace(u) == "" {
		return "", false
	}
	h := HostOf(u)
	if h == "" {
		return "through a gateway or proxy", true
	}
	if !isOfficial(h, []string{"api.openai.com"}) {
		return "through a gateway or proxy (" + h + ")", true
	}
	return "", false
}
