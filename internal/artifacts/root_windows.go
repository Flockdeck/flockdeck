package artifacts

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

var procGetFinalPathNameByHandleW = syscall.NewLazyDLL("kernel32.dll").NewProc("GetFinalPathNameByHandleW")

const openFlags = 0

// canonical is where dir really is. filepath.EvalSymlinks follows symbolic
// links but not junctions, so the system's own name for an open handle is
// asked instead: it has been through every kind of link there is, and it
// spells a folder with its long names, which a short 8.3 name in a temporary
// folder's path would otherwise not match.
func canonical(dir string) (string, error) {
	name, err := syscall.UTF16PtrFromString(dir)
	if err != nil {
		return "", err
	}
	h, err := syscall.CreateFile(name, 0,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
		nil, syscall.OPEN_EXISTING, syscall.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return "", err
	}
	defer syscall.CloseHandle(h)
	return finalPathOf(h)
}

// finalPathOf is the system's name for an open handle, without the \\?\ prefix.
func finalPathOf(h syscall.Handle) (string, error) {
	buf := make([]uint16, syscall.MAX_PATH)
	for {
		n, _, callErr := procGetFinalPathNameByHandleW.Call(uintptr(h), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), 0)
		if n == 0 {
			return "", callErr
		}
		if int(n) < len(buf) {
			break
		}
		buf = make([]uint16, n) // n is the size it needs, terminator included
	}
	s := syscall.UTF16ToString(buf)
	switch {
	case strings.HasPrefix(s, `\\?\UNC\`):
		s = `\\` + s[len(`\\?\UNC\`):]
	case strings.HasPrefix(s, `\\?\`):
		s = s[len(`\\?\`):]
	}
	return filepath.Clean(s), nil
}

// devOf has no device to report: a mount point is a reparse point here, which
// walk refuses as one.
func devOf(fs.FileInfo) (uint64, bool) { return 0, false }

// linkCount is how many names the open file has.
func linkCount(f *os.File) (uint64, error) {
	var d syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(syscall.Handle(f.Fd()), &d); err != nil {
		return 0, err
	}
	return uint64(d.NumberOfLinks), nil
}

// checkRealName asks the system what the open file is really called and holds
// that to the same rules as the name it was asked for by. An 8.3 alias, a
// trailing dot, a case the file system folds, or any link not seen by walk
// would show here as another name: the path is refused unless the real name is
// inside the root, is the same name (ignoring case), and is not denied.
func (r *Root) checkRealName(f *os.File, rel string) error {
	real, err := finalPathOf(syscall.Handle(f.Fd()))
	if err != nil {
		return refuse(ReasonMissing)
	}
	got, ok := under(r.path, real)
	if !ok {
		return refuse(ReasonOutside)
	}
	if denied(got) {
		return refuse(ReasonDenied)
	}
	if !strings.EqualFold(got, rel) {
		return refuse(ReasonChanged)
	}
	return nil
}

// osProfileDir is the user's profile folder as the system says, whatever the
// environment says.
func osProfileDir() string {
	t, err := syscall.OpenCurrentProcessToken()
	if err != nil {
		return ""
	}
	defer t.Close()
	d, err := t.GetUserProfileDirectory()
	if err != nil {
		return ""
	}
	return d
}

var (
	procGetNamedSecurityInfoW      = syscall.NewLazyDLL("advapi32.dll").NewProc("GetNamedSecurityInfoW")
	procGetSystemWindowsDirectoryW = syscall.NewLazyDLL("kernel32.dll").NewProc("GetSystemWindowsDirectoryW")
)

// administrators is the SID of the Administrators group, which owns what an
// elevated process makes.
const administrators = "S-1-5-32-544"

// ownedBy is whether the folder at path is owned by the process's user (or by
// the Administrators group, which owns what an elevated process creates). A
// HOME or APPDATA that names another user's profile (C:\Users\bob) is therefore
// not this user's home. If the user's or the owner's SID cannot be read, it is
// not: a home that cannot be shown to be the user's is no home.
func ownedBy(path string, _ fs.FileInfo, _ int) bool {
	tok, err := syscall.OpenCurrentProcessToken()
	if err != nil {
		return false
	}
	defer tok.Close()
	tu, err := tok.GetTokenUser()
	if err != nil {
		return false
	}
	me, err := tu.User.Sid.String()
	if err != nil {
		return false
	}
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return false
	}
	var owner *syscall.SID
	var sd uintptr
	const seFileObject, ownerSecurityInformation = 1, 1
	r, _, _ := procGetNamedSecurityInfoW.Call(uintptr(unsafe.Pointer(p)), seFileObject, ownerSecurityInformation,
		uintptr(unsafe.Pointer(&owner)), 0, 0, 0, uintptr(unsafe.Pointer(&sd)))
	if r != 0 || owner == nil {
		return false
	}
	defer syscall.LocalFree(syscall.Handle(sd))
	got, err := owner.String()
	if err != nil {
		return false
	}
	return got == me || got == administrators
}

// osSystemDirs are the Windows folder as the system says, and the program and
// data folders beside it by their usual names on its drive, for when the
// environment names none of them.
func osSystemDirs() []string {
	buf := make([]uint16, 512)
	n, _, _ := procGetSystemWindowsDirectoryW.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n == 0 || int(n) >= len(buf) {
		return nil
	}
	win := syscall.UTF16ToString(buf[:n])
	drive := filepath.VolumeName(win) + `\`
	return []string{win, drive + "Windows", drive + "Program Files", drive + "Program Files (x86)", drive + "ProgramData"}
}
