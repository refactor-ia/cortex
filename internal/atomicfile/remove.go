package atomicfile

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/refactor-ia/cortex/internal/safepath"
)

// RemoveIfMatches removes a regular file beneath root only when its bytes exactly
// match expected. A missing leaf succeeds without mutation. It revalidates the
// path and bytes before removal to reduce, but not eliminate, TOCTOU races.
func RemoveIfMatches(root, relativePath string, expected []byte) error {
	destination, exists, err := removalDestination(root, relativePath)
	if err != nil || !exists {
		return err
	}
	matches, err := removalMatches(destination, expected)
	if err != nil || !matches {
		return err
	}

	destination, exists, err = removalDestination(root, relativePath)
	if err != nil || !exists {
		return err
	}
	matches, err = removalMatches(destination, expected)
	if err != nil || !matches {
		return err
	}
	if err := os.Remove(destination); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("atomic remove: remove destination: %w", err)
	}
	if err := syncDirectory(filepath.Dir(destination)); err != nil {
		return fmt.Errorf("atomic remove: removal succeeded but directory sync or absence verification failed; caller intervention may be needed: %w", err)
	}
	info, err := os.Lstat(destination)
	if err == nil && info != nil {
		return fmt.Errorf("atomic remove: removal succeeded but directory sync or absence verification failed; caller intervention may be needed: destination remains")
	}
	if !os.IsNotExist(err) {
		return fmt.Errorf("atomic remove: removal succeeded but directory sync or absence verification failed; caller intervention may be needed: inspect destination: %w", err)
	}
	return nil
}

func removalDestination(root, relativePath string) (string, bool, error) {
	destination, err := safepath.Resolve(root, relativePath)
	if err != nil {
		return "", false, fmt.Errorf("atomic remove: destination is unsafe: %w", err)
	}
	parentInfo, err := os.Lstat(filepath.Dir(destination))
	if err != nil || !parentInfo.IsDir() || parentInfo.Mode()&os.ModeSymlink != 0 {
		return "", false, fmt.Errorf("atomic remove: destination parent is missing or invalid")
	}
	info, err := os.Lstat(destination)
	if os.IsNotExist(err) {
		return destination, false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("atomic remove: inspect destination: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", false, fmt.Errorf("atomic remove: destination is not a regular file")
	}
	return destination, true, nil
}

func removalMatches(destination string, expected []byte) (bool, error) {
	info, err := os.Lstat(destination)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("atomic remove: inspect destination: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return false, fmt.Errorf("atomic remove: destination is not a regular file")
	}
	actual, err := os.ReadFile(destination)
	if err != nil {
		return false, fmt.Errorf("atomic remove: read destination: %w", err)
	}
	if !bytes.Equal(actual, expected) {
		return false, fmt.Errorf("atomic remove: destination bytes do not match")
	}
	return true, nil
}
