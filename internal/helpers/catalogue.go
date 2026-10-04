package helpers

import (
	"fmt"
	"regexp"
	"runtime"
)

// Entry describes one helper. The catalogue is compiled into Flockdeck on
// purpose: everything that makes an install safe (the release URL, the keys it
// is checked against, the environment the helper is given) has to come from
// something Flockdeck already trusts, and no file a user or another program can
// write is that.
type Entry struct {
	// ID is the folder name under apps/ and the name on the command line.
	ID string
	// Name is what the dialog calls it.
	Name string
	// Summary is one sentence for the dialog.
	Summary string
	// Repo is "owner/name" on github.com. Releases are fetched from there and
	// nowhere else.
	Repo string
	// Binary is the executable's file name inside the archive's folder, without
	// the ".exe" Windows adds.
	Binary string
	// MinVersion is the oldest version that is ever installed, even when
	// signed. It is how a release found to be bad is shut out after the fact.
	MinVersion string
	// Args are the command line arguments, with "{port}" replaced by the port
	// Flockdeck chose.
	Args []string
	// Env is the environment the helper is given on top of the allowlisted
	// part of Flockdeck's own. Values may use "{port}", "{host}", "{data_dir}"
	// and "{allowed_hosts}".
	Env map[string]string
	// Banner must match the first line the helper prints. Its first capture
	// group is the port the helper says it is listening on, which has to be the
	// one Flockdeck chose. This is the check that what answers on that port is
	// the program Flockdeck started.
	Banner *regexp.Regexp
	// Ready is polled until it answers 200, then Health is polled every ten
	// seconds. Both are paths.
	Ready, Health string
	// UIPath is the path "Open" goes to.
	UIPath string
	// Allows are the lines shown before an install, under "what it may do".
	Allows []string
	// MaxArchive, MaxUnpacked and MaxFiles cap the download, what it unpacks to
	// and how many members it has. Zero takes the defaults.
	MaxArchive, MaxUnpacked int64
	MaxFiles                int
}

// Defaults for the caps an Entry leaves at zero.
const (
	defaultMaxArchive  = 256 << 20
	defaultMaxUnpacked = 1 << 30
	defaultMaxFiles    = 2000
)

// Sizes of the two files a release's signature covers, as the self-updater
// limits them.
const (
	maxChecksums = 1 << 20
	maxSignature = 1 << 10
)

func (e Entry) maxArchive() int64 {
	if e.MaxArchive > 0 {
		return e.MaxArchive
	}
	return defaultMaxArchive
}

func (e Entry) maxUnpacked() int64 {
	if e.MaxUnpacked > 0 {
		return e.MaxUnpacked
	}
	return defaultMaxUnpacked
}

func (e Entry) maxFiles() int {
	if e.MaxFiles > 0 {
		return e.MaxFiles
	}
	return defaultMaxFiles
}

// lens is the one helper there is. See https://github.com/Flockdeck/lens.
var lens = Entry{
	ID:         "lens",
	Name:       "lens",
	Summary:    "Reads agent session transcripts and shows what they did, in a page on your own machine.",
	Repo:       "Flockdeck/lens",
	Binary:     "lens",
	MinVersion: "0.1.0",
	Args:       []string{"serve", "--host", "127.0.0.1", "--port", "{port}"},
	Env: map[string]string{
		"PORT":          "{port}",
		"HOST":          "{host}",
		"DATA_DIR":      "{data_dir}",
		"ALLOWED_HOSTS": "{allowed_hosts}",
		// "-" asks the helper to log to stdout, which Flockdeck writes to its
		// own rotating log file.
		"LOG_FILE": "-",
	},
	Banner: regexp.MustCompile(`^lens \S+ at http://localhost:(\d{1,5})/$`),
	Ready:  "/readyz",
	Health: "/healthz",
	UIPath: "/",
	Allows: []string{
		"Runs as your user, with whatever access that gives it",
		"Listens on 127.0.0.1 only, on a port Flockdeck picks",
		"Reads and writes its own data folder",
		"May contact whatever AI service you configure in its own settings",
	},
}

// Catalogue is every helper Flockdeck knows. The slice is a copy.
func Catalogue() []Entry { return []Entry{lens} }

// Lookup finds a helper by id.
func Lookup(id string) (Entry, bool) {
	for _, e := range Catalogue() {
		if e.ID == id {
			return e, true
		}
	}
	return Entry{}, false
}

var idPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// validID guards every path built from an id.
func validID(id string) bool { return idPattern.MatchString(id) }

// archiveExt is the archive's extension for an operating system.
func archiveExt(goos string) string {
	if goos == "windows" {
		return ".zip"
	}
	return ".tar.gz"
}

// BinaryName is the executable's file name on goos.
func (e Entry) BinaryName(goos string) string {
	if goos == "windows" {
		return e.Binary + ".exe"
	}
	return e.Binary
}

// TopFolder is the folder an archive holds everything in: id_version_os_arch.
func (e Entry) TopFolder(version, goos, goarch string) string {
	return fmt.Sprintf("%s_%s_%s_%s", e.ID, version, goos, goarch)
}

// ArchiveName is the release asset for a version on a platform.
func (e Entry) ArchiveName(version, goos, goarch string) string {
	return e.TopFolder(version, goos, goarch) + archiveExt(goos)
}

// assetPattern finds this helper's archive for a platform in checksums.txt
// and reads the version out of the name. The version is what the signature
// covers, so it is taken from here and from nowhere else.
func (e Entry) assetPattern(goos, goarch string) *regexp.Regexp {
	return regexp.MustCompile("^" + regexp.QuoteMeta(e.ID+"_") + "(" + versionPattern + ")" +
		regexp.QuoteMeta("_"+goos+"_"+goarch+archiveExt(goos)) + "$")
}

// Platform is the operating system and architecture this build runs on.
func Platform() (goos, goarch string) { return runtime.GOOS, runtime.GOARCH }
