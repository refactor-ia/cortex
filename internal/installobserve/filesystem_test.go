package installobserve_test

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/refactor-ia/cortex/internal/installobserve"
	"github.com/refactor-ia/cortex/internal/installplan"
	"github.com/refactor-ia/cortex/internal/installstate"
)

func TestObserveReadsOnlyCanonicalStateAndSlots(t *testing.T) {
	candidate, _ := makeCandidate(t, "one", "alpha")
	writeCandidateFiles(t, candidate)
	if err := os.WriteFile(filepath.Join(candidate.RootPath(), "unrelated.txt"), []byte("not observed"), 0o600); err != nil {
		t.Fatal(err)
	}

	observed, err := installobserve.Observe(candidate, installobserve.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	prior := observed.PriorState()
	if prior == nil || prior.StateSHA256 != hashBytes(candidate.StateJSON()) || !reflect.DeepEqual(prior.Manifest.Artifacts(), candidate.InstalledState().Artifacts()) {
		t.Fatalf("PriorState() = %#v", prior)
	}
	want := []installobserve.SlotObservation{{LogicalID: "skills/alpha", Present: true, SHA256: hashes(candidate)["skills/alpha"]}}
	if !reflect.DeepEqual(observed.Slots(), want) {
		t.Fatalf("Slots() = %#v, want %#v", observed.Slots(), want)
	}
}

func TestObserveRetainsExactEvidenceByLogicalID(t *testing.T) {
	candidate, _ := makeCandidate(t, "one", "alpha")
	writeCandidateFiles(t, candidate)
	files := candidate.Files()
	if err := os.Chmod(files[0].AbsolutePath(), 0o640); err != nil {
		t.Fatal(err)
	}

	observed, err := installobserve.Observe(candidate, installobserve.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	skill, found := observed.Exact("skills/alpha")
	if !found || skill.Mode().Perm() != 0o640 || !bytes.Equal(skill.Bytes(), files[0].Content()) || hashBytes(skill.Bytes()) != observed.Slots()[0].SHA256 {
		t.Fatal("skill exact evidence did not match bytes, mode, and hash")
	}
	state, found := observed.Exact("state/install-state")
	if !found || !bytes.Equal(state.Bytes(), candidate.StateJSON()) || hashBytes(state.Bytes()) != observed.PriorState().StateSHA256 {
		t.Fatal("state exact evidence did not match bytes and hash")
	}
	mutated := skill.Bytes()
	mutated[0] ^= 1
	again, found := observed.Exact("skills/alpha")
	if !found || bytes.Equal(mutated, again.Bytes()) {
		t.Fatal("exact evidence exposed mutable bytes")
	}
	if _, found := observed.Exact("arbitrary/path"); found {
		t.Fatal("arbitrary logical ID returned exact evidence")
	}
}

func TestObserveTreatsMissingCanonicalStateAsNeutral(t *testing.T) {
	candidate, _ := makeCandidate(t, "one", "alpha")
	file := candidate.Files()[0]
	if err := os.MkdirAll(filepath.Dir(file.AbsolutePath()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file.AbsolutePath(), file.Content(), 0o600); err != nil {
		t.Fatal(err)
	}

	observed, err := installobserve.Observe(candidate, installobserve.DefaultOptions())
	if err != nil || observed.PriorState() != nil {
		t.Fatalf("Observe() did not return a neutral missing-state observation: %v", err)
	}
	if slots := observed.Slots(); len(slots) != 1 || !slots[0].Present || slots[0].LogicalID != "skills/alpha" {
		t.Fatalf("Slots() = %#v", slots)
	}
	if _, found := observed.Exact("state/install-state"); found {
		t.Fatal("absent state retained exact evidence")
	}
}

func TestObserveRejectsUnsafeAndBoundedFiles(t *testing.T) {
	for _, tc := range []struct {
		name    string
		setup   func(t *testing.T, candidate installplan.Plan)
		options installobserve.Options
	}{
		{
			name: "state symlink",
			setup: func(t *testing.T, candidate installplan.Plan) {
				path := filepath.Join(candidate.RootPath(), ".cortex", "install-state.json")
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("elsewhere", path); err != nil {
					t.Fatal(err)
				}
			},
			options: installobserve.DefaultOptions(),
		},
		{
			name: "skill directory",
			setup: func(t *testing.T, candidate installplan.Plan) {
				path := candidate.Files()[0].AbsolutePath()
				if err := os.MkdirAll(path, 0o700); err != nil {
					t.Fatal(err)
				}
			},
			options: installobserve.DefaultOptions(),
		},
		{
			name:    "state byte limit",
			setup:   func(t *testing.T, candidate installplan.Plan) { writeCandidateFiles(t, candidate) },
			options: installobserve.Options{MaxStateBytes: 1, MaxEntries: 10, MaxFileBytes: 1024},
		},
		{
			name:    "file byte limit",
			setup:   func(t *testing.T, candidate installplan.Plan) { writeCandidateFiles(t, candidate) },
			options: installobserve.Options{MaxStateBytes: 1024, MaxEntries: 10, MaxFileBytes: 1},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate, _ := makeCandidate(t, "one", "alpha")
			tc.setup(t, candidate)
			if _, err := installobserve.Observe(candidate, tc.options); err == nil {
				t.Fatal("Observe() succeeded")
			}
		})
	}
}

func TestObserveRejectsPriorStateAboveEntryLimit(t *testing.T) {
	candidate, _ := makeCandidate(t, "one", "alpha")
	prior, err := installstate.New(candidate.RuntimeID(), candidate.RootKind(), candidate.SnapshotFingerprint(), []installstate.ArtifactInput{
		{LogicalID: "skills/alpha", RelativePath: "skills/cortex-alpha/SKILL.md", SHA256: hash("alpha")},
		{LogicalID: "skills/beta", RelativePath: "skills/cortex-beta/SKILL.md", SHA256: hash("beta")},
	})
	if err != nil {
		t.Fatal(err)
	}
	state, err := installstate.Encode(prior)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(candidate.RootPath(), ".cortex", "install-state.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, state, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installobserve.Observe(candidate, installobserve.Options{MaxStateBytes: 1024, MaxEntries: 1, MaxFileBytes: 1024}); err == nil {
		t.Fatal("Observe() accepted a prior state above the entry limit")
	}
}

func writeCandidateFiles(t *testing.T, candidate interface{ Files() []installplan.File }) {
	t.Helper()
	for _, file := range candidate.Files() {
		if err := os.MkdirAll(filepath.Dir(file.AbsolutePath()), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file.AbsolutePath(), file.Content(), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
