package backupjournal

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestTransitionBothTargets(t *testing.T) {
	for _, target := range []State{Committed, Recovered} {
		t.Run(string(target), func(t *testing.T) {
			home, manifest, _ := openFixture(t)
			handle, err := Transition(home, manifest.TransactionID(), target)
			require(t, err)
			check(t, handle.State() == target, "state=%q", handle.State())
			_, err = os.Lstat(filepath.Join(transactionPath(home, manifest), transitionIntentFile))
			check(t, errors.Is(err, os.ErrNotExist), "intent remains: %v", err)
			opened, err := Open(home, manifest.TransactionID())
			require(t, err)
			check(t, opened.State() == target, "strict final state=%q", opened.State())
		})
	}
}

func TestTransitionRejectsReplayAndExposesClassifiedErrors(t *testing.T) {
	home, manifest, _ := openFixture(t)
	_, err := Transition(home, manifest.TransactionID(), Committed)
	require(t, err)
	_, err = Transition(home, manifest.TransactionID(), Committed)
	check(t, errors.Is(err, ErrTransitionInvalid), "replay error=%v", err)
	for _, classified := range []error{ErrTransitionBusy, ErrTransitionInvalid, ErrTransitionCleanup, ErrTransitionCorrupt, ErrTransitionInterrupted} {
		check(t, errors.Is(classified, classified) && !bytes.Contains([]byte(classified.Error()), []byte(home)), "classification=%v", classified)
	}
}

func TestReconcileNoIntentReturnsStrictPreparedHandle(t *testing.T) {
	home, manifest, _ := openFixture(t)
	handle, err := Reconcile(home, manifest.TransactionID())
	require(t, err)
	check(t, handle.State() == Prepared, "state=%q", handle.State())
}

func TestReconcileCompletesCrashSeams(t *testing.T) {
	for _, seam := range []string{"after-intent", "after-manifest"} {
		t.Run(seam, func(t *testing.T) {
			home, manifest, _ := openFixture(t)
			hooks := transitionHooks{afterIntent: func() error {
				if seam == "after-intent" {
					return errors.New("crash")
				}
				return nil
			}, afterManifest: func() error {
				if seam == "after-manifest" {
					return errors.New("crash")
				}
				return nil
			}}
			_, err := transitionWithHooks(home, manifest.TransactionID(), Recovered, hooks)
			check(t, errors.Is(err, ErrTransitionInterrupted), "crash error=%v", err)
			_, err = Open(home, manifest.TransactionID())
			check(t, err != nil, "crash journal passed strict open")
			handle, err := Reconcile(home, manifest.TransactionID())
			require(t, err)
			check(t, handle.State() == Recovered, "state=%q", handle.State())
		})
	}
}

func TestReconcileResumesValidTempWithoutRuntimeMutation(t *testing.T) {
	home, manifest, _ := openFixture(t)
	directory := transactionPath(home, manifest)
	prepared := mustJSON(t, manifest)
	target, err := manifest.Transition(Committed)
	require(t, err)
	intent, err := createTransitionIntent(directory, manifest.TransactionID(), prepared, mustJSON(t, target))
	require(t, err)
	require(t, os.WriteFile(manifestTempName(directory, intent), intent.target, 0600))
	runtime := filepath.Join(home, "runtime-root")
	require(t, os.Mkdir(runtime, 0700))
	require(t, os.WriteFile(filepath.Join(runtime, "sentinel"), []byte("unchanged"), 0600))
	handle, err := Reconcile(home, manifest.TransactionID())
	require(t, err)
	check(t, handle.State() == Committed, "state=%q", handle.State())
	data, err := os.ReadFile(filepath.Join(runtime, "sentinel"))
	require(t, err)
	check(t, bytes.Equal(data, []byte("unchanged")), "runtime root mutated")
}

func TestReconcileRemovesStaleIntentAndRecoversRemovalFailure(t *testing.T) {
	home, manifest, _ := openFixture(t)
	hooks := transitionHooks{removeIntent: func(string, transitionIntent) error { return errors.New("refuse") }}
	_, err := transitionWithHooks(home, manifest.TransactionID(), Committed, hooks)
	check(t, errors.Is(err, ErrTransitionCleanup), "remove error=%v", err)
	directory := transactionPath(home, manifest)
	_, err = os.Lstat(filepath.Join(directory, transitionIntentFile))
	check(t, err == nil, "intent missing after failed removal: %v", err)
	handle, err := Reconcile(home, manifest.TransactionID())
	require(t, err)
	check(t, handle.State() == Committed, "state=%q", handle.State())
}

func TestReconcileFailsClosedOnIntentAndTempDrift(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*testing.T, string, transitionIntent)
	}{
		{"target substitution", func(t *testing.T, directory string, _ transitionIntent) {
			wrong := transitionBytes(t, Recovered)
			require(t, os.WriteFile(filepath.Join(directory, manifestFile), wrong, 0600))
		}},
		{"temp drift", func(t *testing.T, directory string, intent transitionIntent) {
			require(t, os.WriteFile(manifestTempName(directory, intent), []byte("drift"), 0600))
		}},
		{"corrupt intent", func(t *testing.T, directory string, _ transitionIntent) {
			require(t, os.WriteFile(filepath.Join(directory, transitionIntentFile), []byte("{"), 0600))
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			home, manifest, _ := openFixture(t)
			directory := transactionPath(home, manifest)
			target, err := manifest.Transition(Committed)
			require(t, err)
			intent, err := createTransitionIntent(directory, manifest.TransactionID(), mustJSON(t, manifest), mustJSON(t, target))
			require(t, err)
			test.mutate(t, directory, intent)
			before := journalSnapshot(t, home)
			_, err = Reconcile(home, manifest.TransactionID())
			check(t, errors.Is(err, ErrTransitionCorrupt), "error=%v", err)
			check(t, journalSnapshot(t, home) == before, "corrupt state mutated")
		})
	}
}

func TestTransitionRaceChoosesOneTarget(t *testing.T) {
	for _, targets := range [][2]State{{Committed, Committed}, {Committed, Recovered}} {
		home, manifest, _ := openFixture(t)
		reached, release := make(chan struct{}), make(chan struct{})
		var once sync.Once
		hooks := transitionHooks{afterIntent: func() error { once.Do(func() { close(reached) }); <-release; return nil }}
		type result struct {
			target State
			err    error
		}
		results := make(chan result, 2)
		for _, target := range targets {
			go func(target State) {
				_, err := transitionWithHooks(home, manifest.TransactionID(), target, hooks)
				results <- result{target, err}
			}(target)
		}
		<-reached
		first := <-results
		check(t, errors.Is(first.err, ErrTransitionBusy), "loser error=%v", first.err)
		close(release)
		second := <-results
		check(t, second.err == nil, "winner error=%v", second.err)
		handle, err := Open(home, manifest.TransactionID())
		require(t, err)
		check(t, handle.State() == second.target, "winner target=%q state=%q", second.target, handle.State())
	}
}
