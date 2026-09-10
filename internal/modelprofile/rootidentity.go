package modelprofile

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type rootIdentity struct {
	Device uint64 `json:"device"`
	Inode  uint64 `json:"inode"`
}

func rootIdentityFor(root string) (rootIdentity, error) {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return rootIdentity{}, errors.New("model profile restore: invalid trusted runtime root")
	}
	current := filepath.VolumeName(root) + string(filepath.Separator)
	if info, err := os.Lstat(current); err != nil || !safeDirectory(info) {
		return rootIdentity{}, errors.New("model profile restore: invalid trusted runtime root")
	}
	for _, part := range strings.Split(strings.TrimPrefix(root, current), string(filepath.Separator)) {
		if part == "" {
			continue
		}
		current = filepath.Join(current, part)
		if info, err := os.Lstat(current); err != nil || !safeDirectory(info) {
			return rootIdentity{}, errors.New("model profile restore: invalid trusted runtime root")
		}
	}
	return rootStat(root)
}
func rootMatches(root string, expected rootIdentity) error {
	actual, err := rootIdentityFor(root)
	if err != nil || actual != expected {
		return errors.New("model profile restore: runtime root changed")
	}
	return nil
}
func safeDirectory(info fs.FileInfo) bool { return info.Mode()&os.ModeSymlink == 0 && info.IsDir() }
