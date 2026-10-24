//go:build !unix && !windows

package filetxn

import "os"

// directoryIdentity refuses on platforms without a directory identity source,
// so accepting or restarting a created directory fails closed there.
func directoryIdentity(string, os.FileInfo) (device, inode uint64, ok bool) {
	return 0, 0, false
}
