package server

// networkFS reports whether a Linux file system type number (statfs f_type) is a
// network file system: NFS, SMB and CIFS, AFS, Ceph, Coda and 9P. FUSE is not
// listed, since local file systems are mounted through it as often as remote ones.
// It is here, and not with the Linux code that uses it, so that it is tested
// wherever the tests run.
func networkFS(fsType int64) bool {
	switch uint32(fsType) {
	case 0x6969, // NFS
		0x517B,     // SMB
		0xFF534D42, // CIFS
		0xFE534D42, // SMB2
		0x5346414F, // AFS
		0x00C36400, // Ceph
		0x73757245, // Coda
		0x01021997: // 9P
		return true
	}
	return false
}
