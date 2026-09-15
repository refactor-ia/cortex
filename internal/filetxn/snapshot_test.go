package filetxn

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCaptureRecordsExistingAndAbsentFilesWithoutChangingTargets(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "config", "z.txt"), []byte("z-content"), 0o640)
	writeFile(t, filepath.Join(root, "config", "a.txt"), []byte("secret-content"), 0o600)
	before, err := os.ReadFile(filepath.Join(root, "config", "a.txt"))
	must(t, err)
	backupRoot := t.TempDir()
	snapshot, err := Capture(root, backupRoot, "backup", []string{"config/z.txt", "config/missing.txt", "config/a.txt"})
	must(t, err)
	if err := Verify(backupRoot, "backup"); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	manifestBytes, err := os.ReadFile(filepath.Join(snapshot.Dir, manifestName))
	must(t, err)
	const wantManifest = "{\n  \"version\": 1,\n  \"entries\": [\n    {\n      \"path\": \"config/a.txt\",\n      \"exists\": true,\n      \"mode\": 384,\n      \"sha256\": \"ca36af0056ea1b203c097393458357da986bae4cc88ac7bda03fe744c92685d3\"\n    },\n    {\n      \"path\": \"config/missing.txt\",\n      \"exists\": false\n    },\n    {\n      \"path\": \"config/z.txt\",\n      \"exists\": true,\n      \"mode\": 416,\n      \"sha256\": \"df8589eb7d0329695fc09f10da04f9c7a3ce8eb08b7b20f3fe1b37387058340d\"\n    }\n  ]\n}\n"
	if string(manifestBytes) != wantManifest {
		t.Fatalf("v1 manifest bytes changed:\n%s", manifestBytes)
	}
	var manifest Manifest
	must(t, json.Unmarshal(manifestBytes, &manifest))
	if manifest.Version != manifestVersion || strings.Contains(string(manifestBytes), "secret-content") {
		t.Fatalf("manifest = %s", manifestBytes)
	}
	if got := []string{manifest.Entries[0].Path, manifest.Entries[1].Path, manifest.Entries[2].Path}; strings.Join(got, ",") != "config/a.txt,config/missing.txt,config/z.txt" {
		t.Fatalf("entry paths = %v", got)
	}
	if !manifest.Entries[0].Exists || manifest.Entries[1].Exists || !manifest.Entries[2].Exists {
		t.Fatalf("entry existence = %#v", manifest.Entries)
	}
	if manifest.Entries[0].Mode != 0o600 || manifest.Entries[0].SHA256 == "" || manifest.Entries[1].Mode != 0 || manifest.Entries[1].SHA256 != "" {
		t.Fatalf("entry metadata = %#v", manifest.Entries)
	}
	after, err := os.ReadFile(filepath.Join(root, "config", "a.txt"))
	if err != nil || string(after) != string(before) {
		t.Fatalf("target bytes changed: %q, %v", after, err)
	}
	if mode := fileMode(t, filepath.Join(root, "config", "a.txt")); mode != 0o600 {
		t.Fatalf("target mode = %#o, want 0600", mode)
	}
}

func TestCaptureRejectsInvalidCandidatesBeforeCreatingBackup(t *testing.T) {
	cases := map[string][]string{
		"duplicate": {"file.txt", "file.txt"},
		"traversal": {"../outside.txt"},
		"directory": {"directory"},
		"symlink":   {"link.txt"},
	}
	for name, candidates := range cases {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, filepath.Join(root, "file.txt"), []byte("file"), 0o600)
			must(t, os.Mkdir(filepath.Join(root, "directory"), 0o700))
			must(t, os.Symlink("file.txt", filepath.Join(root, "link.txt")))
			backupRoot := t.TempDir()
			backup := filepath.Join(backupRoot, "backup")
			if _, err := Capture(root, backupRoot, "backup", candidates); err == nil {
				t.Fatal("Capture() error = nil")
			}
			if _, err := os.Lstat(backup); !os.IsNotExist(err) {
				t.Fatalf("backup directory was created: %v", err)
			}
		})
	}
}

func TestCaptureRejectsUnsafeRootsAndBackupDirectories(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "file.txt"), []byte("file"), 0o600)
	linkedRoot := filepath.Join(t.TempDir(), "linked-root")
	must(t, os.Symlink(root, linkedRoot))
	backupRoot := t.TempDir()
	if _, err := Capture(linkedRoot, backupRoot, "backup", []string{"file.txt"}); err == nil {
		t.Fatal("Capture() accepted a symlink root")
	}
	backup := filepath.Join(backupRoot, "backup")
	must(t, os.Mkdir(backup, 0o700))
	if _, err := Capture(root, backupRoot, "backup", []string{"file.txt"}); err == nil {
		t.Fatal("Capture() accepted an existing backup directory")
	}
	linkedBackupRoot, linkedAncestor := filepath.Join(t.TempDir(), "linked-backup-root"), filepath.Join(backupRoot, "linked")
	for _, link := range [][2]string{{backupRoot, linkedBackupRoot}, {t.TempDir(), linkedAncestor}} {
		must(t, os.Symlink(link[0], link[1]))
	}
	for _, location := range [][2]string{{linkedBackupRoot, "backup"}, {backupRoot, "linked/backup"}, {backupRoot, filepath.Join(t.TempDir(), "backup")}, {backupRoot, "../backup"}} {
		if _, err := Capture(root, location[0], location[1], []string{"file.txt"}); err == nil {
			t.Fatalf("Capture() accepted unsafe backup location %q", location[1])
		}
		if err := Verify(location[0], location[1]); err == nil {
			t.Fatalf("Verify() accepted unsafe backup location %q", location[1])
		}
	}
}

func TestVerifyRejectsMissingTamperedAndStrictManifestData(t *testing.T) {
	for _, damage := range []string{"missing", "tampered", "unknown field", "trailing value"} {
		t.Run(damage, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, filepath.Join(root, "file.txt"), []byte("original"), 0o600)
			backupRoot := t.TempDir()
			snapshot, err := Capture(root, backupRoot, "backup", []string{"file.txt"})
			if err != nil {
				t.Fatal(err)
			}
			payload := backupPayloadPath(snapshot.Dir, snapshot.Manifest.Entries[0].Path)
			switch damage {
			case "missing":
				err = os.Remove(payload)
			case "tampered":
				err = os.WriteFile(payload, []byte("changed"), 0o600)
			case "unknown field":
				err = os.WriteFile(filepath.Join(snapshot.Dir, manifestName), []byte(`{"version":1,"entries":[],"extra":true}`), 0o600)
			case "trailing value":
				err = os.WriteFile(filepath.Join(snapshot.Dir, manifestName), []byte(`{"version":1,"entries":[]} {}`), 0o600)
			}
			must(t, err)
			if err := Verify(backupRoot, "backup"); err == nil {
				t.Fatal("Verify() error = nil")
			}
		})
	}
}
func TestVerifyRejectsSymlinkedBackupReads(t *testing.T) {
	for _, leaf := range []string{"payloads", manifestName} {
		t.Run(leaf, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, filepath.Join(root, "file.txt"), []byte("original"), 0o600)
			backupRoot := t.TempDir()
			snapshot, err := Capture(root, backupRoot, "backup", []string{"file.txt"})
			must(t, err)
			path := filepath.Join(snapshot.Dir, leaf)
			external := filepath.Join(t.TempDir(), leaf)
			must(t, os.Rename(path, external))
			must(t, os.Symlink(external, path))
			if Verify(backupRoot, "backup") == nil {
				t.Fatalf("Verify() accepted a symlinked %s", leaf)
			}
		})
	}
}

func TestVerifyRejectsInvalidDirectoryPreimageSemantics(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Manifest)
	}{
		{"unsafe", func(manifest *Manifest) { manifest.AbsentDirectories[0].Path = "../absent" }},
		{"duplicate", func(manifest *Manifest) {
			manifest.AbsentDirectories = append(manifest.AbsentDirectories, manifest.AbsentDirectories[0])
		}},
		{"out of order", func(manifest *Manifest) {
			manifest.AbsentDirectories[0], manifest.AbsentDirectories[1] = manifest.AbsentDirectories[1], manifest.AbsentDirectories[0]
		}},
		{"invalid mode", func(manifest *Manifest) { manifest.AbsentDirectories[0].Mode = 0o1000 }},
		{"existing entry below absent directory", func(manifest *Manifest) {
			manifest.Entries = append(manifest.Entries, Entry{Path: "absent/file", Exists: true, Mode: 0o600, SHA256: strings.Repeat("a", 64)})
		}},
		{"entry conflicts with absent directory", func(manifest *Manifest) { manifest.Entries = append(manifest.Entries, Entry{Path: "absent"}) }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			root, backupRoot := t.TempDir(), t.TempDir()
			snapshot, err := captureWithDirectoryPreimage(root, backupRoot, "backup", nil, []Directory{{Path: "absent", Mode: 0o700}, {Path: "other", Mode: 0o700}})
			must(t, err)
			manifest := snapshot.Manifest
			test.mutate(&manifest)
			data, err := json.Marshal(manifest)
			must(t, err)
			must(t, os.WriteFile(filepath.Join(snapshot.Dir, manifestName), data, 0o600))
			if err := Verify(backupRoot, "backup"); err == nil {
				t.Fatal("Verify() error = nil")
			}
		})
	}
}

func TestCaptureWithDirectoryPreimageRecordsNestedAbsentDirectories(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "existing.txt"), []byte("existing"), 0o640)
	backupRoot := t.TempDir()
	snapshot, err := captureWithDirectoryPreimage(root, backupRoot, "backup", []string{"missing/nested/leaf.txt", "existing.txt"}, []Directory{{Path: "missing", Mode: 0o750}, {Path: "missing/nested", Mode: 0o700}})
	must(t, err)
	if got := snapshot.Manifest; got.Version != 2 || len(got.AbsentDirectories) != 2 || got.AbsentDirectories[0].Path != "missing" || got.AbsentDirectories[0].Mode != 0o750 || got.AbsentDirectories[1].Path != "missing/nested" || got.AbsentDirectories[1].Mode != 0o700 {
		t.Fatalf("manifest = %#v", got)
	}
	if got := snapshot.Manifest.Entries; len(got) != 2 || got[0].Path != "existing.txt" || !got[0].Exists || got[1].Path != "missing/nested/leaf.txt" || got[1].Exists {
		t.Fatalf("entries = %#v", got)
	}
	stored, err := readManifest(snapshot.Dir)
	must(t, err)
	if !reflect.DeepEqual(snapshot.Manifest, stored) {
		t.Fatalf("returned manifest differs from stored manifest\nreturned: %#v\nstored: %#v", snapshot.Manifest, stored)
	}
	must(t, Verify(backupRoot, "backup"))
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path string, content []byte, mode os.FileMode) {
	t.Helper()
	must(t, os.MkdirAll(filepath.Dir(path), 0o700))
	must(t, os.WriteFile(path, content, mode))
	must(t, os.Chmod(path, mode))
}

func fileMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	must(t, err)
	return info.Mode().Perm()
}

func TestVerifyAcceptsV3AcceptedDirectories(t *testing.T) {
	root, backupRoot := t.TempDir(), t.TempDir()
	snapshot, err := captureWithDirectoryPreimage(root, backupRoot, "backup", nil, []Directory{{Path: "absent", Mode: 0o700}, {Path: "other", Mode: 0o750}})
	must(t, err)
	manifest := snapshot.Manifest
	manifest.Version = manifestV3
	manifest.AcceptedDirectories = []AcceptedDirectory{
		{Path: "absent", Device: 0, Inode: 42, Mode: 0o700},
		{Path: "other", Device: 7, Inode: 43, Mode: 0o750},
	}
	must(t, writeManifest(snapshot.Dir, manifest))

	got, err := reloadAndVerify(backupRoot, "backup")
	must(t, err)
	if !reflect.DeepEqual(got.Manifest, manifest) {
		t.Fatalf("manifest = %#v, want %#v", got.Manifest, manifest)
	}
}

func TestVerifyRejectsInvalidV3AcceptedDirectories(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Manifest)
	}{
		{"empty", func(manifest *Manifest) { manifest.AcceptedDirectories = nil }},
		{"mismatch", func(manifest *Manifest) {
			manifest.AcceptedDirectories = manifest.AcceptedDirectories[:1]
		}},
		{"path mismatch", func(manifest *Manifest) { manifest.AcceptedDirectories[0].Path = "different" }},
		{"mode mismatch", func(manifest *Manifest) { manifest.AcceptedDirectories[0].Mode = 0o755 }},
		{"ordering", func(manifest *Manifest) {
			manifest.AcceptedDirectories[0], manifest.AcceptedDirectories[1] = manifest.AcceptedDirectories[1], manifest.AcceptedDirectories[0]
		}},
		{"invalid mode", func(manifest *Manifest) { manifest.AcceptedDirectories[0].Mode = 0o1000 }},
		{"zero inode", func(manifest *Manifest) { manifest.AcceptedDirectories[0].Inode = 0 }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			root, backupRoot := t.TempDir(), t.TempDir()
			snapshot, err := captureWithDirectoryPreimage(root, backupRoot, "backup", nil, []Directory{{Path: "absent", Mode: 0o700}, {Path: "other", Mode: 0o750}})
			must(t, err)
			manifest := snapshot.Manifest
			manifest.Version = manifestV3
			manifest.AcceptedDirectories = []AcceptedDirectory{
				{Path: "absent", Device: 0, Inode: 42, Mode: 0o700},
				{Path: "other", Device: 7, Inode: 43, Mode: 0o750},
			}
			test.mutate(&manifest)
			must(t, writeManifest(snapshot.Dir, manifest))
			if err := Verify(backupRoot, "backup"); err == nil {
				t.Fatal("Verify() error = nil")
			}
		})
	}
}

func TestVerifyRejectsAcceptedDirectoriesOutsideV3(t *testing.T) {
	for _, version := range []int{manifestVersion, manifestV2} {
		t.Run(string(rune('0'+version)), func(t *testing.T) {
			root, backupRoot := t.TempDir(), t.TempDir()
			snapshot, err := Capture(root, backupRoot, "backup", nil)
			must(t, err)
			manifest := snapshot.Manifest
			if version == manifestV2 {
				snapshot, err = captureWithDirectoryPreimage(root, backupRoot, "backup-v2", nil, []Directory{{Path: "absent", Mode: 0o700}})
				must(t, err)
				manifest = snapshot.Manifest
			}
			manifest.AcceptedDirectories = []AcceptedDirectory{{Path: "absent", Inode: 1, Mode: 0o700}}
			must(t, writeManifest(snapshot.Dir, manifest))
			if err := Verify(backupRoot, filepath.Base(snapshot.Dir)); err == nil {
				t.Fatal("Verify() error = nil")
			}
		})
	}
}

func TestVerifyRejectsUnknownOrTrailingV3AcceptedDirectoryData(t *testing.T) {
	for _, manifest := range []string{
		`{"version":3,"entries":[],"absentDirectories":[{"path":"absent","mode":448}],"acceptedDirectories":[{"path":"absent","device":0,"inode":1,"mode":448,"unknown":true}]}`,
		`{"version":3,"entries":[],"absentDirectories":[{"path":"absent","mode":448}],"acceptedDirectories":[{"path":"absent","device":0,"inode":1,"mode":448}]} {}`,
	} {
		t.Run(manifest, func(t *testing.T) {
			backupRoot, backup := t.TempDir(), "backup"
			must(t, os.Mkdir(filepath.Join(backupRoot, backup), 0o700))
			must(t, os.Mkdir(filepath.Join(backupRoot, backup, "payloads"), 0o700))
			must(t, os.WriteFile(filepath.Join(backupRoot, backup, manifestName), []byte(manifest), 0o600))
			if err := Verify(backupRoot, backup); err == nil {
				t.Fatal("Verify() error = nil")
			}
		})
	}
}
