package atomicfile

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
