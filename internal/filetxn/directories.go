package filetxn

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
)

// Directory is a canonical source-root-relative directory in parent-first order.
type Directory struct {
	Path string
	Mode fs.FileMode
}
type preparedDirectory struct {
	path, target string
	mode         fs.FileMode
}

func ApplyOperationsWithDirectories(sourceRoot, backupRoot, backupName string, directories []Directory, operations []Operation) (Snapshot, error) {
	return applyOperationsWithDirectories(defaultApplyDependencies(), sourceRoot, backupRoot, backupName, directories, operations)
}
func applyOperationsWithDirectories(deps applyDependencies, sourceRoot, backupRoot, backupName string, rawDirectories []Directory, rawOperations []Operation) (Snapshot, error) {
	directories, err := prepareDirectories(sourceRoot, rawDirectories)
	if err != nil {
		return Snapshot{}, err
	}
	operations, err := prepareOperationsAllowingMissingParents(sourceRoot, rawOperations)
	if err != nil {
		return Snapshot{}, err
	}
	for _, operation := range operations {
		for _, directory := range directories {
			if operation.path == directory.path {
				return Snapshot{}, fmt.Errorf("apply operation conflicts with directory: %s", operation.path)
			}
		}
	}
	created, err := createDirectories(directories)
	if err != nil {
		return Snapshot{}, errors.Join(err, rollbackDirectories(created))
	}
	snapshot, err := applyOperations(deps, sourceRoot, backupRoot, backupName, rawOperations)
	if err != nil {
		return snapshot, errors.Join(err, rollbackDirectories(created))
	}
	return snapshot, nil
}
func prepareDirectories(sourceRoot string, raw []Directory) ([]preparedDirectory, error) {
	root, err := inspectRoot(sourceRoot)
	if err != nil {
		return nil, err
	}
	prepared := make([]preparedDirectory, len(raw))
	indexes := make(map[string]int, len(raw))
	for index, directory := range raw {
		if directory.Mode&^fs.FileMode(0o777) != 0 {
			return nil, fmt.Errorf("apply directory has unsupported mode")
		}
		canonical, err := canonicalDirectoryPath(directory.Path)
		if err != nil {
			return nil, fmt.Errorf("apply directory path is unsafe: %w", err)
		}
		if _, exists := indexes[canonical]; exists {
			return nil, fmt.Errorf("apply directory path is duplicated: %s", canonical)
		}
		indexes[canonical] = index
		prepared[index] = preparedDirectory{path: canonical, target: filepath.Join(root, filepath.FromSlash(canonical)), mode: directory.Mode}
	}
	for index, directory := range prepared {
		parent := path.Dir(directory.path)
		for ancestor := parent; ancestor != "."; ancestor = path.Dir(ancestor) {
			if ancestorIndex, declared := indexes[ancestor]; declared && ancestorIndex > index {
				return nil, fmt.Errorf("apply directory parent is out of order: %s", directory.path)
			}
		}
		if _, declared := indexes[parent]; parent != "." && !declared {
			info, err := os.Lstat(filepath.Dir(directory.target))
			if os.IsNotExist(err) {
				return nil, fmt.Errorf("apply directory parent is missing: %s", directory.path)
			}
			if err != nil || !isRealDirectory(info) {
				return nil, fmt.Errorf("apply directory parent is invalid: %s", directory.path)
			}
		}
		if err := inspectPath(root, directory.path, true); err != nil {
			return nil, err
		}
	}
	return prepared, nil
}
func createDirectories(directories []preparedDirectory) ([]preparedDirectory, error) {
	created := make([]preparedDirectory, 0, len(directories))
	for _, directory := range directories {
		info, err := os.Lstat(directory.target)
		switch {
		case err == nil:
			if !isRealDirectory(info) {
				return created, fmt.Errorf("create directory is invalid: %s", directory.path)
			}
			continue
		case !os.IsNotExist(err):
			return created, fmt.Errorf("create directory inspection failed: %s", directory.path)
		}
		parentInfo, err := os.Lstat(filepath.Dir(directory.target))
		if err != nil || !isRealDirectory(parentInfo) {
			return created, fmt.Errorf("create directory parent is invalid: %s", directory.path)
		}
		if err := os.Mkdir(directory.target, directory.mode.Perm()); err != nil {
			if !os.IsExist(err) {
				return created, fmt.Errorf("create directory failed: %s", directory.path)
			}
			info, inspectErr := os.Lstat(directory.target)
			if inspectErr != nil || !isRealDirectory(info) {
				return created, fmt.Errorf("create directory conflict: %s", directory.path)
			}
			continue
		}
		created = append(created, directory)
		if err := os.Chmod(directory.target, directory.mode.Perm()); err != nil {
			return created, fmt.Errorf("set directory mode failed: %s", directory.path)
		}
		if err := syncDirectory(filepath.Dir(directory.target)); err != nil {
			return created, fmt.Errorf("sync directory parent failed: %s", directory.path)
		}
	}
	return created, nil
}
func rollbackDirectories(created []preparedDirectory) error {
	var rollbackErr error
	for index := len(created) - 1; index >= 0; index-- {
		directory := created[index]
		info, err := os.Lstat(directory.target)
		if err != nil || !isRealDirectory(info) {
			if !os.IsNotExist(err) {
				rollbackErr = errors.Join(rollbackErr, fmt.Errorf("rollback directory changed: %s", directory.path))
			}
			continue
		}
		if err := os.Remove(directory.target); err != nil {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("rollback directory remains: %s", directory.path))
			continue
		}
		if err := syncDirectory(filepath.Dir(directory.target)); err != nil {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("rollback directory parent sync failed: %s", directory.path))
		}
	}
	if rollbackErr != nil {
		return fmt.Errorf("rollback failed; caller intervention required: %w", rollbackErr)
	}
	return nil
}
func prepareOperationsAllowingMissingParents(sourceRoot string, raw []Operation) ([]operation, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("apply operations are empty")
	}
	if _, err := inspectRoot(sourceRoot); err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(raw))
	prepared := make([]operation, 0, len(raw))
	for _, rawOperation := range raw {
		actions, candidate := 0, ""
		for _, present := range []bool{rawOperation.Write != nil, rawOperation.Create != nil, rawOperation.Replace != nil, rawOperation.Remove != nil} {
			if present {
				actions++
			}
		}
		switch {
		case rawOperation.Write != nil:
			candidate = rawOperation.Write.Path
			if rawOperation.Write.Mode&^fs.FileMode(0o777) != 0 {
				return nil, fmt.Errorf("apply write has unsupported mode")
			}
		case rawOperation.Create != nil:
			candidate = rawOperation.Create.Path
			if rawOperation.Create.Mode&^fs.FileMode(0o777) != 0 {
				return nil, fmt.Errorf("apply create has unsupported mode")
			}
		case rawOperation.Replace != nil:
			candidate = rawOperation.Replace.Path
			if rawOperation.Replace.ExpectedData == nil || rawOperation.Replace.ExpectedMode&^fs.FileMode(0o777) != 0 || rawOperation.Replace.Mode&^fs.FileMode(0o777) != 0 {
				return nil, fmt.Errorf("apply replace has missing or unsupported evidence")
			}
		case rawOperation.Remove != nil:
			candidate = rawOperation.Remove.Path
			if rawOperation.Remove.ExpectedMode&^fs.FileMode(0o777) != 0 {
				return nil, fmt.Errorf("apply remove has unsupported expected mode")
			}
		}
		if actions != 1 {
			return nil, fmt.Errorf("apply operation must specify exactly one action")
		}
		candidate = filepath.ToSlash(filepath.Clean(candidate))
		if _, exists := seen[candidate]; exists {
			return nil, fmt.Errorf("apply operation path is duplicated: %s", candidate)
		}
		seen[candidate] = struct{}{}
		if err := inspectPath(sourceRoot, candidate, false); err != nil {
			return nil, fmt.Errorf("apply operation path is unsafe: %w", err)
		}
		prepared = append(prepared, operation{path: candidate})
	}
	return prepared, nil
}
func canonicalDirectoryPath(raw string) (string, error) {
	clean := path.Clean(raw)
	if raw == "" || filepath.IsAbs(raw) || strings.Contains(raw, "\\") || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || clean != raw {
		return "", errors.New("path is not canonical relative")
	}
	return clean, nil
}
func inspectRoot(root string) (string, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", errors.New("trusted root is invalid")
	}
	info, err := os.Lstat(absolute)
	if err != nil || !isRealDirectory(info) {
		return "", errors.New("trusted root is missing or invalid")
	}
	return absolute, nil
}
func inspectPath(root, relative string, directory bool) error {
	absolute, err := inspectRoot(root)
	if err != nil {
		return err
	}
	if relative == "" || filepath.IsAbs(relative) {
		return errors.New("path is empty or absolute")
	}
	clean := filepath.Clean(relative)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return errors.New("path escapes root")
	}
	current := absolute
	parts := strings.Split(filepath.FromSlash(clean), string(filepath.Separator))
	for index, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("path contains an invalid component")
		}
		if index < len(parts)-1 && !info.IsDir() {
			return errors.New("path parent is not a directory")
		}
		if index == len(parts)-1 && ((directory && !info.IsDir()) || (!directory && !info.Mode().IsRegular())) {
			return errors.New("path target is invalid")
		}
	}
	return nil
}
func isRealDirectory(info os.FileInfo) bool {
	return info != nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0
}
func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil && !errors.Is(err, syscall.EINVAL) && !errors.Is(err, syscall.ENOTSUP) {
		return err
	}
	return nil
}
