// Package artifacts is the security core of the planned read-only artifacts
// viewer for a remote window: how a file on this machine is turned, safely,
// into something a remote device may be shown. See DESIGN-remote-artifacts.md.
//
// It is wired into nothing. No server, socket, preference or window uses it
// yet, so it changes nothing a user can see. It exists first, and alone, so
// that the rules every later slice depends on can be read, attacked and fuzzed
// without any transport around them.
//
// # What it guarantees
//
// A remote client never names a path. The host discovers candidate paths
// itself (later slices), hands the client an opaque id (Registry), and on every
// request opens the file again with Root.Open, which either returns a handle
// that is certainly a regular, unlinked file inside the root, or refuses with
// ErrUnavailable and no reason a client could use to probe the disk.
//
// Root.Open refuses, in this order:
//   - a path that is lexically unsafe: control characters, a Windows
//     alternate data stream, a trailing dot or space, an 8.3 short name, a UNC
//     or extended-length prefix, and on Windows only a reserved device name such
//     as CON or COM1 (checkLexical);
//   - a path outside the root, including by "..", by an absolute path, and by
//     any link (os.Root refuses escapes and races; this package also refuses
//     every symbolic link, junction and other reparse point, even one that
//     stays inside the root, because a link inside the root can point at a
//     secret that is also inside it);
//   - a secret by name in ANY component of the path, file or folder
//     (internal/secretname: dotenv and key files, credential and state files,
//     .git, .ssh, .aws, secrets/, private/ and more; a superset of what
//     internal/review.SecretPath treats as secret, including the argv forms
//     with an "=", and more: that function is an argument matcher and misses
//     names such as "-prod.pem" and "key.pem="). Case is folded the way file
//     systems fold it, so ".ſsh" (a long s) is ".ssh";
//   - on Unix, a component that is not ASCII and is not, byte for byte, an entry
//     of its folder: a file system that ignores case or composition (APFS, ext4
//     casefold, vfat, SMB) resolves such a spelling to another entry (the
//     name check cannot know which), so it is refused (ReasonAlias);
//   - on Unix, a folder inside the root on another device (a submount; a bind
//     mount of the same file system has the same device and is NOT seen, and
//     needs privilege to make);
//   - anything that is not a regular file, with exactly one name (a hardlink
//     out of the root cannot be seen by path, so a file with more than one
//     link is refused);
//   - on Windows, a file whose real long name, read back from the open handle,
//     is outside the root or is denied.
//
// A path longer than MaxCandidateBytes, or with more than MaxComponents parts, is
// refused unread.
//
// The checks that depend on the file's identity are made on the handle that
// was opened, after it was opened, so a path swapped for a link between the
// check and the read is caught rather than followed.
//
// # Roots
//
// NewRoot refuses a root that is empty or relative, a filesystem or drive root,
// a home directory (the user's or anyone's: /home/x, /Users/x, C:/Users/x,
// /mnt/c/Users/x, judged by where it is as well as by HOME) and any folder that
// holds homes or is above one, a folder that keeps secrets (~/.ssh, ~/.aws,
// ~/.gnupg, ~/.kube, ~/.config/gh, browser profiles ... and anything below
// them), a folder that holds every program's settings (~/.config, AppData,
// XDG directories, ~/Documents, also as redirected into OneDrive), a system or
// program folder, and a place with a folder named as a secret on the way
// (/mnt/backup/.ssh/a, /run/secrets; the plain words private and secrets count
// only for the root itself). It follows links in the root's own path, so the
// root must come from a path the host chose, never one a client, an agent or a
// file's contents could influence.
//
// It fails closed: unless a home directory is found that exists (from HOME,
// USERPROFILE, the parents of APPDATA, the password file entry of the process's
// user, or the system's own profile answer on Windows) and, where the platform
// says, is owned by the process's user, every root is refused. A project
// of someone else's, or below a home this process does not know as its own, is
// refused too.
//
// # Reading
//
// File.Limited serves at most MaxViewBytes in all. A File expects one reader:
// readers share a position, so concurrent ones interleave ranges. A read or
// close error is never the system's (which names an absolute path): it is the
// generic refusal, and CauseOf has the detail for a log.
//
// # What a name check cannot do
//
// A secret copied or saved under an unremarkable name ("notes.txt",
// "config.json"), or written inside a file that is not itself a secret, is not
// caught, and nothing in this package can catch it. Whatever turns the viewer
// on must say so to the person, and must not present the name check as a
// classifier.
//
// # What it deliberately does not do
//
// It never writes, deletes, renames or creates anything, and never dials the
// network or starts a process; a test holds it to that by what it and
// everything it imports can reach. It does not list directories, discover files,
// sniff types, redact or clip content, render anything, speak any protocol,
// read preferences, or keep an audit log. Those are later slices.
package artifacts
