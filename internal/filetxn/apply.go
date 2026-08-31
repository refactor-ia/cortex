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

// Create describes one source-root-relative file creation that must not overwrite
// an existing target.
type Create struct {
	Path string
	Data []byte
	Mode fs.FileMode
}

// Replace describes one source-root-relative replacement. ExpectedData and
// ExpectedMode are the exact evidence that must match the snapshot and target.
type Replace struct {
	Path         string
	ExpectedData []byte
	ExpectedMode fs.FileMode
	Data         []byte
	Mode         fs.FileMode
}

// Remove describes one authorized source-root-relative file removal. ExpectedData
// and ExpectedMode are the exact evidence that must match the snapshot and target.
type Remove struct {
	Path         string
	ExpectedData []byte
	ExpectedMode fs.FileMode
}

// Operation describes exactly one write, create, replace, or removal.
type Operation struct {
	Write   *Write
	Create  *Create
	Replace *Replace
	Remove  *Remove
}

type applyDependencies struct {
	capture          func(string, string, string, []string) (Snapshot, error)
	verify           func(string, string) error
	replace          func(string, string, []byte, fs.FileMode) error
	createIfAbsent   func(string, string, []byte, fs.FileMode) error
	replaceIfMatches func(string, string, []byte, fs.FileMode, []byte, fs.FileMode) error
	restoreIfAbsent  func(string, string, []byte, fs.FileMode) error
	removeIfMatches  func(string, string, []byte) error
	finalVerify      func() error
}

type operation struct {
	write   *Write
	create  *Create
	replace *Replace
	remove  *Remove
	path    string
}

// Apply captures one durable snapshot and applies all writes or rolls them back.
func Apply(sourceRoot, backupRoot, backupName string, writes []Write) (Snapshot, error) {
	return apply(defaultApplyDependencies(), sourceRoot, backupRoot, backupName, writes)
}

// ApplyOperations captures one durable snapshot and applies ordered writes,
// creates, replacements, and removals or rolls completed operations back in reverse order.
func ApplyOperations(sourceRoot, backupRoot, backupName string, operations []Operation) (Snapshot, error) {
	return applyOperations(defaultApplyDependencies(), sourceRoot, backupRoot, backupName, operations)
}

// ApplyOperationsWithDirectoriesAndVerify runs finalVerify after every operation
// succeeds and before the transaction is accepted. A verification failure rolls
// the completed operations back with the existing transaction snapshot.
func ApplyOperationsWithDirectoriesAndVerify(sourceRoot, backupRoot, backupName string, directories []Directory, operations []Operation, finalVerify func() error) (Snapshot, error) {
	if finalVerify == nil {
		return Snapshot{}, errors.New("apply final verification is required")
	}
	deps := defaultApplyDependencies()
	deps.finalVerify = finalVerify
	return applyOperationsWithDirectories(deps, sourceRoot, backupRoot, backupName, directories, operations)
}

func defaultApplyDependencies() applyDependencies {
	return applyDependencies{
		capture: Capture, verify: Verify, replace: atomicfile.Replace,
		createIfAbsent: atomicfile.CreateIfAbsent, replaceIfMatches: atomicfile.ReplaceIfMatches, restoreIfAbsent: restoreIfAbsent,
		removeIfMatches: atomicfile.RemoveIfMatches,
	}
}

func apply(deps applyDependencies, sourceRoot, backupRoot, backupName string, writes []Write) (Snapshot, error) {
	operations := make([]Operation, len(writes))
	for index := range writes {
		write := writes[index]
		operations[index] = Operation{Write: &write}
	}
	return applyOperations(deps, sourceRoot, backupRoot, backupName, operations)
}

func applyOperationsWithFinalVerification(deps applyDependencies, sourceRoot, backupRoot, backupName string, raw []Operation, finalVerify func() error) (Snapshot, error) {
	deps.finalVerify = finalVerify
	return applyOperations(deps, sourceRoot, backupRoot, backupName, raw)
}

func applyOperations(deps applyDependencies, sourceRoot, backupRoot, backupName string, raw []Operation) (Snapshot, error) {
	operations, err := prepareOperations(sourceRoot, raw)
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
	if err := verifyOperationEvidence(snapshot, operations); err != nil {
		return snapshot, err
	}

	attempted := make([]operation, 0, len(operations))
	for _, operation := range operations {
		attempted = append(attempted, operation)
		var err error
		kind := "write"
		switch {
		case operation.write != nil:
			err = deps.replace(sourceRoot, operation.path, operation.write.Data, operation.write.Mode)
		case operation.create != nil:
			kind = "create"
			err = deps.createIfAbsent(sourceRoot, operation.path, operation.create.Data, operation.create.Mode)
		case operation.replace != nil:
			kind = "replace"
			var matches bool
			matches, err = targetMatches(sourceRoot, operation.path, operation.replace.ExpectedData, operation.replace.ExpectedMode)
			if err == nil && !matches {
				err = errors.New("target does not match authorized evidence")
			}
			if err == nil {
				err = deps.replaceIfMatches(sourceRoot, operation.path, operation.replace.ExpectedData, operation.replace.ExpectedMode, operation.replace.Data, operation.replace.Mode)
			}
		default:
			kind = "remove"
			var matches bool
			matches, err = targetMatches(sourceRoot, operation.path, operation.remove.ExpectedData, operation.remove.ExpectedMode)
			if err == nil && !matches {
				err = errors.New("target does not match authorized evidence")
			}
			if err == nil {
				err = deps.removeIfMatches(sourceRoot, operation.path, operation.remove.ExpectedData)
			}
		}
		if err != nil {
			applyErr := fmt.Errorf("apply %s %s: %w", kind, operation.path, err)
			return snapshot, errors.Join(applyErr, rollback(deps, sourceRoot, backupRoot, backupName, snapshot, attempted))
		}
	}
	if deps.finalVerify != nil {
		if err := deps.finalVerify(); err != nil {
			applyErr := fmt.Errorf("apply final verification: %w", err)
			return snapshot, errors.Join(applyErr, rollback(deps, sourceRoot, backupRoot, backupName, snapshot, attempted))
		}
	}
	return snapshot, nil
}

func prepareOperations(sourceRoot string, raw []Operation) ([]operation, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("apply operations are empty")
	}
	operations := make([]operation, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, rawOperation := range raw {
		actions := 0
		for _, present := range []bool{rawOperation.Write != nil, rawOperation.Create != nil, rawOperation.Replace != nil, rawOperation.Remove != nil} {
			if present {
				actions++
			}
		}
		if actions != 1 {
			return nil, fmt.Errorf("apply operation must specify exactly one action")
		}
		var path string
		operation := operation{}
		switch {
		case rawOperation.Write != nil:
			if rawOperation.Write.Mode&^fs.FileMode(0o777) != 0 {
				return nil, fmt.Errorf("apply write has unsupported mode")
			}
			path = rawOperation.Write.Path
			copy := Write{Path: path, Data: append([]byte(nil), rawOperation.Write.Data...), Mode: rawOperation.Write.Mode}
			operation.write = &copy
		case rawOperation.Create != nil:
			if rawOperation.Create.Mode&^fs.FileMode(0o777) != 0 {
				return nil, fmt.Errorf("apply create has unsupported mode")
			}
			path = rawOperation.Create.Path
			copy := Create{Path: path, Data: append([]byte(nil), rawOperation.Create.Data...), Mode: rawOperation.Create.Mode}
			operation.create = &copy
		case rawOperation.Replace != nil:
			if rawOperation.Replace.ExpectedData == nil || rawOperation.Replace.ExpectedMode&^fs.FileMode(0o777) != 0 || rawOperation.Replace.Mode&^fs.FileMode(0o777) != 0 {
				return nil, fmt.Errorf("apply replace has missing or unsupported evidence")
			}
			path = rawOperation.Replace.Path
			copy := Replace{Path: path, ExpectedData: append([]byte(nil), rawOperation.Replace.ExpectedData...), ExpectedMode: rawOperation.Replace.ExpectedMode, Data: append([]byte(nil), rawOperation.Replace.Data...), Mode: rawOperation.Replace.Mode}
			operation.replace = &copy
		case rawOperation.Remove != nil:
			if rawOperation.Remove.ExpectedMode&^fs.FileMode(0o777) != 0 {
				return nil, fmt.Errorf("apply remove has unsupported expected mode")
			}
			path = rawOperation.Remove.Path
			copy := Remove{Path: path, ExpectedData: append([]byte(nil), rawOperation.Remove.ExpectedData...), ExpectedMode: rawOperation.Remove.ExpectedMode}
			operation.remove = &copy
		}
		if _, err := safepath.Resolve(sourceRoot, path); err != nil {
			return nil, fmt.Errorf("apply operation path is unsafe: %w", err)
		}
		path = filepath.ToSlash(filepath.Clean(path))
		if _, exists := seen[path]; exists {
			return nil, fmt.Errorf("apply operation path is duplicated: %s", path)
		}
		seen[path] = struct{}{}
		operation.path = path
		operations = append(operations, operation)
	}
	return operations, nil
}

func verifyOperationEvidence(snapshot Snapshot, operations []operation) error {
	entries := make(map[string]Entry, len(snapshot.Manifest.Entries))
	for _, entry := range snapshot.Manifest.Entries {
		entries[entry.Path] = entry
	}
	for _, operation := range operations {
		entry, exists := entries[operation.path]
		if !exists {
			return fmt.Errorf("apply operation snapshot entry is missing")
		}
		if operation.create != nil {
			if entry.Exists {
				return fmt.Errorf("apply create evidence does not match snapshot")
			}
			continue
		}
		var expected []byte
		var expectedMode fs.FileMode
		kind := "remove"
		if operation.replace != nil {
			expected, expectedMode, kind = operation.replace.ExpectedData, operation.replace.ExpectedMode, "replace"
		} else if operation.remove != nil {
			expected, expectedMode = operation.remove.ExpectedData, operation.remove.ExpectedMode
		} else {
			continue
		}
		if !entry.Exists || fs.FileMode(entry.Mode).Perm() != expectedMode.Perm() {
			return fmt.Errorf("apply %s evidence does not match snapshot", kind)
		}
		payload, err := snapshotPayload(snapshot.Dir, entry.Path)
		if err != nil || !bytes.Equal(payload, expected) {
			return fmt.Errorf("apply %s evidence does not match snapshot", kind)
		}
	}
	return nil
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
		if operation.remove != nil {
			if err := restoreRemoved(deps, sourceRoot, operation, entry, payloads[entry.Path]); err != nil {
				rollbackErr = errors.Join(rollbackErr, fmt.Errorf("restore %s: %w", operation.path, err))
			}
			continue
		}
		if operation.create != nil {
			if err := removeCreated(deps, sourceRoot, operation); err != nil {
				rollbackErr = errors.Join(rollbackErr, fmt.Errorf("remove %s: %w", operation.path, err))
			}
			continue
		}
		if entry.Exists {
			original := payloads[entry.Path]
			var desiredData []byte
			var desiredMode fs.FileMode
			if operation.replace != nil {
				desiredData, desiredMode = operation.replace.Data, operation.replace.Mode
			} else {
				desiredData, desiredMode = operation.write.Data, operation.write.Mode
			}
			matches, err := targetMatches(sourceRoot, operation.path, original, fs.FileMode(entry.Mode))
			if err == nil && matches {
				continue
			}
			if err == nil {
				err = deps.replaceIfMatches(sourceRoot, operation.path, desiredData, desiredMode, original, fs.FileMode(entry.Mode))
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

func removeCreated(deps applyDependencies, sourceRoot string, operation operation) error {
	matches, err := targetMatches(sourceRoot, operation.path, operation.create.Data, operation.create.Mode)
	if err != nil {
		return err
	}
	if !matches {
		target, err := safepath.Resolve(sourceRoot, operation.path)
		if err != nil {
			return err
		}
		if _, err := os.Lstat(target); os.IsNotExist(err) {
			return nil
		}
		return errors.New("target changed after create")
	}
	return deps.removeIfMatches(sourceRoot, operation.path, operation.create.Data)
}

func restoreRemoved(deps applyDependencies, sourceRoot string, operation operation, entry Entry, payload []byte) error {
	if !entry.Exists {
		return errors.New("snapshot entry is absent")
	}
	matches, err := targetMatches(sourceRoot, operation.path, payload, fs.FileMode(entry.Mode))
	if err != nil {
		return err
	}
	if matches {
		return nil
	}
	return deps.restoreIfAbsent(sourceRoot, operation.path, payload, fs.FileMode(entry.Mode))
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

func restoreIfAbsent(root, path string, data []byte, mode fs.FileMode) (resultErr error) {
	target, err := safepath.Resolve(root, path)
	if err != nil {
		return err
	}
	parentInfo, err := os.Lstat(filepath.Dir(target))
	if err != nil || !parentInfo.IsDir() || parentInfo.Mode()&os.ModeSymlink != 0 {
		return errors.New("target parent is missing or invalid")
	}
	file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode.Perm())
	if err != nil {
		return errors.New("target changed after removal")
	}
	open := true
	defer func() {
		if open {
			resultErr = errors.Join(resultErr, file.Close())
		}
	}()
	if err := file.Chmod(mode.Perm()); err != nil {
		return err
	}
	written, err := file.Write(data)
	if err != nil || written != len(data) {
		return errors.New("restore target write failed")
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	open = false
	directory, err := os.Open(filepath.Dir(target))
	if err != nil {
		return err
	}
	directoryOpen := true
	defer func() {
		if directoryOpen {
			resultErr = errors.Join(resultErr, directory.Close())
		}
	}()
	if err := directory.Sync(); err != nil {
		return err
	}
	if err := directory.Close(); err != nil {
		return err
	}
	directoryOpen = false
	matches, err := targetMatches(root, path, data, mode)
	if err != nil || !matches {
		return errors.New("restore target readback failed")
	}
	return nil
}
