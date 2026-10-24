//go:build windows

package filetxn

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// directoryIdentity returns the volume serial number and file index of the
// directory at path. Lstat results on Windows carry no file identity, so the
// directory is opened without following reparse points and the handle must
// still name the same object as info.
func directoryIdentity(path string, info os.FileInfo) (device, inode uint64, ok bool) {
	if !isRealDirectory(info) {
		return 0, 0, false
	}
	name, err := syscall.UTF16PtrFromString(extendedLengthPath(path))
	if err != nil {
		return 0, 0, false
	}
	handle, err := syscall.CreateFile(
		name,
		0,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
		nil,
		syscall.OPEN_EXISTING,
		syscall.FILE_FLAG_BACKUP_SEMANTICS|syscall.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		return 0, 0, false
	}
	file := os.NewFile(uintptr(handle), path)
	defer file.Close()
	var data syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(handle, &data); err != nil {
		return 0, 0, false
	}
	if data.FileAttributes&syscall.FILE_ATTRIBUTE_DIRECTORY == 0 || data.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return 0, 0, false
	}
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return 0, 0, false
	}
	// The file index is the full NTFS file ID. ReFS volumes (including Dev
	// Drives) use 128-bit IDs that this 64-bit index does not cover, so
	// uniqueness there is not guaranteed; the persisted identity stays
	// uint64 until the Windows semantics design decides otherwise (#389).
	index := uint64(data.FileIndexHigh)<<32 | uint64(data.FileIndexLow)
	if index == 0 {
		return 0, 0, false
	}
	return uint64(data.VolumeSerialNumber), index, true
}

// extendedLengthPath adds the \\?\ prefix to a long clean absolute path, as the
// os package does when the Go runtime cannot opt the process into long-path
// support (Windows builds before 10.0.15063). Prefixing every long path keeps
// the identity read independent of that opt-in and of the LongPathsEnabled
// registry value. Short, relative, unclean, already extended, or device
// (\\.\) paths are returned unchanged; os also leaves device paths alone,
// because the prefix would change their meaning.
func extendedLengthPath(path string) string {
	const maxShortDirectory = 248 // MAX_PATH minus room for an 8.3 name, as in os
	if len(path) < maxShortDirectory || !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.HasPrefix(path, `\\?\`) || strings.HasPrefix(path, `\\.\`) {
		return path
	}
	if strings.HasPrefix(path, `\\`) {
		return `\\?\UNC\` + path[2:]
	}
	return `\\?\` + path
}
