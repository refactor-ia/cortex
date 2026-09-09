package backuprecovery

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/refactor-ia/cortex/internal/backupjournal"
	"github.com/refactor-ia/cortex/internal/filetxn"
)

func TestExecuteRestoreIntentPlannedEmptySkipsValidationAndMutation(t *testing.T) {
	deps := restoreExecutorOperations{openRoot: func(string) (*os.Root, error) { t.Fatal("validated empty intent"); return nil, nil }}
	if err := executeRestoreIntentWith(restoreIntent{planned: true}, backupjournal.Manifest{}, nil, deps); err != nil {
		t.Fatalf("execute planned empty: %v", err)
	}
}

func TestExecuteRestoreIntentAppliesCanonicalOperations(t *testing.T) {
	manifest, roots, intent := executorFixture(t, true)
	journal, _ := json.Marshal(manifest)
	outside := filepath.Join(t.TempDir(), "outside")
	mustNoFilesystemError(t, os.WriteFile(outside, []byte("outside"), 0600))
	for index, operation := range intent.operations {
		if operation.runtime != runtimeList[index] {
			t.Fatalf("runtime %d = %q, want %q", index, operation.runtime, runtimeList[index])
		}
	}
	var calls, rootChecks []string
	deps := executorDependencies()
	openRoot, remove, create, replace := deps.openRoot, deps.remove, deps.create, deps.replace
	deps.openRoot = func(path string) (*os.Root, error) {
		rootChecks = append(rootChecks, path)
		return openRoot(path)
	}
	deps.remove = func(root *os.Root, path string, data []byte, mode os.FileMode) error {
		if root == nil || path != "config.json" || data == nil || !bytes.Equal(data, []byte{}) || mode != 0600 {
			t.Fatalf("remove = %v, %q, %q, %v", root, path, data, mode)
		}
		calls = append(calls, "remove")
		return remove(root, path, data, mode)
	}
	deps.create = func(root *os.Root, path string, data []byte, mode os.FileMode) error {
		if root == nil || path != "config.json" || !bytes.Equal(data, []byte("before-opencode")) || mode != 0600 {
			t.Fatalf("create = %v, %q, %q, %v", root, path, data, mode)
		}
		calls = append(calls, "create")
		return create(root, path, data, mode)
	}
	deps.replace = func(root *os.Root, path string, expectedData []byte, expectedMode os.FileMode, data []byte, mode os.FileMode) error {
		if root == nil || path != "config.json" || !bytes.Equal(expectedData, []byte("after-claude-code")) || expectedMode != 0600 || !bytes.Equal(data, []byte("before-claude-code")) || mode != 0600 {
			t.Fatalf("replace = %v, %q, %q, %v, %q, %v", root, path, expectedData, expectedMode, data, mode)
		}
		calls = append(calls, "replace")
		return replace(root, path, expectedData, expectedMode, data, mode)
	}
	if err := executeRestoreIntentWith(intent, manifest, roots, deps); err != nil || strings.Join(calls, ",") != "remove,create,replace" || strings.Join(rootChecks, ",") != strings.Join([]string{roots[0].path, roots[1].path, roots[2].path}, ",") {
		t.Fatalf("execute = %v, calls = %v, root checks = %v", err, calls, rootChecks)
	}
	assertExecutorFile(t, roots[0], nil, 0)
	assertExecutorFile(t, roots[1], []byte("before-opencode"), 0600)
	assertExecutorFile(t, roots[2], []byte("before-claude-code"), 0600)
	after, _ := json.Marshal(manifest)
	if !bytes.Equal(journal, after) || string(mustReadExecutorFile(t, outside)) != "outside" {
		t.Fatal("journal or outside file changed")
	}
}

func TestExecuteRestoreIntentAnchorsMutationToValidatedRoot(t *testing.T) {
	manifest, roots, intent := executorFixture(t, false)
	path, moved := roots[0].path, roots[0].path+"-moved"
	deps := executorDependencies()
	remove := deps.remove
	deps.remove = func(root *os.Root, relative string, data []byte, mode os.FileMode) error {
		mustNoFilesystemError(t, os.Rename(path, moved))
		mustNoFilesystemError(t, os.Mkdir(path, 0700))
		mustNoFilesystemError(t, os.WriteFile(filepath.Join(path, relative), []byte("after-pi"), 0600))
		return remove(root, relative, data, mode)
	}
	if err := executeRestoreIntentWith(intent, manifest, roots, deps); err != nil {
		t.Fatalf("execute = %v", err)
	}
	assertExecutorFile(t, filesystemRoot{path: moved}, nil, 0)
	assertExecutorFile(t, filesystemRoot{path: path}, []byte("after-pi"), 0600)
}

func TestExecuteRestoreIntentStopsOnForwardFailure(t *testing.T) {
	t.Run("root validation", func(t *testing.T) {
		manifest, roots, intent := executorFixture(t, false)
		deps := executorDependencies()
		openRoot, checks := deps.openRoot, 0
		deps.openRoot = func(path string) (*os.Root, error) {
			checks++
			if checks == 2 {
				return nil, errors.New("injected root failure")
			}
			return openRoot(path)
		}
		if err := executeRestoreIntentWith(intent, manifest, roots, deps); err == nil || err.Error() != "backup recovery: restore forward mutation failed" {
			t.Fatalf("execute = %v", err)
		}
		assertExecutorFile(t, roots[0], nil, 0)
		assertExecutorFile(t, roots[1], nil, 0)
		assertExecutorFile(t, roots[2], []byte("after-claude-code"), 0600)
	})
	t.Run("atomic mutation", func(t *testing.T) {
		manifest, roots, intent := executorFixture(t, false)
		deps := executorDependencies()
		deps.remove = func(*os.Root, string, []byte, os.FileMode) error { return errors.New("injected private failure") }
		if err := executeRestoreIntentWith(intent, manifest, roots, deps); err == nil || err.Error() != "backup recovery: restore forward mutation failed" {
			t.Fatalf("execute = %v", err)
		}
		assertExecutorFile(t, roots[0], []byte("after-pi"), 0600)
	})
}

func TestExecuteRestoreIntentRejectsUnplannedAndMalformedIntent(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*restoreIntent)
	}{
		{name: "unplanned", mutate: func(intent *restoreIntent) { intent.planned = false }},
		{name: "wrong evidence", mutate: func(intent *restoreIntent) { intent.operations[0].operation.Remove.ExpectedData = []byte("wrong") }},
		{name: "wrong mode", mutate: func(intent *restoreIntent) { intent.operations[0].operation.Remove.ExpectedMode = 0644 }},
		{name: "missing entry", mutate: func(intent *restoreIntent) { intent.operations[0].operation.Remove.Path = "missing.json" }},
		{name: "duplicate operation", mutate: func(intent *restoreIntent) { intent.operations[1] = intent.operations[0] }},
		{name: "write operation", mutate: func(intent *restoreIntent) {
			intent.operations[0].operation = filetxn.Operation{Write: &filetxn.Write{Path: "config.json", Data: []byte("write"), Mode: 0600}}
		}},
		{name: "multiple actions", mutate: func(intent *restoreIntent) {
			intent.operations[0].operation.Create = &filetxn.Create{Path: "config.json", Data: []byte("create"), Mode: 0600}
		}},
		{name: "noncanonical operations", mutate: func(intent *restoreIntent) {
			intent.operations[0], intent.operations[1] = intent.operations[1], intent.operations[0]
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest, roots, intent := executorFixture(t, false)
			test.mutate(&intent)
			if err := executeRestoreIntentWith(intent, manifest, roots, executorDependencies()); err == nil || !strings.Contains(err.Error(), "invalid restore intent") {
				t.Fatalf("execute = %v", err)
			}
			assertExecutorFile(t, roots[0], []byte("after-pi"), 0600)
			assertExecutorFile(t, roots[1], nil, 0)
			assertExecutorFile(t, roots[2], []byte("after-claude-code"), 0600)
		})
	}
}

func executorFixture(t *testing.T, zero bool) (backupjournal.Manifest, []filesystemRoot, restoreIntent) {
	t.Helper()
	roots, bindings, inputs, blobs, current := []filesystemRoot{}, []backupjournal.RootBinding{}, []backupjournal.RecoverableEntryInput{}, []beforeBlob{}, []currentEvidence{}
	for index, runtime := range runtimeList {
		root, err := newFilesystemRoot(runtime, backupjournal.RootKind(runtime), t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		before, after := []byte("before-"+string(runtime)), []byte("after-"+string(runtime))
		if zero && index == 0 {
			after = []byte{}
		}
		input := backupjournal.RecoverableEntryInput{Before: entry(runtime, backupjournal.Present, before), After: evidence(backupjournal.Present, after)}
		if index == 0 {
			input.Before, input.After = entry(runtime, backupjournal.Absent, nil), evidence(backupjournal.Present, after)
		}
		if index == 1 {
			input.After = evidence(backupjournal.Absent, nil)
		}
		if input.After.Existence == backupjournal.Present {
			mustNoFilesystemError(t, os.WriteFile(filepath.Join(root.path, "config.json"), after, 0600))
			current = append(current, newCurrentPresent(entryKey(string(runtime)+"\x00config.json"), 0600, after))
		} else {
			current = append(current, newCurrentAbsent(entryKey(string(runtime)+"\x00config.json")))
		}
		if input.Before.Existence == backupjournal.Present {
			blobs = append(blobs, newBeforeBlob(entryKey(string(runtime)+"\x00config.json"), before))
		}
		roots, bindings, inputs = append(roots, root), append(bindings, root.binding), append(inputs, input)
	}
	manifest, err := backupjournal.NewRecoverable(hash("executor transaction"), hash("executor candidate"), bindings, inputs)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := classify(manifest, blobs, current)
	if err != nil {
		t.Fatal(err)
	}
	intent, err := planRestoreIntent(plan)
	if err != nil {
		t.Fatal(err)
	}
	return manifest, roots, intent
}

func executorDependencies() restoreExecutorOperations {
	return restoreExecutorOperations{
		openRoot: os.OpenRoot,
		create: func(root *os.Root, path string, data []byte, mode os.FileMode) error {
			if _, err := root.Lstat(path); !errors.Is(err, os.ErrNotExist) {
				return errors.New("create destination exists")
			}
			return root.WriteFile(path, data, mode)
		},
		replace: func(root *os.Root, path string, expectedData []byte, expectedMode os.FileMode, data []byte, mode os.FileMode) error {
			if info, err := root.Lstat(path); err != nil || info.Mode().Perm() != expectedMode.Perm() {
				return errors.New("replace destination differs")
			}
			if current, err := root.ReadFile(path); err != nil || !bytes.Equal(current, expectedData) {
				return errors.New("replace destination differs")
			}
			return root.WriteFile(path, data, mode)
		},
		remove: func(root *os.Root, path string, expectedData []byte, expectedMode os.FileMode) error {
			if info, err := root.Lstat(path); err != nil || info.Mode().Perm() != expectedMode.Perm() {
				return errors.New("remove destination differs")
			}
			if current, err := root.ReadFile(path); err != nil || !bytes.Equal(current, expectedData) {
				return errors.New("remove destination differs")
			}
			return root.Remove(path)
		},
	}
}
func mustReadExecutorFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
func assertExecutorFile(t *testing.T, root filesystemRoot, want []byte, mode os.FileMode) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(root.path, "config.json"))
	if want == nil {
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("file = %q, %v", got, err)
		}
		return
	}
	info, statErr := os.Lstat(filepath.Join(root.path, "config.json"))
	if err != nil || statErr != nil || !bytes.Equal(got, want) || info.Mode().Perm() != mode {
		t.Fatalf("file = %q, %v, mode = %v", got, err, info.Mode())
	}
}
