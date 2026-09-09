package modelprofile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestApplySavedSwapsImagesWithoutMutatingBackup(t *testing.T) {
	root, store := tempRoot(t), tempRoot(t)
	path := filepath.Join(root, "settings.json")
	put(t, path, "before", 0o640)
	roots := RuntimeRoots{Pi: root}
	if err := Save(store, roots, []Change{{Target: PiSettings, Before: []byte("before"), BeforeMode: 0o640, After: []byte("after"), AfterMode: 0o600}}); err != nil {
		t.Fatal(err)
	}
	backup, err := Load(store)
	if err != nil {
		t.Fatal(err)
	}
	if err := ApplySaved(roots, backup); err != nil {
		t.Fatal(err)
	}
	want(t, path, "after", 0o600)
	if err := Restore(roots, backup); err != nil {
		t.Fatal(err)
	}
	want(t, path, "before", 0o640)
	data, err := os.ReadFile(filepath.Join(store, backupName))
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Fatal("backup was altered")
	}
}
