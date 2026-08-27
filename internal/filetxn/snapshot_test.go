package filetxn

import (
	"encoding/json"
	"os"
	"path/filepath"
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

func TestVerifyRejectsMissingAndTamperedPayloads(t *testing.T) {
	for _, damage := range []string{"missing", "tampered"} {
		t.Run(damage, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, filepath.Join(root, "file.txt"), []byte("original"), 0o600)
			backupRoot := t.TempDir()
			snapshot, err := Capture(root, backupRoot, "backup", []string{"file.txt"})
			if err != nil {
				t.Fatal(err)
			}
			payload := backupPayloadPath(snapshot.Dir, snapshot.Manifest.Entries[0].Path)
			if damage == "missing" {
				err = os.Remove(payload)
			} else {
				err = os.WriteFile(payload, []byte("changed"), 0o600)
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
