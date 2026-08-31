package atomicfile

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/refactor-ia/cortex/internal/safepath"
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

func TestRemoveIfExactRemovesMatchingZeroByteFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "safe", "empty.txt")
	writeFile(t, path, []byte{})
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := RemoveIfExact(root, "safe/empty.txt", []byte{}, 0o600); err != nil {
		t.Fatalf("RemoveIfExact() error = %v", err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("removed zero-byte file stat error = %v, want not exist", err)
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
	replacementPath := filepath.Join(root, "replacement.txt")
	writeFile(t, path, original)
	writeFile(t, replacementPath, replacement)
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(replacementPath, 0o600); err != nil {
		t.Fatal(err)
	}
	lstatCalls := 0
	operations := exactRemovalOperations{
		resolve: safepath.Resolve,
		lstat: func(name string) (fs.FileInfo, error) {
			lstatCalls++
			if lstatCalls == 2 {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(replacementPath, path); err != nil {
					t.Fatal(err)
				}
			}
			return os.Lstat(name)
		},
		open: os.Open,
		remove: func(string) error {
			t.Fatal("removeIfExact() attempted removal after identity drift")
			return nil
		},
		syncDirectory: syncDirectory,
	}

	err := removeIfExact(root, "safe/config.txt", original, 0o600, operations)
	if err == nil || !strings.Contains(err.Error(), "destination drifted") {
		t.Fatalf("removeIfExact() error = %v, want identity drift", err)
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil || !bytes.Equal(data, replacement) {
		t.Fatalf("replacement data = %q, error %v", data, readErr)
	}
}

func TestRemoveIfExactRevalidatesPathBeforeRemoval(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	data := []byte("cortex-created")
	path := filepath.Join(root, "safe", "config.txt")
	outsidePath := filepath.Join(outside, "config.txt")
	writeFile(t, path, data)
	writeFile(t, outsidePath, data)
	for _, name := range []string{path, outsidePath} {
		if err := os.Chmod(name, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	resolveCalls := 0
	operations := exactRemovalOperations{
		resolve: func(root, relativePath string) (string, error) {
			resolveCalls++
			if resolveCalls == 2 {
				if err := os.Rename(filepath.Join(root, "safe"), filepath.Join(root, "displaced")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filepath.Join(root, "safe")); err != nil {
					t.Fatal(err)
				}
			}
			return safepath.Resolve(root, relativePath)
		},
		lstat: os.Lstat,
		open:  os.Open,
		remove: func(string) error {
			t.Fatal("removeIfExact() attempted removal after ancestor drift")
			return nil
		},
		syncDirectory: syncDirectory,
	}

	err := removeIfExact(root, "safe/config.txt", data, 0o600, operations)
	if err == nil || !strings.Contains(err.Error(), "destination is unsafe") {
		t.Fatalf("removeIfExact() error = %v, want unsafe destination", err)
	}
	for _, preserved := range []string{filepath.Join(root, "displaced", "config.txt"), outsidePath} {
		actual, readErr := os.ReadFile(preserved)
		if readErr != nil || !bytes.Equal(actual, data) {
			t.Fatalf("preserved file %q = %q, error %v", preserved, actual, readErr)
		}
	}
}

func TestRemoveIfExactRechecksBytesBeforeRemoval(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "safe", "config.txt")
	original := []byte("cortex-created")
	replacement := []byte("user-rewritten")
	writeFile(t, path, original)
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}

	resolveCalls := 0
	operations := exactRemovalOperations{
		resolve: func(root, relativePath string) (string, error) {
			resolveCalls++
			if resolveCalls == 2 {
				if err := os.WriteFile(path, replacement, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			return safepath.Resolve(root, relativePath)
		},
		lstat: os.Lstat,
		open:  os.Open,
		remove: func(string) error {
			t.Fatal("removeIfExact() attempted removal after byte drift")
			return nil
		},
		syncDirectory: syncDirectory,
	}

	err := removeIfExact(root, "safe/config.txt", original, 0o600, operations)
	if err == nil || !strings.Contains(err.Error(), "destination bytes do not match") {
		t.Fatalf("removeIfExact() error = %v, want byte drift", err)
	}
	actual, readErr := os.ReadFile(path)
	if readErr != nil || !bytes.Equal(actual, replacement) {
		t.Fatalf("preserved replacement = %q, error %v", actual, readErr)
	}
}

func TestRemoveIfExactRechecksModeBeforeRemoval(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "safe", "config.txt")
	data := []byte("cortex-created")
	writeFile(t, path, data)
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}

	resolveCalls := 0
	operations := exactRemovalOperations{
		resolve: func(root, relativePath string) (string, error) {
			resolveCalls++
			if resolveCalls == 2 {
				if err := os.Chmod(path, 0o640); err != nil {
					t.Fatal(err)
				}
			}
			return safepath.Resolve(root, relativePath)
		},
		lstat: os.Lstat,
		open:  os.Open,
		remove: func(string) error {
			t.Fatal("removeIfExact() attempted removal after mode drift")
			return nil
		},
		syncDirectory: syncDirectory,
	}

	err := removeIfExact(root, "safe/config.txt", data, 0o600, operations)
	if err == nil || !strings.Contains(err.Error(), "destination mode does not match") {
		t.Fatalf("removeIfExact() error = %v, want mode drift", err)
	}
	actual, readErr := os.ReadFile(path)
	if readErr != nil || !bytes.Equal(actual, data) {
		t.Fatalf("preserved file = %q, error %v", actual, readErr)
	}
	if info, statErr := os.Lstat(path); statErr != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("preserved mode = %v, error %v", info, statErr)
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
					if lstatCalls < 5 {
						return os.Lstat(name)
					}
					return nil, errors.New("readback unavailable")
				}
			}
			operations := exactRemovalOperations{
				resolve: safepath.Resolve,
				lstat:   lstat,
				open:    os.Open,
				remove:  os.Remove,
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

func TestObserveRootedExactContract(t *testing.T) {
	secret := []byte("rooted-evidence")
	for _, tt := range []struct {
		name, relative, want string
		expected             []byte
		mode                 fs.FileMode
		setup                func(*testing.T, string, string)
		closed, nilRoot      bool
	}{
		{"observes nonempty", "safe/config.txt", "", secret, 0o600, nil, false, false},
		{"observes nonnil zero-byte", "safe/config.txt", "", []byte{}, 0o600, nil, false, false},
		{"rejects nil root", "safe/config.txt", "invalid root", secret, 0o600, nil, false, true},
		{"rejects closed root", "safe/config.txt", "invalid root", secret, 0o600, nil, true, false},
		{"rejects nil evidence", "safe/config.txt", "invalid expected bytes", nil, 0o600, nil, false, false},
		{"rejects oversized evidence", "safe/config.txt", "invalid expected bytes", make([]byte, removeExactMaxEvidenceBytes+1), 0o600, nil, false, false},
		{"rejects empty path", "", "invalid relative path", secret, 0o600, nil, false, false},
		{"rejects absolute path", "/outside", "invalid relative path", secret, 0o600, nil, false, false},
		{"rejects traversal", "../outside", "invalid relative path", secret, 0o600, nil, false, false},
		{"rejects backslash", `safe\config.txt`, "invalid relative path", secret, 0o600, nil, false, false},
		{"rejects dot", ".", "invalid relative path", secret, 0o600, nil, false, false},
		{"rejects empty component", "safe//config.txt", "invalid relative path", secret, 0o600, nil, false, false},
		{"rejects noncanonical path", "safe/./config.txt", "invalid relative path", secret, 0o600, nil, false, false},
		{"rejects unsupported mode", "safe/config.txt", "invalid expected mode", secret, fs.ModeDir | 0o600, nil, false, false},
		{"rejects wrong bytes", "safe/config.txt", "destination bytes do not match", secret, 0o600, func(t *testing.T, _, target string) { writeMode(t, target, []byte("changed"), 0o600) }, false, false},
		{"rejects wrong mode", "safe/config.txt", "destination mode does not match", secret, 0o600, func(t *testing.T, _, target string) { writeMode(t, target, secret, 0o640) }, false, false},
		{"rejects missing leaf", "missing/config.txt", "destination is missing", secret, 0o600, func(t *testing.T, root, _ string) {
			if err := os.Mkdir(filepath.Join(root, "missing"), 0o700); err != nil {
				t.Fatal(err)
			}
		}, false, false},
		{"rejects symlink leaf", "safe/config.txt", "destination is not a regular file", secret, 0o600, func(t *testing.T, root, target string) {
			if err := os.Remove(target); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("../decoy", target); err != nil {
				t.Fatal(err)
			}
		}, false, false},
		{"rejects nonregular leaf", "safe/config.txt", "destination is not a regular file", secret, 0o600, func(t *testing.T, _, target string) {
			if err := os.Remove(target); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(target, 0o700); err != nil {
				t.Fatal(err)
			}
		}, false, false},
		{"rejects missing parent", "missing/config.txt", "parent is missing or invalid", secret, 0o600, nil, false, false},
		{"rejects symlink parent", "linked/config.txt", "parent is missing or invalid", secret, 0o600, func(t *testing.T, root, _ string) {
			if err := os.Symlink("safe", filepath.Join(root, "linked")); err != nil {
				t.Fatal(err)
			}
		}, false, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rootPath := t.TempDir()
			target := filepath.Join(rootPath, "safe/config.txt")
			initial := secret
			if tt.want == "" {
				initial = tt.expected
			}
			writeMode(t, target, initial, 0o600)
			writeFile(t, filepath.Join(rootPath, "safe/decoy"), []byte("decoy"))
			outside := filepath.Join(t.TempDir(), "outside")
			writeFile(t, outside, []byte("outside"))
			if tt.setup != nil {
				tt.setup(t, rootPath, target)
			}
			beforeInfo, statErr := os.Lstat(target)
			if statErr != nil {
				t.Fatal(statErr)
			}
			before, readErr := os.ReadFile(target)
			if readErr != nil {
				before = nil
			}
			root := openTestRoot(t, rootPath)
			if tt.closed {
				if err := root.Close(); err != nil {
					t.Fatal(err)
				}
			} else {
				defer root.Close()
			}
			actual := root
			if tt.nilRoot {
				actual = nil
			}
			evidence, err := observeRootedExact(actual, tt.relative, tt.expected, tt.mode, rootedExactEvidenceOperations{})
			if tt.want == "" {
				if err != nil || evidence.leaf == nil || len(evidence.parents) != 2 {
					t.Fatalf("observe = %#v, %v", evidence, err)
				}
				assertRootedPreserved(t, target, initial, outside)
				info, statErr := os.Lstat(target)
				if statErr != nil || info.Mode().Perm() != tt.mode || !os.SameFile(info, evidence.leaf) {
					t.Fatalf("leaf changed: %v, %v", info, statErr)
				}
				return
			}
			rootedObservationError(t, err, tt.want, tt.relative, tt.expected)
			assertRootedPreserved(t, target, before, outside)
			afterInfo, statErr := os.Lstat(target)
			if statErr != nil || afterInfo.Mode() != beforeInfo.Mode() || !os.SameFile(afterInfo, beforeInfo) {
				t.Fatalf("target identity changed: %v, %v", afterInfo, statErr)
			}
		})
	}
}

func TestObserveRootedExactRejectsSpecialDestinationMode(t *testing.T) {
	t.Run("setuid", func(t *testing.T) {
		rootPath := t.TempDir()
		target := filepath.Join(rootPath, "safe", "config.txt")
		secret := []byte("rooted-evidence")
		writeMode(t, target, secret, 0o600)
		if err := os.Chmod(target, fs.ModeSetuid|0o600); err != nil {
			t.Fatal(err)
		}
		beforeInfo, err := os.Lstat(target)
		if err != nil {
			t.Fatal(err)
		}
		if beforeInfo.Mode()&fs.ModeSetuid == 0 {
			t.Skip("filesystem does not preserve setuid mode bit")
		}
		writeFile(t, filepath.Join(rootPath, "safe", "decoy"), []byte("decoy"))
		outside := filepath.Join(t.TempDir(), "outside")
		writeFile(t, outside, []byte("outside"))
		root := openTestRoot(t, rootPath)
		defer root.Close()

		_, err = observeRootedExact(root, "safe/config.txt", secret, 0o600, rootedExactEvidenceOperations{})
		rootedObservationError(t, err, "destination mode does not match", "safe/config.txt", secret)
		assertRootedPreserved(t, target, secret, outside)
		afterInfo, err := os.Lstat(target)
		if err != nil || afterInfo.Mode() != beforeInfo.Mode() || !os.SameFile(afterInfo, beforeInfo) {
			t.Fatalf("target identity or mode changed: %v, %v", afterInfo, err)
		}
	})
}

func TestObserveRootedExactDetectsEvidenceDrift(t *testing.T) {
	for _, tt := range []struct {
		name, watch, want string
		at                int
		mutate            func(*testing.T, string, string)
		wantData          []byte
		wantMode          fs.FileMode
	}{
		{"leaf identity replacement", "safe/config.txt", "destination drifted", 3, func(t *testing.T, target, _ string) {
			if err := os.Remove(target); err != nil {
				t.Fatal(err)
			}
			writeMode(t, target, []byte("expected"), 0o600)
		}, []byte("expected"), 0o600},
		{"byte rewrite", "safe/config.txt", "destination drifted", 4, func(t *testing.T, target, _ string) { writeMode(t, target, []byte("changed!"), 0o600) }, []byte("changed!"), 0o600},
		{"mode rewrite", "safe/config.txt", "destination drifted", 3, func(t *testing.T, target, _ string) { writeMode(t, target, []byte("expected"), 0o640) }, []byte("expected"), 0o640},
		{"parent replacement", "safe/config.txt", "parent drifted", 3, func(t *testing.T, target, root string) {
			if err := os.Rename(filepath.Dir(target), filepath.Join(root, "displaced")); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(filepath.Join(root, "safe"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Link(filepath.Join(root, "displaced", "config.txt"), target); err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(root, "safe/decoy"), []byte("decoy"))
		}, []byte("expected"), 0o600},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rootPath := t.TempDir()
			target := filepath.Join(rootPath, "safe/config.txt")
			writeMode(t, target, []byte("expected"), 0o600)
			writeFile(t, filepath.Join(rootPath, "safe/decoy"), []byte("decoy"))
			outside := filepath.Join(t.TempDir(), "outside")
			writeFile(t, outside, []byte("outside"))
			root := openTestRoot(t, rootPath)
			defer root.Close()
			hits := 0
			_, err := observeRootedExact(root, "safe/config.txt", []byte("expected"), 0o600, rootedExactEvidenceOperations{lstat: func(r *os.Root, name string) (fs.FileInfo, error) {
				if name == tt.watch {
					hits++
					if hits == tt.at {
						tt.mutate(t, target, rootPath)
					}
				}
				return r.Lstat(name)
			}})
			rootedObservationError(t, err, tt.want, "safe/config.txt", []byte("expected"))
			assertRootedPreserved(t, target, tt.wantData, outside)
			if info, statErr := os.Lstat(target); statErr != nil || info.Mode().Perm() != tt.wantMode {
				t.Fatalf("target mode = %v, %v", info, statErr)
			}
		})
	}
}

func rootedObservationError(t *testing.T, err error, want, relative string, evidence []byte) {
	t.Helper()
	if err == nil || want != "" && !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v", err)
	}
	if len(relative) > 0 && strings.Contains(err.Error(), relative) || len(evidence) > 0 && strings.Contains(err.Error(), string(evidence)) {
		t.Fatalf("error leaked private input: %v", err)
	}
}

func assertRootedPreserved(t *testing.T, target string, want []byte, outside string) {
	t.Helper()
	if _, err := os.Lstat(target); err != nil {
		t.Fatalf("target changed: %v", err)
	}
	if want != nil {
		if actual, err := os.ReadFile(target); err != nil || !bytes.Equal(actual, want) {
			t.Fatalf("target = %q, error = %v", actual, err)
		}
	}
	for _, path := range []string{filepath.Join(filepath.Dir(target), "decoy"), outside} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("sentinel changed: %v", err)
		}
	}
}

func openTestRoot(t *testing.T, path string) *os.Root {
	t.Helper()
	root, err := os.OpenRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func writeMode(t *testing.T, path string, data []byte, mode fs.FileMode) {
	t.Helper()
	writeFile(t, path, data)
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
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
