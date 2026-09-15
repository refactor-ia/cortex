package backuprecovery

import (
	"io/fs"
	"path"
	"strings"

	"github.com/refactor-ia/cortex/internal/backupjournal"
	"github.com/refactor-ia/cortex/internal/filetxn"
)

type restoreOperation struct {
	runtime   backupjournal.Runtime
	operation filetxn.Operation
}

type restoreIntent struct {
	operations []restoreOperation
	planned    bool
}

func planRestoreIntent(plan recoveryPlan) (restoreIntent, error) {
	if !validRecoveryPlan(plan) {
		return restoreIntent{}, invalidModel()
	}
	operations := make([]restoreOperation, 0, len(plan.entries))
	for _, entry := range plan.entries {
		if entry.state == atBefore {
			continue
		}
		operation := restoreOperation{runtime: entry.runtime}
		switch {
		case entry.before.state == currentAbsent:
			operation.operation.Remove = &filetxn.Remove{
				Path:         entry.relativePath,
				ExpectedData: cloneBytes(entry.current.bytes),
				ExpectedMode: fs.FileMode(entry.current.mode),
			}
		case entry.current.state == currentAbsent:
			operation.operation.Create = &filetxn.Create{
				Path: entry.relativePath,
				Data: cloneBytes(entry.before.bytes),
				Mode: fs.FileMode(entry.before.mode),
			}
		default:
			operation.operation.Replace = &filetxn.Replace{
				Path:         entry.relativePath,
				ExpectedData: cloneBytes(entry.current.bytes),
				ExpectedMode: fs.FileMode(entry.current.mode),
				Data:         cloneBytes(entry.before.bytes),
				Mode:         fs.FileMode(entry.before.mode),
			}
		}
		operations = append(operations, operation)
	}
	return restoreIntent{operations: operations, planned: true}, nil
}

func validRecoveryPlan(plan recoveryPlan) bool {
	if !plan.ready || len(plan.entries) == 0 {
		return false
	}
	seenRuntimes := make(map[backupjournal.Runtime]bool, 3)
	paths := make(map[backupjournal.Runtime][]string, 3)
	for index, entry := range plan.entries {
		if !validRecoveryEntry(entry) || index > 0 && !recoveryEntryLess(plan.entries[index-1], entry) {
			return false
		}
		for _, existing := range paths[entry.runtime] {
			if recoveryPathsOverlap(existing, entry.relativePath) {
				return false
			}
		}
		paths[entry.runtime] = append(paths[entry.runtime], entry.relativePath)
		seenRuntimes[entry.runtime] = true
	}
	return len(seenRuntimes) == 3 && seenRuntimes[backupjournal.RuntimePi] && seenRuntimes[backupjournal.RuntimeOpenCode] && seenRuntimes[backupjournal.RuntimeClaude]
}

func validRecoveryEntry(entry recoveryEntry) bool {
	key := entryKey(string(entry.runtime) + "\x00" + entry.relativePath)
	if restoreRuntimeRank(entry.runtime) < 0 || !validRecoveryPath(entry.relativePath) || entry.key != key || entry.before.key != key || entry.after.key != key || entry.current.key != key {
		return false
	}
	if !validCurrent(entry.before) || !validJournal(entry.before) || !validJournal(entry.after) || len(entry.after.bytes) != 0 || !validCurrent(entry.current) || sameJournal(entry.before, entry.after) {
		return false
	}
	if entry.state != atBefore && entry.state != atAfter {
		return false
	}
	return classifyCurrent(entry.current, entry.before, entry.after) == entry.state
}

func recoveryEntryLess(left, right recoveryEntry) bool {
	if left.runtime != right.runtime {
		return restoreRuntimeRank(left.runtime) < restoreRuntimeRank(right.runtime)
	}
	return left.relativePath < right.relativePath
}

func restoreRuntimeRank(runtime backupjournal.Runtime) int {
	switch runtime {
	case backupjournal.RuntimePi:
		return 0
	case backupjournal.RuntimeOpenCode:
		return 1
	case backupjournal.RuntimeClaude:
		return 2
	}
	return -1
}

func validRecoveryPath(value string) bool {
	if value == "" || strings.Contains(value, "\\") || path.IsAbs(value) || path.Clean(value) != value || value == "." {
		return false
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

func recoveryPathsOverlap(left, right string) bool {
	return left == right || strings.HasPrefix(left, right+"/") || strings.HasPrefix(right, left+"/")
}
