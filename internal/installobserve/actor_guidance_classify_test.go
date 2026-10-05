package installobserve_test

import (
	"bytes"
	"os"
	"testing"

	"github.com/refactor-ia/cortex/internal/installobserve"
	"github.com/refactor-ia/cortex/internal/installplan"
	"github.com/refactor-ia/cortex/internal/installstate"
	"github.com/refactor-ia/cortex/internal/ownership"
)

func TestClassifyActorGuidance(t *testing.T) {
	for _, name := range []string{"legacy adoption", "legacy update", "unmarked owned", "no optin", "v3 repeat", "v3 tamper", "skill drift", "unrelated skill", "skill mode", "state mode", "actor mode", "unmarked drift"} {
		t.Run(name, func(t *testing.T) {
			canonical := makeActorAwareCandidate(t)
			writeCandidateFiles(t, canonical)
			inputs := []installstate.V2ArtifactInput{}
			unrelated := ""
			for _, a := range canonical.InstalledState().Artifacts() {
				if name == "unrelated skill" && a.Kind() == installstate.KindSkill && unrelated == "" {
					unrelated = a.LogicalID()
					continue
				}
				digest := a.SHA256()
				for _, f := range canonical.Files() {
					if f.LogicalID() == a.LogicalID() && f.Role() == "actor" && name != "unmarked owned" {
						body := bytes.TrimRight(f.Content(), "\n")
						if name == "legacy update" {
							body = append(body, []byte("\nHistorical canonical prose.")...)
							digest = hashBytes(append(bytes.Clone(body), '\n'))
						}
						if err := os.WriteFile(f.AbsolutePath(), append(body, bridgeSuffix...), 0o600); err != nil {
							t.Fatal(err)
						}
					}
				}
				inputs = append(inputs, installstate.V2ArtifactInput{LogicalID: a.LogicalID(), Kind: a.Kind(), CapabilityID: a.CapabilityID(), RoleID: a.RoleID(), ActorContractVersion: a.ActorContractVersion(), RelativePath: a.RelativePath(), SHA256: digest, InstallationID: a.InstallationID()})
			}
			if name == "legacy update" || name == "unrelated skill" {
				prior, err := installstate.NewV2(canonical.RuntimeID(), canonical.RootKind(), canonical.SnapshotFingerprint(), canonical.InstalledState().InstallationID(), inputs)
				if err != nil {
					t.Fatal(err)
				}
				data, err := installstate.Encode(prior)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(canonical.Files()[len(canonical.Files())-1].AbsolutePath(), data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			observe := func() installobserve.FilesystemObservation {
				o, err := installobserve.Observe(canonical, installobserve.DefaultOptions())
				if err != nil {
					t.Fatal(err)
				}
				return o
			}
			if name == "v3 repeat" || name == "v3 tamper" {
				first, err := installobserve.ClassifyActorGuidance(canonical, observe(), true)
				if err != nil {
					t.Fatal(err)
				}
				writeCandidateFiles(t, first.Candidate())
				if name == "v3 tamper" {
					f := actorFile(t, first.Candidate())
					if err := os.WriteFile(f.AbsolutePath(), bytes.Replace(f.Content(), []byte("external guidance"), []byte("altered guidance"), 1), 0o600); err != nil {
						t.Fatal(err)
					}
				}
			}
			for _, f := range canonical.Files() {
				if name == "skill drift" && f.Role() == "skill" || name == "unmarked drift" && f.Role() == "actor" {
					if err := os.WriteFile(f.AbsolutePath(), []byte("tampered"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				if name == "skill mode" && f.Role() == "skill" || name == "state mode" && f.Role() == "state" || name == "actor mode" && f.Role() == "actor" {
					if err := os.Chmod(f.AbsolutePath(), 0o640); err != nil {
						t.Fatal(err)
					}
				}
			}
			o := observe()
			got, err := installobserve.ClassifyActorGuidance(canonical, o, name != "no optin" && name != "v3 repeat" && name != "unmarked owned")
			refuse := name == "no optin" || name == "v3 tamper" || name == "state mode" || name == "actor mode" || name == "unmarked drift"
			if (err != nil) != refuse {
				t.Fatalf("classification error = %v, refuse = %v", err, refuse)
			}
			if refuse {
				if got.Matches(canonical, o) {
					t.Fatal("failed proof matched")
				}
				return
			}
			if !got.Matches(got.Candidate(), o) || got.Matches(canonical, o) || got.Matches(installplan.Plan{}, o) || got.Matches(got.Candidate(), installobserve.FilesystemObservation{}) {
				t.Fatal("candidate/observation binding incorrect")
			}
			for _, f := range got.Candidate().Files() {
				if f.Role() == "actor" && name != "unmarked owned" && !bytes.HasSuffix(f.Content(), bridgeSuffix) {
					t.Fatal("external bytes changed")
				}
			}
			wantActor := ownership.Unchanged
			if name == "legacy update" {
				wantActor = ownership.Replace
			}
			actorCount := 0
			for _, d := range got.Result().ArtifactDecisions() {
				if d.Kind == installstate.KindPiActor {
					actorCount++
				}
				if d.Kind == installstate.KindPiActor && (d.ObservedOwnership == ownership.CortexOwned || d.Action != wantActor) {
					t.Fatalf("actor decision = %#v", d)
				}
				if d.Kind == installstate.KindSkill {
					want := ownership.Unchanged
					if name == "skill drift" || name == "skill mode" || d.LogicalID == unrelated {
						want = ownership.Conflict
					}
					if d.LogicalID == unrelated && d.ObservedOwnership != ownership.Unrelated {
						t.Fatal("unrelated skill taken over")
					}
					if d.Action != want {
						t.Fatalf("skill decision = %#v", d)
					}
				}
			}
			if actorCount != 6 {
				t.Fatalf("classified %d actors, want six", actorCount)
			}
			wantState := ownership.Replace
			if name == "v3 repeat" {
				wantState = ownership.Unchanged
			}
			if got.Result().StateAction() != wantState {
				t.Fatal("wrong state action")
			}
			ordinary, ordinaryErr := installobserve.ClassifyFilesystem(canonical, o)
			if (ordinaryErr != nil) != (o.PriorState().Manifest.SchemaVersion() == 3) {
				t.Fatalf("ordinary classifier schema gate: %v", ordinaryErr)
			}
			if ordinaryErr == nil {
				for _, d := range ordinary.ArtifactDecisions() {
					if d.Kind == installstate.KindPiActor && name != "unmarked owned" && d.Action != ownership.Conflict {
						t.Fatal("ordinary classifier trusted guidance drift")
					}
				}
			}
			unproven, err := installobserve.Observe(got.Candidate(), installobserve.DefaultOptions())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := installobserve.ClassifyFilesystem(got.Candidate(), unproven); err == nil {
				t.Fatal("ordinary classifier accepted observation-bound v3")
			}
			// A new safe capture with changed state, mode, or preimage cannot reuse proof.
			first := actorFile(t, canonical)
			if err := os.Chmod(first.AbsolutePath(), 0o640); err != nil {
				t.Fatal(err)
			}
			if got.Matches(got.Candidate(), observe()) {
				t.Fatal("changed preimage mode matched")
			}
			writeCandidateFiles(t, got.Candidate())
			if name != "v3 repeat" && got.Matches(got.Candidate(), observe()) {
				t.Fatal("changed prior state matched")
			}
			root, err := installobserve.NewUninstallRoot(canonical.RuntimeID(), canonical.RootKind(), canonical.RootPath())
			if err != nil {
				t.Fatal(err)
			}
			removal, err := installobserve.ObserveUninstall(root, installobserve.DefaultOptions())
			if err == nil || len(removal.RemovalCandidates()) != 0 {
				t.Fatal("exact composed actor removal authorized")
			}
			if err := os.WriteFile(first.AbsolutePath(), []byte("different preimage"), 0o600); err != nil {
				t.Fatal(err)
			}
			if got.Matches(got.Candidate(), observe()) {
				t.Fatal("changed preimage digest matched")
			}
		})
	}
	var zero installobserve.ActorGuidanceClassification
	if zero.Matches(installplan.Plan{}, installobserve.FilesystemObservation{}) {
		t.Fatal("zero proof matched")
	}
	canonical := makeActorAwareCandidate(t)
	writeCandidateFiles(t, canonical)
	o, err := installobserve.Observe(canonical, installobserve.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := installobserve.ClassifyActorGuidance(makeActorAwareCandidate(t), o, true); err == nil {
		t.Fatal("wrong root/path accepted")
	}
}
