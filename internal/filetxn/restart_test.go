package filetxn

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestOpenRejectsInvalidOrTamperedSnapshots(t *testing.T) {
	for _, damage := range []string{"manifest", "payload"} {
		t.Run(damage, func(t *testing.T) {
			root, backups := t.TempDir(), t.TempDir()
			writeFile(t, filepath.Join(root, "file.txt"), []byte("original"), 0o600)
			snapshot, err := Capture(root, backups, "backup", []string{"file.txt"})
			must(t, err)
			if damage == "manifest" {
				must(t, os.WriteFile(filepath.Join(snapshot.Dir, manifestName), []byte(`{"version":1,"entries":[],"invalid":true}`), 0o600))
			} else {
				must(t, os.WriteFile(backupPayloadPath(snapshot.Dir, "file.txt"), []byte("changed"), 0o600))
			}
			if _, err := Open(backups, "backup"); err == nil || strings.Contains(err.Error(), "file.txt") {
				t.Fatalf("Open() error = %v, want generic failure", err)
			}
		})
	}
}

func TestSnapshotPayloadRequiresVerifiedExactEvidence(t *testing.T) {
	root, backups := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(root, "file.txt"), []byte("original"), 0o600)
	_, err := Capture(root, backups, "backup", []string{"file.txt", "missing.txt"})
	must(t, err)
	snapshot, err := Open(backups, "backup")
	must(t, err)
	payload, err := snapshot.Payload("file.txt")
	must(t, err)
	payload[0] = 'X'
	for _, path := range []string{"file.txt", "missing.txt", "unknown.txt"} {
		_, err := snapshot.Payload(path)
		if (path == "file.txt") != (err == nil) {
			t.Fatalf("Payload(%q) error = %v", path, err)
		}
	}
	if payload, err = snapshot.Payload("file.txt"); err != nil || string(payload) != "original" {
		t.Fatalf("Payload() = %q, %v", payload, err)
	}
	snapshot.Manifest.Entries = append(snapshot.Manifest.Entries, snapshot.Manifest.Entries[0])
	if _, err := snapshot.Payload("file.txt"); err == nil {
		t.Fatal("Payload() accepted duplicate evidence")
	}
	opened, err := Open(backups, "backup")
	must(t, err)
	must(t, os.WriteFile(backupPayloadPath(opened.Dir, "file.txt"), []byte("changed"), 0o600))
	if _, err := opened.Payload("file.txt"); err == nil {
		t.Fatal("Payload() accepted tampered payload")
	}
}

func TestNewAfter(t *testing.T) {
	for _, test := range []struct {
		name   string
		path   string
		exists bool
		data   []byte
		mode   os.FileMode
		ok     bool
	}{
		{"present empty", "file.txt", true, []byte{}, 0o600, true},
		{"present data", "file.txt", true, []byte("data"), 0o600, true},
		{"absent", "file.txt", false, nil, 0, true},
		{"nil present", "file.txt", true, nil, 0o600, false},
		{"absent data", "file.txt", false, []byte{}, 0, false},
		{"absent mode", "file.txt", false, nil, 0o600, false},
		{"non-permission mode", "file.txt", true, []byte("data"), 0o1000, false},
		{"noncanonical path", "../file.txt", true, []byte("data"), 0o600, false},
		{"zero path", "", false, nil, 0, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			after, err := NewAfter(test.path, test.exists, test.data, test.mode)
			if (err == nil) != test.ok {
				t.Fatalf("NewAfter() error = %v", err)
			}
			if !test.ok {
				return
			}
			if after.Path() != test.path || after.Exists() != test.exists || after.Mode() != test.mode {
				t.Fatalf("After getters = %#v", after)
			}
			if test.exists && test.data != nil && after.Data() == nil {
				t.Fatal("After.Data() lost nonnil empty evidence")
			}
			if len(test.data) != 0 {
				test.data[0] = 'X'
				copy := after.Data()
				copy[0] = 'Y'
				if string(after.Data()) != "data" {
					t.Fatalf("After data was aliased: %q", after.Data())
				}
			}
		})
	}
}

func TestSnapshotPayloadClonesEmptyBytes(t *testing.T) {
	root, backups := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(root, "empty.txt"), []byte{}, 0o600)
	_, err := Capture(root, backups, "backup", []string{"empty.txt"})
	must(t, err)
	snapshot, err := Open(backups, "backup")
	must(t, err)
	payload, err := snapshot.Payload("empty.txt")
	must(t, err)
	if payload == nil {
		t.Fatal("Payload() lost nonnil empty evidence")
	}
}

func TestPlanRestartEvidenceCreatesDetachedCanonicalLeaves(t *testing.T) {
	root, backups := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(root, "remove.txt"), []byte("remove"), 0o640)
	writeFile(t, filepath.Join(root, "replace.txt"), []byte("before"), 0o600)
	snapshot, err := captureWithDirectoryPreimage(root, backups, "backup", []string{"absent/create.txt", "remove.txt", "replace.txt"}, []Directory{{Path: "absent", Mode: 0o700}})
	must(t, err)
	create, err := NewAfter("absent/create.txt", true, []byte("create"), 0o600)
	must(t, err)
	replace, err := NewAfter("replace.txt", true, []byte("after"), 0o600)
	must(t, err)
	remove, err := NewAfter("remove.txt", false, nil, 0)
	must(t, err)
	input := []After{replace, create, remove}
	plan, err := planRestartEvidence(snapshot, input)
	must(t, err)
	for index, want := range []string{"absent/create.txt", "remove.txt", "replace.txt"} {
		if leaf := plan.leaves[index]; leaf.before.Path() != want || leaf.after.Path() != want {
			t.Fatalf("leaf %d = %#v", index, leaf)
		}
	}
	if plan.leaves[0].before.Exists() || string(plan.leaves[1].before.Data()) != "remove" || string(plan.leaves[2].after.Data()) != "after" {
		t.Fatalf("plan leaves = %#v", plan.leaves)
	}
	snapshot.Manifest.Entries[0].Path = "changed.txt"
	snapshot.Manifest.AbsentDirectories[0].Path = "changed"
	input[0].data[0] = 'X'
	if plan.snapshot.Manifest.Entries[0].Path != "absent/create.txt" || plan.snapshot.Manifest.AbsentDirectories[0].Path != "absent" || string(plan.leaves[2].after.Data()) != "after" {
		t.Fatalf("plan aliases caller data: %#v", plan)
	}
}

type restartFile struct {
	path   string
	exists bool
	data   string
	mode   os.FileMode
}

type restartCase struct {
	name          string
	before, after []restartFile
	current       []restartFile
	absent        []Directory
	manualVersion int
	unsafe        string
	want          []restartStatus
	wantErr       bool
}

func TestClassifyRestartLeaves(t *testing.T) {
	for _, test := range []restartCase{
		{"before", []restartFile{{"file.txt", true, "before", 0o600}}, []restartFile{{"file.txt", true, "after", 0o600}}, nil, nil, 0, "", []restartStatus{exactBefore}, false},
		{"after empty file", []restartFile{{"file.txt", true, "before", 0o600}}, []restartFile{{"file.txt", true, "", 0o640}}, []restartFile{{"file.txt", true, "", 0o640}}, nil, 0, "", []restartStatus{exactAfter}, false},
		{"mixed", []restartFile{{"keep.txt", true, "before", 0o600}, {"remove.txt", true, "remove", 0o600}}, []restartFile{{"create.txt", true, "create", 0o600}, {"keep.txt", true, "after", 0o600}, {"remove.txt", false, "", 0}}, []restartFile{{"create.txt", true, "create", 0o600}, {"remove.txt", false, "", 0}}, nil, 0, "", []restartStatus{exactAfter, exactBefore, exactAfter}, false},
		{"bytes drift", []restartFile{{"file.txt", true, "before", 0o600}}, []restartFile{{"file.txt", true, "after", 0o600}}, []restartFile{{"file.txt", true, "drift", 0o600}}, nil, 0, "", nil, true},
		{"mode drift", []restartFile{{"file.txt", true, "before", 0o600}}, []restartFile{{"file.txt", true, "after", 0o600}}, []restartFile{{"file.txt", true, "before", 0o640}}, nil, 0, "", nil, true},
		{"symlink leaf", []restartFile{{"file.txt", true, "before", 0o600}}, []restartFile{{"file.txt", true, "after", 0o600}}, nil, nil, 0, "symlink", nil, true},
		{"nonregular leaf", []restartFile{{"file.txt", true, "before", 0o600}}, []restartFile{{"file.txt", true, "after", 0o600}}, nil, nil, 0, "directory", nil, true},
		{"missing final leaf", nil, []restartFile{{"file.txt", true, "after", 0o600}}, nil, nil, 0, "", []restartStatus{exactBefore}, false},
		{"v2 declared first missing parent", nil, []restartFile{{"missing/file.txt", true, "after", 0o600}}, nil, []Directory{{Path: "missing", Mode: 0o700}}, 0, "", []restartStatus{unresolvedDirectory}, false},
		{"v1 missing parent", []restartFile{{"missing/file.txt", false, "", 0}}, []restartFile{{"missing/file.txt", true, "after", 0o600}}, nil, nil, manifestVersion, "", nil, true},
		{"undeclared first missing parent", []restartFile{{"missing/file.txt", false, "", 0}}, []restartFile{{"missing/file.txt", true, "after", 0o600}}, nil, []Directory{{Path: "declared", Mode: 0o700}}, manifestV2, "", nil, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, backups := t.TempDir(), t.TempDir()
			writeRestartFiles(t, root, test.before)
			plan := classifierPlan(t, root, backups, test)
			writeRestartFiles(t, root, test.current)
			if test.unsafe != "" {
				must(t, os.Remove(filepath.Join(root, "file.txt")))
				if test.unsafe == "symlink" {
					must(t, os.Symlink("target", filepath.Join(root, "file.txt")))
				} else {
					must(t, os.Mkdir(filepath.Join(root, "file.txt"), 0o700))
				}
			}
			got, statuses, err := classifyRestartLeaves(root, plan)
			if test.wantErr {
				if err == nil || len(got.leaves) != 0 || statuses != nil {
					t.Fatalf("classifyRestartLeaves() = %#v, %#v, %v", got, statuses, err)
				}
				return
			}
			must(t, err)
			for index, want := range test.want {
				if statuses[index] != want {
					t.Fatalf("status %d = %q, want %q", index, statuses[index], want)
				}
			}
			if len(statuses) != len(test.want) || &got.leaves[0] == &plan.leaves[0] {
				t.Fatalf("returned plan is not detached: %#v", got)
			}
		})
	}
}

func classifierPlan(t *testing.T, root, backups string, test restartCase) restartPlan {
	t.Helper()
	after := restartAfters(t, test.after)
	if test.manualVersion != 0 {
		entries := make([]Entry, len(test.before))
		for index, file := range test.before {
			entries[index] = Entry{Path: file.path}
		}
		directories := make([]directoryEntry, len(test.absent))
		for index, directory := range test.absent {
			directories[index] = directoryEntry{Path: directory.Path, Mode: uint32(directory.Mode)}
		}
		plan, err := planRestartEvidence(Snapshot{Manifest: Manifest{Version: test.manualVersion, Entries: entries, AbsentDirectories: directories}}, after)
		must(t, err)
		return plan
	}
	paths := make([]string, len(test.after))
	for index, file := range test.after {
		paths[index] = file.path
	}
	var snapshot Snapshot
	var err error
	if test.absent == nil {
		snapshot, err = Capture(root, backups, "backup", paths)
	} else {
		snapshot, err = captureWithDirectoryPreimage(root, backups, "backup", paths, test.absent)
	}
	must(t, err)
	plan, err := planRestartEvidence(snapshot, after)
	must(t, err)
	return plan
}

func restartAfters(t *testing.T, files []restartFile) []After {
	t.Helper()
	after := make([]After, len(files))
	for index, file := range files {
		var data []byte
		if file.exists {
			data = []byte(file.data)
		}
		after[index], _ = NewAfter(file.path, file.exists, data, file.mode)
	}
	return after
}

func writeRestartFiles(t *testing.T, root string, files []restartFile) {
	t.Helper()
	for _, file := range files {
		path := filepath.Join(root, file.path)
		if !file.exists {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			continue
		}
		writeFile(t, path, []byte(file.data), file.mode)
	}
}

func TestClassifyRestartDirectories(t *testing.T) {
	for _, test := range []struct {
		name    string
		version int
		paths   []string
		change  func(t *testing.T, root string)
		want    []restartStatus
		wantErr bool
	}{
		{"v3 all before", manifestV3, []string{"made"}, func(t *testing.T, root string) { must(t, os.Remove(filepath.Join(root, "made"))) }, []restartStatus{exactBefore}, false},
		{"v3 all after", manifestV3, []string{"made"}, nil, []restartStatus{exactAfter}, false},
		{"v3 parent after child before", manifestV3, []string{"made", "made/child"}, func(t *testing.T, root string) { must(t, os.Remove(filepath.Join(root, "made", "child"))) }, []restartStatus{exactAfter, exactBefore}, false},
		{"v3 identity drift", manifestV3, []string{"made"}, func(t *testing.T, root string) {
			must(t, os.Remove(filepath.Join(root, "made")))
			must(t, os.Mkdir(filepath.Join(root, "made"), 0o700))
		}, nil, true},
		{"v3 mode drift", manifestV3, []string{"made"}, func(t *testing.T, root string) { must(t, os.Chmod(filepath.Join(root, "made"), 0o755)) }, nil, true},
		{"v3 wrong type", manifestV3, []string{"made"}, func(t *testing.T, root string) {
			must(t, os.Remove(filepath.Join(root, "made")))
			writeFile(t, filepath.Join(root, "made"), []byte("file"), 0o600)
		}, nil, true},
		{"v3 symlink", manifestV3, []string{"made"}, func(t *testing.T, root string) {
			must(t, os.Remove(filepath.Join(root, "made")))
			must(t, os.Symlink("target", filepath.Join(root, "made")))
		}, nil, true},
		{"v2 all absent", manifestV2, []string{"made"}, nil, []restartStatus{exactBefore}, false},
		{"v2 present rejects", manifestV2, []string{"made"}, func(t *testing.T, root string) { must(t, os.Mkdir(filepath.Join(root, "made"), 0o700)) }, nil, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			plan := restartDirectoryPlan(t, root, test.version, test.paths)
			if test.change != nil {
				test.change(t, root)
			}
			got, _, err := classifyRestart(root, plan)
			if (err != nil) != test.wantErr {
				t.Fatalf("classifyRestart() error = %v", err)
			}
			if test.wantErr {
				return
			}
			for index, want := range test.want {
				if got.directories[index].status != want {
					t.Fatalf("directory status %d = %q, want %q", index, got.directories[index].status, want)
				}
			}
		})
	}
}

func TestPreflightRestartDirectoryContents(t *testing.T) {
	for _, test := range []struct {
		name    string
		dirs    []string
		leaves  []restartFile
		change  func(t *testing.T, root string)
		wantErr bool
	}{
		{"exact tree", []string{"made"}, []restartFile{{"made/file.txt", true, "after", 0o600}}, nil, false},
		{"foreign file", []string{"made"}, []restartFile{{"made/file.txt", true, "after", 0o600}}, func(t *testing.T, root string) {
			writeFile(t, filepath.Join(root, "made", "foreign.txt"), []byte("foreign"), 0o600)
		}, true},
		{"foreign directory", []string{"made"}, []restartFile{{"made/file.txt", true, "after", 0o600}}, func(t *testing.T, root string) { must(t, os.Mkdir(filepath.Join(root, "made", "foreign"), 0o700)) }, true},
		{"foreign symlink", []string{"made"}, []restartFile{{"made/file.txt", true, "after", 0o600}}, func(t *testing.T, root string) {
			must(t, os.Symlink("file.txt", filepath.Join(root, "made", "foreign")))
		}, true},
		{"bounded overflow", []string{"made"}, []restartFile{{"made/file.txt", true, "after", 0o600}}, func(t *testing.T, root string) {
			writeFile(t, filepath.Join(root, "made", "foreign-one"), []byte("one"), 0o600)
			writeFile(t, filepath.Join(root, "made", "foreign-two"), []byte("two"), 0o600)
		}, true},
		{"nested direct children", []string{"made", "made/nested"}, []restartFile{{"made/file.txt", true, "after", 0o600}, {"made/nested/deep.txt", true, "after", 0o600}}, nil, false},
		{"partial missing planned leaf", []string{"made"}, []restartFile{{"made/file.txt", true, "after", 0o600}}, func(t *testing.T, root string) { must(t, os.Remove(filepath.Join(root, "made", "file.txt"))) }, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			plan := restartDirectoryPlan(t, root, manifestV3, test.dirs)
			plan.snapshot.Manifest.Entries = make([]Entry, len(test.leaves))
			for index, leaf := range test.leaves {
				plan.snapshot.Manifest.Entries[index] = Entry{Path: leaf.path}
			}
			plan, err := planRestartEvidence(plan.snapshot, restartAfters(t, test.leaves))
			must(t, err)
			writeRestartFiles(t, root, test.leaves)
			classified, statuses, err := classifyRestart(root, plan)
			must(t, err)
			if test.change != nil {
				test.change(t, root)
			}
			err = preflightRestartDirectoryContents(root, classified, statuses)
			if (err != nil) != test.wantErr {
				t.Fatalf("preflightRestartDirectoryContents() error = %v", err)
			}
			data, readErr := os.ReadFile(filepath.Join(root, test.leaves[0].path))
			if test.wantErr && (readErr != nil || string(data) != test.leaves[0].data) {
				t.Fatalf("preflight mutated planned leaf: %q, %v", data, readErr)
			}
		})
	}
}

func TestClassifyRestartResolvesLeavesOnlyFromBeforeDirectories(t *testing.T) {
	root := t.TempDir()
	snapshot := Snapshot{Manifest: Manifest{
		Version:             manifestV3,
		Entries:             []Entry{{Path: "made/file.txt"}},
		AbsentDirectories:   []directoryEntry{{Path: "made", Mode: 0o700}},
		AcceptedDirectories: []AcceptedDirectory{{Path: "made", Inode: 1, Mode: 0o700}},
	}}
	after, err := NewAfter("made/file.txt", true, []byte("after"), 0o600)
	must(t, err)
	plan, err := planRestartEvidence(snapshot, []After{after})
	must(t, err)
	got, statuses, err := classifyRestart(root, plan)
	must(t, err)
	if statuses[0] != exactBefore || got.directories[0].status != exactBefore {
		t.Fatalf("classification = %#v, %#v", statuses, got.directories)
	}
}

func TestCloneRestartSnapshotClonesAcceptedDirectories(t *testing.T) {
	snapshot := Snapshot{Manifest: Manifest{AcceptedDirectories: []AcceptedDirectory{{Path: "made", Device: 1, Inode: 2, Mode: 0o700}}}}
	clone := cloneRestartSnapshot(snapshot)
	snapshot.Manifest.AcceptedDirectories[0].Inode = 3
	if clone.Manifest.AcceptedDirectories[0].Inode != 2 {
		t.Fatalf("accepted directories were aliased: %#v", clone.Manifest.AcceptedDirectories)
	}
}

func restartDirectoryPlan(t *testing.T, root string, version int, paths []string) restartPlan {
	t.Helper()
	absent := make([]directoryEntry, len(paths))
	var accepted []AcceptedDirectory
	if version == manifestV3 {
		accepted = make([]AcceptedDirectory, len(paths))
	}
	for index, value := range paths {
		absent[index] = directoryEntry{Path: value, Mode: 0o700}
		if version != manifestV3 {
			continue
		}
		must(t, os.Mkdir(filepath.Join(root, value), 0o700))
		info, err := os.Lstat(filepath.Join(root, value))
		must(t, err)
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Ino == 0 {
			t.Fatal("directory identity is unavailable")
		}
		accepted[index] = AcceptedDirectory{Path: value, Device: uint64(stat.Dev), Inode: uint64(stat.Ino), Mode: uint32(info.Mode().Perm())}
	}
	plan, err := planRestartEvidence(Snapshot{Manifest: Manifest{Version: version, AbsentDirectories: absent, AcceptedDirectories: accepted}}, nil)
	must(t, err)
	return plan
}

func TestRollbackRestartLeaves(t *testing.T) {
	for _, test := range []struct {
		name    string
		current []restartFile
		drift   bool
		wantErr bool
		want    []restartFile
	}{
		{
			name: "restores exact after leaves and preserves exact before leaves",
			current: []restartFile{
				{"create.txt", true, "after create", 0o640},
				{"remove.txt", false, "", 0},
				{"replace.txt", true, "before replace", 0o600},
			},
			want: []restartFile{
				{"create.txt", false, "", 0},
				{"remove.txt", true, "before remove", 0o600},
				{"replace.txt", true, "before replace", 0o600},
			},
		},
		{
			name: "late preflight drift leaves earlier exact after leaf untouched",
			current: []restartFile{
				{"create.txt", true, "after create", 0o640},
				{"remove.txt", true, "drift", 0o600},
				{"replace.txt", true, "after replace", 0o640},
			},
			drift:   true,
			wantErr: true,
			want: []restartFile{
				{"create.txt", true, "after create", 0o640},
				{"remove.txt", true, "drift", 0o600},
				{"replace.txt", true, "after replace", 0o640},
			},
		},
		{
			name: "rerun converges after partial completion",
			current: []restartFile{
				{"create.txt", false, "", 0},
				{"remove.txt", false, "", 0},
				{"replace.txt", true, "after replace", 0o640},
			},
			want: []restartFile{
				{"create.txt", false, "", 0},
				{"remove.txt", true, "before remove", 0o600},
				{"replace.txt", true, "before replace", 0o600},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, backups := t.TempDir(), t.TempDir()
			before := []restartFile{
				{"remove.txt", true, "before remove", 0o600},
				{"replace.txt", true, "before replace", 0o600},
			}
			writeRestartFiles(t, root, before)
			must(t, os.Mkdir(filepath.Join(root, "untouched"), 0o700))
			snapshot, err := Capture(root, backups, "backup", []string{"create.txt", "remove.txt", "replace.txt"})
			must(t, err)
			plan, err := planRestartEvidence(snapshot, restartAfters(t, []restartFile{
				{"create.txt", true, "after create", 0o640},
				{"remove.txt", false, "", 0},
				{"replace.txt", true, "after replace", 0o640},
			}))
			must(t, err)
			writeRestartFiles(t, root, test.current)
			err = rollbackRestartLeaves(root, plan)
			if (err != nil) != test.wantErr {
				t.Fatalf("rollbackRestartLeaves() error = %v", err)
			}
			for _, want := range test.want {
				path := filepath.Join(root, want.path)
				info, statErr := os.Lstat(path)
				if !want.exists {
					if !os.IsNotExist(statErr) {
						t.Fatalf("%s exists after rollback: %v", want.path, statErr)
					}
					continue
				}
				must(t, statErr)
				data, readErr := os.ReadFile(path)
				must(t, readErr)
				if !info.Mode().IsRegular() || string(data) != want.data || info.Mode().Perm() != want.mode {
					t.Fatalf("%s = %q %#o, want %q %#o", want.path, data, info.Mode().Perm(), want.data, want.mode)
				}
			}
			info, err := os.Lstat(filepath.Join(root, "untouched"))
			if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
				t.Fatalf("directory changed: %v, %#v", err, info)
			}
		})
	}
}

func TestRollbackRestartLeavesRestoresFromEmptyAfterEvidence(t *testing.T) {
	root, backups := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(root, "empty.txt"), []byte("before"), 0o600)
	snapshot, err := Capture(root, backups, "backup", []string{"empty.txt"})
	must(t, err)
	after, err := NewAfter("empty.txt", true, []byte{}, 0o640)
	must(t, err)
	plan, err := planRestartEvidence(snapshot, []After{after})
	must(t, err)
	writeFile(t, filepath.Join(root, "empty.txt"), after.Data(), after.Mode())
	must(t, rollbackRestartLeaves(root, plan))
	info, err := os.Lstat(filepath.Join(root, "empty.txt"))
	must(t, err)
	data, err := os.ReadFile(filepath.Join(root, "empty.txt"))
	must(t, err)
	if string(data) != "before" || info.Mode().Perm() != 0o600 {
		t.Fatalf("restored evidence = %q %#o", data, info.Mode().Perm())
	}
}

func TestRollbackRestartRestoresV3AndConverges(t *testing.T) {
	root, backups := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(root, "replace.txt"), []byte("before"), 0o600)
	snapshot, err := captureWithDirectoryPreimage(root, backups, "backup", []string{"made/nested/create.txt", "replace.txt"}, []Directory{{Path: "made", Mode: 0o700}, {Path: "made/nested", Mode: 0o700}})
	must(t, err)
	must(t, os.Mkdir(filepath.Join(root, "made"), 0o700))
	must(t, os.Mkdir(filepath.Join(root, "made", "nested"), 0o700))
	snapshot.Manifest.Version = manifestV3
	snapshot.Manifest.AcceptedDirectories = acceptedRestartDirectories(t, root, snapshot.Manifest.AbsentDirectories)
	writeRestartFiles(t, root, []restartFile{{"made/nested/create.txt", true, "after", 0o640}, {"replace.txt", true, "after", 0o640}})
	after := restartAfters(t, []restartFile{{"made/nested/create.txt", true, "after", 0o640}, {"replace.txt", true, "after", 0o640}})
	must(t, RollbackRestart(root, snapshot, after))
	must(t, RollbackRestart(root, snapshot, after))
	if _, err := os.Lstat(filepath.Join(root, "made")); !os.IsNotExist(err) {
		t.Fatalf("created directories remain: %v", err)
	}
	info, err := os.Lstat(filepath.Join(root, "replace.txt"))
	must(t, err)
	data, err := os.ReadFile(filepath.Join(root, "replace.txt"))
	must(t, err)
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || string(data) != "before" {
		t.Fatalf("final leaf = %q %#o", data, info.Mode().Perm())
	}
}

func acceptedRestartDirectories(t *testing.T, root string, absent []directoryEntry) []AcceptedDirectory {
	t.Helper()
	accepted := make([]AcceptedDirectory, len(absent))
	for index, directory := range absent {
		info, err := os.Lstat(filepath.Join(root, directory.Path))
		must(t, err)
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Ino == 0 {
			t.Fatal("directory identity is unavailable")
		}
		accepted[index] = AcceptedDirectory{Path: directory.Path, Device: uint64(stat.Dev), Inode: uint64(stat.Ino), Mode: uint32(info.Mode().Perm())}
	}
	return accepted
}

func TestRollbackRestartRejectsV3DirectoryDriftBeforeLeafMutation(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(t *testing.T, root string)
	}{
		{"foreign content", func(t *testing.T, root string) {
			writeFile(t, filepath.Join(root, "made", "foreign.txt"), []byte("foreign"), 0o600)
		}},
		{"identity drift", func(t *testing.T, root string) {
			must(t, os.Remove(filepath.Join(root, "made")))
			must(t, os.Mkdir(filepath.Join(root, "made"), 0o700))
		}},
		{"mode drift", func(t *testing.T, root string) { must(t, os.Chmod(filepath.Join(root, "made"), 0o755)) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, backups := t.TempDir(), t.TempDir()
			writeFile(t, filepath.Join(root, "replace.txt"), []byte("before"), 0o600)
			snapshot, err := captureWithDirectoryPreimage(root, backups, "backup", []string{"replace.txt"}, []Directory{{Path: "made", Mode: 0o700}})
			must(t, err)
			must(t, os.Mkdir(filepath.Join(root, "made"), 0o700))
			snapshot.Manifest.Version = manifestV3
			snapshot.Manifest.AcceptedDirectories = acceptedRestartDirectories(t, root, snapshot.Manifest.AbsentDirectories)
			writeFile(t, filepath.Join(root, "replace.txt"), []byte("after"), 0o640)
			test.change(t, root)
			after := restartAfters(t, []restartFile{{"replace.txt", true, "after", 0o640}})
			if err := RollbackRestart(root, snapshot, after); err == nil {
				t.Fatal("RollbackRestart() error = nil")
			}
			info, err := os.Lstat(filepath.Join(root, "replace.txt"))
			must(t, err)
			data, err := os.ReadFile(filepath.Join(root, "replace.txt"))
			must(t, err)
			if string(data) != "after" || info.Mode().Perm() != 0o640 {
				t.Fatalf("leaf mutated on rejection: %q %#o", data, info.Mode().Perm())
			}
		})
	}
}

func TestRollbackRestartV2AndV1Compatibility(t *testing.T) {
	for _, test := range []struct {
		name          string
		withDirectory bool
		present       bool
		wantErr       bool
	}{
		{"v2 present rejects", true, true, true},
		{"v2 absent restores leaves", true, false, false},
		{"v1 restores leaves", false, false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, backups := t.TempDir(), t.TempDir()
			writeFile(t, filepath.Join(root, "replace.txt"), []byte("before"), 0o600)
			var snapshot Snapshot
			var err error
			if test.withDirectory {
				snapshot, err = captureWithDirectoryPreimage(root, backups, "backup", []string{"replace.txt"}, []Directory{{Path: "made", Mode: 0o700}})
			} else {
				snapshot, err = Capture(root, backups, "backup", []string{"replace.txt"})
			}
			must(t, err)
			if test.present {
				must(t, os.Mkdir(filepath.Join(root, "made"), 0o700))
			}
			writeFile(t, filepath.Join(root, "replace.txt"), []byte("after"), 0o640)
			after := restartAfters(t, []restartFile{{"replace.txt", true, "after", 0o640}})
			err = RollbackRestart(root, snapshot, after)
			if (err != nil) != test.wantErr {
				t.Fatalf("RollbackRestart() error = %v", err)
			}
			data, readErr := os.ReadFile(filepath.Join(root, "replace.txt"))
			must(t, readErr)
			want := "before"
			if test.wantErr {
				want = "after"
			}
			if string(data) != want {
				t.Fatalf("leaf = %q, want %q", data, want)
			}
		})
	}
}

func TestPlanRestartEvidenceRejectsMalformedCoverage(t *testing.T) {
	root, backups := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(root, "file.txt"), []byte("before"), 0o600)
	snapshot, err := Capture(root, backups, "backup", []string{"file.txt"})
	must(t, err)
	after, err := NewAfter("file.txt", true, []byte("after"), 0o600)
	must(t, err)
	extra, err := NewAfter("extra.txt", true, []byte("extra"), 0o600)
	must(t, err)
	unchanged, err := NewAfter("file.txt", true, []byte("before"), 0o600)
	must(t, err)
	for name, evidence := range map[string][]After{
		"extra":     {after, extra},
		"duplicate": {after, after},
		"omitted":   nil,
		"zero":      {{}},
		"no-op":     {unchanged},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := planRestartEvidence(snapshot, evidence); err == nil {
				t.Fatal("planRestartEvidence() error = nil")
			}
		})
	}
}
