package filetxn

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/refactor-ia/cortex/internal/atomicfile"
)

func TestApplyWritesInOrderAndKeepsVerifiedSnapshot(t *testing.T) {
	root, backups := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(root, "existing.txt"), []byte("original"), 0o640)

	deps := defaultApplyDependencies()
	var order []string
	deps.replace = func(root, path string, data []byte, mode fs.FileMode) error {
		order = append(order, path)
		return atomicfile.Replace(root, path, data, mode)
	}
	snapshot, err := apply(deps, root, backups, "batch", []Write{
		{Path: "existing.txt", Data: []byte("first"), Mode: 0o600},
		{Path: "new.txt", Data: []byte("second"), Mode: 0o644},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertFile(t, filepath.Join(root, "existing.txt"), "first", 0o600)
	assertFile(t, filepath.Join(root, "new.txt"), "second", 0o644)
	if strings.Join(order, ",") != "existing.txt,new.txt" {
		t.Fatalf("write order = %v", order)
	}
	if err := Verify(backups, "batch"); err != nil {
		t.Fatalf("Verify() after success = %v", err)
	}
	if snapshot.Dir != filepath.Join(backups, "batch") {
		t.Fatalf("snapshot directory = %q", snapshot.Dir)
	}
}

func TestApplyRejectsInvalidBatchesBeforeBackup(t *testing.T) {
	cases := []struct {
		name   string
		writes []Write
	}{
		{"empty", nil},
		{"duplicate", []Write{{Path: "a.txt", Mode: 0o600}, {Path: "a.txt", Mode: 0o600}}},
		{"unsupported mode", []Write{{Path: "a.txt", Mode: fs.ModeSetuid | 0o600}}},
		{"unsafe path", []Write{{Path: "../outside", Mode: 0o600}}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			root, backups := t.TempDir(), t.TempDir()
			if _, err := Apply(root, backups, "batch", tt.writes); err == nil {
				t.Fatal("Apply() error = nil")
			}
			if _, err := os.Lstat(filepath.Join(backups, "batch")); !os.IsNotExist(err) {
				t.Fatalf("backup was created: %v", err)
			}
		})
	}
}

func TestApplyRollsBackFailedWrites(t *testing.T) {
	for _, afterMutation := range []bool{false, true} {
		t.Run(fmt.Sprintf("after mutation %t", afterMutation), func(t *testing.T) {
			root, backups := t.TempDir(), t.TempDir()
			writeFile(t, filepath.Join(root, "existing.txt"), []byte("original"), 0o640)
			deps := defaultApplyDependencies()
			deps.replace = func(root, path string, data []byte, mode fs.FileMode) error {
				if path != "missing.txt" {
					return atomicfile.Replace(root, path, data, mode)
				}
				if afterMutation {
					if err := atomicfile.Replace(root, path, data, mode); err != nil {
						return err
					}
				}
				return errors.New("injected replace failure")
			}
			_, err := apply(deps, root, backups, "batch", []Write{
				{Path: "existing.txt", Data: []byte("changed"), Mode: 0o600},
				{Path: "missing.txt", Data: []byte("created"), Mode: 0o644},
			})
			if err == nil || !strings.Contains(err.Error(), "injected replace failure") {
				t.Fatalf("Apply() error = %v", err)
			}
			assertFile(t, filepath.Join(root, "existing.txt"), "original", 0o640)
			if _, err := os.Lstat(filepath.Join(root, "missing.txt")); !os.IsNotExist(err) {
				t.Fatalf("missing target remains: %v", err)
			}
			if err := Verify(backups, "batch"); err != nil {
				t.Fatalf("Verify() after rollback = %v", err)
			}
		})
	}
}

func TestApplyRollsBackInReverseOrder(t *testing.T) {
	root, backups := t.TempDir(), t.TempDir()
	deps := defaultApplyDependencies()
	var removals []string
	deps.replace = func(root, path string, data []byte, mode fs.FileMode) error {
		if err := atomicfile.Replace(root, path, data, mode); err != nil {
			return err
		}
		if path == "c.txt" {
			return errors.New("injected replace failure")
		}
		return nil
	}
	deps.removeIfExact = func(root, path string, data []byte, mode fs.FileMode) error {
		removals = append(removals, path)
		return atomicfile.RemoveIfExact(root, path, data, mode)
	}
	_, err := apply(deps, root, backups, "batch", []Write{
		{Path: "a.txt", Data: []byte("a"), Mode: 0o600},
		{Path: "b.txt", Data: []byte("b"), Mode: 0o600},
		{Path: "c.txt", Data: []byte("c"), Mode: 0o600},
	})
	if err == nil || strings.Join(removals, ",") != "c.txt,b.txt,a.txt" {
		t.Fatalf("error = %v, rollback order = %v", err, removals)
	}
}

func TestApplyAcceptsAlreadyRestoredTargetDuringRollback(t *testing.T) {
	root, backups := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(root, "existing.txt"), []byte("original"), 0o640)
	deps := defaultApplyDependencies()
	var restores int
	deps.replace = func(root, path string, data []byte, mode fs.FileMode) error {
		if err := atomicfile.Replace(root, path, data, mode); err != nil {
			return err
		}
		if path == "new.txt" {
			if err := atomicfile.Replace(root, "existing.txt", []byte("original"), 0o640); err != nil {
				return err
			}
			return errors.New("injected replace failure")
		}
		return nil
	}
	deps.replaceIfMatches = func(root, path string, expected []byte, expectedMode fs.FileMode, data []byte, mode fs.FileMode) error {
		restores++
		return atomicfile.ReplaceIfMatches(root, path, expected, expectedMode, data, mode)
	}
	_, err := apply(deps, root, backups, "batch", []Write{
		{Path: "existing.txt", Data: []byte("changed"), Mode: 0o600},
		{Path: "new.txt", Data: []byte("created"), Mode: 0o600},
	})
	if err == nil || restores != 0 {
		t.Fatalf("error = %v, restore calls = %d", err, restores)
	}
	assertFile(t, filepath.Join(root, "existing.txt"), "original", 0o640)
}

func TestApplyPreservesUserDriftAndRequiresIntervention(t *testing.T) {
	root, backups := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(root, "existing.txt"), []byte("original"), 0o640)
	deps := defaultApplyDependencies()
	deps.replace = func(root, path string, data []byte, mode fs.FileMode) error {
		if err := atomicfile.Replace(root, path, data, mode); err != nil {
			return err
		}
		if path == "new.txt" {
			if err := atomicfile.Replace(root, "existing.txt", []byte("user drift"), 0o600); err != nil {
				return err
			}
			return errors.New("injected replace failure")
		}
		return nil
	}
	_, err := apply(deps, root, backups, "batch", []Write{
		{Path: "existing.txt", Data: []byte("changed"), Mode: 0o600},
		{Path: "new.txt", Data: []byte("created"), Mode: 0o600},
	})
	if err == nil || !strings.Contains(err.Error(), "caller intervention required") {
		t.Fatalf("Apply() error = %v", err)
	}
	assertFile(t, filepath.Join(root, "existing.txt"), "user drift", 0o600)
	if err := Verify(backups, "batch"); err != nil {
		t.Fatalf("Verify() after drift = %v", err)
	}
}

func TestApplyOperationsRemovesVerifiedFile(t *testing.T) {
	root, backups := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(root, "stale.txt"), []byte("owned"), 0o640)

	snapshot, err := ApplyOperations(root, backups, "batch", []Operation{{Remove: &Remove{
		Path: "stale.txt", ExpectedData: []byte("owned"), ExpectedMode: 0o640,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(root, "stale.txt")); !os.IsNotExist(err) {
		t.Fatalf("removed target remains: %v", err)
	}
	if !snapshot.Manifest.Entries[0].Exists || snapshot.Manifest.Entries[0].Mode != 0o640 {
		t.Fatalf("snapshot = %#v", snapshot.Manifest.Entries)
	}
	if err := Verify(backups, "batch"); err != nil {
		t.Fatalf("Verify() after removal = %v", err)
	}
}

func TestApplyOperationsRemovesVerifiedEmptyFile(t *testing.T) {
	root, backups := t.TempDir(), t.TempDir()
	path := filepath.Join(root, "empty.txt")
	writeFile(t, path, []byte{}, 0o640)

	_, err := ApplyOperations(root, backups, "batch", []Operation{{Remove: &Remove{
		Path: "empty.txt", ExpectedData: []byte{}, ExpectedMode: 0o640,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("removed target remains: %v", err)
	}
}

func TestApplyOperationsRejectsNilRemovalEvidenceWithoutSnapshot(t *testing.T) {
	root, backups := t.TempDir(), t.TempDir()
	path := filepath.Join(root, "stale.txt")
	writeFile(t, path, []byte("owned"), 0o640)

	_, err := ApplyOperations(root, backups, "batch", []Operation{{Remove: &Remove{
		Path: "stale.txt", ExpectedMode: 0o640,
	}}})
	if err == nil || !strings.Contains(err.Error(), "missing or unsupported evidence") {
		t.Fatalf("ApplyOperations() error = %v", err)
	}
	assertFile(t, path, "owned", 0o640)
	if _, err := os.Lstat(filepath.Join(backups, "batch")); !os.IsNotExist(err) {
		t.Fatalf("backup was created: %v", err)
	}
}

func TestApplyOperationsRestoresRemovedFileWithoutOverwritingDrift(t *testing.T) {
	for _, drift := range []bool{false, true} {
		t.Run(fmt.Sprintf("drift %t", drift), func(t *testing.T) {
			root, backups := t.TempDir(), t.TempDir()
			writeFile(t, filepath.Join(root, "stale.txt"), []byte("owned"), 0o640)
			deps := defaultApplyDependencies()
			deps.replace = func(root, path string, data []byte, mode fs.FileMode) error {
				if path == "later.txt" {
					if drift {
						if err := atomicfile.Replace(root, "stale.txt", []byte("user drift"), 0o600); err != nil {
							return err
						}
					}
					return errors.New("injected later failure")
				}
				return atomicfile.Replace(root, path, data, mode)
			}
			_, err := applyOperations(deps, root, backups, "batch", []Operation{
				{Remove: &Remove{Path: "stale.txt", ExpectedData: []byte("owned"), ExpectedMode: 0o640}},
				{Write: &Write{Path: "later.txt", Data: []byte("later"), Mode: 0o600}},
			})
			if err == nil || !strings.Contains(err.Error(), "injected later failure") {
				t.Fatalf("ApplyOperations() error = %v", err)
			}
			if drift {
				if !strings.Contains(err.Error(), "caller intervention required") {
					t.Fatalf("ApplyOperations() error = %v", err)
				}
				assertFile(t, filepath.Join(root, "stale.txt"), "user drift", 0o600)
				return
			}
			assertFile(t, filepath.Join(root, "stale.txt"), "owned", 0o640)
		})
	}
}

func TestApplyOperationsRejectsRemovalEvidenceThatDoesNotMatchSnapshot(t *testing.T) {
	for _, removal := range []Remove{
		{Path: "stale.txt", ExpectedData: []byte("other"), ExpectedMode: 0o640},
		{Path: "stale.txt", ExpectedData: []byte("owned"), ExpectedMode: 0o600},
	} {
		root, backups := t.TempDir(), t.TempDir()
		writeFile(t, filepath.Join(root, "stale.txt"), []byte("owned"), 0o640)
		_, err := ApplyOperations(root, backups, "batch", []Operation{{Remove: &removal}})
		if err == nil || !strings.Contains(err.Error(), "evidence does not match snapshot") {
			t.Fatalf("ApplyOperations() error = %v", err)
		}
		assertFile(t, filepath.Join(root, "stale.txt"), "owned", 0o640)
		if err := Verify(backups, "batch"); err != nil {
			t.Fatalf("Verify() after rejected removal = %v", err)
		}
	}
}

func TestApplyOperationsFinalRemovalRefusesModeDrift(t *testing.T) {
	root, backups := t.TempDir(), t.TempDir()
	path := filepath.Join(root, "stale.txt")
	writeFile(t, path, []byte("owned"), 0o640)
	deps := defaultApplyDependencies()
	called := false
	deps.removeIfExact = func(root, path string, data []byte, mode fs.FileMode) error {
		called = true
		if mode != 0o640 {
			t.Fatalf("removal mode = %#o, want %#o", mode, fs.FileMode(0o640))
		}
		if err := os.Chmod(filepath.Join(root, path), 0o600); err != nil {
			return err
		}
		return atomicfile.RemoveIfExact(root, path, data, mode)
	}

	_, err := applyOperations(deps, root, backups, "batch", []Operation{{Remove: &Remove{
		Path: "stale.txt", ExpectedData: []byte("owned"), ExpectedMode: 0o640,
	}}})
	if err == nil || !strings.Contains(err.Error(), "destination mode does not match") {
		t.Fatalf("ApplyOperations() error = %v", err)
	}
	if !called {
		t.Fatal("final removal primitive was not called")
	}
	assertFile(t, path, "owned", 0o600)
}

func TestApplyOperationsRejectsTargetDriftBeforeRemoval(t *testing.T) {
	for _, drift := range []struct {
		data []byte
		mode fs.FileMode
	}{
		{data: []byte("user drift"), mode: 0o640},
		{data: []byte("owned"), mode: 0o600},
	} {
		root, backups := t.TempDir(), t.TempDir()
		writeFile(t, filepath.Join(root, "stale.txt"), []byte("owned"), 0o640)
		deps := defaultApplyDependencies()
		deps.capture = func(root, backupRoot, backupName string, paths []string) (Snapshot, error) {
			snapshot, err := Capture(root, backupRoot, backupName, paths)
			if err != nil {
				return Snapshot{}, err
			}
			return snapshot, atomicfile.Replace(root, "stale.txt", drift.data, drift.mode)
		}
		_, err := applyOperations(deps, root, backups, "batch", []Operation{{Remove: &Remove{
			Path: "stale.txt", ExpectedData: []byte("owned"), ExpectedMode: 0o640,
		}}})
		if err == nil || !strings.Contains(err.Error(), "authorized evidence") {
			t.Fatalf("ApplyOperations() error = %v", err)
		}
		assertFile(t, filepath.Join(root, "stale.txt"), string(drift.data), drift.mode)
	}
}

func TestApplyOperationsPreservesCallerOrderAndRollsBackInReverse(t *testing.T) {
	root, backups := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(root, "stale.txt"), []byte("owned"), 0o640)
	deps := defaultApplyDependencies()
	var calls []string
	deps.replace = func(root, path string, data []byte, mode fs.FileMode) error {
		calls = append(calls, "replace:"+path)
		if err := atomicfile.Replace(root, path, data, mode); err != nil {
			return err
		}
		if path == "later.txt" {
			return errors.New("injected later failure")
		}
		return nil
	}
	deps.removeIfExact = func(root, path string, data []byte, mode fs.FileMode) error {
		calls = append(calls, fmt.Sprintf("remove:%s:%#o", path, mode))
		return atomicfile.RemoveIfExact(root, path, data, mode)
	}
	deps.restoreIfAbsent = func(root, path string, data []byte, mode fs.FileMode) error {
		calls = append(calls, "restore:"+path)
		return restoreIfAbsent(root, path, data, mode)
	}
	_, err := applyOperations(deps, root, backups, "batch", []Operation{
		{Write: &Write{Path: "first.txt", Data: []byte("first"), Mode: 0o600}},
		{Remove: &Remove{Path: "stale.txt", ExpectedData: []byte("owned"), ExpectedMode: 0o640}},
		{Write: &Write{Path: "later.txt", Data: []byte("later"), Mode: 0o600}},
	})
	if err == nil || strings.Join(calls, ",") != "replace:first.txt,remove:stale.txt:0640,replace:later.txt,remove:later.txt:0600,restore:stale.txt,remove:first.txt:0600" {
		t.Fatalf("ApplyOperations() error = %v, calls = %v", err, calls)
	}
	assertFile(t, filepath.Join(root, "stale.txt"), "owned", 0o640)
	if _, err := os.Lstat(filepath.Join(root, "first.txt")); !os.IsNotExist(err) {
		t.Fatalf("first target remains: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(root, "later.txt")); !os.IsNotExist(err) {
		t.Fatalf("later target remains: %v", err)
	}
}

func TestPrepareOperationsRejectsInvalidConditionalEvidence(t *testing.T) {
	for _, tt := range []struct {
		name string
		ops  []Operation
	}{
		{"missing replace bytes", []Operation{{Replace: &Replace{Path: "a.txt", ExpectedMode: 0o600, Mode: 0o600}}}},
		{"missing remove bytes", []Operation{{Remove: &Remove{Path: "a.txt", ExpectedMode: 0o600}}}},
		{"unsupported create mode", []Operation{{Create: &Create{Path: "a.txt", Mode: fs.ModeSetuid | 0o600}}}},
		{"duplicate paths", []Operation{
			{Create: &Create{Path: "a.txt", Mode: 0o600}},
			{Replace: &Replace{Path: "a.txt", ExpectedData: []byte{}, ExpectedMode: 0o600, Mode: 0o600}},
		}},
		{"contradictory actions", []Operation{{
			Create:  &Create{Path: "a.txt", Mode: 0o600},
			Replace: &Replace{Path: "a.txt", ExpectedData: []byte{}, ExpectedMode: 0o600, Mode: 0o600},
		}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := prepareOperations(t.TempDir(), tt.ops); err == nil {
				t.Fatal("prepareOperations() error = nil")
			}
		})
	}
}

func TestApplyOperationsConditionalCreateAndReplace(t *testing.T) {
	for _, tt := range []struct {
		name       string
		operations []Operation
		setup      func(t *testing.T, root string)
		verify     func(t *testing.T, root string)
	}{
		{
			name:       "create",
			operations: []Operation{{Create: &Create{Path: "created.txt", Data: []byte("created"), Mode: 0o600}}},
			verify: func(t *testing.T, root string) {
				assertFile(t, filepath.Join(root, "created.txt"), "created", 0o600)
			},
		},
		{
			name: "replace",
			setup: func(t *testing.T, root string) {
				writeFile(t, filepath.Join(root, "existing.txt"), []byte("original"), 0o640)
			},
			operations: []Operation{{Replace: &Replace{
				Path: "existing.txt", ExpectedData: []byte("original"), ExpectedMode: 0o640,
				Data: []byte("replacement"), Mode: 0o600,
			}}},
			verify: func(t *testing.T, root string) {
				assertFile(t, filepath.Join(root, "existing.txt"), "replacement", 0o600)
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root, backups := t.TempDir(), t.TempDir()
			if tt.setup != nil {
				tt.setup(t, root)
			}
			if _, err := ApplyOperations(root, backups, "batch", tt.operations); err != nil {
				t.Fatal(err)
			}
			tt.verify(t, root)
		})
	}
}

func TestApplyOperationsConditionalCreateRejectsExistingTarget(t *testing.T) {
	root, backups := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(root, "existing.txt"), []byte("existing"), 0o640)
	_, err := ApplyOperations(root, backups, "batch", []Operation{{Create: &Create{
		Path: "existing.txt", Data: []byte("created"), Mode: 0o600,
	}}})
	if err == nil || !strings.Contains(err.Error(), "create evidence") {
		t.Fatalf("ApplyOperations() error = %v", err)
	}
	assertFile(t, filepath.Join(root, "existing.txt"), "existing", 0o640)
}

func TestApplyOperationsConditionalReplaceRejectsPreApplyDrift(t *testing.T) {
	root, backups := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(root, "existing.txt"), []byte("original"), 0o640)
	deps := defaultApplyDependencies()
	deps.capture = func(root, backupRoot, backupName string, paths []string) (Snapshot, error) {
		snapshot, err := Capture(root, backupRoot, backupName, paths)
		if err != nil {
			return Snapshot{}, err
		}
		return snapshot, atomicfile.Replace(root, "existing.txt", []byte("drift"), 0o640)
	}
	_, err := applyOperations(deps, root, backups, "batch", []Operation{{Replace: &Replace{
		Path: "existing.txt", ExpectedData: []byte("original"), ExpectedMode: 0o640,
		Data: []byte("replacement"), Mode: 0o600,
	}}})
	if err == nil || !strings.Contains(err.Error(), "authorized evidence") {
		t.Fatalf("ApplyOperations() error = %v", err)
	}
	assertFile(t, filepath.Join(root, "existing.txt"), "drift", 0o640)
}

func TestApplyOperationsConditionalCreateRollbackPreservesDrift(t *testing.T) {
	root, backups := t.TempDir(), t.TempDir()
	deps := defaultApplyDependencies()
	deps.replace = func(root, path string, data []byte, mode fs.FileMode) error {
		if err := atomicfile.Replace(root, path, data, mode); err != nil {
			return err
		}
		if err := atomicfile.Replace(root, "created.txt", []byte("user drift"), 0o644); err != nil {
			return err
		}
		return errors.New("injected write failure")
	}
	_, err := applyOperations(deps, root, backups, "batch", []Operation{
		{Create: &Create{Path: "created.txt", Data: []byte("created"), Mode: 0o600}},
		{Write: &Write{Path: "later.txt", Data: []byte("later"), Mode: 0o600}},
	})
	if err == nil || !strings.Contains(err.Error(), "caller intervention required") {
		t.Fatalf("ApplyOperations() error = %v", err)
	}
	assertFile(t, filepath.Join(root, "created.txt"), "user drift", 0o644)
	if _, err := os.Lstat(filepath.Join(root, "later.txt")); !os.IsNotExist(err) {
		t.Fatalf("later target remains: %v", err)
	}
}

func TestApplyOperationsConditionalReplaceRollbackPreservesDrift(t *testing.T) {
	root, backups := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(root, "existing.txt"), []byte("original"), 0o640)
	deps := defaultApplyDependencies()
	deps.createIfAbsent = func(root, path string, data []byte, mode fs.FileMode) error {
		if err := atomicfile.CreateIfAbsent(root, path, data, mode); err != nil {
			return err
		}
		if err := atomicfile.Replace(root, "existing.txt", []byte("user drift"), 0o600); err != nil {
			return err
		}
		return errors.New("injected create failure")
	}
	_, err := applyOperations(deps, root, backups, "batch", []Operation{
		{Replace: &Replace{Path: "existing.txt", ExpectedData: []byte("original"), ExpectedMode: 0o640, Data: []byte("replacement"), Mode: 0o600}},
		{Create: &Create{Path: "later.txt", Data: []byte("later"), Mode: 0o600}},
	})
	if err == nil || !strings.Contains(err.Error(), "caller intervention required") {
		t.Fatalf("ApplyOperations() error = %v", err)
	}
	assertFile(t, filepath.Join(root, "existing.txt"), "user drift", 0o600)
	if _, err := os.Lstat(filepath.Join(root, "later.txt")); !os.IsNotExist(err) {
		t.Fatalf("later target remains: %v", err)
	}
}

func TestApplyOperationsConditionalMixedRollbackIsReverse(t *testing.T) {
	root, backups := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(root, "existing.txt"), []byte("original"), 0o640)
	writeFile(t, filepath.Join(root, "stale.txt"), []byte("stale"), 0o640)
	deps := defaultApplyDependencies()
	var calls []string
	deps.createIfAbsent = func(root, path string, data []byte, mode fs.FileMode) error {
		calls = append(calls, "create:"+path)
		if err := atomicfile.CreateIfAbsent(root, path, data, mode); err != nil {
			return err
		}
		if path == "later.txt" {
			return errors.New("injected create failure")
		}
		return nil
	}
	deps.replaceIfMatches = func(root, path string, expected []byte, expectedMode fs.FileMode, data []byte, mode fs.FileMode) error {
		calls = append(calls, "replace:"+path)
		return atomicfile.ReplaceIfMatches(root, path, expected, expectedMode, data, mode)
	}
	deps.removeIfExact = func(root, path string, data []byte, mode fs.FileMode) error {
		calls = append(calls, fmt.Sprintf("remove:%s:%#o", path, mode))
		return atomicfile.RemoveIfExact(root, path, data, mode)
	}
	deps.restoreIfAbsent = func(root, path string, data []byte, mode fs.FileMode) error {
		calls = append(calls, "restore:"+path)
		return restoreIfAbsent(root, path, data, mode)
	}
	_, err := applyOperations(deps, root, backups, "batch", []Operation{
		{Create: &Create{Path: "created.txt", Data: []byte("created"), Mode: 0o600}},
		{Replace: &Replace{Path: "existing.txt", ExpectedData: []byte("original"), ExpectedMode: 0o640, Data: []byte("replacement"), Mode: 0o600}},
		{Remove: &Remove{Path: "stale.txt", ExpectedData: []byte("stale"), ExpectedMode: 0o640}},
		{Create: &Create{Path: "later.txt", Data: []byte("later"), Mode: 0o600}},
	})
	if err == nil || strings.Join(calls, ",") != "create:created.txt,replace:existing.txt,remove:stale.txt:0640,create:later.txt,remove:later.txt:0600,restore:stale.txt,replace:existing.txt,remove:created.txt:0600" {
		t.Fatalf("ApplyOperations() error = %v, calls = %v", err, calls)
	}
	assertFile(t, filepath.Join(root, "existing.txt"), "original", 0o640)
	assertFile(t, filepath.Join(root, "stale.txt"), "stale", 0o640)
	for _, path := range []string{"created.txt", "later.txt"} {
		if _, err := os.Lstat(filepath.Join(root, path)); !os.IsNotExist(err) {
			t.Fatalf("created target remains: %s: %v", path, err)
		}
	}
}

func assertFile(t *testing.T, path, want string, mode fs.FileMode) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil || string(data) != want || fileMode(t, path) != mode {
		t.Fatalf("file %q = %q, %#o, %v", path, data, fileMode(t, path), err)
	}
}

func TestApplyOperationsFinalVerificationRollsBackEmptyCreate(t *testing.T) {
	root, backups := t.TempDir(), t.TempDir()
	path := filepath.Join(root, "created.txt")
	_, err := applyOperationsWithFinalVerification(defaultApplyDependencies(), root, backups, "batch", []Operation{
		{Create: &Create{Path: "created.txt", Data: []byte{}, Mode: 0o600}},
	}, func() error {
		assertFile(t, path, "", 0o600)
		return errors.New("injected final verification failure")
	})
	if err == nil || !strings.Contains(err.Error(), "injected final verification failure") {
		t.Fatalf("final verification error = %v", err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("created target remains: %v", err)
	}
}

func TestApplyOperationsFinalVerificationRollsBackInReverse(t *testing.T) {
	root, backups := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(root, "existing.txt"), []byte("before"), 0o640)
	var verified []string
	_, err := applyOperationsWithFinalVerification(defaultApplyDependencies(), root, backups, "batch", []Operation{
		{Replace: &Replace{Path: "existing.txt", ExpectedData: []byte("before"), ExpectedMode: 0o640, Data: []byte("after"), Mode: 0o600}},
		{Create: &Create{Path: "created.txt", Data: []byte("created"), Mode: 0o600}},
	}, func() error {
		for _, name := range []string{"existing.txt", "created.txt"} {
			verified = append(verified, name)
		}
		assertFile(t, filepath.Join(root, "existing.txt"), "after", 0o600)
		assertFile(t, filepath.Join(root, "created.txt"), "created", 0o600)
		return errors.New("injected final verification failure")
	})
	if err == nil || !strings.Contains(err.Error(), "injected final verification failure") {
		t.Fatalf("final verification error = %v", err)
	}
	if strings.Join(verified, ",") != "existing.txt,created.txt" {
		t.Fatalf("verified = %v", verified)
	}
	assertFile(t, filepath.Join(root, "existing.txt"), "before", 0o640)
	if _, err := os.Lstat(filepath.Join(root, "created.txt")); !os.IsNotExist(err) {
		t.Fatalf("created target remains: %v", err)
	}
}

func TestApplyOperationsWithDirectoriesAndFinalize(t *testing.T) {
	for _, tt := range []struct {
		name            string
		finalVerify     func(t *testing.T, backups, path string) error
		finalize        error
		payload         bool
		finalizeCalls   int
		rollbackTargets bool
		rollbackFile    string
	}{
		{name: "success", finalizeCalls: 1},
		{name: "final verification failure", finalVerify: func(t *testing.T, _, _ string) error { return errors.New("final verify failed") }, rollbackTargets: true},
		{name: "manifest tamper", finalVerify: func(t *testing.T, backups, _ string) error {
			return os.WriteFile(filepath.Join(backups, "batch", manifestName), []byte("{"), 0o600)
		}, rollbackTargets: true},
		{name: "payload tamper", payload: true, finalVerify: func(t *testing.T, backups, path string) error {
			return os.WriteFile(backupPayloadPath(filepath.Join(backups, "batch"), path), []byte("tampered"), 0o600)
		}, rollbackTargets: true, rollbackFile: "original"},
		{name: "finalize failure", finalize: errors.New("finalize failed"), finalizeCalls: 1, rollbackTargets: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root, backups, path := t.TempDir(), t.TempDir(), "generated/file.txt"
			operations := []Operation{{Write: &Write{Path: path, Data: []byte("created"), Mode: 0o600}}}
			if tt.payload {
				path = "existing.txt"
				writeFile(t, filepath.Join(root, path), []byte("original"), 0o640)
				operations = []Operation{{Write: &Write{Path: path, Data: []byte("created"), Mode: 0o600}}}
			}
			var calls int
			var finalized Snapshot
			snapshot, err := ApplyOperationsWithDirectoriesAndFinalize(root, backups, "batch",
				[]Directory{{Path: "generated", Mode: 0o700}}, operations,
				func() error {
					assertFile(t, filepath.Join(root, path), "created", 0o600)
					if tt.finalVerify != nil {
						return tt.finalVerify(t, backups, path)
					}
					return nil
				},
				func(reloaded Snapshot) error {
					calls++
					finalized = reloaded
					return tt.finalize
				},
			)
			if tt.finalizeCalls == 1 && tt.finalize == nil {
				reloaded, reloadErr := reloadAndVerify(backups, "batch")
				if err != nil || reloadErr != nil || !reflect.DeepEqual(finalized, reloaded) || !reflect.DeepEqual(snapshot, reloaded) || finalized.Manifest.Version != manifestV2 || len(finalized.Manifest.AbsentDirectories) != 1 || finalized.Manifest.AbsentDirectories[0].Path != "generated" {
					t.Fatalf("result = %#v, finalized = %#v, reloaded = %#v, errors = %v, %v", snapshot, finalized, reloaded, err, reloadErr)
				}
				return
			}
			if err == nil || calls != tt.finalizeCalls {
				t.Fatalf("error = %v, finalize calls = %d", err, calls)
			}
			if tt.rollbackTargets {
				if tt.rollbackFile == "" {
					if _, statErr := os.Lstat(filepath.Join(root, path)); !os.IsNotExist(statErr) {
						t.Fatalf("created target remains: %v", statErr)
					}
				} else {
					assertFile(t, filepath.Join(root, path), tt.rollbackFile, 0o640)
				}
				if _, statErr := os.Lstat(filepath.Join(root, "generated")); !os.IsNotExist(statErr) {
					t.Fatalf("created directory remains: %v", statErr)
				}
			}
		})
	}
}
