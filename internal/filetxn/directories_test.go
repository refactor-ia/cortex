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
	_, err := ApplyOperationsWithDirectories(root, backups, "batch", []Directory{{Path: "skills", Mode: 0o700}, {Path: "skills/cortex-demo", Mode: 0o700}}, []Operation{{Create: &Create{Path: "skills/cortex-demo/router.md", Data: []byte("router"), Mode: 0o600}}})
	must(t, err)
	assertFile(t, filepath.Join(root, "skills", "cortex-demo", "router.md"), "router", 0o600)
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
func TestApplyOperationsWithDirectoriesMustCreate(t *testing.T) {
	for _, tc := range []struct {
		name     string
		existing bool
	}{
		{name: "absent succeeds"},
		{name: "existing rejects", existing: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, backups := t.TempDir(), t.TempDir()
			if tc.existing {
				must(t, os.Mkdir(filepath.Join(root, "runtime"), 0o700))
			}
			_, err := ApplyOperationsWithDirectories(root, backups, "batch", []Directory{{Path: "runtime", Mode: 0o700, MustCreate: true}}, []Operation{{Create: &Create{Path: "runtime/file", Data: []byte("data"), Mode: 0o600}}})
			if tc.existing {
				if err == nil {
					t.Fatal("ApplyOperationsWithDirectories() error = nil")
				}
				if _, err := os.Lstat(filepath.Join(root, "runtime", "file")); !os.IsNotExist(err) {
					t.Fatalf("operation was applied: %v", err)
				}
				return
			}
			must(t, err)
			assertFile(t, filepath.Join(root, "runtime", "file"), "data", 0o600)
		})
	}
}

func TestApplyOperationsWithDirectoriesMustCreateRejectsRaceBeforeOperations(t *testing.T) {
	root, backups := t.TempDir(), t.TempDir()
	operations := 0
	deps := defaultApplyDependencies()
	deps.createIfAbsent = func(root, path string, data []byte, mode fs.FileMode) error {
		operations++
		return atomicfile.CreateIfAbsent(root, path, data, mode)
	}
	_, err := applyOperationsWithDirectoriesBeforeCreate(deps, root, backups, "batch", []Directory{{Path: "created", Mode: 0o700, MustCreate: true}, {Path: "conflict", Mode: 0o700, MustCreate: true}}, []Operation{{Create: &Create{Path: "created/file", Data: []byte("data"), Mode: 0o600}}}, func(directory preparedDirectory) {
		if directory.path == "conflict" {
			must(t, os.Mkdir(filepath.Join(root, "conflict"), 0o700))
		}
	})
	if err == nil {
		t.Fatal("applyOperationsWithDirectoriesBeforeCreate() error = nil")
	}
	if operations != 0 {
		t.Fatalf("operations applied = %d", operations)
	}
	if _, err := os.Lstat(filepath.Join(root, "created")); !os.IsNotExist(err) {
		t.Fatalf("created directory remains: %v", err)
	}
	if info, err := os.Lstat(filepath.Join(root, "conflict")); err != nil || !isRealDirectory(info) {
		t.Fatalf("racing directory = (%v, %v)", info, err)
	}
}

func TestApplyOperationsWithDirectoriesRollsBackMustCreateDirectories(t *testing.T) {
	home := t.TempDir()
	must(t, os.Mkdir(filepath.Join(home, "existing"), 0o700))
	writeFile(t, filepath.Join(home, "existing", "file"), []byte("old"), 0o600)
	deps := defaultApplyDependencies()
	deps.replace = func(root, path string, data []byte, mode fs.FileMode) error {
		if err := atomicfile.Replace(root, path, data, mode); err != nil {
			return err
		}
		if path == ".cortex/state.json" {
			return errors.New("injected state failure")
		}
		return nil
	}
	_, err := applyOperationsWithDirectories(deps, home, home, ".cortex-backup", []Directory{{Path: "runtime", Mode: 0o700, MustCreate: true}, {Path: ".cortex", Mode: 0o700, MustCreate: true}}, []Operation{{Replace: &Replace{Path: "existing/file", ExpectedData: []byte("old"), ExpectedMode: 0o600, Data: []byte("new"), Mode: 0o600}}, {Write: &Write{Path: ".cortex/state.json", Data: []byte("state"), Mode: 0o600}}})
	if err == nil || !strings.Contains(err.Error(), "injected state failure") {
		t.Fatalf("applyOperationsWithDirectories() error = %v", err)
	}
	assertFile(t, filepath.Join(home, "existing", "file"), "old", 0o600)
	for _, path := range []string{"runtime", ".cortex"} {
		if _, err := os.Lstat(filepath.Join(home, path)); !os.IsNotExist(err) {
			t.Fatalf("transaction-created directory remains: %s: %v", path, err)
		}
	}
	if info, err := os.Lstat(filepath.Join(home, ".cortex-backup")); err != nil || !isRealDirectory(info) {
		t.Fatalf("backup root = (%v, %v)", info, err)
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
