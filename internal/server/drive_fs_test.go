package server

import "testing"

func TestNetworkFileSystemTypes(t *testing.T) {
	for _, tc := range []struct {
		fsType int64
		want   bool
	}{
		{0x6969, true}, {0x517B, true}, {0xFF534D42, true}, {0xFE534D42, true}, {0x01021997, true},
		{0xEF53, false},     // ext4
		{0x58465342, false}, // xfs
		{0x9123683E, false}, // btrfs
		{0x65735546, false}, // fuse
	} {
		if got := networkFS(tc.fsType); got != tc.want {
			t.Errorf("networkFS(%#x) = %v, want %v", tc.fsType, got, tc.want)
		}
	}
}
