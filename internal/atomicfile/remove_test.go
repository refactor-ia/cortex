package atomicfile

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemoveIfExactRemovesMatchingRegularFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "safe", "config.txt")
	data := []byte("cortex-created\n")
	writeFile(t, path, data)
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := RemoveIfExact(root, "safe/config.txt", data, fs.FileMode(0o600)); err != nil {
		t.Fatalf("RemoveIfExact() error = %v", err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("removed file stat error = %v, want not exist", err)
	}
}

func TestRemoveIfExactFailsClosed(t *testing.T) {
	tests := []struct {
		name     string
		relative string
		expected []byte
		mode     fs.FileMode
		setup    func(t *testing.T, root, path string)
		wantErr  string
		wantData []byte
	}{
		{
			name:     "preserves wrong bytes and sentinel",
			relative: "safe/config.txt",
			expected: []byte("expected-secret"),
			mode:     0o600,
			wantErr:  "bytes do not match",
			wantData: []byte("actual-secret"),
			setup: func(t *testing.T, root, path string) {
				t.Helper()
				writeFile(t, path, []byte("actual-secret"))
				if err := os.Chmod(path, 0o600); err != nil {
					t.Fatal(err)
				}
				writeFile(t, filepath.Join(root, "safe", "sentinel.txt"), []byte("keep"))
			},
		},
		{
			name:     "preserves wrong mode",
			relative: "safe/config.txt",
			expected: []byte("cortex-created"),
			mode:     0o600,
			wantErr:  "mode does not match",
			wantData: []byte("cortex-created"),
			setup: func(t *testing.T, _ string, path string) {
				t.Helper()
				writeFile(t, path, []byte("cortex-created"))
				if err := os.Chmod(path, 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:     "rejects symlink",
			relative: "safe/config.txt",
			expected: []byte("cortex-created"),
			mode:     0o600,
			wantErr:  "destination is unsafe",
			setup: func(t *testing.T, root, path string) {
				t.Helper()
				writeFile(t, filepath.Join(root, "target.txt"), []byte("cortex-created"))
				if err := os.Symlink(filepath.Join(root, "target.txt"), path); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:     "rejects nonregular destination",
			relative: "safe/config.txt",
			expected: []byte("cortex-created"),
			mode:     0o600,
			wantErr:  "not a regular file",
			setup: func(t *testing.T, _ string, path string) {
				t.Helper()
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:     "rejects missing destination",
			relative: "safe/config.txt",
			expected: []byte("cortex-created"),
			mode:     0o600,
			wantErr:  "destination is missing",
			setup:    func(t *testing.T, _, _ string) { t.Helper() },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, tt.relative)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			tt.setup(t, root, path)

			err := RemoveIfExact(root, tt.relative, tt.expected, tt.mode)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("RemoveIfExact() error = %v, want substring %q", err, tt.wantErr)
			}
			for _, secret := range [][]byte{tt.expected, tt.wantData} {
				if len(secret) > 0 && strings.Contains(err.Error(), string(secret)) {
					t.Fatalf("RemoveIfExact() error leaked evidence %q", secret)
				}
			}
			if tt.wantData != nil {
				data, readErr := os.ReadFile(path)
				if readErr != nil || !bytes.Equal(data, tt.wantData) {
					t.Fatalf("preserved file data = %q, error %v", data, readErr)
				}
			}
			if tt.name == "preserves wrong bytes and sentinel" {
				data, readErr := os.ReadFile(filepath.Join(root, "safe", "sentinel.txt"))
				if readErr != nil || !bytes.Equal(data, []byte("keep")) {
					t.Fatalf("sentinel data = %q, error %v", data, readErr)
				}
			}
		})
	}
}

func TestRemoveIfExactRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name     string
		root     string
		relative string
		expected []byte
		mode     fs.FileMode
		wantErr  string
	}{
		{"empty root", "", "safe/config.txt", []byte("evidence"), 0o600, "invalid root"},
		{"absolute path", t.TempDir(), string(filepath.Separator) + "outside.txt", []byte("evidence"), 0o600, "invalid relative path"},
		{"traversal", t.TempDir(), "../outside.txt", []byte("evidence"), 0o600, "invalid relative path"},
		{"dot", t.TempDir(), ".", []byte("evidence"), 0o600, "invalid relative path"},
		{"backslash", t.TempDir(), `safe\config.txt`, []byte("evidence"), 0o600, "invalid relative path"},
		{"empty evidence", t.TempDir(), "safe/config.txt", nil, 0o600, "invalid expected bytes"},
		{"oversized evidence", t.TempDir(), "safe/config.txt", make([]byte, removeExactMaxEvidenceBytes+1), 0o600, "invalid expected bytes"},
		{"type bits", t.TempDir(), "safe/config.txt", []byte("evidence"), fs.ModeDir | 0o600, "invalid expected mode"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := RemoveIfExact(tt.root, tt.relative, tt.expected, tt.mode)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("RemoveIfExact() error = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestRemoveIfExactDetectsIdentityDrift(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "safe", "config.txt")
	original := []byte("cortex-created")
	replacement := []byte("sentinel replacement")
	writeFile(t, path, original)
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	lstatCalls := 0
	removed := false
	operations := exactRemovalOperations{
		lstat: func(name string) (fs.FileInfo, error) {
			lstatCalls++
			if lstatCalls == 2 {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				writeFile(t, path, replacement)
				if err := os.Chmod(path, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			return os.Lstat(name)
		},
		open: os.Open,
		remove: func(string) error {
			removed = true
			return nil
		},
		syncDirectory: syncDirectory,
	}

	err := removeIfExact(root, "safe/config.txt", original, 0o600, operations)
	if err == nil || !strings.Contains(err.Error(), "destination drifted") {
		t.Fatalf("removeIfExact() error = %v, want identity drift", err)
	}
	if removed {
		t.Fatal("removeIfExact() attempted removal after identity drift")
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil || !bytes.Equal(data, replacement) {
		t.Fatalf("replacement data = %q, error %v", data, readErr)
	}
}

func TestRemoveIfExactRequiresDurabilityAndReadback(t *testing.T) {
	for _, tt := range []struct {
		name            string
		sync            func(string) error
		readbackFailure bool
		wantErr         string
		wantSync        bool
		wantMissing     bool
	}{
		{
			name:        "parent sync failure is not success",
			sync:        func(string) error { return errors.New("unsupported directory sync") },
			wantErr:     "parent directory sync failed",
			wantSync:    true,
			wantMissing: true,
		},
		{
			name:            "absence readback failure is not success",
			sync:            syncDirectory,
			readbackFailure: true,
			wantErr:         "absence verification failed",
			wantMissing:     true,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "safe", "config.txt")
			data := []byte("cortex-created")
			writeFile(t, path, data)
			if err := os.Chmod(path, 0o600); err != nil {
				t.Fatal(err)
			}
			syncCalls := 0
			lstatCalls := 0
			lstat := os.Lstat
			if tt.readbackFailure {
				lstat = func(name string) (fs.FileInfo, error) {
					lstatCalls++
					if lstatCalls < 3 {
						return os.Lstat(name)
					}
					return nil, errors.New("readback unavailable")
				}
			}
			operations := exactRemovalOperations{
				lstat:  lstat,
				open:   os.Open,
				remove: os.Remove,
				syncDirectory: func(directory string) error {
					syncCalls++
					return tt.sync(directory)
				},
			}

			err := removeIfExact(root, "safe/config.txt", data, 0o600, operations)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("removeIfExact() error = %v, want substring %q", err, tt.wantErr)
			}
			if tt.wantSync && syncCalls != 1 {
				t.Fatalf("sync calls = %d, want 1", syncCalls)
			}
			if tt.wantMissing {
				if _, statErr := os.Lstat(path); !errors.Is(statErr, os.ErrNotExist) {
					t.Fatalf("removed file stat error = %v, want not exist", statErr)
				}
			}
		})
	}
}

func TestRemoveIfMatches(t *testing.T) {
	tests := []struct {
		name       string
		relative   string
		expected   []byte
		setup      func(t *testing.T, root string) string
		wantErr    string
		wantAbsent bool
		preserve   []byte
		noLeak     []string
	}{
		{
			name:       "removes a matching regular file",
			relative:   "safe/config.txt",
			expected:   []byte("cortex-created\n"),
			wantAbsent: true,
			setup: func(t *testing.T, root string) string {
				t.Helper()
				writeFile(t, filepath.Join(root, "safe", "config.txt"), []byte("cortex-created\n"))
				return root
			},
		},
		{
			name:     "accepts a missing leaf",
			relative: "safe/config.txt",
			expected: []byte("cortex-created\n"),
			setup: func(t *testing.T, root string) string {
				t.Helper()
				if err := os.Mkdir(filepath.Join(root, "safe"), 0o755); err != nil {
					t.Fatal(err)
				}
				return root
			},
		},
		{
			name:     "preserves mismatched content without leaking bytes",
			relative: "safe/config.txt",
			expected: []byte("expected-secret"),
			wantErr:  "bytes do not match",
			preserve: []byte("actual-secret"),
			noLeak:   []string{"expected-secret", "actual-secret"},
			setup: func(t *testing.T, root string) string {
				t.Helper()
				writeFile(t, filepath.Join(root, "safe", "config.txt"), []byte("actual-secret"))
				return root
			},
		},
		{
			name:     "rejects traversal",
			relative: filepath.Join("..", "outside.txt"),
			expected: []byte("cortex-created"),
			wantErr:  "escapes root",
		},
		{
			name:     "rejects absolute paths",
			relative: filepath.Join(string(filepath.Separator), "outside.txt"),
			expected: []byte("cortex-created"),
			wantErr:  "absolute",
		},
		{
			name:     "rejects a missing parent",
			relative: "missing/config.txt",
			expected: []byte("cortex-created"),
			wantErr:  "parent does not exist",
		},
		{
			name:     "rejects a directory target",
			relative: "safe/config",
			expected: []byte("cortex-created"),
			wantErr:  "not a regular file",
			setup: func(t *testing.T, root string) string {
				t.Helper()
				if err := os.MkdirAll(filepath.Join(root, "safe", "config"), 0o755); err != nil {
					t.Fatal(err)
				}
				return root
			},
		},
		{
			name:     "rejects a symlink root",
			relative: "config.txt",
			expected: []byte("cortex-created"),
			wantErr:  "root is a symlink",
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
		},
		{
			name:     "rejects a symlink ancestor",
			relative: "linked/config.txt",
			expected: []byte("cortex-created"),
			wantErr:  "symlink component",
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
		},
		{
			name:     "rejects a symlink leaf",
			relative: "safe/config.txt",
			expected: []byte("cortex-created"),
			wantErr:  "symlink component",
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
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			if tt.setup != nil {
				root = tt.setup(t, root)
			}

			err := RemoveIfMatches(root, tt.relative, tt.expected)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("RemoveIfMatches() error = %v, want substring %q", err, tt.wantErr)
				}
				for _, value := range tt.noLeak {
					if strings.Contains(err.Error(), value) {
						t.Fatalf("RemoveIfMatches() error leaked file content %q", value)
					}
				}
				if tt.preserve != nil {
					data, readErr := os.ReadFile(filepath.Join(root, tt.relative))
					if readErr != nil || !bytes.Equal(data, tt.preserve) {
						t.Fatalf("preserved file data = %q, error %v", data, readErr)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("RemoveIfMatches() error = %v", err)
			}
			if tt.wantAbsent {
				_, statErr := os.Lstat(filepath.Join(root, tt.relative))
				if !errors.Is(statErr, os.ErrNotExist) {
					t.Fatalf("removed file stat error = %v, want not exist", statErr)
				}
			}
		})
	}
}
