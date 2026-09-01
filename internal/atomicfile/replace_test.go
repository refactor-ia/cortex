package atomicfile

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
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
func TestStageRootedCreate(t *testing.T) {
	if runtime.GOOS == "js" || runtime.GOOS == "plan9" {
		t.Skip("rooted staging is unsupported")
	}
	if !rootedCreateModeOK(0o666, 0o600, true) || !rootedCreateModeOK(0o444, 0o000, true) || rootedCreateModeOK(0o666, 0o000, true) || rootedCreateModeOK(0o666, 0o600, false) || !rootedCreateModeOK(0o600, 0o600, false) {
		t.Fatal("rooted create mode matching is incorrect")
	}
	for _, tt := range []struct {
		name, relative string
		data           []byte
		mode           fs.FileMode
	}{
		{"top level empty mode", "config", nil, 0o000},
		{"nested content", "safe/config", []byte("staged data"), 0o600},
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
			stage, err := stageRootedCreate(root, tt.relative, tt.data, tt.mode, rootedCreateStagingOperations{random: bytes.NewReader(make([]byte, 16))})
			if err != nil {
				t.Fatal(err)
			}
			if stage.parent == root || stage.basename != "config" || !stage.info.Mode().IsRegular() || !rootedCreateModeOK(stage.info.Mode(), tt.mode, runtime.GOOS == "windows") || !bytes.Equal(stage.data, tt.data) {
				t.Fatalf("stage = %+v", stage)
			}
			if info, err := stage.parent.Lstat(stage.temporary); err != nil || !os.SameFile(stage.info, info) {
				t.Fatalf("staged inode = %v, error = %v", info, err)
			}
			if tt.mode != 0 {
				file, err := stage.parent.Open(stage.temporary)
				if err != nil {
					t.Fatal(err)
				}
				got, err := io.ReadAll(file)
				closeErr := file.Close()
				if err != nil || closeErr != nil || !bytes.Equal(got, tt.data) {
					t.Fatalf("staged content = %q, errors = %v, %v", got, err, closeErr)
				}
			}
			if err := discardRootedCreateStage(&stage, rootedCreateStagingOperations{}); err != nil || stage.parent != nil || stage.temporary != "" || stage.data != nil {
				t.Fatalf("discard error = %v, stage = %+v", err, stage)
			}
			assertNoTemporaryFiles(t, path)
		})
	}
}
func TestStageRootedCreateRetriesAndUsesRetainedParent(t *testing.T) {
	if runtime.GOOS == "js" || runtime.GOOS == "plan9" {
		t.Skip("rooted staging is unsupported")
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
	attempts, removes := 0, []string(nil)
	collision := &rootedCreateTestFile{}
	stage, err := stageRootedCreate(root, "safe/config", []byte("data"), 0o600, rootedCreateStagingOperations{
		random: bytes.NewReader(append(make([]byte, 16), append([]byte{1}, make([]byte, 15)...)...)),
		openFile: func(parent *os.Root, name string, flag int, mode fs.FileMode) (rootedCreateStageFile, error) {
			attempts++
			if attempts == 1 {
				return collision, fs.ErrExist
			}
			if err := os.Rename(filepath.Join(path, "safe"), filepath.Join(path, "old")); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(filepath.Join(path, "safe"), 0o755); err != nil {
				t.Fatal(err)
			}
			return parent.OpenFile(name, flag, mode)
		},
		remove: func(parent *os.Root, name string) error { removes = append(removes, name); return parent.Remove(name) },
	})
	if err != nil || attempts != 2 || collision.closes != 1 || len(removes) != 0 {
		t.Fatalf("stage error = %v, attempts/closes/removes = %d/%d/%d", err, attempts, collision.closes, len(removes))
	}
	if _, err := os.Stat(filepath.Join(path, "old", stage.temporary)); err != nil {
		t.Fatalf("temporary was not created under retained parent: %v", err)
	}
	temporary := stage.temporary
	if err := discardRootedCreateStage(&stage, rootedCreateStagingOperations{remove: func(parent *os.Root, name string) error { removes = append(removes, name); return parent.Remove(name) }}); err != nil {
		t.Fatal(err)
	}
	if len(removes) != 1 || removes[0] != temporary {
		t.Fatalf("removed = %v, want owned temporary", removes)
	}
	root, err = os.OpenRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	attempts = 0
	_, err = stageRootedCreate(root, "safe/config", nil, 0o600, rootedCreateStagingOperations{
		random: bytes.NewReader(make([]byte, 16*128)),
		openFile: func(*os.Root, string, int, fs.FileMode) (rootedCreateStageFile, error) {
			attempts++
			return nil, fs.ErrExist
		},
	})
	if err == nil || attempts != 128 || strings.Contains(err.Error(), "exist") {
		t.Fatalf("error = %v, attempts = %d", err, attempts)
	}
}
func TestStageRootedCreateStopsBeforeTemporary(t *testing.T) {
	if runtime.GOOS == "js" || runtime.GOOS == "plan9" {
		t.Skip("rooted staging is unsupported")
	}
	for _, tt := range []struct {
		name      string
		random    io.Reader
		collision bool
	}{
		{"entropy", strings.NewReader(""), false},
		{"open", bytes.NewReader(make([]byte, 16)), false},
		{"collision close", bytes.NewReader(make([]byte, 16)), true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root, err := os.OpenRoot(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			attempts, removes, syncs, closes := 0, 0, 0, 0
			private := errors.New("private cause/name/data")
			collision := &rootedCreateTestFile{closeErr: private}
			ops := rootedCreateStagingOperations{random: tt.random, remove: func(*os.Root, string) error { removes++; return nil }, syncParent: func(*os.Root) error { syncs++; return nil }, closeParent: func(*os.Root) error { closes++; return nil }}
			ops.openFile = func(*os.Root, string, int, fs.FileMode) (rootedCreateStageFile, error) {
				attempts++
				if tt.collision {
					return collision, fs.ErrExist
				}
				return nil, private
			}
			_, err = stageRootedCreate(root, "config", nil, 0o600, ops)
			if err == nil || strings.Contains(err.Error(), "private") || syncs != 0 || closes != 1 {
				t.Fatalf("error = %v, cleanup = %d/%d/%d", err, removes, syncs, closes)
			}
			_, rootErr := root.Lstat(".")
			if tt.collision && (attempts != 1 || removes != 0 || collision.closes != 1 || rootErr != nil || !strings.Contains(err.Error(), "close temporary failed")) {
				t.Fatalf("error/root = %v/%v, attempts/removes/file closes = %d/%d/%d", err, rootErr, attempts, removes, collision.closes)
			}
		})
	}
}
func TestStageRootedCreateFailureCleanupAndDiscard(t *testing.T) {
	if runtime.GOOS == "js" || runtime.GOOS == "plan9" {
		t.Skip("rooted staging is unsupported")
	}
	for _, tt := range []struct {
		name string
		file rootedCreateStageFile
	}{
		{"chmod", &rootedCreateTestFile{chmodErr: errors.New("private")}},
		{"write", &rootedCreateTestFile{writeErr: errors.New("private")}},
		{"zero progress", &rootedCreateTestFile{zeroWrite: true}},
		{"sync", &rootedCreateTestFile{syncErr: errors.New("private")}},
		{"stat", &rootedCreateTestFile{statErr: errors.New("private")}},
		{"invalid stat", &rootedCreateTestFile{info: rootedCreateTestInfo{mode: fs.ModeDir | 0o600}}},
		{"close", &rootedCreateTestFile{info: rootedCreateTestInfo{mode: 0o600}, closeErr: errors.New("private")}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root, err := os.OpenRoot(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			removes, syncs, closes := 0, 0, 0
			_, err = stageRootedCreate(root, "config", []byte("x"), 0o600, rootedCreateStagingOperations{random: bytes.NewReader(make([]byte, 16)), openFile: func(*os.Root, string, int, fs.FileMode) (rootedCreateStageFile, error) { return tt.file, nil }, remove: func(*os.Root, string) error { removes++; return errors.New("private") }, syncParent: func(*os.Root) error { syncs++; return errors.New("private") }, closeParent: func(*os.Root) error { closes++; return errors.New("private") }})
			if err == nil || strings.Contains(err.Error(), "private") || removes != 1 || syncs != 1 || closes != 1 || tt.file.(*rootedCreateTestFile).closes != 1 {
				t.Fatalf("error = %v, cleanup = %d/%d/%d, file closes = %d", err, removes, syncs, closes, tt.file.(*rootedCreateTestFile).closes)
			}
		})
	}
	path := t.TempDir()
	root, err := os.OpenRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	stage := rootedCreateStage{parent: root, temporary: ".cortex-create-test", data: []byte("owned")}
	file, err := root.OpenFile(stage.temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil || file.Close() != nil {
		t.Fatal(err)
	}
	removes, syncs, closes := 0, 0, 0
	err = discardRootedCreateStage(&stage, rootedCreateStagingOperations{remove: func(root *os.Root, name string) error { removes++; _ = root.Remove(name); return errors.New("private") }, syncParent: func(*os.Root) error { syncs++; return errors.New("private") }, closeParent: func(*os.Root) error { closes++; return errors.New("private") }})
	if err == nil || strings.Contains(err.Error(), "private") || removes != 1 || syncs != 1 || closes != 1 || stage.parent != nil || stage.temporary != "" || stage.data != nil {
		t.Fatalf("discard = %v, counts = %d/%d/%d, stage = %+v", err, removes, syncs, closes, stage)
	}
	if _, err := root.Lstat(".cortex-create-test"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("temporary residue error = %v", err)
	}
	if err := discardRootedCreateStage(&stage, rootedCreateStagingOperations{}); err != nil {
		t.Fatalf("reused discard = %v", err)
	}
}

func TestFinalizeRootedCreateStage(t *testing.T) {
	if runtime.GOOS == "js" || runtime.GOOS == "plan9" {
		t.Skip("rooted finalization is unsupported")
	}
	for _, tt := range []struct {
		name, relative string
		data           []byte
		mode           fs.FileMode
	}{
		{"nested content", "safe/config", []byte("final data"), 0o600},
	} {
		t.Run(tt.name, func(t *testing.T) {
			stage, path := rootedCreateLinkedStage(t, tt.relative, tt.data, tt.mode)
			temporary := stage.temporary
			closes := 0
			operations := rootedCreateFinalizationOperations{rootedCreateStagingOperations: rootedCreateStagingOperations{closeParent: func(root *os.Root) error {
				closes++
				return root.Close()
			}}}
			verified, err := finalizeRootedCreateStage(&stage, operations)
			if err != nil || !verified || closes != 1 || stage.parent != nil || stage.temporary != "" || stage.info != nil || stage.data != nil {
				t.Fatalf("finalize = %v, %v, closes = %d, stage = %+v", verified, err, closes, stage)
			}
			destination := filepath.Join(path, tt.relative)
			assertFile(t, destination, tt.data, tt.mode)
			if _, err := os.Lstat(filepath.Join(path, filepath.Dir(tt.relative), temporary)); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("temporary remains: %v", err)
			}
		})
	}
}

func TestFinalizeRootedCreateStageRejectsUnreadableStageAfterCleanup(t *testing.T) {
	if runtime.GOOS == "js" || runtime.GOOS == "plan9" {
		t.Skip("rooted finalization is unsupported")
	}
	stage, path := rootedCreateLinkedStage(t, "config", []byte("data"), 0o000)
	temporary, calls, reads := stage.temporary, [3]int{}, 0
	verified, err := finalizeRootedCreateStage(&stage, rootedCreateFinalizationOperations{rootedCreateStagingOperations: rootedCreateStagingOperations{
		remove: func(root *os.Root, name string) error { calls[0]++; return root.Remove(name) },
		syncParent: func(root *os.Root) error {
			calls[1]++
			return rootedCreateSyncParent(rootedCreateStagingOperations{}, root)
		},
		closeParent: func(root *os.Root) error { calls[2]++; return root.Close() },
	}, openRead: func(*os.Root, string) (rootedCreateReadbackFile, error) { reads++; return nil, errors.New("private") }})
	if verified || err == nil || !strings.Contains(err.Error(), "readback failed") || strings.Contains(err.Error(), "private") || calls != [3]int{1, 1, 1} || reads != 0 || stage.parent != nil {
		t.Fatalf("finalize = %v, %v, cleanup/reads = %v/%d", verified, err, calls, reads)
	}
	if info, statErr := os.Lstat(filepath.Join(path, "config")); statErr != nil || info.Mode().Perm() != 0o000 {
		t.Fatalf("destination = %v, error = %v", info, statErr)
	}
	if _, statErr := os.Lstat(filepath.Join(path, temporary)); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("temporary remains: %v", statErr)
	}
}

func TestFinalizeRootedCreateStageRejectsRivalAndConsumesStage(t *testing.T) {
	if runtime.GOOS == "js" || runtime.GOOS == "plan9" {
		t.Skip("rooted finalization is unsupported")
	}
	stage, path := rootedCreateStaged(t, "safe/config", []byte("same"), 0o600)
	if err := os.WriteFile(filepath.Join(path, "safe", "config"), []byte("same"), 0o600); err != nil {
		t.Fatal(err)
	}
	temporary := stage.temporary
	verified, err := finalizeRootedCreateStage(&stage, rootedCreateFinalizationOperations{})
	if verified || err == nil || stage.parent != nil || strings.Contains(err.Error(), path) || strings.Contains(err.Error(), "same") {
		t.Fatalf("finalize = %v, %v, stage = %+v", verified, err, stage)
	}
	assertFile(t, filepath.Join(path, "safe", "config"), []byte("same"), 0o600)
	if _, err := os.Lstat(filepath.Join(path, "safe", temporary)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("temporary remains: %v", err)
	}
}

func TestFinalizeRootedCreateStageReadbackMatrix(t *testing.T) {
	if runtime.GOOS == "js" || runtime.GOOS == "plan9" {
		t.Skip("rooted finalization is unsupported")
	}
	private := errors.New("private path and data")
	for _, tt := range []struct {
		name       string
		lstatAt    int
		openErr    error
		statAt     int
		firstErr   error
		secondData []byte
		seekErr    error
		closeErr   error
		wantTrue   bool
		wantCloses int
	}{
		{"first lstat", 1, nil, 0, nil, nil, nil, nil, false, 0},
		{"open nonnil error", 0, private, 0, nil, nil, nil, private, false, 1},
		{"fstat", 0, nil, 1, nil, nil, nil, nil, false, 1},
		{"first read", 0, nil, 0, private, nil, nil, nil, false, 1},
		{"second lstat", 2, nil, 0, nil, nil, nil, nil, false, 1},
		{"seek", 0, nil, 0, nil, nil, private, nil, false, 1},
		{"second read", 0, nil, 0, nil, []byte("drift"), nil, nil, false, 1},
		{"final fstat", 0, nil, 2, nil, nil, nil, nil, false, 1},
		{"final lstat", 3, nil, 0, nil, nil, nil, nil, false, 1},
		{"read close after proof", 0, nil, 0, nil, nil, nil, private, true, 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			stage, _ := rootedCreateLinkedStage(t, "config", []byte("data"), 0o600)
			info, err := stage.parent.Lstat(stage.basename)
			if err != nil {
				t.Fatal(err)
			}
			file := &rootedCreateReadbackTestFile{info: info, first: stage.data, second: stage.data, statAt: tt.statAt, firstErr: tt.firstErr, seekErr: tt.seekErr, closeErr: tt.closeErr}
			if tt.secondData != nil {
				file.second = tt.secondData
			}
			lstats := 0
			operations := rootedCreateFinalizationOperations{rootedCreateStagingOperations: rootedCreateStagingOperations{rootedAbsentEvidenceOperations: rootedAbsentEvidenceOperations{lstat: func(root *os.Root, name string) (fs.FileInfo, error) {
				lstats++
				if lstats == tt.lstatAt {
					return nil, private
				}
				return root.Lstat(name)
			}}}, openRead: func(*os.Root, string) (rootedCreateReadbackFile, error) { return file, tt.openErr }}
			verified, err := finalizeRootedCreateStage(&stage, operations)
			if verified != tt.wantTrue || err == nil || file.closes != tt.wantCloses || strings.Contains(err.Error(), "private") || tt.name == "open nonnil error" && (!strings.Contains(err.Error(), "readback failed") || !strings.Contains(err.Error(), "read close failed")) {
				t.Fatalf("finalize = %v, %v, file closes = %d", verified, err, file.closes)
			}
		})
	}
}

func TestFinalizeRootedCreateStageAttemptsCleanupAndPinsInvalidReuse(t *testing.T) {
	if runtime.GOOS == "js" || runtime.GOOS == "plan9" {
		t.Skip("rooted finalization is unsupported")
	}
	stage, _ := rootedCreateLinkedStage(t, "config", []byte("data"), 0o600)
	private := errors.New("private cause")
	calls := [3]int{}
	fileInfo, err := stage.parent.Lstat(stage.basename)
	if err != nil {
		t.Fatal(err)
	}
	file := &rootedCreateReadbackTestFile{info: fileInfo, first: stage.data, second: stage.data}
	verified, err := finalizeRootedCreateStage(&stage, rootedCreateFinalizationOperations{rootedCreateStagingOperations: rootedCreateStagingOperations{
		remove: func(*os.Root, string) error { calls[0]++; return private }, syncParent: func(*os.Root) error { calls[1]++; return private }, closeParent: func(*os.Root) error { calls[2]++; return private },
	}, openRead: func(*os.Root, string) (rootedCreateReadbackFile, error) { return file, nil }})
	if !verified || err == nil || calls != [3]int{1, 1, 1} || file.closes != 1 || strings.Contains(err.Error(), "private") {
		t.Fatalf("finalize = %v, %v, calls = %v, closes = %d", verified, err, calls, file.closes)
	}
	if verified, err = finalizeRootedCreateStage(&stage, rootedCreateFinalizationOperations{}); verified || err == nil || !strings.Contains(err.Error(), "invalid stage") {
		t.Fatalf("reuse = %v, %v", verified, err)
	}
	root, openErr := os.OpenRoot(t.TempDir())
	if openErr != nil {
		t.Fatal(openErr)
	}
	invalid := rootedCreateStage{parent: root}
	if verified, err = finalizeRootedCreateStage(&invalid, rootedCreateFinalizationOperations{}); verified || err == nil || invalid.parent != nil {
		t.Fatalf("invalid = %v, %v, stage = %+v", verified, err, invalid)
	}
}

func TestCreateIfAbsentRoot(t *testing.T) {
	if runtime.GOOS == "js" || runtime.GOOS == "plan9" {
		t.Skip("rooted create is unsupported")
	}
	for _, tt := range []struct {
		name, relative string
		data           []byte
		mode           fs.FileMode
	}{
		{"top-level empty", "config", nil, 0o600},
		{"nested content", "safe/nested/config", []byte("created"), 0o640},
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
			if err := CreateIfAbsentRoot(root, tt.relative, tt.data, tt.mode); err != nil {
				t.Fatal(err)
			}
			assertFile(t, filepath.Join(path, tt.relative), tt.data, tt.mode)
			if _, err := root.Lstat("."); err != nil {
				t.Fatalf("caller root unusable: %v", err)
			}
			assertNoTemporaryFiles(t, path)
		})
	}
}

func TestCreateIfAbsentRootRejectsUnreadableModeBeforeRootUse(t *testing.T) {
	if runtime.GOOS == "js" || runtime.GOOS == "plan9" {
		t.Skip("rooted create is unsupported")
	}
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	err = CreateIfAbsentRoot(root, "config", nil, 0o200)
	if err == nil || errors.Is(err, ErrCreateIfAbsentRootPublicationAttempted) || errors.Is(err, ErrCreateIfAbsentRootPublicationVerified) {
		t.Fatalf("error = %v", err)
	}
	if _, err := root.Lstat("."); err != nil {
		t.Fatalf("caller root changed: %v", err)
	}
}

func TestCreateIfAbsentRootPrePublicationFailures(t *testing.T) {
	if runtime.GOOS == "js" || runtime.GOOS == "plan9" {
		t.Skip("rooted create is unsupported")
	}
	for _, tt := range []struct {
		name  string
		lstat func(*os.Root, string) (fs.FileInfo, error)
	}{
		{"appearance", func(r *os.Root, name string) (fs.FileInfo, error) { return r.Lstat(name) }},
		{"info and error", func(r *os.Root, name string) (fs.FileInfo, error) {
			info, _ := r.Lstat(name)
			return info, errors.New("private")
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := t.TempDir()
			root, err := os.OpenRoot(path)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			leafChecks, links, removes, syncs, closes := 0, 0, 0, 0, 0
			ops := rootedCreateOperations{rootedCreateFinalizationOperations: rootedCreateFinalizationOperations{rootedCreateStagingOperations: rootedCreateStagingOperations{rootedAbsentEvidenceOperations: rootedAbsentEvidenceOperations{lstat: func(r *os.Root, name string) (fs.FileInfo, error) {
				if name == "config" {
					leafChecks++
					if leafChecks == 2 {
						if err := os.WriteFile(filepath.Join(path, name), []byte("rival"), 0o600); err != nil {
							t.Fatal(err)
						}
						return tt.lstat(r, name)
					}
				}
				return r.Lstat(name)
			}}, remove: func(r *os.Root, name string) error { removes++; return r.Remove(name) }, syncParent: func(r *os.Root) error { syncs++; return rootedCreateSyncParent(rootedCreateStagingOperations{}, r) }, closeParent: func(r *os.Root) error { closes++; return r.Close() }}}, link: func(*os.Root, string, string) error { links++; return nil }}
			err = createIfAbsentRoot(root, "config", []byte("mine"), 0o600, ops)
			if err == nil || links != 0 || removes != 1 || syncs != 1 || closes != 1 || errors.Is(err, ErrCreateIfAbsentRootPublicationAttempted) || errors.Is(err, ErrCreateIfAbsentRootPublicationVerified) || strings.Contains(err.Error(), "private") {
				t.Fatalf("error/cleanup = %v/%d/%d/%d/%d", err, links, removes, syncs, closes)
			}
			assertFile(t, filepath.Join(path, "config"), []byte("rival"), 0o600)
			assertNoTemporaryFiles(t, path)
		})
	}
}

func TestCreateIfAbsentRootPublicationMarkers(t *testing.T) {
	if runtime.GOOS == "js" || runtime.GOOS == "plan9" {
		t.Skip("rooted create is unsupported")
	}
	for _, tt := range []struct {
		name         string
		link         func(*os.Root, string, string) error
		operations   func(rootedCreateOperations) rootedCreateOperations
		wantVerified bool
		wantData     []byte
	}{
		{"rival at link", nil, nil, false, []byte("rival")},
		{"successful link reports private error", func(p *os.Root, temporary, basename string) error {
			if err := p.Link(temporary, basename); err != nil {
				return err
			}
			return errors.New("private")
		}, nil, true, []byte("mine")},
		{"readback failure", func(p *os.Root, temporary, basename string) error { return p.Link(temporary, basename) }, func(ops rootedCreateOperations) rootedCreateOperations {
			ops.openRead = func(*os.Root, string) (rootedCreateReadbackFile, error) { return nil, errors.New("private") }
			return ops
		}, false, []byte("mine")},
		{"cleanup failure after proof", func(p *os.Root, temporary, basename string) error { return p.Link(temporary, basename) }, func(ops rootedCreateOperations) rootedCreateOperations {
			ops.remove = func(p *os.Root, name string) error {
				err := p.Remove(name)
				return errors.Join(err, errors.New("private"))
			}
			return ops
		}, true, []byte("mine")},
		{"close failure after proof", func(p *os.Root, temporary, basename string) error { return p.Link(temporary, basename) }, func(ops rootedCreateOperations) rootedCreateOperations {
			ops.closeParent = func(p *os.Root) error { err := p.Close(); return errors.Join(err, errors.New("private")) }
			return ops
		}, true, []byte("mine")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := t.TempDir()
			root, err := os.OpenRoot(path)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			ops := rootedCreateOperations{link: tt.link}
			if tt.operations != nil {
				ops = tt.operations(ops)
			}
			if tt.name == "rival at link" {
				ops.link = func(p *os.Root, temporary, basename string) error {
					file, err := p.OpenFile(basename, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
					if err != nil {
						return err
					}
					if _, err = file.Write([]byte("rival")); err != nil {
						_ = file.Close()
						return err
					}
					if err := file.Close(); err != nil {
						return err
					}
					return p.Link(temporary, basename)
				}
			}
			err = createIfAbsentRoot(root, "config", []byte("mine"), 0o600, ops)
			if err == nil || !errors.Is(err, ErrCreateIfAbsentRootPublicationAttempted) || errors.Is(err, ErrCreateIfAbsentRootPublicationVerified) != tt.wantVerified || strings.Contains(err.Error(), "private") {
				t.Fatalf("error = %v", err)
			}
			assertFile(t, filepath.Join(path, "config"), tt.wantData, 0o600)
			assertNoTemporaryFiles(t, path)
		})
	}
}

func TestCreateIfAbsentRootRetainsDescriptorAnchoredParent(t *testing.T) {
	if runtime.GOOS == "js" || runtime.GOOS == "plan9" {
		t.Skip("rooted create is unsupported")
	}
	for _, tt := range []struct{ name, relative, moved string }{{"root rename", "config", ""}, {"nested replacement", "safe/config", "safe"}} {
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
			ops := rootedCreateOperations{link: func(p *os.Root, temporary, basename string) error {
				moved := path + "-moved"
				if tt.moved != "" {
					moved = filepath.Join(path, "old")
				}
				if err := os.Rename(filepath.Join(path, tt.moved), moved); err != nil {
					return err
				}
				if err := os.MkdirAll(filepath.Join(path, filepath.Dir(tt.relative)), 0o755); err != nil {
					return err
				}
				return p.Link(temporary, basename)
			}}
			if err := createIfAbsentRoot(root, tt.relative, []byte("mine"), 0o600, ops); err != nil {
				t.Fatal(err)
			}
			moved := path + "-moved"
			if tt.moved != "" {
				moved = filepath.Join(path, "old")
			}
			assertFile(t, filepath.Join(moved, filepath.Base(tt.relative)), []byte("mine"), 0o600)
			if _, err := os.Lstat(filepath.Join(path, tt.relative)); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("decoy = %v", err)
			}
			assertNoTemporaryFiles(t, path)
			assertNoTemporaryFiles(t, moved)
		})
	}
}

func TestCreateIfAbsentRootConcurrentCallers(t *testing.T) {
	if runtime.GOOS == "js" || runtime.GOOS == "plan9" {
		t.Skip("rooted create is unsupported")
	}
	path := t.TempDir()
	rootA, err := os.OpenRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	defer rootA.Close()
	rootB, err := os.OpenRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	defer rootB.Close()
	var linked sync.WaitGroup
	linked.Add(2)
	release, results := make(chan struct{}), make(chan error, 2)
	operations := rootedCreateOperations{link: func(parent *os.Root, temporary, basename string) error {
		linked.Done()
		<-release
		return parent.Link(temporary, basename)
	}}
	go func() { results <- createIfAbsentRoot(rootA, "config", []byte("one"), 0o600, operations) }()
	go func() { results <- createIfAbsentRoot(rootB, "config", []byte("two"), 0o600, operations) }()
	linked.Wait()
	close(release)
	successes := 0
	for range 2 {
		if err := <-results; err == nil {
			successes++
		} else if !errors.Is(err, ErrCreateIfAbsentRootPublicationAttempted) || errors.Is(err, ErrCreateIfAbsentRootPublicationVerified) {
			t.Fatalf("loser error = %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("successes = %d", successes)
	}
	data, err := os.ReadFile(filepath.Join(path, "config"))
	if err != nil || (string(data) != "one" && string(data) != "two") {
		t.Fatalf("data/error = %q/%v", data, err)
	}
	assertNoTemporaryFiles(t, path)
}

func rootedCreateStaged(t *testing.T, relative string, data []byte, mode fs.FileMode) (rootedCreateStage, string) {
	t.Helper()
	path := t.TempDir()
	if err := os.MkdirAll(filepath.Join(path, filepath.Dir(relative)), 0o755); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	stage, err := stageRootedCreate(root, relative, data, mode, rootedCreateStagingOperations{random: bytes.NewReader(make([]byte, 16))})
	if err != nil {
		t.Fatal(err)
	}
	return stage, path
}

func rootedCreateLinkedStage(t *testing.T, relative string, data []byte, mode fs.FileMode) (rootedCreateStage, string) {
	t.Helper()
	stage, path := rootedCreateStaged(t, relative, data, mode)
	if err := stage.parent.Link(stage.temporary, stage.basename); err != nil {
		t.Fatal(err)
	}
	return stage, path
}

type rootedCreateReadbackTestFile struct {
	info                        fs.FileInfo
	first, second               []byte
	statAt                      int
	firstErr, seekErr, closeErr error
	stats, seeks, closes        int
}

func (f *rootedCreateReadbackTestFile) Stat() (fs.FileInfo, error) {
	f.stats++
	if f.stats == f.statAt {
		return nil, errors.New("private stat")
	}
	return f.info, nil
}
func (f *rootedCreateReadbackTestFile) Read(p []byte) (int, error) {
	if f.seeks == 0 && f.firstErr != nil {
		return 0, f.firstErr
	}
	data := f.first
	if f.seeks > 0 {
		data = f.second
	}
	return copy(p, data), io.EOF
}
func (f *rootedCreateReadbackTestFile) Seek(int64, int) (int64, error) {
	f.seeks++
	return 0, f.seekErr
}
func (f *rootedCreateReadbackTestFile) Close() error { f.closes++; return f.closeErr }

type rootedCreateTestFile struct {
	chmodErr, writeErr, syncErr, statErr, closeErr error
	zeroWrite                                      bool
	info                                           rootedCreateTestInfo
	closes                                         int
}

func (f *rootedCreateTestFile) Chmod(fs.FileMode) error { return f.chmodErr }
func (f *rootedCreateTestFile) Write(data []byte) (int, error) {
	if f.writeErr != nil || f.zeroWrite {
		return 0, f.writeErr
	}
	return len(data), nil
}
func (f *rootedCreateTestFile) Sync() error                { return f.syncErr }
func (f *rootedCreateTestFile) Stat() (fs.FileInfo, error) { return f.info, f.statErr }
func (f *rootedCreateTestFile) Close() error               { f.closes++; return f.closeErr }

type rootedCreateTestInfo struct{ mode fs.FileMode }

func (i rootedCreateTestInfo) Name() string       { return "temporary" }
func (i rootedCreateTestInfo) Size() int64        { return 0 }
func (i rootedCreateTestInfo) Mode() fs.FileMode  { return i.mode }
func (i rootedCreateTestInfo) ModTime() time.Time { return time.Time{} }
func (i rootedCreateTestInfo) IsDir() bool        { return i.mode.IsDir() }
func (i rootedCreateTestInfo) Sys() any           { return nil }
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
