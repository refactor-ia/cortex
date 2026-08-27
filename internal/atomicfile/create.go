package atomicfile

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// CreateIfAbsent durably creates a regular file beneath root only when the leaf
// is absent. It stages and syncs the data before publishing it with a hard link,
// which cannot overwrite a concurrently created path, then verifies the result.
// Filesystems that do not support hard links return an error: there is no portable
// fallback that preserves both atomic publication and create-if-absent semantics.
// After publication, a caller-level transaction owns rollback for any error.
func CreateIfAbsent(root, relativePath string, data []byte, mode fs.FileMode) (resultErr error) {
	if mode&^fs.FileMode(0o777) != 0 {
		return fmt.Errorf("atomic create: unsupported mode")
	}
	destination, err := validateDestination(root, relativePath)
	if err != nil {
		return err
	}
	if err := destinationAbsent(destination); err != nil {
		return err
	}
	parent := filepath.Dir(destination)
	temporary, err := os.CreateTemp(parent, ".cortex-create-")
	if err != nil {
		return fmt.Errorf("atomic create: create temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	temporaryOpen := true
	defer func() {
		if temporaryOpen {
			if err := temporary.Close(); err != nil {
				resultErr = errors.Join(resultErr, fmt.Errorf("atomic create: close temporary file: %w", err))
			}
		}
		if temporaryPath != "" {
			if err := os.Remove(temporaryPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
				resultErr = errors.Join(resultErr, fmt.Errorf("atomic create: remove temporary file: %w", err))
			}
		}
	}()
	if err := temporary.Chmod(mode.Perm()); err != nil {
		return fmt.Errorf("atomic create: set temporary mode: %w", err)
	}
	written, err := temporary.Write(data)
	if err != nil {
		return fmt.Errorf("atomic create: write temporary file: %w", err)
	}
	if written != len(data) {
		return fmt.Errorf("atomic create: write temporary file: %w", io.ErrShortWrite)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("atomic create: sync temporary file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("atomic create: close temporary file: %w", err)
	}
	temporaryOpen = false

	destination, err = validateDestination(root, relativePath)
	if err != nil {
		return err
	}
	if err := destinationAbsent(destination); err != nil {
		return err
	}
	if err := os.Link(temporaryPath, destination); err != nil {
		if absentErr := destinationAbsent(destination); absentErr != nil {
			return absentErr
		}
		return fmt.Errorf("atomic create: link temporary file: %w", err)
	}
	if err := os.Remove(temporaryPath); err != nil {
		return fmt.Errorf("atomic create: remove temporary file: %w", err)
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

func destinationAbsent(destination string) error {
	info, err := os.Lstat(destination)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("atomic create: inspect destination: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("atomic create: destination is not a regular file")
	}
	return fmt.Errorf("atomic create: destination already exists")
}
