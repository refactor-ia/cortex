package installobserve_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/refactor-ia/cortex/internal/installobserve"
	"github.com/refactor-ia/cortex/internal/installplan"
	"github.com/refactor-ia/cortex/internal/ownership"
)

func guidanceShadowFixture(t *testing.T, scenario string) (installplan.Plan, installobserve.FilesystemObservation, installobserve.ActorGuidanceClassification) {
	t.Helper()
	canonical := makeActorAwareCandidate(t)
	writeCandidateFiles(t, canonical)
	for _, f := range canonical.Files() {
		if f.Role() == "actor" {
			if err := os.WriteFile(f.AbsolutePath(), append(bytes.TrimRight(f.Content(), "\n"), bridgeSuffix...), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if scenario == "legacy update" {
		actor := actorFile(t, canonical)
		oldBody := append(bytes.TrimRight(actor.Content(), "\n"), []byte("\nHistorical canonical prose.")...)
		priorBytes := map[string][]byte{actor.LogicalID(): append(bytes.Clone(oldBody), '\n')}
		observed := make(map[string][]byte)
		for _, f := range canonical.Files() {
			if f.Role() == "actor" {
				observed[f.LogicalID()] = append(bytes.TrimRight(f.Content(), "\n"), bridgeSuffix...)
			}
		}
		observed[actor.LogicalID()] = append(oldBody, bridgeSuffix...)
		writePriorV2(t, canonical, priorV2(t, canonical, "", priorBytes), priorBytes, observed, nil)
	}
	capture := func(adopt bool) (installobserve.FilesystemObservation, installobserve.ActorGuidanceClassification) {
		o, err := installobserve.Observe(canonical, installobserve.DefaultOptions())
		if err != nil {
			t.Fatal(err)
		}
		p, err := installobserve.ClassifyActorGuidance(canonical, o, adopt)
		if err != nil {
			t.Fatal(err)
		}
		return o, p
	}
	o, p := capture(true)
	if scenario == "v3 repeat" {
		writeCandidateFiles(t, p.Candidate())
		o, p = capture(false)
	}
	return canonical, o, p
}

func TestObserveActorGuidanceShadows(t *testing.T) {
	for _, name := range []string{"legacy explicit adoption", "legacy update", "v3 repeat"} {
		t.Run(name, func(t *testing.T) {
			canonical, o, proof := guidanceShadowFixture(t, name)
			if name == "legacy update" {
				replaces := 0
				for _, d := range proof.Result().ArtifactDecisions() {
					if d.Action == ownership.Replace && d.LogicalID == actorFile(t, canonical).LogicalID() {
						replaces++
					}
				}
				if replaces != 1 {
					t.Fatal("fixture did not exercise actor replacement")
				}
			}
			cwd := physicalTempDir(t)
			writeShadow(t, cwd, ".pi/agents", "unrelated.md", []byte("---\nname: unrelated\n---\n"), 0o600)
			got, err := installobserve.ObserveActorGuidanceShadows(canonical, o, proof, cwd)
			if err != nil || !got.Clean() || len(got.Conflicts()) != 0 {
				t.Fatalf("proof-backed scan: clean=%v, err=%v", got.Clean(), err)
			}
			ordinary, err := installobserve.ObserveActorShadows(canonical, o, cwd)
			if err == nil && ordinary.Clean() {
				t.Fatal("ordinary scan accepted composed actors")
			}
		})
	}
}

func TestObserveActorGuidanceShadowsRejects(t *testing.T) {
	for _, name := range []string{
		"zero canonical", "composed canonical", "wrong canonical", "zero observation", "wrong observation", "zero proof", "wrong proof", "changed observation", "skill conflict",
		"duplicate global subagent", "duplicate local agent", "duplicate local subagent", "global alias", "local alias", "escaped alias", "duplicate name alias",
		"mode", "changed bytes", "last actor changed", "symlink target", "symlink cwd", "symlink local root", "relative cwd",
	} {
		t.Run(name, func(t *testing.T) {
			canonical, o, proof := guidanceShadowFixture(t, "legacy explicit adoption")
			cwd := physicalTempDir(t)
			actor := actorFile(t, canonical)
			mutate := func(path string, data []byte) {
				t.Helper()
				if err := os.WriteFile(path, data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			link := func(target, path string) {
				t.Helper()
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			}
			conflict := false
			switch name {
			case "zero canonical":
				canonical = installplan.Plan{}
			case "composed canonical":
				canonical = proof.Candidate()
			case "wrong canonical":
				canonical = makeActorAwareCandidate(t)
			case "zero observation":
				o = installobserve.FilesystemObservation{}
			case "wrong observation", "wrong proof":
				_, otherO, otherProof := guidanceShadowFixture(t, "legacy explicit adoption")
				if name == "wrong observation" {
					o = otherO
				} else {
					proof = otherProof
				}
			case "zero proof":
				proof = installobserve.ActorGuidanceClassification{}
			case "changed observation", "skill conflict":
				for _, f := range canonical.Files() {
					if f.Role() == "skill" {
						mutate(f.AbsolutePath(), []byte("user skill drift"))
						break
					}
				}
				var err error
				o, err = installobserve.Observe(canonical, installobserve.DefaultOptions())
				if err != nil {
					t.Fatal(err)
				}
				if name == "skill conflict" {
					proof, err = installobserve.ClassifyActorGuidance(canonical, o, true)
					if err != nil || !proof.Matches(proof.Candidate(), o) {
						t.Fatalf("conflicting skill fixture: %v", err)
					}
				}
			case "duplicate global subagent", "duplicate local agent", "duplicate local subagent":
				root, dir := canonical.RootPath(), "subagents"
				if name == "duplicate local agent" {
					root, dir = cwd, ".pi/agents"
				} else if name == "duplicate local subagent" {
					root, dir = cwd, ".pi/subagents"
				}
				writeShadow(t, root, dir, filepath.Base(actor.AbsolutePath()), actor.Content(), 0o600)
				conflict = true
			case "global alias", "local alias", "escaped alias", "duplicate name alias":
				root, dir := canonical.RootPath(), "agents"
				body := "---\nname: 'cortex-test-designer'\n---\n"
				if name == "local alias" {
					root, dir = cwd, ".pi/subagents"
				} else if name == "escaped alias" {
					body = "---\nname: \"cortex-test-\\u0064esigner\"\n---\n"
				} else if name == "duplicate name alias" {
					body = "---\nname: unrelated\nname: cortex-test-designer # alias\n---\n"
				}
				writeShadow(t, root, dir, "alias.md", []byte(body), 0o600)
				conflict = true
			case "mode":
				if err := os.Chmod(actor.AbsolutePath(), 0o640); err != nil {
					t.Fatal(err)
				}
			case "changed bytes", "last actor changed":
				if name == "last actor changed" {
					for _, f := range canonical.Files() {
						if f.Role() == "actor" {
							actor = f
						}
					}
				}
				mutate(actor.AbsolutePath(), []byte("later drift"))
			case "symlink target":
				saved := actor.AbsolutePath() + ".saved"
				if err := os.Rename(actor.AbsolutePath(), saved); err != nil {
					t.Fatal(err)
				}
				link(saved, actor.AbsolutePath())
			case "symlink cwd":
				path := filepath.Join(physicalTempDir(t), "cwd")
				link(cwd, path)
				cwd = path
			case "symlink local root":
				link(physicalTempDir(t), filepath.Join(cwd, ".pi"))
			case "relative cwd":
				cwd = "relative"
			}
			got, err := installobserve.ObserveActorGuidanceShadows(canonical, o, proof, cwd)
			if err == nil || got.Clean() {
				t.Fatalf("unsafe scan accepted: clean=%v err=%v", got.Clean(), err)
			}
			if conflict && len(got.Conflicts()) != 1 {
				t.Fatalf("lost bounded shadow conflict: %#v", got.Conflicts())
			}
		})
	}
}
