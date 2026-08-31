package installobserve_test

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/refactor-ia/cortex/internal/adapterplan"
	"github.com/refactor-ia/cortex/internal/catalog"
	"github.com/refactor-ia/cortex/internal/installobserve"
	"github.com/refactor-ia/cortex/internal/installplan"
	"github.com/refactor-ia/cortex/internal/installstate"
	"github.com/refactor-ia/cortex/internal/projection"
	"github.com/refactor-ia/cortex/internal/qaactor"
	"github.com/refactor-ia/cortex/internal/runtimematrix"
	"github.com/refactor-ia/cortex/internal/skillartifact"
	"github.com/refactor-ia/cortex/internal/skilldest"
	"github.com/refactor-ia/cortex/internal/skillprojection"
	"github.com/refactor-ia/cortex/internal/skillrender"
	"github.com/refactor-ia/cortex/internal/skillroot"
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
	if err := os.MkdirAll(candidate.RootPath(), 0o700); err != nil {
		t.Fatal(err)
	}

	observed, err := installobserve.Observe(candidate, installobserve.DefaultOptions())
	if err != nil || observed.PriorState() != nil {
		t.Fatalf("Observe() did not return a neutral missing-state observation: %v", err)
	}
	if !observed.MatchesCandidate(candidate) {
		t.Fatal("first-install observation did not match its candidate")
	}
	if slots := observed.Slots(); len(slots) != 1 || slots[0].Present || slots[0].LogicalID != "skills/alpha" {
		t.Fatalf("Slots() = %#v", slots)
	}
	for _, candidateFile := range candidate.Files() {
		if _, err := os.Stat(candidateFile.AbsolutePath()); !os.IsNotExist(err) {
			t.Fatalf("Observe() wrote %q: %v", candidateFile.AbsolutePath(), err)
		}
	}
	if _, found := observed.Exact("state/install-state"); found {
		t.Fatal("absent state retained exact evidence")
	}
}

func TestFilesystemObservationMatchesOnlyItsExactCandidate(t *testing.T) {
	candidate, _ := makeCandidate(t, "one", "alpha")
	writeCandidateFiles(t, candidate)
	observed, err := installobserve.Observe(candidate, installobserve.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name      string
		candidate installplan.Plan
	}{
		{name: "zero candidate"},
		{name: "different root", candidate: func() installplan.Plan { plan, _ := makeCandidate(t, "one", "alpha"); return plan }()},
		{name: "different snapshot content and hash", candidate: func() installplan.Plan { plan, _ := makeCandidate(t, "two", "alpha"); return plan }()},
		{name: "different file set", candidate: func() installplan.Plan { plan, _ := makeCandidate(t, "one", "alpha", "beta"); return plan }()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if observed.MatchesCandidate(tc.candidate) {
				t.Fatal("MatchesCandidate() accepted a different candidate")
			}
		})
	}
	if !observed.MatchesCandidate(candidate) {
		t.Fatal("MatchesCandidate() rejected the observed candidate")
	}

	failed, err := installobserve.Observe(installplan.Plan{}, installobserve.DefaultOptions())
	if err == nil || failed.MatchesCandidate(candidate) {
		t.Fatal("failed observation retained a usable candidate binding")
	}
}

func TestFilesystemObservationExposesNoBindingOrPath(t *testing.T) {
	typeOfObservation := reflect.TypeOf(installobserve.FilesystemObservation{})
	for index := 0; index < typeOfObservation.NumField(); index++ {
		if typeOfObservation.Field(index).IsExported() {
			t.Fatalf("FilesystemObservation exposes field %q", typeOfObservation.Field(index).Name)
		}
	}
	for _, name := range []string{"Binding", "BindingDigest", "RootPath", "Candidate", "SetBinding"} {
		if _, found := typeOfObservation.MethodByName(name); found {
			t.Fatalf("FilesystemObservation exposes %s", name)
		}
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

func TestObserveActorAwareV2CanonicalEvidence(t *testing.T) {
	candidate := makeActorAwareCandidate(t)
	writeCandidateFiles(t, candidate)

	observed, err := installobserve.Observe(candidate, installobserve.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	prior := observed.PriorState()
	if prior == nil || prior.Manifest.SchemaVersion() != 2 || !reflect.DeepEqual(prior.Manifest, candidate.InstalledState()) {
		t.Fatalf("PriorState() = %#v", prior)
	}
	files := candidate.Files()
	if !observed.MatchesCandidate(candidate) || observed.MatchesCandidate(makeActorAwareCandidate(t)) || len(observed.Slots()) != len(files)-1 {
		t.Fatal("actor-aware observation did not retain only its exact candidate and all asset slots")
	}
	for _, file := range files {
		if file.Role() == "state" {
			continue
		}
		exact, found := observed.Exact(file.LogicalID())
		if !found || !bytes.Equal(exact.Bytes(), file.Content()) || exact.Mode() != installplan.CanonicalFileMode {
			t.Fatalf("Exact(%q) did not retain canonical actor-aware evidence", file.LogicalID())
		}
	}
	state, found := observed.Exact("state/install-state")
	if !found || !bytes.Equal(state.Bytes(), candidate.StateJSON()) || state.Mode() != installplan.CanonicalFileMode {
		t.Fatal("state exact evidence did not retain canonical bytes and mode")
	}
	for index, slot := range observed.Slots() {
		if !slot.Present || slot.LogicalID == "" || (index > 0 && observed.Slots()[index-1].LogicalID >= slot.LogicalID) {
			t.Fatalf("Slots() = %#v, want present logical-ID order", observed.Slots())
		}
	}
}

func TestObserveActorAwareV2ReadsOnlyDesiredPathsWhenFresh(t *testing.T) {
	candidate := makeActorAwareCandidate(t)
	if err := os.MkdirAll(candidate.RootPath(), 0o700); err != nil {
		t.Fatal(err)
	}
	unrelated := filepath.Join(candidate.RootPath(), "unrelated")
	if err := os.Symlink("missing", unrelated); err != nil {
		t.Fatal(err)
	}

	observed, err := installobserve.Observe(candidate, installobserve.DefaultOptions())
	if err != nil || observed.PriorState() != nil || len(observed.Slots()) != len(candidate.Files())-1 {
		t.Fatalf("Observe() = (%#v, %v)", observed, err)
	}
	for _, file := range candidate.Files() {
		if _, err := os.Lstat(file.AbsolutePath()); !os.IsNotExist(err) {
			t.Fatalf("Observe() wrote %q: %v", file.AbsolutePath(), err)
		}
	}
}

func TestObserveActorAwareV2IncludesPriorV1Paths(t *testing.T) {
	candidate := makeActorAwareCandidate(t)
	inputs := make([]installstate.ArtifactInput, 0)
	for _, artifact := range candidate.InstalledState().Artifacts() {
		if artifact.Kind() == installstate.KindSkill {
			inputs = append(inputs, installstate.ArtifactInput{LogicalID: artifact.LogicalID(), RelativePath: artifact.RelativePath(), SHA256: artifact.SHA256()})
		}
	}
	prior, err := installstate.New(candidate.RuntimeID(), candidate.RootKind(), candidate.SnapshotFingerprint(), inputs)
	if err != nil {
		t.Fatal(err)
	}
	state, err := installstate.Encode(prior)
	if err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(candidate.RootPath(), ".cortex", "install-state.json")
	if err := os.MkdirAll(filepath.Dir(statePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, state, 0o600); err != nil {
		t.Fatal(err)
	}

	observed, err := installobserve.Observe(candidate, installobserve.DefaultOptions())
	if err != nil || observed.PriorState() == nil || observed.PriorState().Manifest.SchemaVersion() != 1 || len(observed.Slots()) != len(candidate.Files())-1 {
		t.Fatalf("Observe() = (%#v, %v), want prior skills plus desired actors", observed, err)
	}
}

func TestObserveActorAwareV2AcceptsPriorDifferentInstallationID(t *testing.T) {
	candidate := makeActorAwareCandidate(t)
	prior := candidate.StateJSON()
	oldID := []byte(candidate.InstalledState().InstallationID())
	prior = bytes.ReplaceAll(prior, oldID, []byte("f0e1d2c3b4a5968778695a4b3c2d1e0f"))
	statePath := filepath.Join(candidate.RootPath(), ".cortex", "install-state.json")
	if err := os.MkdirAll(filepath.Dir(statePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, prior, 0o600); err != nil {
		t.Fatal(err)
	}

	observed, err := installobserve.Observe(candidate, installobserve.DefaultOptions())
	if err != nil || observed.PriorState() == nil || observed.PriorState().Manifest.InstallationID() == candidate.InstalledState().InstallationID() {
		t.Fatalf("Observe() = (%#v, %v), want neutral different-ID v2 prior", observed, err)
	}
	if len(observed.Slots()) != len(candidate.Files())-1 {
		t.Fatalf("Slots() = %#v, want desired skills and actors", observed.Slots())
	}
}

func TestObserveActorAwareV2RejectsUnsafeOrMalformedInputs(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, candidate installplan.Plan)
	}{
		{
			name: "actor symlink",
			setup: func(t *testing.T, candidate installplan.Plan) {
				actor := actorFile(t, candidate)
				if err := os.MkdirAll(filepath.Dir(actor.AbsolutePath()), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("other", actor.AbsolutePath()); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "actor directory",
			setup: func(t *testing.T, candidate installplan.Plan) {
				if err := os.MkdirAll(actorFile(t, candidate).AbsolutePath(), 0o700); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "oversize actor",
			setup: func(t *testing.T, candidate installplan.Plan) {
				actor := actorFile(t, candidate)
				if err := os.MkdirAll(filepath.Dir(actor.AbsolutePath()), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(actor.AbsolutePath(), []byte("too large"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "same ID actor path mismatch",
			setup: func(t *testing.T, candidate installplan.Plan) {
				statePath := filepath.Join(candidate.RootPath(), ".cortex", "install-state.json")
				state := bytes.Replace(candidate.StateJSON(), []byte("agents/cortex-adversarial-tester.md"), []byte("agents/cortex-test-designer.md"), 1)
				if err := os.MkdirAll(filepath.Dir(statePath), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(statePath, state, 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := makeActorAwareCandidate(t)
			tc.setup(t, candidate)
			options := installobserve.DefaultOptions()
			if tc.name == "oversize actor" {
				options.MaxFileBytes = 1
			}
			if _, err := installobserve.Observe(candidate, options); err == nil {
				t.Fatal("Observe() accepted unsafe or malformed v2 evidence")
			}
		})
	}
}

func actorFile(t *testing.T, candidate installplan.Plan) installplan.File {
	t.Helper()
	for _, file := range candidate.Files() {
		if file.Role() == "actor" {
			return file
		}
	}
	t.Fatal("missing actor file")
	return installplan.File{}
}

// makeActorAwareCandidate builds a real-catalog Pi v2 plan for observation tests.
func makeActorAwareCandidate(t *testing.T) installplan.Plan {
	t.Helper()
	catalogRoot := filepath.Join("..", "..", "catalog")
	snapshot, err := catalog.BuildCatalogSnapshot(catalogRoot, "catalog.json", catalog.AdmissionPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	sources, err := skillrender.Render(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	projected, err := skillprojection.Build(runtimematrix.RuntimePi, sources)
	if err != nil {
		t.Fatal(err)
	}
	assessments := make([]projection.Assessment, 0, 3)
	observations := make([]runtimematrix.Observation, 0, 3)
	for _, runtimeID := range []runtimematrix.RuntimeID{runtimematrix.RuntimePi, runtimematrix.RuntimeOpenCode, runtimematrix.RuntimeClaudeCode} {
		assessment, err := skillprojection.Build(runtimeID, sources)
		if err != nil {
			t.Fatal(err)
		}
		assessments = append(assessments, assessment.Assessment())
		observations = append(observations, runtimematrix.Observation{ID: runtimeID, Present: true, Version: "test", Compatibility: runtimematrix.Compatible})
	}
	base, err := adapterplan.Build(snapshot.Fingerprint(), observations)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := projection.BuildPlan(base, assessments)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := skillartifact.Build(projected, plan)
	if err != nil {
		t.Fatal(err)
	}
	bundle, found := binding.Bundle()
	if !found {
		t.Fatal("missing skill bundle")
	}
	destinations, err := skilldest.Build(binding)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := skillroot.Resolve(destinations, skillroot.Inputs{Home: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	skills, err := installplan.BuildWithBundle(resolved, bundle)
	if err != nil {
		t.Fatal(err)
	}
	actorSources, err := qaactor.Sources(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	set, err := qaactor.Render(actorSources)
	if err != nil {
		t.Fatal(err)
	}
	actors, err := qaactor.ProjectPi(set)
	if err != nil {
		t.Fatal(err)
	}
	actorBinding, err := qaactor.Bind(actors)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := installplan.BuildActorAware(skills, actorBinding, "000102030405060708090a0b0c0d0e0f")
	if err != nil {
		t.Fatal(err)
	}
	return candidate
}
