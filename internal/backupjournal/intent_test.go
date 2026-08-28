package backupjournal

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func transitionBytes(t *testing.T, state State) []byte {
	manifest := journal(t)
	if state != Prepared {
		var err error
		manifest, err = manifest.Transition(state)
		if err != nil {
			t.Fatal(err)
		}
	}
	return mustJSON(t, manifest)
}
func transitionIntentFor(t *testing.T, state State) transitionIntent {
	intent, err := newTransitionIntent(hash, transitionBytes(t, Prepared), transitionBytes(t, state))
	if err != nil {
		t.Fatal(err)
	}
	return intent
}
func intentDirectoryFor(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	if err := os.Chmod(directory, 0700); err != nil {
		t.Fatal(err)
	}
	return directory
}
func TestIntentRoundTrip(t *testing.T) {
	directory := intentDirectoryFor(t)
	intent := transitionIntentFor(t, Committed)
	if _, err := parseTransitionIntent(intent.encoded); err != nil {
		t.Fatalf("parse canonical intent: %v", err)
	}
	created, err := createTransitionIntent(directory, hash, intent.prepared, intent.target)
	if err != nil || !bytes.Equal(created.encoded, intent.encoded) {
		t.Fatalf("create: %v", err)
	}
	stored, err := readTransitionIntent(directory)
	if err != nil || !bytes.Equal(stored.encoded, intent.encoded) {
		t.Fatalf("read: %v", err)
	}
	info, err := os.Lstat(filepath.Join(directory, transitionIntentFile))
	if err != nil || !regular0600(info) {
		t.Fatalf("intent mode: %v", err)
	}
	if err := removeTransitionIntent(directory, stored); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(directory, transitionIntentFile)); !os.IsNotExist(err) {
		t.Fatalf("intent remains: %v", err)
	}
}
func TestIntentRejectsCorruptionAndSubstitution(t *testing.T) {
	intent := transitionIntentFor(t, Committed)
	cases := [][]byte{
		[]byte("{"), append(intent.encoded, '\n'),
		append([]byte(`{"unknown":true,`), intent.encoded[1:]...),
		[]byte(strings.Replace(string(intent.encoded), `"targetState":"committed"`, `"targetState":"committed","targetState":"committed"`, 1)),
		[]byte(strings.Replace(string(intent.encoded), hash, strings.Repeat("f", 64), 1)),
	}
	for _, data := range cases {
		if _, err := parseTransitionIntent(data); err == nil {
			t.Fatalf("accepted invalid intent %q", data)
		}
	}
	if _, err := newTransitionIntent(strings.Repeat("f", 64), intent.prepared, intent.target); err == nil {
		t.Fatal("accepted substituted transaction")
	}
}
func TestIntentRaceChoosesOneTarget(t *testing.T) {
	directory := intentDirectoryFor(t)
	prepared := transitionBytes(t, Prepared)
	targets := map[State][]byte{Committed: transitionBytes(t, Committed), Recovered: transitionBytes(t, Recovered)}
	var winners int
	var lock sync.Mutex
	var group sync.WaitGroup
	for _, state := range []State{Committed, Recovered} {
		group.Add(1)
		go func(state State) {
			defer group.Done()
			if _, err := createTransitionIntent(directory, hash, prepared, targets[state]); err == nil {
				lock.Lock()
				winners++
				lock.Unlock()
			}
		}(state)
	}
	group.Wait()
	if winners != 1 {
		t.Fatalf("winners=%d", winners)
	}
	stored, err := readTransitionIntent(directory)
	if err != nil || (stored.targetState != Committed && stored.targetState != Recovered) {
		t.Fatalf("stored=%v err=%v", stored.targetState, err)
	}
}
func TestIntentReplacesBothTargets(t *testing.T) {
	for _, state := range []State{Committed, Recovered} {
		t.Run(string(state), func(t *testing.T) {
			directory, intent := intentDirectoryFor(t), transitionIntentFor(t, state)
			if err := os.WriteFile(filepath.Join(directory, manifestFile), intent.prepared, 0600); err != nil {
				t.Fatal(err)
			}
			if err := replaceManifest(directory, intent); err != nil {
				t.Fatal(err)
			}
			got, err := readRegular(filepath.Join(directory, manifestFile), maxLength)
			if err != nil || !bytes.Equal(got, intent.target) {
				t.Fatalf("readback: %v", err)
			}
		})
	}
}
func TestIntentReplaceRejectsUnsafeOrDriftedInputs(t *testing.T) {
	for _, test := range []struct {
		name  string
		setup func(*testing.T, string, transitionIntent)
	}{
		{"mismatch", func(t *testing.T, d string, _ transitionIntent) {
			if err := os.WriteFile(filepath.Join(d, manifestFile), transitionBytes(t, Committed), 0600); err != nil {
				t.Fatal(err)
			}
		}},
		{"mode", func(t *testing.T, d string, i transitionIntent) {
			if err := os.WriteFile(filepath.Join(d, manifestFile), i.prepared, 0644); err != nil {
				t.Fatal(err)
			}
		}},
		{"symlink", func(t *testing.T, d string, i transitionIntent) {
			target := filepath.Join(d, "other")
			if err := os.WriteFile(target, i.prepared, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, filepath.Join(d, manifestFile)); err != nil {
				t.Fatal(err)
			}
		}},
		{"temp drift", func(t *testing.T, d string, i transitionIntent) {
			if err := os.WriteFile(filepath.Join(d, manifestFile), i.prepared, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(manifestTempName(d, i), nil, 0600); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory, intent := intentDirectoryFor(t), transitionIntentFor(t, Committed)
			test.setup(t, directory, intent)
			before, _ := os.ReadFile(filepath.Join(directory, manifestFile))
			err := replaceManifest(directory, intent)
			if !errors.Is(err, transitionIntentError{}) || strings.Contains(err.Error(), directory) {
				t.Fatalf("err=%v", err)
			}
			after, _ := os.ReadFile(filepath.Join(directory, manifestFile))
			if !bytes.Equal(before, after) {
				t.Fatal("target mutated")
			}
		})
	}
}
