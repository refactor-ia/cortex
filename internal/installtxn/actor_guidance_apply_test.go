package installtxn

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/refactor-ia/cortex/internal/filetxn"
	"github.com/refactor-ia/cortex/internal/installobserve"
	"github.com/refactor-ia/cortex/internal/installplan"
	"github.com/refactor-ia/cortex/internal/ownership"
)

func TestActorGuidanceApplyUpdateAndRepeat(t *testing.T) {
	canonical, proof, observation, _ := guidanceAcceptanceFixture(t, "update")
	candidate, cwd, backups := proof.Candidate(), physicalTempDir(t), physicalTempDir(t)
	called := false
	result, err := applyActorGuidanceWith(canonical, observation, proof, cwd, backups, "apply", func(root, backup, name string, directories []filetxn.Directory, operations []filetxn.Operation, verify func() error, finalize func(filetxn.Snapshot) error) (filetxn.Snapshot, error) {
		called = true
		rank := 0
		for _, operation := range operations {
			if operation.Write != nil {
				t.Fatal("unguarded write")
			}
			relative := ""
			switch {
			case operation.Create != nil:
				relative = operation.Create.Path
			case operation.Replace != nil:
				relative = operation.Replace.Path
			case operation.Remove != nil:
				relative = operation.Remove.Path
			}
			next := 0
			if filepath.Dir(relative) == "agents" {
				next = 1
				if operation.Replace == nil {
					t.Fatal("actor is not an exact replacement")
				}
			} else if relative == stateRelativePath {
				next = 2
			}
			if next < rank {
				t.Fatal("operations not skills->actors->state")
			}
			rank = next
		}
		if rank != 2 {
			t.Fatal("state not last")
		}
		return filetxn.ApplyOperationsWithDirectoriesAndFinalize(root, backup, name, directories, operations, verify, finalize)
	})
	must(t, err)
	if !called || !result.TransactionID().Valid() || len(result.Actions()) != len(proof.Result().ArtifactDecisions())+1 {
		t.Fatal("missing acceptance evidence")
	}
	for _, file := range candidate.Files() {
		assertExactBytesAndMode(t, file.AbsolutePath(), file.Content(), file.DesiredMode())
		if file.Role() == "actor" && !bytes.HasSuffix(file.Content(), acceptanceSuffix) {
			t.Fatal("external suffix changed")
		}
	}
	if _, err := os.Lstat(filepath.Join(canonical.RootPath(), "skills/cortex-retired/SKILL.md")); !os.IsNotExist(err) {
		t.Fatalf("retired skill remains: %v", err)
	}
	fresh, err := installobserve.Observe(canonical, installobserve.DefaultOptions())
	must(t, err)
	repeat, err := installobserve.ClassifyActorGuidance(canonical, fresh, false)
	must(t, err)
	noop, err := applyActorGuidance(canonical, fresh, repeat, cwd, backups, "noop")
	must(t, err)
	if noop.TransactionID().Valid() {
		t.Fatal("no-op assigned transaction")
	}
	for _, action := range noop.Actions() {
		if action.Action != ownership.Unchanged {
			t.Fatalf("repeat action: %#v", action)
		}
	}
	if _, err := os.Lstat(filepath.Join(backups, "noop")); !os.IsNotExist(err) {
		t.Fatalf("no-op created backup: %v", err)
	}
}

func TestActorGuidanceApplyRefusesWithoutMutation(t *testing.T) {
	for _, scenario := range []string{"no optin", "zero proof", "wrong canonical", "state bytes", "valid stale state", "state mode", "skill bytes", "skill mode", "actor bytes", "actor mode", "actor symlink", "shadow", "skill drift", "skill mode conflict", "unrelated skill"} {
		t.Run(scenario, func(t *testing.T) {
			fixture := "update"
			if scenario == "skill drift" || scenario == "unrelated skill" {
				fixture = scenario
			} else if scenario == "skill mode conflict" {
				fixture = "skill mode"
			}
			canonical, proof, observation, _ := guidanceAcceptanceFixture(t, fixture)
			originalProof := proof
			cwd, backups := physicalTempDir(t), physicalTempDir(t)
			var actor installplan.File
			for _, file := range canonical.Files() {
				if file.Role() == "actor" {
					actor = file
					break
				}
			}
			target := canonical.Files()[0].AbsolutePath()
			switch scenario {
			case "no optin":
				var err error
				proof, err = installobserve.ClassifyActorGuidance(canonical, observation, false)
				if err == nil || proof.Matches(proof.Candidate(), observation) {
					t.Fatal("legacy accepted without optin")
				}
			case "zero proof":
				proof = installobserve.ActorGuidanceClassification{}
			case "wrong canonical":
				canonical = actorAwareCandidate(t)
			case "valid stale state":
				must(t, os.WriteFile(filepath.Join(canonical.RootPath(), stateRelativePath), canonical.StateJSON(), 0o600))
			case "state bytes", "state mode":
				target = filepath.Join(canonical.RootPath(), stateRelativePath)
			case "actor bytes", "actor mode":
				target = actor.AbsolutePath()
			case "actor symlink":
				link := filepath.Join(canonical.RootPath(), "agents", "linked.md")
				must(t, os.Rename(actor.AbsolutePath(), link))
				must(t, os.Symlink(link, actor.AbsolutePath()))
			case "shadow":
				mustMkdir(t, filepath.Join(cwd, ".pi", "subagents"))
				must(t, os.WriteFile(filepath.Join(cwd, ".pi", "subagents", "shadow.md"), actor.Content(), 0o600))
			}
			if scenario == "state bytes" || scenario == "skill bytes" || scenario == "actor bytes" {
				must(t, os.WriteFile(target, []byte("stale"), 0o600))
			} else if scenario == "state mode" || scenario == "skill mode" || scenario == "actor mode" {
				must(t, os.Chmod(target, 0o640))
			}
			before := guidanceApplyFiles(t, originalProof, observation)
			called := false
			_, err := applyActorGuidanceWith(canonical, observation, proof, cwd, backups, "refused", func(string, string, string, []filetxn.Directory, []filetxn.Operation, func() error, func(filetxn.Snapshot) error) (filetxn.Snapshot, error) {
				called = true
				return filetxn.Snapshot{}, errors.New("unexpected mutation")
			})
			if err == nil || called {
				t.Fatalf("unsafe transaction admitted: %v", err)
			}
			guidanceApplyAssertFiles(t, before)
			entries, err := os.ReadDir(backups)
			must(t, err)
			if len(entries) != 0 {
				t.Fatal("refusal created backup")
			}
		})
	}
}

func TestActorGuidanceApplyRollback(t *testing.T) {
	for _, scenario := range []string{"finalizer", "write", "readback"} {
		t.Run(scenario, func(t *testing.T) {
			canonical, proof, observation, _ := guidanceAcceptanceFixture(t, "update")
			before := guidanceApplyFiles(t, proof, observation)
			injected := errors.New("injected " + scenario)
			finalized := false
			_, err := applyActorGuidanceWith(canonical, observation, proof, physicalTempDir(t), physicalTempDir(t), "failure", func(root, backup, name string, directories []filetxn.Directory, operations []filetxn.Operation, verify func() error, finalize func(filetxn.Snapshot) error) (filetxn.Snapshot, error) {
				if scenario == "write" {
					if os.Geteuid() == 0 {
						t.Skip("permission failure needs non-root user")
					}
					agents := filepath.Join(root, "agents")
					must(t, os.Chmod(agents, 0o500))
					defer func() { must(t, os.Chmod(agents, 0o700)) }()
				}
				if scenario == "readback" {
					file := proof.Candidate().Files()[0]
					// Simulate an omitted write, leaving its exact original intact.
					for i, operation := range operations {
						if operation.Replace != nil && operation.Replace.Path == file.RelativePath() {
							operations = append(operations[:i], operations[i+1:]...)
							break
						}
					}
				}
				return filetxn.ApplyOperationsWithDirectoriesAndFinalize(root, backup, name, directories, operations, verify, func(snapshot filetxn.Snapshot) error {
					finalized = true
					if err := finalize(snapshot); err != nil {
						return err
					}
					return injected
				})
			})
			if !errors.Is(err, ErrFailed) || scenario == "finalizer" && !errors.Is(err, injected) {
				t.Fatalf("failure lost: %v", err)
			}
			if scenario == "readback" && finalized {
				t.Fatal("bad readback reached acceptance")
			}
			if scenario == "write" && !strings.Contains(err.Error(), "apply replace agents/") {
				t.Fatalf("failure was not an actor write: %v", err)
			}
			guidanceApplyAssertFiles(t, before)
		})
	}
}

type guidanceApplyPreimage struct {
	path string
	data []byte
	mode fs.FileMode
}

func guidanceApplyFiles(t *testing.T, proof installobserve.ActorGuidanceClassification, observation installobserve.FilesystemObservation) []guidanceApplyPreimage {
	t.Helper()
	files := proof.Candidate().Files()
	out := []guidanceApplyPreimage{}
	for _, file := range files {
		data, err := os.ReadFile(file.AbsolutePath())
		if os.IsNotExist(err) {
			out = append(out, guidanceApplyPreimage{path: file.AbsolutePath()})
			continue
		}
		must(t, err)
		out = append(out, guidanceApplyPreimage{file.AbsolutePath(), data, mode(t, file.AbsolutePath())})
	}
	if len(files) != 0 && observation.PriorState() != nil {
		retired := filepath.Join(proof.Candidate().RootPath(), "skills/cortex-retired/SKILL.md")
		data, err := os.ReadFile(retired)
		must(t, err)
		out = append(out, guidanceApplyPreimage{retired, data, mode(t, retired)})
	}
	return out
}

func guidanceApplyAssertFiles(t *testing.T, before []guidanceApplyPreimage) {
	t.Helper()
	for _, file := range before {
		if file.data == nil {
			if _, err := os.Lstat(file.path); !os.IsNotExist(err) {
				t.Fatalf("absent file changed: %s: %v", file.path, err)
			}
		} else {
			assertExactBytesAndMode(t, file.path, file.data, file.mode)
		}
	}
}
