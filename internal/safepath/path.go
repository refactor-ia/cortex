// Package safepath resolves paths beneath an explicit filesystem root.
package safepath

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Resolve returns an absolute path for candidate anchored beneath root.
// It rejects symlinks in root and all existing candidate components. A missing
// leaf is allowed only when its parent directory exists without symlinks.
func Resolve(root, candidate string) (string, error) {
	if root == "" {
		return "", fmt.Errorf("safe path: root is empty")
	}
	if candidate == "" {
		return "", fmt.Errorf("safe path: candidate is empty")
	}
	if filepath.IsAbs(candidate) {
		return "", fmt.Errorf("safe path: candidate is absolute")
	}

	cleanCandidate := filepath.Clean(candidate)
	if cleanCandidate == "." {
		return "", fmt.Errorf("safe path: candidate must name a concrete path")
	}
	if cleanCandidate == ".." || strings.HasPrefix(cleanCandidate, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("safe path: candidate escapes root")
	}

	anchor, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("safe path: anchor root: %w", err)
	}
	rootInfo, err := os.Lstat(anchor)
	if err != nil {
		return "", fmt.Errorf("safe path: inspect root: %w", err)
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("safe path: root is a symlink")
	}
	if !rootInfo.IsDir() {
		return "", fmt.Errorf("safe path: root is not a directory")
	}

	current := anchor
	parts := strings.Split(cleanCandidate, string(filepath.Separator))
	for index, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			if index != len(parts)-1 {
				return "", fmt.Errorf("safe path: candidate parent does not exist")
			}
			return current, nil
		}
		if err != nil {
			return "", fmt.Errorf("safe path: inspect candidate component: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("safe path: candidate contains a symlink component")
		}
	}

	return current, nil
}
