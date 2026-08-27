package safepath

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolve(t *testing.T) {
	tests := []struct {
		name         string
		candidate    string
		setup        func(t *testing.T, root string) string
		wantRelative string
		wantErr      string
	}{
		{
			name:      "accepts an existing path beneath the root",
			candidate: "safe/existing.txt",
			setup: func(t *testing.T, root string) string {
				t.Helper()
				writeTestFile(t, filepath.Join(root, "safe", "existing.txt"))
				return root
			},
			wantRelative: "safe/existing.txt",
		},
		{
			name:      "allows a missing leaf beneath an existing directory",
			candidate: "safe/new.txt",
			setup: func(t *testing.T, root string) string {
				t.Helper()
				if err := os.Mkdir(filepath.Join(root, "safe"), 0o755); err != nil {
					t.Fatal(err)
				}
				return root
			},
			wantRelative: "safe/new.txt",
		},
		{
			name:      "rejects an empty candidate",
			candidate: "",
			wantErr:   "empty",
		},
		{
			name:      "rejects the current directory candidate",
			candidate: ".",
			wantErr:   "concrete path",
		},
		{
			name:      "rejects an absolute candidate",
			candidate: filepath.Join(string(filepath.Separator), "outside"),
			wantErr:   "absolute",
		},
		{
			name:      "rejects traversal outside the root",
			candidate: filepath.Join("..", "outside"),
			wantErr:   "escapes root",
		},
		{
			name:      "rejects a symlink root",
			candidate: "file.txt",
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
			name:      "rejects a symlink ancestor",
			candidate: "linked/file.txt",
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
			name:      "rejects a symlink leaf",
			candidate: "safe/linked.txt",
			setup: func(t *testing.T, root string) string {
				t.Helper()
				if err := os.Mkdir(filepath.Join(root, "safe"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("elsewhere.txt", filepath.Join(root, "safe", "linked.txt")); err != nil {
					t.Fatal(err)
				}
				return root
			},
			wantErr: "symlink component",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			if tt.setup != nil {
				root = tt.setup(t, root)
			}

			got, err := Resolve(root, tt.candidate)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("Resolve(%q, %q) returned %q without an error", root, tt.candidate, got)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Resolve(%q, %q) error = %q, want substring %q", root, tt.candidate, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve(%q, %q) error = %v", root, tt.candidate, err)
			}

			want := filepath.Join(root, tt.wantRelative)
			if got != want {
				t.Fatalf("Resolve(%q, %q) = %q, want %q", root, tt.candidate, got, want)
			}
		})
	}
}

func writeTestFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("test"), 0o644); err != nil {
		t.Fatal(err)
	}
}
