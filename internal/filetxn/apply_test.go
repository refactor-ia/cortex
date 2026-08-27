package filetxn

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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
	deps.removeIfMatches = func(root, path string, data []byte) error {
		removals = append(removals, path)
		return atomicfile.RemoveIfMatches(root, path, data)
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

func assertFile(t *testing.T, path, want string, mode fs.FileMode) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil || string(data) != want || fileMode(t, path) != mode {
		t.Fatalf("file %q = %q, %#o, %v", path, data, fileMode(t, path), err)
	}
}
