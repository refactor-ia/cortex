package backupjournal

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func check(t *testing.T, ok bool, format string, args ...interface{}) {
	t.Helper()
	if !ok {
		t.Fatalf(format, args...)
	}
}
func journalBase(t *testing.T) (string, fsAttempt) {
	t.Helper()
	base, attempt, err := ensureJournalBase(t.TempDir())
	check(t, err == nil, "base: %v", err)
	return base, attempt
}
func TestFSHomeAndBase(t *testing.T) {
	home := t.TempDir()
	base, attempt, err := ensureJournalBase(home)
	check(t, err == nil && len(attempt.created) == 2 && filepath.Base(base) == "transactions", "base=%q created=%d err=%v", base, len(attempt.created), err)
	_, again, err := ensureJournalBase(home)
	check(t, err == nil && len(again.created) == 0, "reuse created=%d err=%v", len(again.created), err)
	for _, bad := range []string{"relative", filepath.Join(home, "missing")} {
		_, _, err := ensureJournalBase(bad)
		check(t, err != nil, "accepted home %q", bad)
	}
	fileHome, linkHome := t.TempDir(), t.TempDir()
	check(t, os.WriteFile(filepath.Join(fileHome, ".cortex"), nil, 0600) == nil, "write unsafe component")
	check(t, os.Symlink(linkHome, filepath.Join(linkHome, ".cortex")) == nil, "link unsafe component")
	for _, unsafe := range []string{fileHome, linkHome} {
		_, _, err := ensureJournalBase(unsafe)
		check(t, err != nil, "accepted unsafe component")
	}
}
func TestFSReservationAndPaths(t *testing.T) {
	base, attempt := journalBase(t)
	var winners int
	var winnerMu sync.Mutex
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := (&fsAttempt{}).reserve(base, hash); err == nil {
				winnerMu.Lock()
				winners++
				winnerMu.Unlock()
			}
		}()
	}
	wg.Wait()
	check(t, winners == 1, "winners=%d", winners)
	for _, rel := range []string{"", ".", "../x", "/x", "a\\b", "a/./b"} {
		_, err := attempt.createFile(base, rel)
		check(t, err != nil, "accepted %q", rel)
	}
	name, err := attempt.createFile(base, "blobs/pi/value")
	check(t, err == nil, "create file: %v", err)
	info, err := os.Lstat(name)
	check(t, err == nil && regular0600(info), "file=%v err=%v", info, err)
	parent, err := os.Lstat(filepath.Dir(name))
	check(t, err == nil && realDirInfo(parent, true), "parent=%v err=%v", parent, err)
	info, err = os.Lstat(filepath.Dir(base))
	check(t, err == nil && realDirInfo(info, true), "base parent=%v err=%v", info, err)
}
func TestFSDurableReadAndCleanup(t *testing.T) {
	base, attempt := journalBase(t)
	name, err := attempt.createFile(base, "journal")
	check(t, err == nil, "create file: %v", err)
	check(t, durableWrite(&attempt, name, []byte("value")) == nil, "durable write")
	got, err := readRegular(name, 5)
	check(t, err == nil && string(got) == "value", "read=%q err=%v", got, err)
	for _, limit := range []int64{4, -1} {
		_, err := readRegular(name, limit)
		check(t, err != nil, "accepted limit %d", limit)
	}
	link := filepath.Join(base, "link")
	check(t, os.Symlink(name, link) == nil, "create link")
	for _, unsafe := range []string{link, base} {
		_, err := readRegular(unsafe, 5)
		check(t, err != nil, "accepted unsafe path %q", unsafe)
	}
	check(t, os.Chmod(name, 0644) == nil, "change mode")
	_, err = readRegular(name, 5)
	check(t, err != nil, "accepted wrong mode")
	check(t, os.Chmod(name, 0600) == nil, "restore mode")
	sentinel := filepath.Join(base, "sentinel")
	check(t, os.WriteFile(sentinel, nil, 0600) == nil, "write sentinel")
	check(t, attempt.cleanup() != nil, "removed nonempty base recursively")
	_, err = os.Lstat(sentinel)
	check(t, err == nil, "removed sentinel")
}
func TestFSPartialWriteCleansOnlyOwnedFile(t *testing.T) {
	base, attempt := journalBase(t)
	name, err := attempt.createFile(base, "journal")
	check(t, err == nil, "create file: %v", err)
	sentinel := filepath.Join(base, "sentinel")
	check(t, os.WriteFile(sentinel, nil, 0600) == nil, "write sentinel")
	original := fileWrite
	defer func() { fileWrite = original }()
	fileWrite = func(*os.File, []byte) (int, error) { return 1, errors.New("interrupted") }
	check(t, durableWrite(&attempt, name, []byte("value")) != nil, "accepted partial write")
	_, err = os.Lstat(name)
	check(t, errors.Is(err, os.ErrNotExist), "owned file remains: %v", err)
	_, err = os.Lstat(sentinel)
	check(t, err == nil, "removed sentinel")
}
func TestFSPostCreateFailureCleansAttempt(t *testing.T) {
	tests := []struct {
		name, created string
		create        func(*fsAttempt, string) (string, error)
		hook          func(string) func()
	}{
		{"reservation Lstat", hash, func(a *fsAttempt, b string) (string, error) { return a.reserve(b, hash) }, func(name string) func() {
			original := pathLstat
			pathLstat = func(path string) (os.FileInfo, error) {
				if path == name {
					return nil, errors.New("interrupted")
				}
				return original(path)
			}
			return func() { pathLstat = original }
		}},
		{"file Stat", "journal", func(a *fsAttempt, b string) (string, error) { return a.createFile(b, "journal") }, func(string) func() {
			original := fileStat
			fileStat = func(*os.File) (os.FileInfo, error) { return nil, errors.New("interrupted") }
			return func() { fileStat = original }
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base, attempt := journalBase(t)
			sentinel, name := filepath.Join(base, "sentinel"), filepath.Join(base, tt.created)
			check(t, os.WriteFile(sentinel, nil, 0600) == nil, "write sentinel")
			restore := tt.hook(name)
			_, err := tt.create(&attempt, base)
			restore()
			check(t, err != nil, "accepted failed validation")
			check(t, attempt.cleanup() != nil, "removed unknown child")
			_, err = os.Lstat(name)
			check(t, errors.Is(err, os.ErrNotExist), "created path remains: %v", err)
			_, err = os.Lstat(sentinel)
			check(t, err == nil, "removed unknown child")
		})
	}
	drift := fsAttempt{}
	check(t, drift.remove(t.TempDir()) != nil, "removed unowned path")
	name := t.TempDir()
	info, err := os.Lstat(name)
	check(t, err == nil, "stat drift path: %v", err)
	drift.created = []ownedPath{{name: name, info: info}}
	check(t, os.Chmod(name, 0755) == nil, "change drift mode")
	check(t, drift.cleanup() != nil, "removed drifted path")
}
