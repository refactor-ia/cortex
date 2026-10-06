package installtxn

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/refactor-ia/cortex/internal/filetxn"
	"github.com/refactor-ia/cortex/internal/installobserve"
	"github.com/refactor-ia/cortex/internal/installplan"
	"github.com/refactor-ia/cortex/internal/installstate"
	"github.com/refactor-ia/cortex/internal/ownership"
)

var acceptanceSuffix = []byte("\n\n<!-- gentle-ai:pi-codegraph-guidance -->\nexternal guidance\r\n\t  \n<!-- /gentle-ai:pi-codegraph -->\n")

func TestActorGuidanceAcceptance(t *testing.T) {
	for _, scenario := range []string{"update", "actor noop", "skill drift", "skill mode", "unrelated skill"} {
		t.Run(scenario, func(t *testing.T) {
			canonical, proof, observation, snapshot := guidanceAcceptanceFixture(t, scenario)
			candidate := proof.Candidate()
			after, err := deriveActorGuidanceAcceptedAfter(candidate, proof, observation, snapshot)
			if scenario == "skill drift" || scenario == "skill mode" || scenario == "unrelated skill" {
				if !errors.Is(err, ErrConflict) || len(after) != 0 {
					t.Fatalf("unsafe skills accepted: %v", err)
				}
				return
			}
			must(t, err)
			if len(after) != len(snapshot.Manifest.Entries) {
				t.Fatal("incomplete after evidence")
			}
			files := map[string]installplan.File{}
			for _, file := range candidate.Files() {
				files[file.RelativePath()] = file
			}
			actors := 0
			for _, value := range after {
				file, desired := files[value.Path()]
				if !desired {
					if value.Path() != "skills/cortex-retired/SKILL.md" || value.Exists() || value.Data() != nil || value.Mode() != 0 {
						t.Fatal("invalid skill removal result")
					}
					continue
				}
				if !value.Exists() || !bytes.Equal(value.Data(), file.Content()) || value.Mode() != file.DesiredMode() {
					t.Fatal("after differs from exact candidate")
				}
				if file.Role() == "actor" {
					actors++
					if !bytes.HasSuffix(value.Data(), acceptanceSuffix) {
						t.Fatal("external bytes lost")
					}
				}
			}
			if scenario == "update" && actors != 6 || scenario == "actor noop" && actors != 0 {
				t.Fatalf("wrong actor coverage: %d", actors)
			}
			id, err := transactionID(candidate, snapshot) // Trusted acceptance-side fixture evidence.
			must(t, err)
			_, err = actorGuidanceRecoveryAfter(candidate, proof, observation, snapshot, id)
			must(t, err)
			copyRoot := physicalTempDir(t)
			must(t, os.CopyFS(filepath.Join(copyRoot, "copy"), os.DirFS(snapshot.Dir)))
			copied, err := filetxn.Open(copyRoot, "copy")
			must(t, err)
			_, err = actorGuidanceRecoveryAfter(candidate, proof, observation, copied, id)
			must(t, err)
			// A v3 repeat has no operations, including no state operation.
			for _, file := range candidate.Files() {
				must(t, os.WriteFile(file.AbsolutePath(), file.Content(), file.DesiredMode()))
			}
			repeatObservation, err := installobserve.Observe(canonical, installobserve.DefaultOptions())
			must(t, err)
			repeat, err := installobserve.ClassifyActorGuidance(canonical, repeatObservation, false)
			must(t, err)
			empty, err := filetxn.Capture(candidate.RootPath(), physicalTempDir(t), "empty", nil)
			must(t, err)
			values, err := deriveActorGuidanceAcceptedAfter(repeat.Candidate(), repeat, repeatObservation, empty)
			if err != nil || len(values) != 0 {
				t.Fatalf("v3 no-op: %v", err)
			}
		})
	}
}

func TestActorGuidanceAcceptanceRejectsTampering(t *testing.T) {
	for _, scenario := range []string{"zero proof", "zero observation", "stale observation", "candidate", "zero ID", "wrong ID", "root ID", "missing", "extra", "duplicate", "mode", "absent", "preimage", "state", "corrupt payload"} {
		t.Run(scenario, func(t *testing.T) {
			canonical, proof, observation, snapshot := guidanceAcceptanceFixture(t, "update")
			candidate := proof.Candidate()
			id, err := transactionID(candidate, snapshot)
			must(t, err)
			switch scenario {
			case "zero proof":
				proof = installobserve.ActorGuidanceClassification{}
			case "zero observation":
				observation = installobserve.FilesystemObservation{}
			case "stale observation":
				must(t, os.Chmod(canonical.Files()[0].AbsolutePath(), 0o640))
				observation, err = installobserve.Observe(canonical, installobserve.DefaultOptions())
				must(t, err)
			case "candidate":
				candidate = canonical
			case "zero ID":
				id = TransactionID{}
			case "wrong ID":
				id, err = ParseTransactionID("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
				must(t, err)
			case "root ID":
				_, otherProof, otherObservation, _ := guidanceAcceptanceFixture(t, "update")
				candidate, proof, observation = otherProof.Candidate(), otherProof, otherObservation
			case "missing":
				snapshot.Manifest.Entries = snapshot.Manifest.Entries[1:]
			case "extra":
				snapshot.Manifest.Entries = append(snapshot.Manifest.Entries, filetxn.Entry{Path: "unrelated.txt"})
			case "duplicate":
				snapshot.Manifest.Entries = append(snapshot.Manifest.Entries, snapshot.Manifest.Entries[0])
			case "mode":
				snapshot.Manifest.Entries[0].Mode = 0o640
			case "absent":
				snapshot.Manifest.Entries[0] = filetxn.Entry{Path: stateRelativePath}
			case "preimage", "state", "corrupt payload":
				entry := snapshot.Manifest.Entries[1] // First actor, after the sorted state entry.
				if scenario == "state" {
					entry = snapshot.Manifest.Entries[0]
				}
				must(t, os.WriteFile(acceptancePayloadPath(snapshot, entry.Path), []byte("tampered"), 0o600))
				if scenario != "corrupt payload" {
					for i := range snapshot.Manifest.Entries {
						if snapshot.Manifest.Entries[i].Path == entry.Path {
							snapshot.Manifest.Entries[i].SHA256 = sha256Hex([]byte("tampered"))
						}
					}
				}
			}
			data, err := json.Marshal(snapshot.Manifest)
			must(t, err)
			must(t, os.WriteFile(filepath.Join(snapshot.Dir, "manifest.json"), data, 0o600))
			// Acceptance validates even a newly proposed self-consistent snapshot;
			// recovery additionally compares the ORIGINAL trusted ID, never rehashed.
			if scenario != "root ID" && scenario != "zero ID" && scenario != "wrong ID" {
				if values, err := deriveActorGuidanceAcceptedAfter(candidate, proof, observation, snapshot); err == nil || len(values) != 0 {
					t.Fatal("tampered acceptance evidence accepted")
				}
			}
			if values, err := actorGuidanceRecoveryAfter(candidate, proof, observation, snapshot, id); err == nil || len(values) != 0 {
				t.Fatal("tampered recovery evidence accepted")
			}
		})
	}
}

func guidanceAcceptanceFixture(t *testing.T, scenario string) (installplan.Plan, installobserve.ActorGuidanceClassification, installobserve.FilesystemObservation, filetxn.Snapshot) {
	t.Helper()
	canonical := actorAwareCandidate(t)
	inputs := []installstate.V2ArtifactInput{}
	skill := 0
	for _, artifact := range canonical.InstalledState().Artifacts() {
		data := []byte{}
		for _, file := range canonical.Files() {
			if file.LogicalID() == artifact.LogicalID() {
				data = file.Content()
			}
		}
		digest := artifact.SHA256()
		if artifact.Kind() == installstate.KindPiActor {
			if scenario != "actor noop" {
				data = append(bytes.TrimRight(data, "\n"), []byte("\nHistorical canonical prose.\n")...)
				digest = sha256Hex(data)
			}
			data = append(bytes.TrimRight(data, "\n"), acceptanceSuffix...)
		} else {
			skill++
			if skill == 1 {
				data, digest = []byte("old owned skill"), sha256Hex([]byte("old owned skill"))
			}
		}
		input := installstate.V2ArtifactInput{LogicalID: artifact.LogicalID(), Kind: artifact.Kind(), CapabilityID: artifact.CapabilityID(), RoleID: artifact.RoleID(), ActorContractVersion: artifact.ActorContractVersion(), RelativePath: artifact.RelativePath(), SHA256: digest, InstallationID: artifact.InstallationID()}
		if scenario != "unrelated skill" || skill != 1 || artifact.Kind() != installstate.KindSkill {
			inputs = append(inputs, input)
		}
		absolute := filepath.Join(canonical.RootPath(), artifact.RelativePath())
		mustMkdir(t, filepath.Dir(absolute))
		if artifact.Kind() == installstate.KindSkill && skill == 2 {
			continue // Ordinary absent skill creation.
		}
		if scenario == "skill drift" && skill == 1 && artifact.Kind() == installstate.KindSkill {
			data = []byte("user drift")
		}
		must(t, os.WriteFile(absolute, data, 0o600))
		if scenario == "skill mode" && skill == 1 && artifact.Kind() == installstate.KindSkill {
			must(t, os.Chmod(absolute, 0o640))
		}
	}
	retired := "skills/cortex-retired/SKILL.md"
	inputs = append(inputs, installstate.V2ArtifactInput{LogicalID: "skills/retired", Kind: installstate.KindSkill, CapabilityID: "retired", RelativePath: retired, SHA256: sha256Hex([]byte("retired")), InstallationID: canonical.InstalledState().InstallationID()})
	mustMkdir(t, filepath.Join(canonical.RootPath(), filepath.Dir(retired)))
	must(t, os.WriteFile(filepath.Join(canonical.RootPath(), retired), []byte("retired"), 0o600))
	prior, err := installstate.NewV2(canonical.RuntimeID(), canonical.RootKind(), canonical.SnapshotFingerprint(), canonical.InstalledState().InstallationID(), inputs)
	must(t, err)
	data, err := installstate.Encode(prior)
	must(t, err)
	mustMkdir(t, filepath.Join(canonical.RootPath(), ".cortex"))
	must(t, os.WriteFile(filepath.Join(canonical.RootPath(), stateRelativePath), data, 0o600))
	observation, err := installobserve.Observe(canonical, installobserve.DefaultOptions())
	must(t, err)
	proof, err := installobserve.ClassifyActorGuidance(canonical, observation, true)
	must(t, err)
	paths := []string{stateRelativePath}
	for _, decision := range proof.Result().ArtifactDecisions() {
		if decision.Action == ownership.Unchanged || decision.Action == ownership.Preserve {
			continue
		}
		relative := retired
		for _, file := range proof.Candidate().Files() {
			if file.LogicalID() == decision.LogicalID {
				relative = file.RelativePath()
			}
		}
		paths = append(paths, relative)
	}
	snapshot, err := filetxn.Capture(canonical.RootPath(), physicalTempDir(t), "acceptance", paths)
	must(t, err)
	return canonical, proof, observation, snapshot
}

func acceptancePayloadPath(snapshot filetxn.Snapshot, relative string) string {
	return filepath.Join(snapshot.Dir, "payloads", sha256Hex([]byte(relative)))
}
