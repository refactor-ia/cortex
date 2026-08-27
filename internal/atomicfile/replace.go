// Package atomicfile replaces one safe-root-anchored regular file at a time.
package atomicfile

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/refactor-ia/cortex/internal/safepath"
)

// Replace atomically replaces one regular file beneath root with data and mode.
// It revalidates the destination before rename but does not eliminate TOCTOU races.
// After rename, a caller-level transaction owns rollback for any reported error.
func Replace(root, relativePath string, data []byte, mode fs.FileMode) error {
	return replace(root, relativePath, data, mode, nil)
}

// ReplaceIfMatches atomically replaces an existing regular file only when its bytes
// and permission mode match expectedData and expectedMode. It rechecks the target
// immediately before rename to reduce, but not eliminate, TOCTOU exposure.
// After rename, a caller-level transaction owns rollback for any reported error.
func ReplaceIfMatches(root, relativePath string, expectedData []byte, expectedMode fs.FileMode, replacementData []byte, replacementMode fs.FileMode) error {
	if expectedMode&^fs.FileMode(0o777) != 0 {
		return fmt.Errorf("atomic replace: unsupported expected mode")
	}
	return replace(root, relativePath, replacementData, replacementMode, func(destination string) error {
		return destinationMatches(destination, expectedData, expectedMode)
	})
}

func replace(root, relativePath string, data []byte, mode fs.FileMode, matches func(string) error) (resultErr error) {
	if mode&^fs.FileMode(0o777) != 0 {
		return fmt.Errorf("atomic replace: unsupported mode")
	}

	destination, err := validateDestination(root, relativePath)
	if err != nil {
		return err
	}
	if matches != nil {
		if err := matches(destination); err != nil {
			return err
		}
	}
	parent := filepath.Dir(destination)
	temporary, err := os.CreateTemp(parent, ".cortex-replace-")
	if err != nil {
		return fmt.Errorf("atomic replace: create temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	temporaryOpen := true
	defer func() {
		if temporaryOpen {
			if err := temporary.Close(); err != nil {
				resultErr = errors.Join(resultErr, fmt.Errorf("atomic replace: close temporary file: %w", err))
			}
		}
		if temporaryPath != "" {
			if err := os.Remove(temporaryPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
				resultErr = errors.Join(resultErr, fmt.Errorf("atomic replace: remove temporary file: %w", err))
			}
		}
	}()

	if err := temporary.Chmod(mode.Perm()); err != nil {
		return fmt.Errorf("atomic replace: set temporary mode: %w", err)
	}
	written, err := temporary.Write(data)
	if err != nil {
		return fmt.Errorf("atomic replace: write temporary file: %w", err)
	}
	if written != len(data) {
		return fmt.Errorf("atomic replace: write temporary file: %w", io.ErrShortWrite)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("atomic replace: sync temporary file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		temporaryOpen = false
		return fmt.Errorf("atomic replace: close temporary file: %w", err)
	}
	temporaryOpen = false

	destination, err = validateDestination(root, relativePath)
	if err != nil {
		return err
	}
	if matches != nil {
		if err := matches(destination); err != nil {
			return err
		}
	}
	if err := os.Rename(temporaryPath, destination); err != nil {
		return fmt.Errorf("atomic replace: rename temporary file: %w", err)
	}
	temporaryPath = ""

	if err := syncDirectory(parent); err != nil {
		return err
	}
	if err := verifyReplacement(destination, data, mode.Perm()); err != nil {
		return err
	}
	return nil
}

func validateDestination(root, relativePath string) (string, error) {
	destination, err := safepath.Resolve(root, relativePath)
	if err != nil {
		return "", fmt.Errorf("atomic replace: destination is unsafe: %w", err)
	}
	parentInfo, err := os.Lstat(filepath.Dir(destination))
	if err != nil || !parentInfo.IsDir() || parentInfo.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("atomic replace: destination parent is missing or invalid")
	}
	info, err := os.Lstat(destination)
	if os.IsNotExist(err) {
		return destination, nil
	}
	if err != nil {
		return "", fmt.Errorf("atomic replace: inspect destination: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", fmt.Errorf("atomic replace: destination is not a regular file")
	}
	return destination, nil
}

func destinationMatches(destination string, expectedData []byte, expectedMode fs.FileMode) error {
	info, err := os.Lstat(destination)
	if os.IsNotExist(err) {
		return fmt.Errorf("atomic replace: destination is missing")
	}
	if err != nil {
		return fmt.Errorf("atomic replace: inspect destination: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("atomic replace: destination is not a regular file")
	}
	actual, err := os.ReadFile(destination)
	if err != nil {
		return fmt.Errorf("atomic replace: read destination: %w", err)
	}
	if !bytes.Equal(actual, expectedData) {
		return fmt.Errorf("atomic replace: destination bytes do not match")
	}
	if info.Mode().Perm() != expectedMode.Perm() {
		return fmt.Errorf("atomic replace: destination mode does not match")
	}
	return nil
}

func syncDirectory(path string) (resultErr error) {
	directory, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("atomic replace: open destination directory: %w", err)
	}
	defer func() {
		if err := directory.Close(); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("atomic replace: close destination directory: %w", err))
		}
	}()
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("atomic replace: sync destination directory: %w", err)
	}
	return nil
}

func verifyReplacement(destination string, data []byte, mode fs.FileMode) error {
	info, err := os.Lstat(destination)
	if err != nil {
		return fmt.Errorf("atomic replace: inspect replacement: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("atomic replace: replacement is not a regular file")
	}
	actual, err := os.ReadFile(destination)
	if err != nil {
		return fmt.Errorf("atomic replace: read replacement: %w", err)
	}
	if !bytes.Equal(actual, data) {
		return fmt.Errorf("atomic replace: replacement bytes do not match")
	}
	if info.Mode().Perm() != mode {
		return fmt.Errorf("atomic replace: replacement mode does not match")
	}
	return nil
}
