package atomicfile

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
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

func TestCreateIfAbsent(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(t *testing.T, path string)
		wantErr  string
		wantData []byte
		wantMode fs.FileMode
	}{
		{name: "creates an absent regular file", wantData: []byte("created\n"), wantMode: 0o600},
		{name: "preserves a concurrent appearance", setup: func(t *testing.T, path string) { writeFile(t, path, []byte("user file")) }, wantErr: "destination already exists", wantData: []byte("user file"), wantMode: 0o644},
		{name: "rejects a symlink conflict", setup: func(t *testing.T, path string) {
			target := filepath.Join(filepath.Dir(path), "target")
			writeFile(t, target, []byte("user file"))
			if err := os.Symlink(target, path); err != nil {
				t.Fatal(err)
			}
		}, wantErr: "symlink component"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "safe", "config.txt")
			if err := os.Mkdir(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if tt.setup != nil {
				tt.setup(t, path)
			}
			err := CreateIfAbsent(root, "safe/config.txt", []byte("created\n"), 0o600)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("CreateIfAbsent() error = %v, want %q", err, tt.wantErr)
				}
				if tt.wantData != nil {
					assertFile(t, path, tt.wantData, tt.wantMode)
				}
			} else if err != nil {
				t.Fatalf("CreateIfAbsent() error = %v", err)
			} else {
				assertFile(t, path, tt.wantData, tt.wantMode)
			}
			assertNoTemporaryFiles(t, root)
		})
	}
}

func TestObserveRootedAbsent(t *testing.T) {
	if runtime.GOOS == "js" || runtime.GOOS == "plan9" {
		t.Skip("rooted absence evidence is unsupported")
	}
	t.Run("retains an anchored parent without closing the caller root", func(t *testing.T) {
		for _, tt := range []struct {
			name, relative string
			mode           fs.FileMode
		}{
			{"top level", "config.txt", 0o000},
			{"nested", "safe/nested/config.txt", 0o600},
		} {
			t.Run(tt.name, func(t *testing.T) {
				path := t.TempDir()
				if err := os.MkdirAll(filepath.Join(path, filepath.Dir(tt.relative)), 0o755); err != nil {
					t.Fatal(err)
				}
				root, err := os.OpenRoot(path)
				if err != nil {
					t.Fatal(err)
				}
				defer root.Close()
				evidence, err := observeRootedAbsent(root, tt.relative, tt.mode, rootedAbsentEvidenceOperations{})
				if err != nil {
					t.Fatal(err)
				}
				defer evidence.parent.Close()
				if evidence.basename != "config.txt" || evidence.mode != tt.mode {
					t.Fatalf("evidence = %+v", evidence)
				}
				if _, err := root.Lstat("."); err != nil {
					t.Fatalf("caller root was closed: %v", err)
				}
			})
		}
	})

	t.Run("retains the opened root across a rename", func(t *testing.T) {
		path := t.TempDir()
		if err := os.Mkdir(filepath.Join(path, "safe"), 0o755); err != nil {
			t.Fatal(err)
		}
		root, err := os.OpenRoot(path)
		if err != nil {
			t.Fatal(err)
		}
		defer root.Close()
		dots := 0
		evidence, err := observeRootedAbsent(root, "safe/config.txt", 0o600, rootedAbsentEvidenceOperations{lstat: func(r *os.Root, name string) (fs.FileInfo, error) {
			info, err := r.Lstat(name)
			if name == "." {
				dots++
				if dots == 2 {
					moved := path + "-moved"
					t.Cleanup(func() { _ = os.RemoveAll(moved) })
					if err := os.Rename(path, moved); err != nil {
						t.Fatal(err)
					}
					if err := os.MkdirAll(filepath.Join(path, "safe"), 0o755); err != nil {
						t.Fatal(err)
					}
				}
			}
			return info, err
		}})
		if err != nil {
			t.Fatal(err)
		}
		defer evidence.parent.Close()
		if _, err := evidence.parent.Lstat("."); err != nil {
			t.Fatalf("retained parent is unusable: %v", err)
		}
	})

	t.Run("rejects parent replacement before opening it", func(t *testing.T) {
		path := t.TempDir()
		if err := os.Mkdir(filepath.Join(path, "safe"), 0o755); err != nil {
			t.Fatal(err)
		}
		root, err := os.OpenRoot(path)
		if err != nil {
			t.Fatal(err)
		}
		defer root.Close()
		_, err = observeRootedAbsent(root, "safe/config.txt", 0o600, rootedAbsentEvidenceOperations{lstat: func(r *os.Root, name string) (fs.FileInfo, error) {
			info, err := r.Lstat(name)
			if name == "safe" {
				if err := os.Rename(filepath.Join(path, "safe"), filepath.Join(path, "old")); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(filepath.Join(path, "safe"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			return info, err
		}})
		if err == nil || strings.Contains(err.Error(), "safe") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("rejects destination appearance and existing leaf types", func(t *testing.T) {
		for _, tt := range []struct {
			name  string
			setup func(string) error
		}{
			{"appearance", func(path string) error { return os.WriteFile(path, []byte("appeared"), 0o600) }},
			{"regular", func(path string) error { return os.WriteFile(path, []byte("existing"), 0o600) }},
			{"directory", func(path string) error { return os.Mkdir(path, 0o755) }},
			{"symlink", func(path string) error { return os.Symlink("target", path) }},
		} {
			t.Run(tt.name, func(t *testing.T) {
				path := t.TempDir()
				if err := os.Mkdir(filepath.Join(path, "safe"), 0o755); err != nil {
					t.Fatal(err)
				}
				root, err := os.OpenRoot(path)
				if err != nil {
					t.Fatal(err)
				}
				defer root.Close()
				if tt.name == "appearance" {
					_, err = observeRootedAbsent(root, "safe/config", 0o600, rootedAbsentEvidenceOperations{lstat: func(r *os.Root, name string) (fs.FileInfo, error) {
						if name == "config" {
							_ = tt.setup(filepath.Join(path, "safe", name))
						}
						return r.Lstat(name)
					}})
				} else {
					if err := tt.setup(filepath.Join(path, "safe", "config")); err != nil {
						t.Fatal(err)
					}
					_, err = observeRootedAbsent(root, "safe/config", 0o600, rootedAbsentEvidenceOperations{})
				}
				if err == nil {
					t.Fatal("observeRootedAbsent() error = nil")
				}
			})
		}
	})
}

func TestObserveRootedAbsentRejectsInvalidInputsAndClosesOwnedRoots(t *testing.T) {
	if runtime.GOOS == "js" || runtime.GOOS == "plan9" {
		t.Skip("rooted absence evidence is unsupported")
	}
	path := t.TempDir()
	if err := os.Mkdir(filepath.Join(path, "safe"), 0o755); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	for _, tt := range []struct {
		name, relative string
		root           *os.Root
		mode           fs.FileMode
	}{
		{"nil root", "safe/config", nil, 0o600},
		{"empty path", "", root, 0o600},
		{"traversal", "../secret/path", root, 0o600},
		{"noncanonical", "safe/../secret/path", root, 0o600},
		{"backslash", `safe\secret`, root, 0o600},
		{"unsupported mode", "safe/config", root, fs.ModeSetuid | 0o600},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := observeRootedAbsent(tt.root, tt.relative, tt.mode, rootedAbsentEvidenceOperations{})
			if err == nil || strings.Contains(err.Error(), "secret") {
				t.Fatalf("error = %v", err)
			}
		})
	}
	if err := os.WriteFile(filepath.Join(path, "not-a-directory"), []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, relative := range []string{"missing/config", "not-a-directory/config"} {
		_, err := observeRootedAbsent(root, relative, 0o600, rootedAbsentEvidenceOperations{})
		if err == nil {
			t.Fatalf("invalid parent %q was accepted", relative)
		}
	}
	closed, err := os.OpenRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := closed.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := observeRootedAbsent(closed, "safe/config", 0o600, rootedAbsentEvidenceOperations{}); err == nil {
		t.Fatal("closed root was accepted")
	}
	for _, tt := range []struct {
		name string
		op   func(*os.Root, string) (fs.FileInfo, error)
	}{
		{"nil nil", func(*os.Root, string) (fs.FileInfo, error) { return nil, nil }},
		{"other error", func(*os.Root, string) (fs.FileInfo, error) { return nil, errors.New("secret lstat") }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := observeRootedAbsent(root, "safe/config", 0o600, rootedAbsentEvidenceOperations{lstat: func(r *os.Root, name string) (fs.FileInfo, error) {
				if name == "config" {
					return tt.op(r, name)
				}
				return r.Lstat(name)
			}})
			if err == nil || strings.Contains(err.Error(), "secret") {
				t.Fatalf("error = %v", err)
			}
		})
	}

	for _, tt := range []struct {
		name string
		open func(*os.Root, string) (*os.Root, error)
		want int
	}{
		{"initial nonnil error", func(*os.Root, string) (*os.Root, error) {
			r, _ := os.OpenRoot(path)
			return r, errors.New("secret open")
		}, 1},
		{"intermediate nonnil error", func(r *os.Root, name string) (*os.Root, error) {
			if name == "." {
				return r.OpenRoot(name)
			}
			next, _ := r.OpenRoot(name)
			return next, errors.New("secret open")
		}, 2},
	} {
		t.Run(tt.name, func(t *testing.T) {
			closes := 0
			_, err := observeRootedAbsent(root, "safe/config", 0o600, rootedAbsentEvidenceOperations{openRoot: tt.open, close: func(r *os.Root) error {
				closes++
				return r.Close()
			}})
			if err == nil || strings.Contains(err.Error(), "secret") || closes != tt.want {
				t.Fatalf("error = %v, closes = %d, want %d", err, closes, tt.want)
			}
		})
	}
	t.Run("closes the next root once when prior close fails", func(t *testing.T) {
		closes := 0
		_, err := observeRootedAbsent(root, "safe/config", 0o600, rootedAbsentEvidenceOperations{close: func(r *os.Root) error {
			closes++
			if err := r.Close(); err != nil {
				return err
			}
			if closes == 1 {
				return errors.New("secret close")
			}
			return nil
		}})
		if err == nil || strings.Contains(err.Error(), "secret") || closes != 2 {
			t.Fatalf("error = %v, closes = %d, want 2", err, closes)
		}
	})
}

func TestCreateIfAbsentPreservesConcurrentAppearance(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "safe"), 0o755); err != nil {
		t.Fatal(err)
	}
	var ready sync.WaitGroup
	ready.Add(2)
	start, errs := make(chan struct{}), make(chan error, 2)
	for _, data := range [][]byte{[]byte("first"), []byte("second")} {
		go func(data []byte) {
			ready.Done()
			<-start
			errs <- CreateIfAbsent(root, "safe/config.txt", data, 0o600)
		}(data)
	}
	ready.Wait()
	close(start)
	successes := 0
	for range 2 {
		if <-errs == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("successful creates = %d, want 1", successes)
	}
	data, err := os.ReadFile(filepath.Join(root, "safe/config.txt"))
	if err != nil || (string(data) != "first" && string(data) != "second") {
		t.Fatalf("created file = %q, error %v", data, err)
	}
	assertNoTemporaryFiles(t, root)
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
		if strings.HasPrefix(entry.Name(), ".cortex-replace-") || strings.HasPrefix(entry.Name(), ".cortex-create-") {
			t.Fatalf("temporary file remains: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
