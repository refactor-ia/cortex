package atomicfile

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReplace(t *testing.T) {
	tests := []struct {
		name        string
		relative    string
		data        []byte
		mode        fs.FileMode
		setup       func(t *testing.T, root string) string
		wantErr     string
		wantData    []byte
		wantMode    fs.FileMode
		preserveOld bool
	}{
		{
			name:     "replaces an existing regular file",
			relative: "safe/config.txt",
			data:     []byte("new configuration\n"),
			mode:     0o640,
			setup: func(t *testing.T, root string) string {
				t.Helper()
				writeFile(t, filepath.Join(root, "safe", "config.txt"), []byte("old configuration\n"))
				return root
			},
			wantData: []byte("new configuration\n"),
			wantMode: 0o640,
		},
		{
			name:     "creates a missing leaf in an existing directory",
			relative: "safe/config.txt",
			data:     []byte("created\n"),
			mode:     0o600,
			setup: func(t *testing.T, root string) string {
				t.Helper()
				if err := os.Mkdir(filepath.Join(root, "safe"), 0o755); err != nil {
					t.Fatal(err)
				}
				return root
			},
			wantData: []byte("created\n"),
			wantMode: 0o600,
		},
		{
			name:     "rejects traversal",
			relative: filepath.Join("..", "outside.txt"),
			data:     []byte("replacement"),
			mode:     0o600,
			wantErr:  "escapes root",
		},
		{
			name:     "rejects absolute paths",
			relative: filepath.Join(string(filepath.Separator), "outside.txt"),
			data:     []byte("replacement"),
			mode:     0o600,
			wantErr:  "absolute",
		},
		{
			name:     "rejects a symlink root",
			relative: "config.txt",
			data:     []byte("replacement"),
			mode:     0o600,
			setup: func(t *testing.T, root string) string {
				t.Helper()
				target := filepath.Join(root, "target")
				if err := os.Mkdir(target, 0o755); err != nil {
					t.Fatal(err)
				}
				link := filepath.Join(root, "linked-root")
				if err := os.Symlink(target, link); err != nil {
					t.Fatal(err)
				}
				return link
			},
			wantErr: "root is a symlink",
		},
		{
			name:     "rejects a symlink ancestor",
			relative: "linked/config.txt",
			data:     []byte("replacement"),
			mode:     0o600,
			setup: func(t *testing.T, root string) string {
				t.Helper()
				target := filepath.Join(root, "target")
				if err := os.Mkdir(target, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, filepath.Join(root, "linked")); err != nil {
					t.Fatal(err)
				}
				return root
			},
			wantErr: "symlink component",
		},
		{
			name:     "rejects a symlink leaf without changing its target",
			relative: "safe/config.txt",
			data:     []byte("replacement"),
			mode:     0o600,
			setup: func(t *testing.T, root string) string {
				t.Helper()
				if err := os.Mkdir(filepath.Join(root, "safe"), 0o755); err != nil {
					t.Fatal(err)
				}
				target := filepath.Join(root, "target.txt")
				writeFile(t, target, []byte("original"))
				if err := os.Symlink(target, filepath.Join(root, "safe", "config.txt")); err != nil {
					t.Fatal(err)
				}
				return root
			},
			wantErr:     "symlink component",
			preserveOld: true,
		},
		{
			name:     "rejects a directory leaf",
			relative: "safe/config",
			data:     []byte("replacement"),
			mode:     0o600,
			setup: func(t *testing.T, root string) string {
				t.Helper()
				if err := os.MkdirAll(filepath.Join(root, "safe", "config"), 0o755); err != nil {
					t.Fatal(err)
				}
				return root
			},
			wantErr: "not a regular file",
		},
		{
			name:     "rejects a missing parent",
			relative: "missing/config.txt",
			data:     []byte("replacement"),
			mode:     0o600,
			wantErr:  "parent does not exist",
		},
		{
			name:     "rejects unsupported modes without changing the original",
			relative: "safe/config.txt",
			data:     []byte("replacement"),
			mode:     fs.ModeSetuid | 0o600,
			setup: func(t *testing.T, root string) string {
				t.Helper()
				writeFile(t, filepath.Join(root, "safe", "config.txt"), []byte("original"))
				return root
			},
			wantErr:     "unsupported mode",
			preserveOld: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			if tt.setup != nil {
				root = tt.setup(t, root)
			}

			err := Replace(root, tt.relative, tt.data, tt.mode)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Replace() error = %v, want substring %q", err, tt.wantErr)
				}
				if tt.preserveOld {
					data, readErr := os.ReadFile(filepath.Join(root, tt.relative))
					if readErr != nil || !bytes.Equal(data, []byte("original")) {
						t.Fatalf("original file changed: data %q, error %v", data, readErr)
					}
				}
			} else if err != nil {
				t.Fatalf("Replace() error = %v", err)
			} else {
				path := filepath.Join(root, tt.relative)
				data, readErr := os.ReadFile(path)
				if readErr != nil || !bytes.Equal(data, tt.wantData) {
					t.Fatalf("replacement data = %q, error %v", data, readErr)
				}
				info, statErr := os.Lstat(path)
				if statErr != nil || info.Mode().Perm() != tt.wantMode {
					t.Fatalf("replacement mode = %v, error %v, want %v", info.Mode(), statErr, tt.wantMode)
				}
			}

			assertNoTemporaryFiles(t, root)
		})
	}
}

func TestReplaceIfMatches(t *testing.T) {
	tests := []struct {
		name            string
		initialData     []byte
		initialMode     fs.FileMode
		expectedData    []byte
		expectedMode    fs.FileMode
		replacementData []byte
		replacementMode fs.FileMode
		createTarget    bool
		wantErr         string
	}{
		{
			name:            "replaces a matching existing file",
			initialData:     []byte("original snapshot\n"),
			initialMode:     0o640,
			expectedData:    []byte("original snapshot\n"),
			expectedMode:    0o640,
			replacementData: []byte("restored snapshot\n"),
			replacementMode: 0o600,
			createTarget:    true,
		},
		{
			name:            "preserves bytes drift",
			initialData:     []byte("user update\n"),
			initialMode:     0o640,
			expectedData:    []byte("original snapshot\n"),
			expectedMode:    0o640,
			replacementData: []byte("restored snapshot\n"),
			replacementMode: 0o600,
			createTarget:    true,
			wantErr:         "bytes do not match",
		},
		{
			name:            "preserves mode drift",
			initialData:     []byte("original snapshot\n"),
			initialMode:     0o600,
			expectedData:    []byte("original snapshot\n"),
			expectedMode:    0o640,
			replacementData: []byte("restored snapshot\n"),
			replacementMode: 0o600,
			createTarget:    true,
			wantErr:         "mode does not match",
		},
		{
			name:            "fails when the existing target is missing",
			expectedData:    []byte("original snapshot\n"),
			expectedMode:    0o640,
			replacementData: []byte("restored snapshot\n"),
			replacementMode: 0o600,
			wantErr:         "destination is missing",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "safe", "config.txt")
			if err := os.Mkdir(filepath.Join(root, "safe"), 0o755); err != nil {
				t.Fatal(err)
			}
			if tt.createTarget {
				if err := os.WriteFile(path, tt.initialData, tt.initialMode); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(path, tt.initialMode); err != nil {
					t.Fatal(err)
				}
			}

			err := ReplaceIfMatches(root, "safe/config.txt", tt.expectedData, tt.expectedMode, tt.replacementData, tt.replacementMode)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("ReplaceIfMatches() error = %v, want substring %q", err, tt.wantErr)
				}
				if tt.createTarget {
					assertFile(t, path, tt.initialData, tt.initialMode)
				}
			} else if err != nil {
				t.Fatalf("ReplaceIfMatches() error = %v", err)
			} else {
				assertFile(t, path, tt.replacementData, tt.replacementMode)
			}

			assertNoTemporaryFiles(t, root)
		})
	}
}

func assertFile(t *testing.T, path string, wantData []byte, wantMode fs.FileMode) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(data, wantData) {
		t.Fatalf("file data = %q, error %v, want %q", data, err, wantData)
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode().Perm() != wantMode {
		t.Fatalf("file mode = %v, error %v, want %v", info.Mode(), err, wantMode)
	}
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertNoTemporaryFiles(t *testing.T, root string) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.HasPrefix(entry.Name(), ".cortex-replace-") {
			t.Fatalf("temporary file remains: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
