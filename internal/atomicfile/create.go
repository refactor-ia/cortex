package atomicfile

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
)

// CreateIfAbsent durably creates a regular file beneath root only when the leaf
// is absent. It stages and syncs the data before publishing it with a hard link,
// which cannot overwrite a concurrently created path, then verifies the result.
// Filesystems that do not support hard links return an error: there is no portable
// fallback that preserves both atomic publication and create-if-absent semantics.
// After publication, a caller-level transaction owns rollback for any error.
// The final absence check immediately before Link is mandatory: Link alone cannot
// establish rooted-path evidence.
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

type rootedAbsentEvidence struct {
	parent   *os.Root
	basename string
	mode     fs.FileMode
}

type rootedAbsentEvidenceOperations struct {
	lstat    func(*os.Root, string) (fs.FileInfo, error)
	openRoot func(*os.Root, string) (*os.Root, error)
	close    func(*os.Root) error
}

// observeRootedAbsent captures a rooted parent whose named leaf was absent. The
// caller owns evidence.parent and must close it after its immediate pre-Link
// absence recheck.
func observeRootedAbsent(root *os.Root, relativePath string, mode fs.FileMode, operations rootedAbsentEvidenceOperations) (rootedAbsentEvidence, error) {
	if runtime.GOOS == "js" || runtime.GOOS == "plan9" {
		return rootedAbsentEvidence{}, errors.New("atomic rooted absent evidence: unsupported platform")
	}
	if root == nil || mode&^fs.FileMode(0o777) != 0 || !validRootedAbsentPath(relativePath) {
		return rootedAbsentEvidence{}, errors.New("atomic rooted absent evidence: invalid input")
	}
	info, err := rootedAbsentLstat(operations, root, ".")
	if err != nil || info == nil || info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() {
		return rootedAbsentEvidence{}, errors.New("atomic rooted absent evidence: invalid root")
	}
	parent, err := rootedAbsentOpenRoot(operations, root, ".")
	if err != nil || parent == nil {
		closeRootedAbsent(&parent, operations)
		return rootedAbsentEvidence{}, errors.New("atomic rooted absent evidence: open parent failed")
	}
	defer closeRootedAbsent(&parent, operations)

	parts := strings.Split(relativePath, "/")
	for _, component := range parts[:len(parts)-1] {
		info, err := rootedAbsentLstat(operations, parent, component)
		if err != nil || info == nil || info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() {
			return rootedAbsentEvidence{}, errors.New("atomic rooted absent evidence: parent is missing or invalid")
		}
		next, openErr := rootedAbsentOpenRoot(operations, parent, component)
		if openErr != nil || next == nil {
			closeRootedAbsent(&next, operations)
			return rootedAbsentEvidence{}, errors.New("atomic rooted absent evidence: open parent failed")
		}
		anchored, anchorErr := rootedAbsentLstat(operations, next, ".")
		if anchorErr != nil || anchored == nil || anchored.Mode() != info.Mode() || !os.SameFile(anchored, info) {
			closeRootedAbsent(&next, operations)
			return rootedAbsentEvidence{}, errors.New("atomic rooted absent evidence: parent drifted")
		}
		if closeErr := closeRootedAbsent(&parent, operations); closeErr != nil {
			closeRootedAbsent(&next, operations)
			return rootedAbsentEvidence{}, errors.New("atomic rooted absent evidence: parent close failed")
		}
		parent = next
	}
	info, err = rootedAbsentLstat(operations, parent, parts[len(parts)-1])
	if info != nil || !errors.Is(err, fs.ErrNotExist) {
		return rootedAbsentEvidence{}, errors.New("atomic rooted absent evidence: destination is not absent")
	}
	evidence := rootedAbsentEvidence{parent: parent, basename: parts[len(parts)-1], mode: mode}
	parent = nil
	return evidence, nil
}

func validRootedAbsentPath(relativePath string) bool {
	return relativePath != "" && !path.IsAbs(relativePath) && !strings.Contains(relativePath, `\`) && path.Clean(relativePath) == relativePath && relativePath != "." && relativePath != ".." && !strings.HasPrefix(relativePath, "../")
}

func rootedAbsentLstat(operations rootedAbsentEvidenceOperations, root *os.Root, name string) (fs.FileInfo, error) {
	if operations.lstat != nil {
		return operations.lstat(root, name)
	}
	return root.Lstat(name)
}

func rootedAbsentOpenRoot(operations rootedAbsentEvidenceOperations, root *os.Root, name string) (*os.Root, error) {
	if operations.openRoot != nil {
		return operations.openRoot(root, name)
	}
	return root.OpenRoot(name)
}

func closeRootedAbsent(root **os.Root, operations rootedAbsentEvidenceOperations) error {
	if *root == nil {
		return nil
	}
	owned := *root
	*root = nil
	if operations.close != nil {
		return operations.close(owned)
	}
	return owned.Close()
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
