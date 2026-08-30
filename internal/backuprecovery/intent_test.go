package backuprecovery

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/refactor-ia/cortex/internal/backupjournal"
	"github.com/refactor-ia/cortex/internal/filetxn"
)

func TestPlanRestoreIntentActionsAndDetachment(t *testing.T) {
	tests := []struct {
		name          string
		before, after backupjournal.Existence
		current       string
		want          string
	}{
		{"no-op", backupjournal.Present, backupjournal.Absent, "before", ""},
		{"remove", backupjournal.Absent, backupjournal.Present, "after", "remove"},
		{"create", backupjournal.Present, backupjournal.Absent, "after", "create"},
		{"replace", backupjournal.Present, backupjournal.Present, "after", "replace"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := fixture(t, test.before, test.after)
			current := value.before
			if test.current == "after" {
				current = value.after
			}
			plan := assertPlan(t, value, current, map[string]classification{"before": atBefore, "after": atAfter}[test.current], true)
			intent, err := planRestoreIntent(plan)
			if err != nil || !intent.planned {
				t.Fatalf("intent = %#v, error = %v", intent, err)
			}
			wantCount := 3
			if test.want == "" {
				wantCount = 0
			}
			if len(intent.operations) != wantCount {
				t.Fatalf("operation count = %d, want %d", len(intent.operations), wantCount)
			}
			for index, operation := range intent.operations {
				if operation.runtime != runtimeList[index] {
					t.Fatalf("runtime %d = %q, want %q", index, operation.runtime, runtimeList[index])
				}
				assertRestoreOperation(t, operation.operation, test.want)
			}
			if len(current[0].bytes) != 0 {
				current[0].bytes[0] = 'X'
				if len(plan.entries[0].current.bytes) == 0 || plan.entries[0].current.bytes[0] == 'X' {
					t.Fatal("plan retained mutable current evidence")
				}
			}
			if len(intent.operations) != 0 {
				if len(plan.entries[0].before.bytes) != 0 {
					plan.entries[0].before.bytes[0] = 'Y'
				}
				if len(plan.entries[0].current.bytes) != 0 {
					plan.entries[0].current.bytes[0] = 'Z'
				}
				assertRestoreOperation(t, intent.operations[0].operation, test.want)
			}
		})
	}
}

func TestPlanRestoreIntentPreservesZeroBytePreconditions(t *testing.T) {
	value := fixture(t, backupjournal.Present, backupjournal.Present, []byte("before"), []byte{})
	plan := assertPlan(t, value, value.after, atAfter, true)
	intent, err := planRestoreIntent(plan)
	if err != nil || len(intent.operations) != 3 || intent.operations[0].operation.Replace == nil || intent.operations[0].operation.Replace.ExpectedData == nil || len(intent.operations[0].operation.Replace.ExpectedData) != 0 {
		t.Fatalf("intent = %#v, error = %v", intent, err)
	}
}

func TestPlanRestoreIntentRejectsInvalidModelsWithoutPartialOutput(t *testing.T) {
	tests := []struct {
		name   string
		change func(*recoveryPlan)
	}{
		{"not ready", func(plan *recoveryPlan) { plan.ready = false }},
		{"drifted", func(plan *recoveryPlan) { plan.entries[2].state = drifted }},
		{"unsafe", func(plan *recoveryPlan) { plan.entries[2].state = unsafe }},
		{"missing runtime", func(plan *recoveryPlan) { plan.entries = plan.entries[:2] }},
		{"reordered", func(plan *recoveryPlan) { plan.entries[0], plan.entries[1] = plan.entries[1], plan.entries[0] }},
		{"duplicate", func(plan *recoveryPlan) {
			plan.entries = append(plan.entries[:1], append([]recoveryEntry{plan.entries[0]}, plan.entries[1:]...)...)
		}},
		{"overlap", func(plan *recoveryPlan) {
			nested := plan.entries[0]
			rekeyRecoveryEntry(&nested, nested.runtime, nested.relativePath+"/child")
			plan.entries = append(plan.entries[:1], append([]recoveryEntry{nested}, plan.entries[1:]...)...)
		}},
		{"bad key", func(plan *recoveryPlan) { plan.entries[2].key = "bad" }},
		{"unsafe path", func(plan *recoveryPlan) {
			rekeyRecoveryEntry(&plan.entries[2], plan.entries[2].runtime, "../config.json")
		}},
		{"malformed current", func(plan *recoveryPlan) { plan.entries[2].current.bytes[0] = 'X' }},
		{"inconsistent state", func(plan *recoveryPlan) { plan.entries[2].state = atBefore }},
		{"unexpected after bytes", func(plan *recoveryPlan) { plan.entries[2].after.bytes = []byte("after") }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := validRestorePlan(t)
			test.change(&plan)
			intent, err := planRestoreIntent(plan)
			if !errors.Is(err, invalidModelError{}) || intent.planned || len(intent.operations) != 0 {
				t.Fatalf("intent = %#v, error = %v", intent, err)
			}
		})
	}
}

func TestPlanRestoreIntentDoesNotMutateFilesystemOrJournal(t *testing.T) {
	handle, roots, _ := recoveryFilesystemFixture(t)
	for _, root := range roots {
		if err := os.Remove(filepath.Join(root.path, "config.json")); err != nil {
			t.Fatal(err)
		}
	}
	beforeFiles := snapshotRecoveryFiles(t, roots)
	beforeBlobs := snapshotRecoveryBlobs(t, handle)
	plan, err := observeFilesystemRecovery(handle, roots)
	if err != nil {
		t.Fatal(err)
	}
	intent, err := planRestoreIntent(plan)
	if err != nil || !intent.planned || len(intent.operations) != 3 {
		t.Fatalf("intent = %#v, error = %v", intent, err)
	}
	assertRecoveryFilesUnchanged(t, roots, beforeFiles)
	assertRecoveryBlobsUnchanged(t, handle, beforeBlobs)
	manifest, ok := handle.Manifest()
	if !ok || manifest.State() != backupjournal.Prepared {
		t.Fatalf("journal state = %q, present = %v", manifest.State(), ok)
	}
}

func validRestorePlan(t *testing.T) recoveryPlan {
	t.Helper()
	value := fixture(t, backupjournal.Present, backupjournal.Present)
	return assertPlan(t, value, value.after, atAfter, true)
}

func rekeyRecoveryEntry(entry *recoveryEntry, runtime backupjournal.Runtime, relativePath string) {
	entry.runtime, entry.relativePath = runtime, relativePath
	entry.key = entryKey(string(runtime) + "\x00" + relativePath)
	entry.before.key, entry.after.key, entry.current.key = entry.key, entry.key, entry.key
}

func assertRestoreOperation(t *testing.T, operation filetxn.Operation, kind string) {
	t.Helper()
	if kind == "remove" && operation.Remove != nil && operation.Remove.Path == "config.json" && operation.Remove.ExpectedMode == 0600 && bytes.Equal(operation.Remove.ExpectedData, []byte("after")) {
		return
	}
	if kind == "create" && operation.Create != nil && operation.Create.Path == "config.json" && operation.Create.Mode == 0600 && bytes.Equal(operation.Create.Data, []byte("before")) {
		return
	}
	if kind == "replace" && operation.Replace != nil && operation.Replace.Path == "config.json" && operation.Replace.ExpectedMode == 0600 && operation.Replace.Mode == 0600 && bytes.Equal(operation.Replace.ExpectedData, []byte("after")) && bytes.Equal(operation.Replace.Data, []byte("before")) {
		return
	}
	t.Fatalf("operation = %#v, want %s", operation, kind)
}
