package filetxn

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/refactor-ia/cortex/internal/atomicfile"
)

func TestApplyOperationsWithDirectoriesRollsBackCreatedDirectories(t *testing.T) {
	for _, populated := range []bool{false, true} {
		t.Run("concurrent population "+strconv.FormatBool(populated), func(t *testing.T) {
			root, backups := t.TempDir(), t.TempDir()
			must(t, os.Mkdir(filepath.Join(root, "skills"), 0o700))
			deps := defaultApplyDependencies()
			deps.replace = func(root, path string, data []byte, mode fs.FileMode) error {
				if err := atomicfile.Replace(root, path, data, mode); err != nil {
					return err
				}
				if path == ".cortex/state.json" {
					if populated {
						writeFile(t, filepath.Join(root, "skills", "cortex-demo", "concurrent.txt"), []byte("keep"), 0o600)
					}
					return errors.New("injected late file failure")
				}
				return nil
			}
			_, err := applyOperationsWithDirectories(deps, root, backups, "batch", []Directory{{Path: "skills", Mode: 0o700}, {Path: "skills/cortex-demo", Mode: 0o700}, {Path: "generated", Mode: 0o700}, {Path: "generated/nested", Mode: 0o700}, {Path: ".cortex", Mode: 0o700}}, []Operation{{Write: &Write{Path: "generated/nested/router.md", Data: []byte("router"), Mode: 0o600}}, {Write: &Write{Path: ".cortex/state.json", Data: []byte("state"), Mode: 0o600}}})
			if err == nil || !strings.Contains(err.Error(), "injected late file failure") {
				t.Fatalf("applyOperationsWithDirectories() error = %v", err)
			}
			if info, err := os.Lstat(filepath.Join(root, "skills")); err != nil || !isRealDirectory(info) {
				t.Fatal("pre-existing directory was removed")
			}
			for _, path := range []string{"generated", "generated/nested", ".cortex"} {
				if _, err := os.Lstat(filepath.Join(root, path)); !os.IsNotExist(err) {
					t.Fatalf("created directory remains: %s: %v", path, err)
				}
			}
			cortexDir := filepath.Join(root, "skills", "cortex-demo")
			if populated {
				assertFile(t, filepath.Join(cortexDir, "concurrent.txt"), "keep", 0o600)
				if !strings.Contains(err.Error(), "caller intervention required") {
					t.Fatalf("concurrent population error = %v", err)
				}
			} else if _, err := os.Lstat(cortexDir); !os.IsNotExist(err) {
				t.Fatalf("empty created directory remains: %v", err)
			}
		})
	}
}
func TestApplyOperationsWithDirectoriesCreatesNestedDirectories(t *testing.T) {
	root, backups := t.TempDir(), t.TempDir()
	snapshot, err := ApplyOperationsWithDirectories(root, backups, "batch", []Directory{{Path: "skills", Mode: 0o700}, {Path: "skills/cortex-demo", Mode: 0o700}}, []Operation{{Create: &Create{Path: "skills/cortex-demo/router.md", Data: []byte("router"), Mode: 0o600}}})
	must(t, err)
	assertFile(t, filepath.Join(root, "skills", "cortex-demo", "router.md"), "router", 0o600)
	snapshot, err = ApplyOperationsWithDirectories(root, backups, "existing", []Directory{{Path: "skills", Mode: 0o700}, {Path: "skills/cortex-demo", Mode: 0o700}}, []Operation{{Create: &Create{Path: "skills/cortex-demo/again.md", Data: []byte("again"), Mode: 0o600}}})
	must(t, err)
	if snapshot.Manifest.Version != manifestVersion {
		t.Fatalf("all-existing snapshot version = %d", snapshot.Manifest.Version)
	}
}
func TestApplyOperationsWithDirectoriesRejectsInvalidTargetsBeforeMutation(t *testing.T) {
	for _, setup := range []func(t *testing.T, root string){
		func(t *testing.T, root string) { must(t, os.Symlink("outside", filepath.Join(root, "skills"))) },
		func(t *testing.T, root string) { writeFile(t, filepath.Join(root, "skills"), []byte("file"), 0o600) },
	} {
		root, backups := t.TempDir(), t.TempDir()
		setup(t, root)
		_, err := ApplyOperationsWithDirectories(root, backups, "batch", []Directory{{Path: "skills", Mode: 0o700}}, []Operation{{Create: &Create{Path: "skills/router.md", Mode: 0o600}}})
		if err == nil {
			t.Fatal("ApplyOperationsWithDirectories() error = nil")
		}
		if _, err := os.Lstat(filepath.Join(backups, "batch")); !os.IsNotExist(err) {
			t.Fatalf("backup was created: %v", err)
		}
	}
}
func TestApplyOperationsWithDirectoriesRejectsPreflightMutations(t *testing.T) {
	cases := []struct {
		name, absent string
		setup        func(string)
		directories  []Directory
		operations   []Operation
	}{
		{"empty operations", "created", nil, []Directory{{Path: "created", Mode: 0o700}}, nil},
		{"transitive ancestor out of order", "a/middle/leaf", func(root string) { must(t, os.MkdirAll(filepath.Join(root, "a", "middle"), 0o700)) }, []Directory{{Path: "a/middle/leaf", Mode: 0o700}, {Path: "a", Mode: 0o700}}, []Operation{{Create: &Create{Path: "a/middle/leaf/file", Mode: 0o600}}}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			root, backups := t.TempDir(), t.TempDir()
			if tt.setup != nil {
				tt.setup(root)
			}
			must(t, os.Chtimes(root, time.Unix(1, 0), time.Unix(1, 0)))
			before, err := os.Stat(root)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ApplyOperationsWithDirectories(root, backups, "batch", tt.directories, tt.operations); err == nil {
				t.Fatal("ApplyOperationsWithDirectories() error = nil")
			}
			if after, err := os.Stat(root); err != nil || !after.ModTime().Equal(before.ModTime()) {
				t.Fatalf("root was mutated: %v", err)
			}
			if _, err := os.Lstat(filepath.Join(root, tt.absent)); !os.IsNotExist(err) {
				t.Fatalf("mutation remains: %v", err)
			}
			if _, err := os.Lstat(filepath.Join(backups, "batch")); !os.IsNotExist(err) {
				t.Fatalf("backup was created: %v", err)
			}
		})
	}
}

func TestApplyOperationsWithDirectoriesPreimage(t *testing.T) {
	root, backups := t.TempDir(), t.TempDir()
	deps, captures := defaultApplyDependencies(), 0
	deps.captureDirectoryPreimage = func(source, backup, name string, paths []string, absent []Directory) (Snapshot, error) {
		captures++
		if _, err := os.Lstat(filepath.Join(root, "made")); !os.IsNotExist(err) {
			t.Fatalf("mkdir preceded capture: %v", err)
		}
		return captureWithDirectoryPreimage(source, backup, name, paths, absent)
	}
	snapshot, err := applyOperationsWithDirectories(deps, root, backups, "batch", []Directory{{Path: "made", Mode: 0o700}, {Path: "made/nested", Mode: 0o750}}, []Operation{{Create: &Create{Path: "made/nested/file", Data: []byte("new"), Mode: 0o600}}})
	must(t, err)
	if captures != 1 || snapshot.Manifest.Version != manifestV2 || len(snapshot.Manifest.AbsentDirectories) != 2 {
		t.Fatalf("captures = %d, snapshot = %#v", captures, snapshot.Manifest)
	}
}

func TestApplyOperationsWithDirectoriesPreimageRefusesChanges(t *testing.T) {
	for _, name := range []string{"capture failure", "mkdir race", "existing drift"} {
		t.Run(name, func(t *testing.T) {
			root, backups := t.TempDir(), t.TempDir()
			directories := []Directory{{Path: "made", Mode: 0o700}}
			if name == "existing drift" {
				must(t, os.Mkdir(filepath.Join(root, "stable"), 0o700))
				directories = append([]Directory{{Path: "stable", Mode: 0o700}}, directories...)
			}
			deps := defaultApplyDependencies()
			deps.captureDirectoryPreimage = func(source, backup, batch string, paths []string, absent []Directory) (Snapshot, error) {
				if name == "capture failure" {
					return Snapshot{}, errors.New("injected capture failure")
				}
				snapshot, err := captureWithDirectoryPreimage(source, backup, batch, paths, absent)
				if err != nil {
					return Snapshot{}, err
				}
				if name == "mkdir race" {
					must(t, os.Mkdir(filepath.Join(root, "made"), 0o700))
				} else {
					if len(absent) != 1 || absent[0].Path != "made" {
						t.Fatalf("preimage directories = %#v", absent)
					}
					must(t, os.Remove(filepath.Join(root, "stable")))
					must(t, os.Mkdir(filepath.Join(root, "stable"), 0o700))
				}
				return snapshot, nil
			}
			_, err := applyOperationsWithDirectories(deps, root, backups, "batch", directories, []Operation{{Create: &Create{Path: "made/file", Data: []byte("new"), Mode: 0o600}}})
			if err == nil {
				t.Fatal("applyOperationsWithDirectories() error = nil")
			}
			if name == "mkdir race" {
				if info, statErr := os.Lstat(filepath.Join(root, "made")); statErr != nil || !isRealDirectory(info) {
					t.Fatalf("raced directory = %v", statErr)
				}
				return
			}
			if _, statErr := os.Lstat(filepath.Join(root, "made")); !os.IsNotExist(statErr) {
				t.Fatalf("owned directory remains: %v", statErr)
			}
			if name == "existing drift" && !strings.Contains(err.Error(), "existing directory changed") {
				t.Fatalf("drift error = %v", err)
			}
		})
	}
}

func TestApplyOperationsWithDirectoriesFinalVerificationRollbackPreservesReplacement(t *testing.T) {
	for _, replacement := range []bool{false, true} {
		t.Run(strconv.FormatBool(replacement), func(t *testing.T) {
			root, backups := t.TempDir(), t.TempDir()
			deps := defaultApplyDependencies()
			deps.finalVerify = func() error {
				if replacement {
					must(t, os.Remove(filepath.Join(root, "owned", "child")))
					must(t, os.Remove(filepath.Join(root, "owned")))
					must(t, os.Mkdir(filepath.Join(root, "owned"), 0o700))
				}
				return errors.New("injected final verification failure")
			}
			_, err := applyOperationsWithDirectories(deps, root, backups, "batch", []Directory{{Path: "owned", Mode: 0o700}, {Path: "owned/child", Mode: 0o700}}, []Operation{{Create: &Create{Path: "outside", Data: []byte("new"), Mode: 0o600}}})
			if err == nil || (replacement && !strings.Contains(err.Error(), "caller intervention required")) {
				t.Fatalf("applyOperationsWithDirectories() error = %v", err)
			}
			if _, statErr := os.Lstat(filepath.Join(root, "owned")); replacement != (statErr == nil) {
				t.Fatalf("replacement = %t, directory error = %v", replacement, statErr)
			}
		})
	}
}
