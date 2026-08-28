package backupjournal

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestOpenReadsDetachedJournal(t *testing.T) {
	home, manifest, blobs := openFixture(t)
	handle, err := Open(home, manifest.TransactionID())
	require(t, err)
	check(t, handle.TransactionID() == manifest.TransactionID(), "transaction ID=%q", handle.TransactionID())
	check(t, handle.State() == Prepared && handle.EntryCount() == 3 && handle.BlobCount() == 2, "unexpected handle")
	stored, ok := handle.Manifest()
	check(t, ok && reflect.DeepEqual(stored.Entries(), manifest.Entries()), "manifest was not detached")
	data, ok := handle.Blob(RuntimePi, "config.json")
	check(t, ok && bytes.Equal(data, blobs[0].Bytes), "blob=%q", data)
	data[0] = 'X'
	again, ok := handle.Blob(RuntimePi, "config.json")
	check(t, ok && bytes.Equal(again, blobs[0].Bytes), "blob was not detached")
	_, ok = handle.Blob(RuntimeOpenCode, "config.json")
	check(t, !ok, "absent entry has a blob")
}

func TestOpenRejectsAggregateOversizeWithoutMutation(t *testing.T) {
	home, manifest, _ := openFixture(t)
	before := journalSnapshot(t, home)
	_, err := openWithLimit(home, manifest.TransactionID(), 5)
	check(t, err == errOpenJournal, "error=%v", err)
	check(t, journalSnapshot(t, home) == before, "Open mutated aggregate-oversize journal")
}

func TestOpenAuditsTerminalJournalsWithoutMutation(t *testing.T) {
	for _, state := range []State{Prepared, Committed, Recovered} {
		t.Run(string(state), func(t *testing.T) {
			home, manifest, _ := openFixture(t)
			if state != Prepared {
				terminal, err := manifest.Transition(state)
				require(t, err)
				require(t, os.WriteFile(filepath.Join(transactionPath(home, manifest), manifestFile), mustJSON(t, terminal), 0600))
			}
			before := journalSnapshot(t, home)
			handle, err := Open(home, manifest.TransactionID())
			require(t, err)
			check(t, handle.State() == state && journalSnapshot(t, home) == before, "state=%q or journal mutated", handle.State())
		})
	}
}

func TestOpenRejectsInvalidJournalWithoutMutation(t *testing.T) {
	otherID := strings.Repeat("f", 64)
	cases := []struct {
		name    string
		prepare func(*testing.T, string, Manifest)
	}{
		{"ID mismatch", func(t *testing.T, home string, m Manifest) {
			data := bytes.Replace(mustJSON(t, m), []byte(m.TransactionID()), []byte(otherID), 1)
			require(t, os.WriteFile(filepath.Join(transactionPath(home, m), manifestFile), data, 0600))
		}},
		{"missing blob", func(t *testing.T, home string, m Manifest) {
			require(t, os.Remove(filepath.Join(transactionPath(home, m), "blobs/pi/config.json")))
		}},
		{"extra file", func(t *testing.T, home string, m Manifest) {
			require(t, os.WriteFile(filepath.Join(transactionPath(home, m), "extra"), nil, 0600))
		}},
		{"unexpected nested directory", func(t *testing.T, home string, m Manifest) {
			require(t, os.MkdirAll(filepath.Join(transactionPath(home, m), "blobs/pi/extra/nested"), 0700))
		}},
		{"unexpected nested file", func(t *testing.T, home string, m Manifest) {
			require(t, os.MkdirAll(filepath.Join(transactionPath(home, m), "blobs/pi/extra"), 0700))
			require(t, os.WriteFile(filepath.Join(transactionPath(home, m), "blobs/pi/extra/file"), nil, 0600))
		}},
		{"symlink", func(t *testing.T, home string, m Manifest) {
			require(t, os.Symlink("manifest.json", filepath.Join(transactionPath(home, m), "link")))
		}},
		{"blob path replaced by directory", func(t *testing.T, home string, m Manifest) {
			require(t, os.Remove(filepath.Join(transactionPath(home, m), "blobs/pi/config.json")))
			require(t, os.Mkdir(filepath.Join(transactionPath(home, m), "blobs/pi/config.json"), 0700))
		}},
		{"blob moved to wrong depth", func(t *testing.T, home string, m Manifest) {
			require(t, os.Remove(filepath.Join(transactionPath(home, m), "blobs/pi/config.json")))
			require(t, os.Mkdir(filepath.Join(transactionPath(home, m), "blobs/pi/nested"), 0700))
			require(t, os.WriteFile(filepath.Join(transactionPath(home, m), "blobs/pi/nested/config.json"), []byte("pi"), 0600))
		}},

		{"wrong mode", func(t *testing.T, home string, m Manifest) {
			require(t, os.Chmod(filepath.Join(transactionPath(home, m), "blobs/pi/config.json"), 0644))
		}},
		{"digest mismatch", func(t *testing.T, home string, m Manifest) {
			require(t, os.WriteFile(filepath.Join(transactionPath(home, m), "blobs/pi/config.json"), []byte("PI"), 0600))
		}},
		{"truncated", func(t *testing.T, home string, m Manifest) {
			require(t, os.WriteFile(filepath.Join(transactionPath(home, m), "blobs/pi/config.json"), []byte(""), 0600))
		}},
		{"oversize", func(t *testing.T, home string, m Manifest) {
			require(t, os.WriteFile(filepath.Join(transactionPath(home, m), "blobs/pi/config.json"), []byte("toolong"), 0600))
		}},
		{"unsafe base", func(t *testing.T, home string, _ Manifest) {
			require(t, os.Chmod(filepath.Join(home, ".cortex"), 0755))
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			home, manifest, _ := openFixture(t)
			test.prepare(t, home, manifest)
			before := journalSnapshot(t, home)
			_, err := Open(home, manifest.TransactionID())
			check(t, err == errOpenJournal && !strings.Contains(err.Error(), home), "error=%v", err)
			check(t, journalSnapshot(t, home) == before, "Open mutated %s journal", test.name)
		})
	}
}

func TestOpenRejectsIncompleteCreateAndUnsafeInputs(t *testing.T) {
	home, manifest, _ := createFixture(t)
	base := filepath.Join(home, ".cortex", "transactions")
	require(t, os.MkdirAll(base, 0700))
	require(t, os.Chmod(filepath.Join(home, ".cortex"), 0700))
	require(t, os.Chmod(base, 0700))
	require(t, os.Mkdir(filepath.Join(base, manifest.TransactionID()), 0700))
	before := journalSnapshot(t, home)
	for _, bad := range []struct{ home, id string }{{home, manifest.TransactionID()}, {"relative", manifest.TransactionID()}, {home, "bad"}} {
		_, err := Open(bad.home, bad.id)
		check(t, err == errOpenJournal, "accepted unsafe input")
	}
	check(t, journalSnapshot(t, home) == before, "incomplete journal mutated")
}

func TestOpenConcurrentAndOpaque(t *testing.T) {
	home, manifest, _ := openFixture(t)
	var group sync.WaitGroup
	for range 20 {
		group.Add(1)
		go func() {
			defer group.Done()
			handle, err := Open(home, manifest.TransactionID())
			if err != nil {
				t.Error(err)
				return
			}
			_, _ = handle.Blob(RuntimePi, "config.json")
		}()
	}
	group.Wait()
	var zero Handle
	_, ok := zero.Manifest()
	check(t, !ok && zero.TransactionID() == "" && zero.State() == "" && zero.EntryCount() == 0 && zero.BlobCount() == 0, "unsafe zero handle")
	for index := 0; index < reflect.TypeOf(Handle{}).NumField(); index++ {
		check(t, !reflect.TypeOf(Handle{}).Field(index).IsExported(), "handle exposes storage")
	}
}

func openFixture(t *testing.T) (string, Manifest, []BlobInput) {
	t.Helper()
	home, manifest, blobs := createFixture(t)
	_, err := Create(home, manifest, blobs)
	require(t, err)
	return home, manifest, blobs
}

func journalSnapshot(t *testing.T, root string) string {
	t.Helper()
	var snapshot string
	require(t, filepath.Walk(root, func(name string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		relativeName, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		snapshot += filepath.ToSlash(relativeName) + ":" + info.Mode().String()
		if !info.IsDir() {
			data, err := os.ReadFile(name)
			if err != nil {
				return err
			}
			snapshot += ":" + string(data)
		}
		return nil
	}))
	return snapshot
}
