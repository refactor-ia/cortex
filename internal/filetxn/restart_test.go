package filetxn

import (
	"os"
	"path/filepath"
	"strings"
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
