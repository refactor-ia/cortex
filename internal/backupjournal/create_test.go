package backupjournal

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestCreateWritesPreparedJournal(t *testing.T) {
	home, manifest, blobs := createFixture(t)
	stages := []string{}
	setCreateSeam(t, func(stage, _ string) error { stages = append(stages, stage); return nil })
	result, err := Create(home, manifest, []BlobInput{blobs[1], blobs[0]})
	require(t, err)
	check(t, result.TransactionID() == manifest.TransactionID() && result.State() == Prepared && result.EntryCount() == 3 && result.BlobCount() == 2, "unexpected result: %#v", result)
	txn := transactionPath(home, manifest)
	wantFiles := []string{"blobs/claude-code/settings.json", "blobs/pi/config.json", "manifest.json"}
	check(t, reflect.DeepEqual(journalFiles(t, txn), wantFiles), "files differ")
	for _, name := range append(wantFiles, "blobs", "blobs/claude-code", "blobs/pi") {
		info, statErr := os.Lstat(filepath.Join(txn, name))
		check(t, statErr == nil && info.Mode().Perm() == map[bool]os.FileMode{true: 0700, false: 0600}[info.IsDir()], "%s mode=%v err=%v", name, info, statErr)
	}
	for _, blob := range blobs {
		data, readErr := os.ReadFile(filepath.Join(txn, "blobs", string(blob.Runtime), blob.RelativePath))
		check(t, readErr == nil && bytes.Equal(data, blob.Bytes), "blob %s=%q err=%v", blob.Runtime, data, readErr)
	}
	stored, readErr := os.ReadFile(filepath.Join(txn, "manifest.json"))
	require(t, readErr)
	parsed, parseErr := Parse(stored)
	check(t, parseErr == nil && bytes.Equal(stored, mustJSON(t, parsed)) && reflect.DeepEqual(parsed.Entries(), manifest.Entries()), "manifest parse=%v", parseErr)
	check(t, reflect.DeepEqual(stages, []string{"after-first-blob", "after-manifest-write", "before-tree-verify"}), "write order=%v", stages)
}

func TestCreateValidationDoesNotTouchHome(t *testing.T) {
	home, manifest, blobs := createFixture(t)
	for _, mutate := range []func([]BlobInput){
		func(b []BlobInput) { b[0].Bytes = []byte("xx") },
		func(b []BlobInput) { b[0].Bytes = append(b[0].Bytes, 'x') },
		func(b []BlobInput) { b[0].Mode = 0644 },
		func(b []BlobInput) { b[1].Runtime, b[1].RelativePath = RuntimePi, "config.json" },
		func(b []BlobInput) { b[1].RelativePath = "missing" },
	} {
		inputs := append([]BlobInput(nil), blobs...)
		mutate(inputs)
		_, err := Create(home, manifest, inputs)
		check(t, err != nil, "accepted invalid blob set")
	}
	_, err := Create(home, manifest, blobs[:1])
	check(t, err != nil, "accepted missing blob")
	committed, err := manifest.Transition(Committed)
	require(t, err)
	_, err = Create(home, committed, blobs)
	check(t, err != nil, "accepted non-prepared manifest")
	_, err = os.Lstat(filepath.Join(home, ".cortex"))
	check(t, errors.Is(err, os.ErrNotExist), "validation mutated home")
}

func TestCreateConcurrentSameIDPreservesWinner(t *testing.T) {
	home, manifest, blobs := createFixture(t)
	var winners int
	var mu sync.Mutex
	var group sync.WaitGroup
	for range 20 {
		group.Add(1)
		go func() {
			defer group.Done()
			if _, err := Create(home, manifest, blobs); err == nil {
				mu.Lock()
				winners++
				mu.Unlock()
			}
		}()
	}
	group.Wait()
	check(t, winners == 1, "winners=%d", winners)
	check(t, reflect.DeepEqual(journalFiles(t, transactionPath(home, manifest)), []string{"blobs/claude-code/settings.json", "blobs/pi/config.json", "manifest.json"}), "winner changed")
}

func TestCreateFailureCleanup(t *testing.T) {
	for _, test := range []struct {
		name, stage         string
		unknown, verifyTree bool
	}{
		{"after first blob", "after-first-blob", false, false},
		{"after manifest", "after-manifest-write", false, false},
		{"cleanup refusal", "after-manifest-write", true, false},
		{"unexpected tree", "before-tree-verify", true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			home, manifest, blobs := createFixture(t)
			sentinel := createSentinel(t, home)
			primary := errors.New("interrupted")
			setCreateSeam(t, func(stage, txn string) error {
				if stage != test.stage {
					return nil
				}
				if test.unknown {
					if err := os.WriteFile(filepath.Join(txn, "unexpected"), nil, 0600); err != nil {
						t.Fatal(err)
					}
				}
				if test.verifyTree {
					return nil
				}
				return primary
			})
			_, err := Create(home, manifest, blobs)
			check(t, err != nil, "accepted injected failure")
			hasPrimary := errors.Is(err, primary)
			if test.verifyTree {
				hasPrimary = strings.Contains(err.Error(), "unexpected journal file")
			}
			check(t, hasPrimary, "primary error missing: %v", err)
			check(t, errors.Is(err, ErrCreateCleanup) == test.unknown, "cleanup error=%v", err)
			if test.unknown {
				check(t, !strings.Contains(err.Error(), home), "error leaks home: %v", err)
				_, err = os.Lstat(filepath.Join(transactionPath(home, manifest), "unexpected"))
				check(t, err == nil, "cleanup falsely removed leftover: %v", err)
			} else {
				_, err = os.Lstat(transactionPath(home, manifest))
				check(t, errors.Is(err, os.ErrNotExist), "attempt remains")
			}
			_, err = os.Lstat(sentinel)
			check(t, err == nil, "sentinel removed")
		})
	}
}

func TestCreateResultDoesNotExposeStorageOrMutableBytes(t *testing.T) {
	home, manifest, blobs := createFixture(t)
	result, err := Create(home, manifest, blobs)
	require(t, err)
	for index := 0; index < reflect.TypeOf(result).NumField(); index++ {
		check(t, !reflect.TypeOf(result).Field(index).IsExported(), "result exposes mutable storage")
	}
	blobs[0].Bytes[0] = 'X'
	data, readErr := os.ReadFile(filepath.Join(transactionPath(home, manifest), "blobs/pi/config.json"))
	check(t, readErr == nil && !bytes.Equal(data, blobs[0].Bytes), "caller bytes were retained")
}

func require(t *testing.T, err error) {
	t.Helper()
	check(t, err == nil, "%v", err)
}
func createSentinel(t *testing.T, home string) string {
	t.Helper()
	sentinel := filepath.Join(home, ".cortex", "transactions", "sentinel")
	require(t, os.MkdirAll(filepath.Dir(sentinel), 0700))
	require(t, os.WriteFile(sentinel, nil, 0600))
	return sentinel
}
func createFixture(t *testing.T) (string, Manifest, []BlobInput) {
	t.Helper()
	pi, claude := []byte("pi"), []byte("claude")
	inputs := []EntryInput{{Runtime: RuntimePi, Root: RootPi, RelativePath: "config.json", Existence: Present, Mode: 0600, SHA256: sha256Hex(pi), Length: int64(len(pi))}, {Runtime: RuntimeOpenCode, Root: RootOpenCode, RelativePath: "config.json", Existence: Absent}, {Runtime: RuntimeClaude, Root: RootClaude, RelativePath: "settings.json", Existence: Present, Mode: 0644, SHA256: sha256Hex(claude), Length: int64(len(claude))}}
	manifest, err := New(hash, hash, inputs)
	require(t, err)
	return t.TempDir(), manifest, []BlobInput{{Runtime: RuntimePi, RelativePath: "config.json", Bytes: pi, Mode: 0600}, {Runtime: RuntimeClaude, RelativePath: "settings.json", Bytes: claude, Mode: 0644}}
}
func transactionPath(home string, manifest Manifest) string {
	return filepath.Join(home, ".cortex", "transactions", manifest.TransactionID())
}
func journalFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	if err := filepath.WalkDir(root, func(name string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			relative, _ := filepath.Rel(root, name)
			files = append(files, filepath.ToSlash(relative))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return files
}
func setCreateSeam(t *testing.T, seam func(string, string) error) {
	t.Helper()
	original := createSeam
	createSeam = seam
	t.Cleanup(func() { createSeam = original })
}
