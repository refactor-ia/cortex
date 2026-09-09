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

func TestObserveFilesystemRecoveryBuildsCompletePlan(t *testing.T) {
	handle, roots, before := recoveryFilesystemFixture(t)
	plan, err := observeFilesystemRecovery(handle, roots)
	if err != nil || !plan.ready || len(plan.entries) != len(before) {
		t.Fatalf("plan = %#v, %v", plan, err)
	}
	for index, entry := range plan.entries {
		if entry.state != atBefore || !bytes.Equal(entry.before.bytes, before[index]) {
			t.Fatalf("entry %d = %#v", index, entry)
		}
	}
	plan.entries[0].before.bytes[0] = 'X'
	again, err := observeFilesystemRecovery(handle, roots)
	if err != nil || !bytes.Equal(again.entries[0].before.bytes, before[0]) {
		t.Fatalf("detached plan = %#v, %v", again, err)
	}
}

func TestObserveFilesystemRecoveryRejectsUninitializedHandle(t *testing.T) {
	_, roots, _ := recoveryFilesystemFixture(t)
	plan, err := observeFilesystemRecovery(backupjournal.Handle{}, roots)
	if err == nil || plan.ready || len(plan.entries) != 0 {
		t.Fatalf("plan = %#v, error = %v", plan, err)
	}
}

func TestObserveFilesystemRecoveryRejectsTerminalHandlesBeforeObservation(t *testing.T) {
	for _, state := range []backupjournal.State{backupjournal.Committed, backupjournal.Recovered} {
		t.Run(string(state), func(t *testing.T) {
			handle, roots, _, home := recoveryFilesystemFixtureWithHome(t)
			terminal, err := backupjournal.Transition(home, handle.TransactionID(), state)
			if err != nil {
				t.Fatal(err)
			}
			beforeFiles := snapshotRecoveryFiles(t, roots)
			beforeBlobs := snapshotRecoveryBlobs(t, terminal)

			plan, err := observeFilesystemRecovery(terminal, nil)
			if !errors.Is(err, terminalFilesystemError{}) || err.Error() != "backup recovery: terminal journal handle" || plan.ready || len(plan.entries) != 0 {
				t.Fatalf("plan = %#v, error = %v", plan, err)
			}
			assertRecoveryFilesUnchanged(t, roots, beforeFiles)
			assertRecoveryBlobsUnchanged(t, terminal, beforeBlobs)
		})
	}
}

func TestObserveFilesystemRecoveryClassifiesMixedEvidenceWithoutMutation(t *testing.T) {
	handle, roots := mixedRecoveryFilesystemFixture(t)
	manifest, ok := handle.Manifest()
	if !ok {
		t.Fatal("fixture handle has no manifest")
	}
	beforeFiles := snapshotRecoveryFiles(t, roots)
	beforeBlobs := snapshotRecoveryBlobs(t, handle)

	plan, err := observeFilesystemRecovery(handle, roots)
	if err != nil || plan.ready || len(plan.entries) != 3 {
		t.Fatalf("plan = %#v, error = %v", plan, err)
	}
	for index, want := range []classification{atBefore, atAfter, drifted} {
		if plan.entries[index].state != want || plan.entries[index].key != keyFor(manifest.Entries()[index]) {
			t.Fatalf("entry %d = %#v", index, plan.entries[index])
		}
	}
	assertRecoveryFilesUnchanged(t, roots, beforeFiles)
	assertRecoveryBlobsUnchanged(t, handle, beforeBlobs)
}

func TestObserveFilesystemRecoveryRejectsAmbiguousRootEvidence(t *testing.T) {
	handle, roots, _ := recoveryFilesystemFixture(t)
	for _, test := range []struct {
		name  string
		roots func(*testing.T, []filesystemRoot) []filesystemRoot
	}{
		{"missing", func(_ *testing.T, roots []filesystemRoot) []filesystemRoot { return roots[:len(roots)-1] }},
		{"extra", func(_ *testing.T, roots []filesystemRoot) []filesystemRoot { return append(roots, roots[0]) }},
		{"duplicate", func(_ *testing.T, roots []filesystemRoot) []filesystemRoot { roots[1] = roots[0]; return roots }},
		{"mismatched", func(t *testing.T, roots []filesystemRoot) []filesystemRoot {
			t.Helper()
			replacement, err := newFilesystemRoot(roots[0].runtime, roots[0].kind, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			roots[0] = replacement
			return roots
		}},
		{"uninitialized", func(_ *testing.T, roots []filesystemRoot) []filesystemRoot {
			roots[0] = filesystemRoot{runtime: roots[0].runtime, kind: roots[0].kind}
			return roots
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := append([]filesystemRoot(nil), roots...)
			plan, err := observeFilesystemRecovery(handle, test.roots(t, candidate))
			if err == nil || plan.ready || len(plan.entries) != 0 || bytes.Contains([]byte(err.Error()), []byte(roots[0].path)) {
				t.Fatalf("plan = %#v, error = %v", plan, err)
			}
		})
	}
}

func recoveryFilesystemFixture(t *testing.T) (backupjournal.Handle, []filesystemRoot, [][]byte) {
	handle, roots, before, _ := recoveryFilesystemFixtureWithHome(t)
	return handle, roots, before
}

func recoveryFilesystemFixtureWithHome(t *testing.T) (backupjournal.Handle, []filesystemRoot, [][]byte, string) {
	t.Helper()
	roots := make([]filesystemRoot, 0, len(runtimeList))
	bindings := make([]backupjournal.RootBinding, 0, len(runtimeList))
	inputs := make([]backupjournal.RecoverableEntryInput, 0, len(runtimeList))
	blobs := make([]backupjournal.BlobInput, 0, len(runtimeList))
	before := make([][]byte, 0, len(runtimeList))
	for _, runtime := range runtimeList {
		root, err := newFilesystemRoot(runtime, backupjournal.RootKind(runtime), t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		data := []byte(string(runtime) + " before")
		if err := os.WriteFile(filepath.Join(root.path, "config.json"), data, 0600); err != nil {
			t.Fatal(err)
		}
		roots = append(roots, root)
		bindings = append(bindings, root.binding)
		inputs = append(inputs, backupjournal.RecoverableEntryInput{
			Before: backupjournal.EntryInput{Runtime: runtime, Root: backupjournal.RootKind(runtime), RelativePath: "config.json", Existence: backupjournal.Present, Mode: 0600, SHA256: hashBytes(data), Length: int64(len(data))},
			After:  backupjournal.Evidence{Existence: backupjournal.Absent},
		})
		blobs = append(blobs, backupjournal.BlobInput{Runtime: runtime, RelativePath: "config.json", Bytes: data, Mode: 0600})
		before = append(before, append([]byte(nil), data...))
	}
	manifest, err := backupjournal.NewRecoverable(hashBytes([]byte("transaction")), hashBytes([]byte("candidate")), bindings, inputs)
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	if _, err = backupjournal.Create(home, manifest, blobs); err != nil {
		t.Fatal(err)
	}
	handle, err := backupjournal.Open(home, manifest.TransactionID())
	if err != nil {
		t.Fatal(err)
	}
	return handle, roots, before, home
}

func mixedRecoveryFilesystemFixture(t *testing.T) (backupjournal.Handle, []filesystemRoot) {
	t.Helper()
	inputs := make([]backupjournal.RecoverableEntryInput, 0, len(runtimeList))
	bindings := make([]backupjournal.RootBinding, 0, len(runtimeList))
	blobs := make([]backupjournal.BlobInput, 0, len(runtimeList))
	roots := make([]filesystemRoot, 0, len(runtimeList))
	for index, runtime := range runtimeList {
		root, err := newFilesystemRoot(runtime, backupjournal.RootKind(runtime), t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		before, after := backupjournal.Present, backupjournal.Absent
		beforeData := []byte(string(runtime) + " before")
		if index == 0 {
			before, after = backupjournal.Absent, backupjournal.Present
		}
		inputs = append(inputs, backupjournal.RecoverableEntryInput{
			Before: entry(runtime, before, beforeData),
			After:  evidence(after, []byte(string(runtime)+" after")),
		})
		if before == backupjournal.Present {
			blobs = append(blobs, backupjournal.BlobInput{Runtime: runtime, RelativePath: "config.json", Bytes: beforeData, Mode: 0600})
		}
		if index == 2 {
			name := filepath.Join(root.path, "config.json")
			mustNoFilesystemError(t, os.WriteFile(name, beforeData, 0640))
			mustNoFilesystemError(t, os.Chmod(name, 0640))
		}
		roots = append(roots, root)
		bindings = append(bindings, root.binding)
	}
	manifest, err := backupjournal.NewRecoverable(hash("mixed transaction"), hash("mixed candidate"), bindings, inputs)
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	if _, err = backupjournal.Create(home, manifest, blobs); err != nil {
		t.Fatal(err)
	}
	handle, err := backupjournal.Open(home, manifest.TransactionID())
	if err != nil {
		t.Fatal(err)
	}
	return handle, roots
}

type recoveryFileSnapshot struct {
	exists bool
	data   []byte
	mode   os.FileMode
}

func snapshotRecoveryFiles(t *testing.T, roots []filesystemRoot) []recoveryFileSnapshot {
	t.Helper()
	snapshots := make([]recoveryFileSnapshot, len(roots))
	for index, root := range roots {
		name := filepath.Join(root.path, "config.json")
		info, err := os.Lstat(name)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		snapshots[index] = recoveryFileSnapshot{exists: true, data: data, mode: info.Mode()}
	}
	return snapshots
}

func assertRecoveryFilesUnchanged(t *testing.T, roots []filesystemRoot, want []recoveryFileSnapshot) {
	t.Helper()
	if got := snapshotRecoveryFiles(t, roots); len(got) != len(want) {
		t.Fatalf("file snapshot count = %d, want %d", len(got), len(want))
	} else {
		for index := range want {
			if got[index].exists != want[index].exists || !bytes.Equal(got[index].data, want[index].data) || got[index].mode != want[index].mode {
				t.Fatalf("file %d changed: got %#v, want %#v", index, got[index], want[index])
			}
		}
	}
}

func snapshotRecoveryBlobs(t *testing.T, handle backupjournal.Handle) map[entryKey][]byte {
	t.Helper()
	manifest, ok := handle.Manifest()
	if !ok {
		t.Fatal("handle has no manifest")
	}
	blobs := make(map[entryKey][]byte)
	for _, entry := range manifest.Entries() {
		if entry.Existence == backupjournal.Present {
			data, found := handle.Blob(entry.Runtime, entry.RelativePath)
			if !found {
				t.Fatalf("missing blob for %q", entry.RelativePath)
			}
			blobs[keyFor(entry)] = data
		}
	}
	return blobs
}

func assertRecoveryBlobsUnchanged(t *testing.T, handle backupjournal.Handle, want map[entryKey][]byte) {
	t.Helper()
	got := snapshotRecoveryBlobs(t, handle)
	if len(got) != len(want) {
		t.Fatalf("blob count = %d, want %d", len(got), len(want))
	}
	for key, data := range want {
		if !bytes.Equal(got[key], data) {
			t.Fatalf("blob %q changed", key)
		}
	}
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
