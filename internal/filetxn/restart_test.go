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
