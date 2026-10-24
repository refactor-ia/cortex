//go:build unix

package filetxn

import (
	"os"
	"syscall"
)

// directoryIdentity returns the device and inode numbers of info. The path is
// unused on unix because the identity is already part of the Lstat result.
func directoryIdentity(_ string, info os.FileInfo) (device, inode uint64, ok bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Ino == 0 {
		return 0, 0, false
	}
	return uint64(stat.Dev), uint64(stat.Ino), true
}
