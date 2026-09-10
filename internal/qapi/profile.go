package qapi

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/refactor-ia/cortex/internal/qaadmission"
	"github.com/refactor-ia/cortex/internal/qaroute"
)

const profileDirectory = "cortex"
const profileFilename = "qa-profiles.json"

type profileSource struct {
	root, directory, file os.FileInfo
	path                  string
}

// resolveProfileRoute reads one fixed profile snapshot only for an explicit profile.
func resolveProfileRoute(root string, request AdmissionRequest) (qaroute.ResolvedRoute, qaroute.Failure) {
	routeRequest := qaroute.Request{
		Role: request.Role, Backend: request.Backend, ProfileID: request.Profile, Override: request.Override,
	}
	if request.Profile == "" {
		return qaroute.Resolve(routeRequest, qaroute.Snapshot{})
	}

	source, ok := inspectProfileSource(root)
	if !ok {
		return qaroute.ResolvedRoute{}, qaroute.Failure{Code: "profile_invalid"}
	}
	bytes, ok := source.read()
	if !ok {
		return qaroute.ResolvedRoute{}, qaroute.Failure{Code: "profile_invalid"}
	}
	return qaroute.Resolve(routeRequest, qaroute.Snapshot{Present: true, Bytes: bytes})
}

func inspectProfileSource(root string) (profileSource, bool) {
	if !canonicalProfileRoot(root) {
		return profileSource{}, false
	}
	rootInfo, err := os.Lstat(root)
	if err != nil || !profileDirectoryInfo(rootInfo) {
		return profileSource{}, false
	}
	directory := filepath.Join(root, profileDirectory)
	directoryInfo, err := os.Lstat(directory)
	if err != nil || !profileDirectoryInfo(directoryInfo) {
		return profileSource{}, false
	}
	path := filepath.Join(directory, profileFilename)
	fileInfo, err := os.Lstat(path)
	if err != nil || !profileFileInfo(fileInfo) {
		return profileSource{}, false
	}
	return profileSource{root: rootInfo, directory: directoryInfo, file: fileInfo, path: path}, true
}

func canonicalProfileRoot(root string) bool {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return false
	}
	volume := filepath.VolumeName(root)
	current := volume + string(filepath.Separator)
	if volume == "" {
		current = string(filepath.Separator)
	}
	info, err := os.Lstat(current)
	if err != nil || !profileDirectoryInfo(info) {
		return false
	}
	for _, part := range strings.Split(strings.TrimPrefix(root, current), string(filepath.Separator)) {
		if part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, err = os.Lstat(current)
		if err != nil || !profileDirectoryInfo(info) {
			return false
		}
	}
	return true
}

func (source profileSource) read() ([]byte, bool) {
	// Nonblocking open prevents a regular-file-to-FIFO swap from waiting forever.
	file, err := os.OpenFile(source.path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, false
	}
	opened, statErr := file.Stat()
	if statErr != nil || !sameProfileInfo(source.file, opened) {
		_ = file.Close()
		return nil, false
	}
	data, readErr := io.ReadAll(io.LimitReader(file, qaadmission.MaxProfileSnapshotBytes+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || len(data) > qaadmission.MaxProfileSnapshotBytes || int64(len(data)) != source.file.Size() || !source.unchanged() {
		return nil, false
	}
	return data, true
}

func (source profileSource) unchanged() bool {
	root, rootErr := os.Lstat(filepath.Dir(filepath.Dir(source.path)))
	directory, directoryErr := os.Lstat(filepath.Dir(source.path))
	file, fileErr := os.Lstat(source.path)
	return rootErr == nil && directoryErr == nil && fileErr == nil && sameProfileInfo(source.root, root) && sameProfileInfo(source.directory, directory) && sameProfileInfo(source.file, file)
}

func profileDirectoryInfo(info os.FileInfo) bool {
	return info.Mode()&os.ModeSymlink == 0 && info.IsDir()
}

func profileFileInfo(info os.FileInfo) bool {
	return info.Mode()&os.ModeSymlink == 0 && info.Mode().IsRegular() && info.Size() >= 0 && info.Size() <= qaadmission.MaxProfileSnapshotBytes
}

func sameProfileInfo(before, after os.FileInfo) bool {
	return profileDirectoryOrFileInfo(after) && before.Mode() == after.Mode() && before.Size() == after.Size() && os.SameFile(before, after)
}

func profileDirectoryOrFileInfo(info os.FileInfo) bool {
	return profileDirectoryInfo(info) || profileFileInfo(info)
}
