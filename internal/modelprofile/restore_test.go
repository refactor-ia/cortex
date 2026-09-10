package modelprofile

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/refactor-ia/cortex/internal/atomicfile"
)

func TestSaveLoadRestore(t *testing.T) {
	pi, open, store := tempRoot(t), tempRoot(t), tempRoot(t)
	put(t, filepath.Join(pi, "settings.json"), "before-settings", 0o640)
	put(t, filepath.Join(pi, "subagents.json"), "untouched", 0o600)
	put(t, filepath.Join(open, "opencode.json"), "before-open", 0o644)
	roots := RuntimeRoots{Pi: pi, OpenCode: open}
	changes := []Change{{Target: PiSettings, Before: []byte("before-settings"), BeforeMode: 0o640, After: []byte("after-settings"), AfterMode: 0o600}, {Target: OpenCodeConfig, Before: []byte("before-open"), BeforeMode: 0o644, After: []byte("after-open"), AfterMode: 0o640}}
	if err := Save(store, roots, changes); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(pi, "settings.json"), "after-settings", 0o600)
	put(t, filepath.Join(open, "opencode.json"), "after-open", 0o640)
	backup, err := Load(store)
	if err != nil {
		t.Fatal(err)
	}
	if err := Restore(roots, backup); err != nil {
		t.Fatal(err)
	}
	want(t, filepath.Join(pi, "settings.json"), "before-settings", 0o640)
	want(t, filepath.Join(open, "opencode.json"), "before-open", 0o644)
	want(t, filepath.Join(pi, "subagents.json"), "untouched", 0o600)
}

func TestRestoreRejectsReplacedRoot(t *testing.T) {
	root, store := tempRoot(t), tempRoot(t)
	put(t, filepath.Join(root, "settings.json"), "before", 0o600)
	roots := RuntimeRoots{Pi: root}
	after := "api_token=keep-private"
	if err := Save(store, roots, []Change{{Target: PiSettings, Before: []byte("before"), BeforeMode: 0o600, After: []byte(after), AfterMode: 0o640}}); err != nil {
		t.Fatal(err)
	}
	backup, err := Load(store)
	if err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(root, "settings.json"), after, 0o640)
	old := root + "-old"
	if err := os.Rename(root, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(root, "settings.json"), after, 0o640)
	err = Restore(roots, backup)
	if err == nil {
		t.Fatal("Restore accepted replaced root")
	}
	if strings.Contains(err.Error(), "keep-private") {
		t.Fatal("Restore exposed config content")
	}
	want(t, filepath.Join(root, "settings.json"), after, 0o640)
}

func TestRestorePreflightAndCompensation(t *testing.T) {
	root, store := tempRoot(t), tempRoot(t)
	put(t, filepath.Join(root, "settings.json"), "one", 0o600)
	put(t, filepath.Join(root, "subagents.json"), "two", 0o600)
	roots := RuntimeRoots{Pi: root}
	changes := []Change{{Target: PiSettings, Before: []byte("one"), BeforeMode: 0o600, After: []byte("ONE"), AfterMode: 0o640}, {Target: PiSubagents, Before: []byte("two"), BeforeMode: 0o600, After: []byte("TWO"), AfterMode: 0o640}}
	if err := Save(store, roots, changes); err != nil {
		t.Fatal(err)
	}
	backup, err := Load(store)
	if err != nil {
		t.Fatal(err)
	}
	other := tempRoot(t)
	put(t, filepath.Join(other, "settings.json"), "ONE", 0o640)
	put(t, filepath.Join(other, "subagents.json"), "TWO", 0o640)
	if err := Restore(RuntimeRoots{Pi: other}, backup); err == nil {
		t.Fatal("Restore accepted a different root")
	}
	want(t, filepath.Join(root, "settings.json"), "one", 0o600)
	want(t, filepath.Join(root, "subagents.json"), "two", 0o600)
	want(t, filepath.Join(other, "settings.json"), "ONE", 0o640)
	want(t, filepath.Join(other, "subagents.json"), "TWO", 0o640)
	put(t, filepath.Join(root, "settings.json"), "ONE", 0o640)
	put(t, filepath.Join(root, "subagents.json"), "drift", 0o640)
	if err := Restore(roots, backup); err == nil {
		t.Fatal("Restore accepted drift")
	}
	want(t, filepath.Join(root, "settings.json"), "ONE", 0o640)
	put(t, filepath.Join(root, "subagents.json"), "TWO", 0o640)
	err = restoreWith(roots, backup, restoreOperations{replace: func(root, leaf string, before []byte, beforeMode fs.FileMode, after []byte, afterMode fs.FileMode) error {
		if leaf == "subagents.json" {
			return errors.New("late failure")
		}
		return atomicfile.ReplaceIfMatches(root, leaf, before, beforeMode, after, afterMode)
	}})
	if err == nil {
		t.Fatal("restoreWith accepted late failure")
	}
	want(t, filepath.Join(root, "settings.json"), "ONE", 0o640)
	old := root + "-swapped"
	err = restoreWith(roots, backup, restoreOperations{replace: func(root, leaf string, before []byte, beforeMode fs.FileMode, after []byte, afterMode fs.FileMode) error {
		err := atomicfile.ReplaceIfMatches(root, leaf, before, beforeMode, after, afterMode)
		if err == nil && leaf == "settings.json" {
			if err := os.Rename(root, old); err != nil {
				return err
			}
			if err := os.Mkdir(root, 0o700); err != nil {
				return err
			}
			put(t, filepath.Join(root, "settings.json"), "ONE", 0o640)
			put(t, filepath.Join(root, "subagents.json"), "TWO", 0o640)
		}
		return err
	}})
	if err == nil {
		t.Fatal("restore accepted root swap between leaves")
	}
	if !errors.Is(err, ErrInterventionRequired) {
		t.Fatalf("restore root-swap error = %v, want intervention signal", err)
	}
	want(t, filepath.Join(root, "settings.json"), "ONE", 0o640)
	want(t, filepath.Join(root, "subagents.json"), "TWO", 0o640)
}

func TestEmptyAndInvalidRecords(t *testing.T) {
	root, store := tempRoot(t), tempRoot(t)
	put(t, filepath.Join(root, "settings.json"), "before", 0o600)
	roots := RuntimeRoots{Pi: root}
	if err := Save(store, roots, []Change{{Target: PiSettings, Before: []byte("before"), BeforeMode: 0o600, After: nil, AfterMode: 0o600}}); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(root, "settings.json"), "", 0o600)
	backup, err := Load(store)
	if err != nil {
		t.Fatal(err)
	}
	if err := Restore(roots, backup); err != nil {
		t.Fatal(err)
	}
	want(t, filepath.Join(root, "settings.json"), "before", 0o600)
	if err := Save(tempRoot(t), roots, []Change{{Target: Target("../x"), Before: []byte("before"), BeforeMode: 0o600, After: []byte("x"), AfterMode: 0o600}}); err == nil {
		t.Fatal("unsafe target accepted")
	}
	base := tempRoot(t)
	real := filepath.Join(base, "real", "root")
	if err := os.MkdirAll(real, 0o700); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(real, "settings.json"), "before", 0o600)
	if err := os.Symlink(filepath.Join(base, "real"), filepath.Join(base, "link")); err != nil {
		t.Fatal(err)
	}
	if err := Save(tempRoot(t), RuntimeRoots{Pi: filepath.Join(base, "link", "root")}, []Change{{Target: PiSettings, Before: []byte("before"), BeforeMode: 0o600, After: []byte("x"), AfterMode: 0o600}}); err == nil {
		t.Fatal("ancestor symlink accepted")
	}
	if err := Save(tempRoot(t), roots, []Change{{Target: PiSettings, Before: []byte("before"), BeforeMode: 0o600, After: []byte("x"), AfterMode: 0o600}, {Target: PiSettings, Before: []byte("before"), BeforeMode: 0o600, After: []byte("y"), AfterMode: 0o600}}); err == nil {
		t.Fatal("duplicate target accepted")
	}
	if _, err := Load(tempRoot(t)); err == nil {
		t.Fatal("missing record accepted")
	}
	bad := tempRoot(t)
	put(t, filepath.Join(bad, backupName), "not json", 0o600)
	if _, err := Load(bad); err == nil {
		t.Fatal("corrupt record accepted")
	}
	data, err := os.ReadFile(filepath.Join(store, backupName))
	if err != nil {
		t.Fatal(err)
	}
	var stored diskBackup
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatal(err)
	}
	stored.Entries[0].Root = ""
	data, err = json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(store, backupName), string(data), 0o600)
	if _, err := Load(store); err == nil {
		t.Fatal("missing root binding accepted")
	}
}

func tempRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func put(t *testing.T, path, data string, mode fs.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}
func want(t *testing.T, path, data string, mode fs.FileMode) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != data || info.Mode().Perm() != mode {
		t.Fatalf("%s contents or mode differ", path)
	}
}
