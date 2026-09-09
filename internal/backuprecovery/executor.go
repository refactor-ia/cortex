package backuprecovery

import (
	"io/fs"
	"os"

	"github.com/refactor-ia/cortex/internal/backupjournal"
)

type restoreExecutorOperations struct {
	openRoot func(string) (*os.Root, error)
	create   func(*os.Root, string, []byte, fs.FileMode) error
	replace  func(*os.Root, string, []byte, fs.FileMode, []byte, fs.FileMode) error
	remove   func(*os.Root, string, []byte, fs.FileMode) error
}

func executeRestoreIntentWith(intent restoreIntent, manifest backupjournal.Manifest, roots []filesystemRoot, deps restoreExecutorOperations) error {
	if !intent.planned {
		return invalidRestoreIntent()
	}
	if len(intent.operations) == 0 {
		return nil
	}
	if _, ok := validRestoreIntent(intent, manifest, deps); !ok {
		return invalidRestoreIntent()
	}
	bound, ok := rootsForManifest(manifest, roots)
	if !ok {
		return invalidRestoreIntent()
	}
	for _, operation := range intent.operations {
		descriptor, ok := openValidatedRestoreRoot(bound[operation.runtime], deps)
		if !ok {
			return restoreForwardFailure()
		}
		mutationErr := applyRestore(operation, descriptor, deps)
		closeErr := descriptor.Close()
		if mutationErr != nil || closeErr != nil {
			return restoreForwardFailure()
		}
	}
	return nil
}

func validRestoreIntent(intent restoreIntent, manifest backupjournal.Manifest, deps restoreExecutorOperations) (map[entryKey]backupjournal.Entry, bool) {
	if !manifest.Recoverable() || manifest.State() != backupjournal.Prepared || deps.openRoot == nil || deps.create == nil || deps.replace == nil || deps.remove == nil {
		return nil, false
	}
	entries := make(map[entryKey]backupjournal.Entry, len(manifest.Entries()))
	positions := make(map[entryKey]int, len(manifest.Entries()))
	for position, entry := range manifest.Entries() {
		key := keyFor(entry)
		entries[key] = entry
		positions[key] = position
	}
	seen := make(map[entryKey]bool, len(intent.operations))
	previousPosition := -1
	for _, operation := range intent.operations {
		key := restoreOperationKey(operation)
		entry, found := entries[key]
		position := positions[key]
		if !found || seen[key] || position <= previousPosition || !validRestoreOperation(operation, entry) {
			return nil, false
		}
		seen[key] = true
		previousPosition = position
	}
	return entries, true
}

func restoreOperationKey(operation restoreOperation) entryKey {
	switch {
	case operation.operation.Create != nil:
		return entryKey(string(operation.runtime) + "\x00" + operation.operation.Create.Path)
	case operation.operation.Replace != nil:
		return entryKey(string(operation.runtime) + "\x00" + operation.operation.Replace.Path)
	case operation.operation.Remove != nil:
		return entryKey(string(operation.runtime) + "\x00" + operation.operation.Remove.Path)
	}
	return ""
}

func validRestoreOperation(operation restoreOperation, entry backupjournal.Entry) bool {
	action := 0
	for _, present := range []bool{operation.operation.Create != nil, operation.operation.Replace != nil, operation.operation.Remove != nil, operation.operation.Write != nil} {
		if present {
			action++
		}
	}
	if action != 1 || operation.runtime != entry.Runtime {
		return false
	}
	after := entry.AfterEvidence()
	switch {
	case entry.Existence == backupjournal.Absent && after.Existence == backupjournal.Present && operation.operation.Remove != nil:
		return imageMatches(after, operation.operation.Remove.ExpectedData, operation.operation.Remove.ExpectedMode)
	case entry.Existence == backupjournal.Present && after.Existence == backupjournal.Absent && operation.operation.Create != nil:
		return imageMatches(backupjournal.Evidence{Existence: entry.Existence, Mode: entry.Mode, SHA256: entry.SHA256, Length: entry.Length}, operation.operation.Create.Data, operation.operation.Create.Mode)
	case entry.Existence == backupjournal.Present && after.Existence == backupjournal.Present && operation.operation.Replace != nil:
		return imageMatches(after, operation.operation.Replace.ExpectedData, operation.operation.Replace.ExpectedMode) && imageMatches(backupjournal.Evidence{Existence: entry.Existence, Mode: entry.Mode, SHA256: entry.SHA256, Length: entry.Length}, operation.operation.Replace.Data, operation.operation.Replace.Mode)
	}
	return false
}

func imageMatches(image backupjournal.Evidence, data []byte, mode fs.FileMode) bool {
	return image.Existence == backupjournal.Present && data != nil && mode&^fs.FileMode(0777) == 0 && uint32(mode) == image.Mode && int64(len(data)) == image.Length && hashBytes(data) == image.SHA256
}

func openValidatedRestoreRoot(root filesystemRoot, deps restoreExecutorOperations) (*os.Root, bool) {
	descriptor, err := deps.openRoot(root.path)
	if err != nil || descriptor == nil || !matchesFilesystemRoot(root, descriptor) {
		if descriptor != nil {
			_ = descriptor.Close()
		}
		return nil, false
	}
	return descriptor, true
}

func applyRestore(operation restoreOperation, root *os.Root, deps restoreExecutorOperations) error {
	switch {
	case operation.operation.Create != nil:
		item := operation.operation.Create
		return deps.create(root, item.Path, item.Data, item.Mode)
	case operation.operation.Replace != nil:
		item := operation.operation.Replace
		return deps.replace(root, item.Path, item.ExpectedData, item.ExpectedMode, item.Data, item.Mode)
	case operation.operation.Remove != nil:
		item := operation.operation.Remove
		return deps.remove(root, item.Path, item.ExpectedData, item.ExpectedMode)
	}
	return invalidRestoreIntent()
}

type restoreIntentError string

func (err restoreIntentError) Error() string { return string(err) }
func invalidRestoreIntent() error {
	return restoreIntentError("backup recovery: invalid restore intent")
}
func restoreForwardFailure() error {
	return restoreIntentError("backup recovery: restore forward mutation failed")
}
