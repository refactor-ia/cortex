package installcoord_test

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/refactor-ia/cortex/internal/catalog"
	"github.com/refactor-ia/cortex/internal/installcoord"
	"github.com/refactor-ia/cortex/internal/installobserve"
	"github.com/refactor-ia/cortex/internal/installplan"
	"github.com/refactor-ia/cortex/internal/installstate"
	"github.com/refactor-ia/cortex/internal/ownership"
	"github.com/refactor-ia/cortex/internal/qaactor"
	"github.com/refactor-ia/cortex/internal/runtimematrix"
	"github.com/refactor-ia/cortex/internal/skilldest"
	"github.com/refactor-ia/cortex/internal/skillroot"
)

var coordGuidance = []byte("\n\n<!-- gentle-ai:pi-codegraph-guidance -->\nexternal guidance\r\n\t  \n<!-- /gentle-ai:pi-codegraph -->\n")

func coordPhysicalDir(t *testing.T) string {
	t.Helper()
	return value(filepath.EvalSymlinks(t.TempDir()))
}

func coordGuidanceFixture(t *testing.T, changed bool) installplan.Plan {
	t.Helper()
	home := coordPhysicalDir(t)
	snapshot := value(catalog.BuildCatalogSnapshot(filepath.Join("..", "..", "catalog"), "catalog.json", catalog.AdmissionPolicy{}))
	candidates, _, err := installcoord.BuildCandidates(installcoord.CandidateRequest{
		Snapshot: snapshot, Observations: actorAwareObservations(), LocalTarget: runtimematrix.RuntimePi,
		ResolveRoot: func(dest skilldest.Plan) (skillroot.Plan, error) {
			return skillroot.Resolve(dest, skillroot.Inputs{Home: home})
		},
		Actors: func(snapshot catalog.CatalogSnapshot) (qaactor.Binding, error) {
			return qaactor.Bind(value(qaactor.ProjectPi(value(qaactor.Render(value(qaactor.Sources(snapshot)))))))
		},
		InstallationID: func(skillroot.Plan) (installstate.InstallationID, error) {
			return "000102030405060708090a0b0c0d0e0f", nil
		},
	})
	must(t, err)
	plan := candidates[0].Plan
	coordWritePlan(t, plan)
	inputs := []installstate.V3ArtifactInput{}
	for _, a := range plan.InstalledState().Artifacts() {
		input := installstate.V3ArtifactInput{V2ArtifactInput: installstate.V2ArtifactInput{
			LogicalID: a.LogicalID(), Kind: a.Kind(), CapabilityID: a.CapabilityID(), RoleID: a.RoleID(),
			ActorContractVersion: a.ActorContractVersion(), RelativePath: a.RelativePath(), SHA256: a.SHA256(), InstallationID: a.InstallationID(),
		}}
		for _, f := range plan.Files() {
			if f.LogicalID() != a.LogicalID() || f.Role() != "actor" {
				continue
			}
			body := bytes.TrimRight(f.Content(), "\n")
			if changed {
				body = append(body, []byte("\nHistorical canonical prose.")...)
			}
			effective := append(bytes.Clone(body), coordGuidance...)
			must(t, os.WriteFile(f.AbsolutePath(), effective, 0o600))
			input.CanonicalSHA256 = fmt.Sprintf("%x", sha256.Sum256(append(bytes.Clone(body), '\n')))
			input.SHA256 = fmt.Sprintf("%x", sha256.Sum256(effective))
		}
		inputs = append(inputs, input)
	}
	if changed {
		prior := value(installstate.NewV3(plan.RuntimeID(), plan.RootKind(), plan.SnapshotFingerprint(), plan.InstalledState().InstallationID(), inputs))
		must(t, os.WriteFile(plan.Files()[len(plan.Files())-1].AbsolutePath(), value(installstate.Encode(prior)), 0o600))
	}
	return plan
}

func coordWritePlan(t *testing.T, plan installplan.Plan) {
	t.Helper()
	for _, f := range plan.Files() {
		must(t, os.MkdirAll(filepath.Dir(f.AbsolutePath()), 0o700))
		must(t, os.WriteFile(f.AbsolutePath(), f.Content(), f.DesiredMode()))
	}
}

func coordTree(t *testing.T, root string) map[string]string {
	t.Helper()
	result := map[string]string{}
	must(t, filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info := value(entry.Info())
		result[path] = info.Mode().String()
		if info.Mode().IsRegular() {
			result[path] += string(value(os.ReadFile(path)))
		}
		return nil
	}))
	return result
}

func TestPreflightActorGuidance(t *testing.T) {
	for _, name := range []string{"legacy explicit adoption", "v3 repeat", "changed canonical update", "uncertified local update"} {
		t.Run(name, func(t *testing.T) {
			canonical, cwd := coordGuidanceFixture(t, name == "changed canonical update"), coordPhysicalDir(t)
			observe := func() installobserve.FilesystemObservation {
				return value(installobserve.Observe(canonical, installobserve.DefaultOptions()))
			}
			if name == "v3 repeat" {
				first := value(installcoord.PreflightActorGuidance(actorAwareObservations(), installcoord.Unit{Plan: canonical, Observation: observe()}, true, cwd))
				coordWritePlan(t, first.Proof().Candidate())
			}
			o := observe()
			before, projectBefore := coordTree(t, canonical.RootPath()), coordTree(t, cwd)
			observations := compatible()
			wantOutcome := runtimematrix.OutcomePresentCompatible
			if name == "uncertified local update" {
				for i := range observations {
					if observations[i].ID == runtimematrix.RuntimePi {
						observations[i].Compatibility = runtimematrix.CompatibilityUnknown
					}
				}
				wantOutcome = runtimematrix.OutcomePresentUncertified
			}
			adopt := name != "v3 repeat" && name != "changed canonical update"
			got, err := installcoord.PreflightActorGuidance(observations, installcoord.Unit{Plan: canonical, Observation: o}, adopt, cwd)
			if err != nil || !got.Report().Ready() {
				t.Fatalf("planning: ready=%v err=%v", got.Report().Ready(), err)
			}
			proof, statuses := got.Proof(), got.Report().Statuses()
			candidate := proof.Candidate()
			if candidate.InstalledState().SchemaVersion() != 3 || !proof.Matches(candidate, o) || proof.Matches(canonical, o) {
				t.Fatal("lost exact composed candidate/proof binding")
			}
			if len(statuses) != 3 || statuses[0].Outcome != wantOutcome || !statuses[0].Ready() || statuses[1].Ready() || statuses[2].Ready() || len(statuses[1].Actions()) != 0 || len(statuses[2].Actions()) != 0 {
				t.Fatalf("local report: %#v", statuses)
			}
			wantState, wantActor := ownership.Replace, ownership.Unchanged
			if name == "v3 repeat" {
				wantState = ownership.Unchanged
			}
			if name == "changed canonical update" {
				wantActor = ownership.Replace
			}
			wantActions, actorCount := []installcoord.Action{}, 0
			for _, d := range proof.Result().ArtifactDecisions() {
				if d.Kind == installstate.KindPiActor {
					actorCount++
				} else if d.Action != ownership.Unchanged {
					t.Fatalf("clean skill action: %#v", d)
				}
				wantActions = append(wantActions, d.Action)
				if d.Kind == installstate.KindPiActor && (d.ObservedOwnership != ownership.UserOwned || d.Action != wantActor) {
					t.Fatalf("actor taken over: %#v", d)
				}
			}
			wantActions = append(wantActions, wantState)
			if actorCount != 6 || proof.Result().StateAction() != wantState || !reflect.DeepEqual(statuses[0].Actions(), wantActions) {
				t.Fatal("report diverged from classifier actions")
			}
			for _, f := range candidate.Files() {
				if f.Role() == "actor" && (!bytes.HasSuffix(f.Content(), coordGuidance) || bytes.Contains(f.Content(), []byte("Historical canonical prose."))) {
					t.Fatal("candidate lost external bytes or retained old canonical content")
				}
			}
			statuses[0].Actions()[0] = ownership.Conflict
			candidate.Files()[0].Content()[0] ^= 1
			candidate.StateJSON()[0] ^= 1
			if !proof.Matches(got.Proof().Candidate(), o) || !reflect.DeepEqual(got.Report().Statuses()[0].Actions(), wantActions) {
				t.Fatal("mutable report/proof getter")
			}
			if !reflect.DeepEqual(before, coordTree(t, canonical.RootPath())) || !reflect.DeepEqual(projectBefore, coordTree(t, cwd)) {
				t.Fatal("planning mutated target or project")
			}
			ordinary, ordinaryErr := installcoord.PreflightLocalUpdate(actorAwareObservations(), runtimematrix.RuntimePi, []installcoord.Unit{{Plan: canonical, Observation: o}})
			if ordinaryErr == nil && ordinary.Ready() {
				t.Fatal("ordinary preflight accepted composed actors")
			}
			composedObservation := value(installobserve.Observe(candidate, installobserve.DefaultOptions()))
			if _, err := installcoord.Classify(candidate, composedObservation); err == nil {
				t.Fatal("ordinary classifier accepted v3")
			}
			if ordinary, err := installcoord.Preflight(observations, []installcoord.Unit{{Plan: candidate, Observation: composedObservation}}); err == nil || ordinary.Ready() {
				t.Fatal("ordinary preflight accepted composed unit")
			}
		})
	}
}

func TestPreflightActorGuidanceRefuses(t *testing.T) {
	for _, name := range []string{"default adoption refusal", "zero unit", "zero observation", "wrong root", "wrong runtime", "missing runtime", "denied runtime", "composed unit", "skill drift", "shadow", "unsafe cwd", "symlink cwd", "unsafe root", "actor mode", "v3 tamper", "stale actor", "zero runtimes"} {
		t.Run(name, func(t *testing.T) {
			canonical, cwd := coordGuidanceFixture(t, false), coordPhysicalDir(t)
			unit := installcoord.Unit{Plan: canonical, Observation: value(installobserve.Observe(canonical, installobserve.DefaultOptions()))}
			observations, adopt := actorAwareObservations(), true
			switch name {
			case "default adoption refusal":
				adopt = false
			case "zero unit":
				unit = installcoord.Unit{}
			case "zero observation":
				unit.Observation = installobserve.FilesystemObservation{}
			case "wrong root":
				unit.Plan = coordGuidanceFixture(t, false)
			case "wrong runtime":
				unit.Plan = candidates(t, "other")[1]
			case "missing runtime":
				observations = observations[1:]
			case "denied runtime":
				observations[0].Compatibility = runtimematrix.Incompatible
			case "zero runtimes":
				observations = nil
			case "composed unit":
				proof := value(installobserve.ClassifyActorGuidance(canonical, unit.Observation, true))
				unit.Plan = proof.Candidate()
				unit.Observation = value(installobserve.Observe(unit.Plan, installobserve.DefaultOptions()))
			case "skill drift":
				must(t, os.WriteFile(canonical.Files()[0].AbsolutePath(), []byte("user skill drift"), 0o600))
				unit.Observation = value(installobserve.Observe(canonical, installobserve.DefaultOptions()))
			case "shadow":
				write(t, cwd, ".pi/subagents/alias.md", "---\nname: cortex-test-designer\n---\n")
			case "unsafe cwd":
				cwd = "relative"
			case "symlink cwd":
				alias := filepath.Join(coordPhysicalDir(t), "alias")
				must(t, os.Symlink(cwd, alias))
				cwd = alias
			case "unsafe root":
				saved := canonical.RootPath() + ".saved"
				must(t, os.Rename(canonical.RootPath(), saved))
				must(t, os.Symlink(saved, canonical.RootPath()))
			case "stale actor", "actor mode", "v3 tamper":
				if name == "v3 tamper" {
					proof := value(installobserve.ClassifyActorGuidance(canonical, unit.Observation, true))
					coordWritePlan(t, proof.Candidate())
					adopt = false
				}
				for _, f := range canonical.Files() {
					if f.Role() == "actor" {
						if name == "actor mode" {
							must(t, os.Chmod(f.AbsolutePath(), 0o640))
						} else {
							must(t, os.WriteFile(f.AbsolutePath(), []byte("later drift"), 0o600))
						}
						break
					}
				}
				if name == "v3 tamper" {
					unit.Observation = value(installobserve.Observe(canonical, installobserve.DefaultOptions()))
				}
			}
			before := coordTree(t, canonical.RootPath())
			got, err := installcoord.PreflightActorGuidance(observations, unit, adopt, cwd)
			if err == nil || got.Report().Ready() || len(got.Report().Statuses()) != 0 || got.Proof().Matches(got.Proof().Candidate(), unit.Observation) {
				t.Fatalf("unsafe evidence accepted: ready=%v err=%v", got.Report().Ready(), err)
			}
			if !reflect.DeepEqual(before, coordTree(t, canonical.RootPath())) {
				t.Fatal("refusal changed target")
			}
		})
	}
}
