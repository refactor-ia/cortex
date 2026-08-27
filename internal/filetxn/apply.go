package filetxn

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/refactor-ia/cortex/internal/atomicfile"
	"github.com/refactor-ia/cortex/internal/safepath"
)

// Write describes one source-root-relative file replacement.
type Write struct {
	Path string
	Data []byte
	Mode fs.FileMode
}

type applyDependencies struct {
	capture          func(string, string, string, []string) (Snapshot, error)
	verify           func(string, string) error
	replace          func(string, string, []byte, fs.FileMode) error
	replaceIfMatches func(string, string, []byte, fs.FileMode, []byte, fs.FileMode) error
	removeIfMatches  func(string, string, []byte) error
}

type operation struct {
	write Write
	path  string
}

// Apply captures one durable snapshot and applies all writes or rolls them back.
func Apply(sourceRoot, backupRoot, backupName string, writes []Write) (Snapshot, error) {
	return apply(defaultApplyDependencies(), sourceRoot, backupRoot, backupName, writes)
}

func defaultApplyDependencies() applyDependencies {
	return applyDependencies{
		capture: Capture, verify: Verify, replace: atomicfile.Replace,
		replaceIfMatches: atomicfile.ReplaceIfMatches, removeIfMatches: atomicfile.RemoveIfMatches,
	}
}

func apply(deps applyDependencies, sourceRoot, backupRoot, backupName string, writes []Write) (Snapshot, error) {
	operations, err := prepareOperations(sourceRoot, writes)
	if err != nil {
		return Snapshot{}, err
	}
	paths := make([]string, len(operations))
	for index, operation := range operations {
		paths[index] = operation.path
	}
	snapshot, err := deps.capture(sourceRoot, backupRoot, backupName, paths)
	if err != nil {
		return Snapshot{}, fmt.Errorf("apply snapshot: %w", err)
	}
	if err := deps.verify(backupRoot, backupName); err != nil {
		return snapshot, fmt.Errorf("apply verify snapshot: %w", err)
	}

	attempted := make([]operation, 0, len(operations))
	for _, operation := range operations {
		attempted = append(attempted, operation)
		if err := deps.replace(sourceRoot, operation.path, operation.write.Data, operation.write.Mode); err != nil {
			applyErr := fmt.Errorf("apply write %s: %w", operation.path, err)
			return snapshot, errors.Join(applyErr, rollback(deps, sourceRoot, backupRoot, backupName, snapshot, attempted))
		}
	}
	return snapshot, nil
}

func prepareOperations(sourceRoot string, writes []Write) ([]operation, error) {
	if len(writes) == 0 {
		return nil, fmt.Errorf("apply writes are empty")
	}
	operations := make([]operation, 0, len(writes))
	seen := make(map[string]struct{}, len(writes))
	for _, write := range writes {
		if write.Mode&^fs.FileMode(0o777) != 0 {
			return nil, fmt.Errorf("apply write has unsupported mode")
		}
		if _, err := safepath.Resolve(sourceRoot, write.Path); err != nil {
			return nil, fmt.Errorf("apply write path is unsafe: %w", err)
		}
		path := filepath.ToSlash(filepath.Clean(write.Path))
		if _, exists := seen[path]; exists {
			return nil, fmt.Errorf("apply write path is duplicated: %s", path)
		}
		seen[path] = struct{}{}
		operations = append(operations, operation{write: Write{Path: path, Data: append([]byte(nil), write.Data...), Mode: write.Mode}, path: path})
	}
	return operations, nil
}

func rollback(deps applyDependencies, sourceRoot, backupRoot, backupName string, snapshot Snapshot, attempted []operation) error {
	if err := deps.verify(backupRoot, backupName); err != nil {
		return fmt.Errorf("rollback failed; caller intervention required: verify snapshot: %w", err)
	}
	entries := make(map[string]Entry, len(snapshot.Manifest.Entries))
	payloads := make(map[string][]byte)
	for _, entry := range snapshot.Manifest.Entries {
		entries[entry.Path] = entry
	}
	for _, operation := range attempted {
		entry, exists := entries[operation.path]
		if !exists {
			return fmt.Errorf("rollback failed; caller intervention required: snapshot entry is missing")
		}
		if entry.Exists {
			payload, err := snapshotPayload(snapshot.Dir, entry.Path)
			if err != nil {
				return fmt.Errorf("rollback failed; caller intervention required: read snapshot payload: %w", err)
			}
			payloads[entry.Path] = payload
		}
	}

	var rollbackErr error
	for index := len(attempted) - 1; index >= 0; index-- {
		operation := attempted[index]
		entry := entries[operation.path]
		if entry.Exists {
			original := payloads[entry.Path]
			matches, err := targetMatches(sourceRoot, operation.path, original, fs.FileMode(entry.Mode))
			if err == nil && matches {
				continue
			}
			if err == nil {
				err = deps.replaceIfMatches(sourceRoot, operation.path, operation.write.Data, operation.write.Mode, original, fs.FileMode(entry.Mode))
			}
			if err != nil {
				rollbackErr = errors.Join(rollbackErr, fmt.Errorf("restore %s: %w", operation.path, err))
			}
			continue
		}
		if err := deps.removeIfMatches(sourceRoot, operation.path, operation.write.Data); err != nil {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("remove %s: %w", operation.path, err))
		}
	}
	if rollbackErr != nil {
		return fmt.Errorf("rollback failed; caller intervention required: %w", rollbackErr)
	}
	return nil
}

func snapshotPayload(snapshotDir, path string) ([]byte, error) {
	payload, err := safepath.Resolve(snapshotDir, filepath.Join("payloads", filepath.Base(backupPayloadPath(snapshotDir, path))))
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(payload)
	if err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("snapshot payload is missing or invalid")
	}
	return os.ReadFile(payload)
}

func targetMatches(root, path string, data []byte, mode fs.FileMode) (bool, error) {
	target, err := safepath.Resolve(root, path)
	if err != nil {
		return false, err
	}
	info, err := os.Lstat(target)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil || !info.Mode().IsRegular() {
		return false, fmt.Errorf("target is missing or invalid")
	}
	actual, err := os.ReadFile(target)
	if err != nil {
		return false, err
	}
	return bytes.Equal(actual, data) && info.Mode().Perm() == mode.Perm(), nil
}
