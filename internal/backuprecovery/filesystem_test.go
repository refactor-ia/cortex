package backuprecovery

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/refactor-ia/cortex/internal/backupjournal"
)

func TestFilesystemReadsCurrentEvidence(t *testing.T) {
	root := filesystemRootForTest(t)
	for _, test := range []struct {
		name string
		path string
		data []byte
		mode os.FileMode
		want currentState
	}{
		{"missing", "missing", nil, 0, currentAbsent},
		{"empty", "empty", nil, 0600, currentPresent},
		{"nonempty", "nested/value", []byte("current"), 0644, currentPresent},
		{"unexpected mode is evidence", "mode", []byte("x"), 0640, currentPresent},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.mode != 0 {
				name := filepath.Join(root.path, test.path)
				if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(name, test.data, test.mode); err != nil {
					t.Fatal(err)
				}
			}
			name := filepath.Join(root.path, test.path)
			before, readErr := os.ReadFile(name)
			beforeInfo, statErr := os.Lstat(name)
			beforeTree, treeErr := os.ReadDir(root.path)
			got, err := readFilesystemEntry(filesystemEntry(test.path), root, defaultFilesystemReadOptions(), osFilesystemOperations())
			if err != nil || got.state != test.want {
				t.Fatalf("read = %#v, %v", got, err)
			}
			if test.want == currentPresent && (!bytes.Equal(got.bytes, test.data) || got.mode != uint32(test.mode) || got.length != int64(len(test.data)) || got.hash != hashBytes(test.data)) {
				t.Fatalf("evidence = %#v", got)
			}
			if test.path == "nested/value" {
				got.bytes[0] = 'X'
				after, afterErr := os.ReadFile(name)
				afterInfo, afterStatErr := os.Lstat(name)
				afterTree, afterTreeErr := os.ReadDir(root.path)
				if readErr != nil || statErr != nil || treeErr != nil || afterErr != nil || afterStatErr != nil || afterTreeErr != nil || !bytes.Equal(before, after) || beforeInfo.Mode() != afterInfo.Mode() || beforeInfo.Size() != afterInfo.Size() || len(beforeTree) != len(afterTree) || beforeTree[0].Name() != afterTree[0].Name() {
					t.Fatal("observer mutated file or tree")
				}
			}
		})
	}
}

func TestFilesystemRejectsUnsafeAndInvalidInputs(t *testing.T) {
	root := filesystemRootForTest(t)
	file := filepath.Join(root.path, "file")
	if err := os.WriteFile(file, []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(file, filepath.Join(root.path, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root.path, "directory"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		entry   backupjournal.Entry
		bound   filesystemRoot
		opts    filesystemReadOptions
		wantErr bool
	}{
		{"symlink", filesystemEntry("link"), root, defaultFilesystemReadOptions(), false},
		{"nonregular", filesystemEntry("directory"), root, defaultFilesystemReadOptions(), false},
		{"runtime root mismatch", backupjournal.Entry{Runtime: backupjournal.RuntimeClaude, Root: backupjournal.RootClaude, RelativePath: "file"}, root, defaultFilesystemReadOptions(), true},
		{"traversal", filesystemEntry("../file"), root, defaultFilesystemReadOptions(), true},
		{"zero options", filesystemEntry("file"), root, filesystemReadOptions{}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := readFilesystemEntry(test.entry, test.bound, test.opts, osFilesystemOperations())
			if test.wantErr {
				if err == nil {
					t.Fatal("accepted invalid input")
				}
				return
			}
			if err != nil || got.state != currentUnsafe {
				t.Fatalf("read = %#v, %v", got, err)
			}
		})
	}
}

func TestFilesystemBoundsReadErrorsAndDriftAreUnsafe(t *testing.T) {
	root := filesystemRootForTest(t)
	name := filepath.Join(root.path, "file")
	if err := os.WriteFile(name, []byte("oversize"), 0600); err != nil {
		t.Fatal(err)
	}
	options := defaultFilesystemReadOptions()
	options.maxBytes = 1
	got, err := readFilesystemEntry(filesystemEntry("file"), root, options, osFilesystemOperations())
	if err != nil || got.state != currentUnsafe {
		t.Fatalf("oversize = %#v, %v", got, err)
	}
	for _, test := range []struct {
		name       string
		path       string
		options    filesystemReadOptions
		operations filesystemOperations
	}{
		{"read error", "file", defaultFilesystemReadOptions(), filesystemOperations{open: func(root *os.Root, path string) (filesystemFile, error) {
			file, err := root.Open(path)
			return failReadFilesystemFile{file}, err
		}}},
		{"truncation", "file", filesystemReadOptions{maxBytes: 1}, filesystemOperations{open: func(root *os.Root, path string) (filesystemFile, error) {
			file, err := root.Open(path)
			return shortStatFilesystemFile{file}, err
		}}},
		{"R3-unstable-content", "file", defaultFilesystemReadOptions(), filesystemOperations{open: osFilesystemOperations().open, afterRead: func() error { return os.WriteFile(name, []byte("changed!"), 0600) }}},
		{"identity drift", "file", defaultFilesystemReadOptions(), filesystemOperations{open: osFilesystemOperations().open, afterLeafLstat: func() error { return os.Remove(name) }}},
		{"R3-absent-root-drift", "missing", defaultFilesystemReadOptions(), filesystemOperations{open: osFilesystemOperations().open, afterLeafLstat: func() error {
			mustNoFilesystemError(t, os.Rename(root.path, root.path+"-old"))
			return os.Mkdir(root.path, 0700)
		}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := readFilesystemEntry(filesystemEntry(test.path), root, test.options, test.operations)
			if err != nil || got.state != currentUnsafe {
				t.Fatalf("read = %#v, %v", got, err)
			}
		})
	}
}

func TestFilesystemRejectsR3ParentSymlinkRace(t *testing.T) {
	root := filesystemRootForTest(t)
	parent := filepath.Join(root.path, "nested")
	mustNoFilesystemError(t, os.Mkdir(parent, 0700))
	mustNoFilesystemError(t, os.WriteFile(filepath.Join(parent, "value"), []byte("inside"), 0600))
	external := t.TempDir()
	mustNoFilesystemError(t, os.Link(filepath.Join(parent, "value"), filepath.Join(external, "value")))
	got, err := readFilesystemEntry(filesystemEntry("nested/value"), root, defaultFilesystemReadOptions(), filesystemOperations{open: func(descriptor *os.Root, name string) (filesystemFile, error) {
		mustNoFilesystemError(t, os.Rename(parent, parent+"-old"))
		mustNoFilesystemError(t, os.Symlink(external, parent))
		return descriptor.Open(name)
	}})
	if err != nil || got.state != currentUnsafe {
		t.Fatalf("read = %#v, %v", got, err)
	}
}
func TestFilesystemRootRequiresCanonicalRealDirectory(t *testing.T) {
	base := t.TempDir()
	if err := os.Symlink(base, filepath.Join(base, "link")); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		runtime backupjournal.Runtime
		kind    backupjournal.RootKind
		root    string
	}{
		{"runtime root mismatch", backupjournal.RuntimePi, backupjournal.RootClaude, base},
		{"unclean", backupjournal.RuntimePi, backupjournal.RootPi, base + "/."},
		{"missing", backupjournal.RuntimePi, backupjournal.RootPi, filepath.Join(base, "missing")},
		{"leaf symlink", backupjournal.RuntimePi, backupjournal.RootPi, filepath.Join(base, "link")},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := newFilesystemRoot(test.runtime, test.kind, test.root); err == nil || bytes.Contains([]byte(err.Error()), []byte(base)) {
				t.Fatalf("root error = %v", err)
			}
		})
	}
}

func TestFilesystemConcurrentReads(t *testing.T) {
	root := filesystemRootForTest(t)
	if err := os.WriteFile(filepath.Join(root.path, "file"), []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	for range 32 {
		group.Add(1)
		go func() {
			defer group.Done()
			got, err := readFilesystemEntry(filesystemEntry("file"), root, defaultFilesystemReadOptions(), osFilesystemOperations())
			if err != nil || got.state != currentPresent {
				t.Errorf("read = %#v, %v", got, err)
			}
		}()
	}
	group.Wait()
}

func filesystemRootForTest(t *testing.T) filesystemRoot {
	t.Helper()
	root, err := newFilesystemRoot(backupjournal.RuntimePi, backupjournal.RootPi, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return root
}
func filesystemEntry(relative string) backupjournal.Entry {
	return backupjournal.Entry{Runtime: backupjournal.RuntimePi, Root: backupjournal.RootPi, RelativePath: relative}
}
func mustNoFilesystemError(t *testing.T, err error) {
	if err != nil {
		t.Fatal(err)
	}
}

type failReadFilesystemFile struct{ filesystemFile }

func (failReadFilesystemFile) Read([]byte) (int, error) { return 0, errors.New("read failure") }

type shortStatFilesystemFile struct{ filesystemFile }

func (file shortStatFilesystemFile) Stat() (os.FileInfo, error) {
	info, err := file.filesystemFile.Stat()
	return shortSizeFileInfo{FileInfo: info}, err
}

type shortSizeFileInfo struct{ os.FileInfo }

func (shortSizeFileInfo) Size() int64 { return 0 }
