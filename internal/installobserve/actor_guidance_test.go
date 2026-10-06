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

var bridgeSuffix = []byte("\n\n<!-- gentle-ai:pi-codegraph-guidance -->\nexternal guidance\r\n\t  \n<!-- /gentle-ai:pi-codegraph -->\n")

func TestActorGuidanceInputsComposesCapturedV2AndV3(t *testing.T) {
	candidate := makeActorAwareCandidate(t)
	writeCandidateFiles(t, candidate)
	for _, f := range candidate.Files() {
		if f.Role() == "actor" {
			if err := os.WriteFile(f.AbsolutePath(), append(bytes.TrimRight(f.Content(), "\n"), bridgeSuffix...), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	observation, err := installobserve.Observe(candidate, installobserve.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	prior, actors, err := observation.ActorGuidanceInputs(candidate)
	if err != nil || prior.RootPath != candidate.RootPath() || len(actors) != 6 || !reflect.DeepEqual(prior.State, candidate.InstalledState()) {
		t.Fatalf("bridge = (%#v, %d actors, %v)", prior, len(actors), err)
	}
	if _, _, err := installplan.BuildActorGuidance(candidate, prior, actors, false); err == nil {
		t.Fatal("legacy guidance accepted without opt-in")
	}
	composed, preimages, err := installplan.BuildActorGuidance(candidate, prior, actors, true)
	if err != nil || len(preimages) != 6 || composed.InstalledState().SchemaVersion() != 3 {
		t.Fatalf("composition = (%#v, %v)", preimages, err)
	}
	for _, f := range composed.Files() {
		if f.Role() == "actor" && !bytes.HasSuffix(f.Content(), bridgeSuffix) {
			t.Fatal("composition changed external suffix")
		}
	}
	for _, actor := range actors {
		if preimages[actor.LogicalID] != hashBytes(actor.Content) || actor.Mode != installplan.CanonicalFileMode || actor.AbsolutePath != filepath.Join(candidate.RootPath(), filepath.FromSlash(actor.RelativePath)) {
			t.Fatal("observation identity not preserved")
		}
	}
	// Output and public prior accessors cannot mutate captured evidence.
	actors[0].Content[0] ^= 1
	actors[0].RootPath = "wrong"
	artifacts := prior.State.Artifacts()
	artifacts[0] = installstate.Artifact{}
	prior.State = installstate.Manifest{}
	again, detached, err := observation.ActorGuidanceInputs(candidate)
	if err != nil || !reflect.DeepEqual(again.State, candidate.InstalledState()) || detached[0].Content[0] == actors[0].Content[0] {
		t.Fatal("bridge aliases output")
	}
	// Canonical v3 prior bytes are already accepted by the existing Observe.
	writeCandidateFiles(t, composed)
	registered, err := installobserve.Observe(candidate, installobserve.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	v3prior, v3actors, err := registered.ActorGuidanceInputs(candidate)
	if err != nil || v3prior.State.SchemaVersion() != 3 {
		t.Fatalf("v3 bridge: %v", err)
	}
	if _, _, err := installplan.BuildActorGuidance(candidate, v3prior, v3actors, false); err != nil {
		t.Fatal(err)
	}
	// No reread: disk drift after capture leaves the captured bytes unchanged.
	if err := os.WriteFile(actorFile(t, candidate).AbsolutePath(), []byte("later drift"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, captured, err := registered.ActorGuidanceInputs(candidate)
	if err != nil || !reflect.DeepEqual(captured, v3actors) {
		t.Fatal("bridge reread the filesystem")
	}
	v3actors[0].Content = bytes.Replace(v3actors[0].Content, []byte("external"), []byte("tampered"), 1)
	if _, _, err := installplan.BuildActorGuidance(candidate, v3prior, v3actors, true); err == nil {
		t.Fatal("builder lost v3 full effective proof")
	}
	if _, _, err := observation.ActorGuidanceInputs(installplan.Plan{}); err == nil {
		t.Fatal("zero candidate accepted")
	}
	if _, _, err := observation.ActorGuidanceInputs(makeActorAwareCandidate(t)); err == nil {
		t.Fatal("different root candidate accepted")
	}
	skills, _ := makeCandidate(t, "one", "alpha")
	writeCandidateFiles(t, skills)
	skillObservation, err := installobserve.Observe(skills, installobserve.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := skillObservation.ActorGuidanceInputs(skills); err == nil {
		t.Fatal("v1 desired candidate accepted")
	}
}

func TestActorGuidanceInputsRejectsUnusableEvidence(t *testing.T) {
	for _, name := range []string{"absent root", "missing state", "missing actor", "actor mode", "state mode", "different installation", "v1 prior", "malformed state", "noncanonical state", "actor symlink", "actor directory", "unsafe ancestor", "file bound", "state bound", "unmarked drift"} {
		t.Run(name, func(t *testing.T) {
			candidate := makeActorAwareCandidate(t)
			first := actorFile(t, candidate)
			if name != "absent root" {
				for _, f := range candidate.Files() {
					if name == "missing state" && f.Role() == "state" || name == "missing actor" && f.LogicalID() == first.LogicalID() || (name == "actor symlink" || name == "actor directory" || name == "unsafe ancestor") && f.Role() == "actor" {
						continue
					}
					writeCandidateFiles(t, bridgeFiles{f})
				}
			}
			statePath := filepath.Join(candidate.RootPath(), ".cortex", "install-state.json")
			data := candidate.StateJSON()
			options := installobserve.DefaultOptions()
			switch name {
			case "actor mode", "state mode":
				path := first.AbsolutePath()
				if name == "state mode" {
					path = statePath
				}
				if err := os.Chmod(path, 0o640); err != nil {
					t.Fatal(err)
				}
			case "different installation":
				data = bytes.ReplaceAll(data, []byte(candidate.InstalledState().InstallationID()), []byte("f0e1d2c3b4a5968778695a4b3c2d1e0f"))
			case "v1 prior":
				var inputs []installstate.ArtifactInput
				for _, a := range candidate.InstalledState().Artifacts() {
					if a.Kind() == installstate.KindSkill {
						inputs = append(inputs, installstate.ArtifactInput{LogicalID: a.LogicalID(), RelativePath: a.RelativePath(), SHA256: a.SHA256()})
					}
				}
				state, err := installstate.New(candidate.RuntimeID(), candidate.RootKind(), candidate.SnapshotFingerprint(), inputs)
				if err != nil {
					t.Fatal(err)
				}
				data, err = installstate.Encode(state)
				if err != nil {
					t.Fatal(err)
				}
			case "malformed state":
				data = []byte("{}")
			case "noncanonical state":
				data = append(data, '\n')
			case "actor symlink", "actor directory", "unsafe ancestor":
				path := first.AbsolutePath()
				if name == "unsafe ancestor" {
					path = filepath.Dir(path)
				}
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				var err error
				if name == "actor directory" {
					err = os.Mkdir(path, 0o700)
				} else {
					err = os.Symlink(t.TempDir(), path)
				}
				if err != nil {
					t.Fatal(err)
				}
			case "file bound":
				options.MaxFileBytes = 1
			case "state bound":
				options.MaxStateBytes = 1
			case "unmarked drift":
				if err := os.WriteFile(first.AbsolutePath(), []byte("unmarked drift"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if !bytes.Equal(data, candidate.StateJSON()) {
				if err := os.WriteFile(statePath, data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			observed, observeErr := installobserve.Observe(candidate, options)
			observeFails := name == "malformed state" || name == "noncanonical state" || name == "actor symlink" || name == "actor directory" || name == "unsafe ancestor" || name == "file bound" || name == "state bound"
			if (observeErr != nil) != observeFails {
				t.Fatalf("unexpected Observe result: %v", observeErr)
			}
			prior, actors, err := observed.ActorGuidanceInputs(candidate)
			if name == "unmarked drift" {
				if observeErr != nil || err != nil {
					t.Fatalf("neutral bridge rejected captured drift: %v / %v", observeErr, err)
				}
				if _, _, err := installplan.BuildActorGuidance(candidate, prior, actors, true); err == nil {
					t.Fatal("builder accepted unproved drift")
				}
			} else if err == nil || prior.RootPath != "" || actors != nil {
				t.Fatalf("unusable evidence accepted: Observe error %v, bridge error %v", observeErr, err)
			}
		})
	}
}

type bridgeFiles []installplan.File

func (files bridgeFiles) Files() []installplan.File { return files }
