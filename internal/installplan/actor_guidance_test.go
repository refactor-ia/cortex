package installplan

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/refactor-ia/cortex/internal/installstate"
	"github.com/refactor-ia/cortex/internal/runtimematrix"
)

var actorGuidanceSuffix = []byte("\n\n<!-- gentle-ai:pi-codegraph-guidance -->\nuntrusted guidance\r\n\t  \n<!-- /gentle-ai:pi-codegraph -->\n")

func guidanceFixture(t *testing.T, change bool) (Plan, ActorGuidancePrior, []ActorGuidanceObservation) {
	t.Helper()
	skills, actors := actorAwareInputs(t, runtimematrix.RuntimePi)
	previous, err := BuildActorAware(skills, actors, actorAwareInstallationID)
	if err != nil {
		t.Fatal(err)
	}
	observed := make([]ActorGuidanceObservation, 0, 6)
	for _, file := range previous.Files() {
		if file.Role() == "actor" {
			observed = append(observed, ActorGuidanceObservation{LogicalID: file.LogicalID(), RootPath: previous.RootPath(), RelativePath: file.RelativePath(), AbsolutePath: file.AbsolutePath(), Mode: CanonicalFileMode, Content: append(bytes.TrimRight(file.Content(), "\n"), actorGuidanceSuffix...)})
		}
	}
	desired := previous
	if change {
		root := t.TempDir()
		if err := os.CopyFS(root, os.DirFS(filepath.Join("..", "..", "catalog"))); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(root, "families", "quality-assurance", "sources", "test-runner.md")
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, bytes.Replace(content, []byte("# "), []byte("# Updated "), 1), 0o600); err != nil {
			t.Fatal(err)
		}
		nextSkills, nextActors := actorAwareInputsAt(t, root, runtimematrix.RuntimePi)
		nextSkills.rootPath = skills.rootPath
		for i := range nextSkills.files {
			nextSkills.files[i].absolutePath = filepath.Join(skills.rootPath, filepath.FromSlash(nextSkills.files[i].relativePath))
		}
		desired, err = BuildActorAware(nextSkills, nextActors, actorAwareInstallationID)
		if err != nil {
			t.Fatal(err)
		}
		if desired.SnapshotFingerprint() == previous.SnapshotFingerprint() {
			t.Fatal("fixture must change desired render")
		}
	}
	return desired, ActorGuidancePrior{RootPath: previous.RootPath(), State: previous.InstalledState()}, observed
}

func TestBuildActorGuidancePreservesSuffixAndIdentity(t *testing.T) {
	for _, change := range []bool{false, true} {
		t.Run(map[bool]string{false: "same render", true: "new render"}[change], func(t *testing.T) {
			desired, prior, observed := guidanceFixture(t, change)
			plan, preimages, err := BuildActorGuidance(desired, prior, observed, true)
			if err != nil {
				t.Fatal(err)
			}
			if plan.InstalledState().SchemaVersion() != 3 || plan.RootPath() != desired.RootPath() || plan.SnapshotFingerprint() != desired.SnapshotFingerprint() || plan.InstalledState().InstallationID() != actorAwareInstallationID {
				t.Fatal("candidate metadata changed")
			}
			bundle, ok := plan.Bundle()
			original, _ := desired.Bundle()
			if !ok || !reflect.DeepEqual(bundle, original) {
				t.Fatal("lost trusted skill bundle")
			}
			artifacts := make(map[string]installstate.Artifact)
			for _, a := range plan.InstalledState().Artifacts() {
				artifacts[a.LogicalID()] = a
			}
			for i, file := range plan.Files() {
				canonical := desired.Files()[i]
				if file.Role() == "state" {
					if !bytes.Equal(file.Content(), mustEncode(t, plan.InstalledState())) || file.SHA256() != digest(plan.StateJSON()) {
						t.Fatal("state bytes/hash mismatch")
					}
					continue
				}
				a := artifacts[file.LogicalID()]
				if file.SHA256() != digest(file.Content()) || a.SHA256() != file.SHA256() || file.RelativePath() != canonical.RelativePath() || file.AbsolutePath() != canonical.AbsolutePath() || file.DesiredMode() != canonical.DesiredMode() {
					t.Fatal("file identity mismatch")
				}
				if file.Role() == "skill" {
					if !reflect.DeepEqual(file, canonical) || a.CanonicalSHA256() != "" {
						t.Fatal("skill changed")
					}
				} else {
					want := append(bytes.TrimRight(canonical.Content(), "\n"), actorGuidanceSuffix...)
					if !bytes.Equal(file.Content(), want) || a.CanonicalSHA256() != digest(canonical.Content()) {
						t.Fatal("suffix or canonical digest changed")
					}
					for _, o := range observed {
						if o.LogicalID == file.LogicalID() && preimages[o.LogicalID] != digest(o.Content) {
							t.Fatal("wrong expected preimage")
						}
					}
				}
			}
			if len(preimages) != 6 {
				t.Fatal("incomplete preimages")
			}
			// Registered v3 needs no adoption, but still proves its effective bytes.
			registered := ActorGuidancePrior{RootPath: plan.RootPath(), State: plan.InstalledState()}
			registeredObserved := append([]ActorGuidanceObservation(nil), observed...)
			for i := range registeredObserved {
				for _, f := range plan.Files() {
					if f.LogicalID() == registeredObserved[i].LogicalID {
						registeredObserved[i].Content = f.Content()
					}
				}
			}
			if _, _, err := BuildActorGuidance(desired, registered, registeredObserved, false); err != nil {
				t.Fatal(err)
			}
			observed[0].Content[0] ^= 1
			if f := plan.files[len(plan.files)-7]; digest(f.content) != f.sha256 {
				t.Fatal("actor aliases observation")
			}
			desired.files[0].content[0] ^= 1
			files := plan.Files()
			files[0].content[0] ^= 1
			if !bytes.Equal(plan.Files()[0].Content(), original.Artifacts()[0].Content()) {
				t.Fatal("input/output alias")
			}
		})
	}
}

func TestBuildActorGuidanceRejectsUnprovedInputs(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Plan, *ActorGuidancePrior, *[]ActorGuidanceObservation, *bool)
	}{
		{"no opt-in", func(_ *Plan, _ *ActorGuidancePrior, _ *[]ActorGuidanceObservation, adopt *bool) { *adopt = false }},
		{"prior root", func(_ *Plan, p *ActorGuidancePrior, _ *[]ActorGuidanceObservation, _ *bool) { p.RootPath += "-other" }},
		{"observation root", func(_ *Plan, _ *ActorGuidancePrior, o *[]ActorGuidanceObservation, _ *bool) {
			(*o)[0].RootPath += "-other"
		}},
		{"relative path", func(_ *Plan, _ *ActorGuidancePrior, o *[]ActorGuidanceObservation, _ *bool) {
			(*o)[0].RelativePath += "-other"
		}},
		{"absolute path", func(_ *Plan, _ *ActorGuidancePrior, o *[]ActorGuidanceObservation, _ *bool) {
			(*o)[0].AbsolutePath += "-other"
		}},
		{"mode", func(_ *Plan, _ *ActorGuidancePrior, o *[]ActorGuidanceObservation, _ *bool) {
			(*o)[0].Mode = 0o644
		}},
		{"missing", func(_ *Plan, _ *ActorGuidancePrior, o *[]ActorGuidanceObservation, _ *bool) { *o = (*o)[1:] }},
		{"extra", func(_ *Plan, _ *ActorGuidancePrior, o *[]ActorGuidanceObservation, _ *bool) { *o = append(*o, (*o)[0]) }},
		{"duplicate", func(_ *Plan, _ *ActorGuidancePrior, o *[]ActorGuidanceObservation, _ *bool) { (*o)[1] = (*o)[0] }},
		{"unknown logical ID", func(_ *Plan, _ *ActorGuidancePrior, o *[]ActorGuidanceObservation, _ *bool) {
			(*o)[0].LogicalID = "actors/unknown"
		}},
		{"unowned actors", func(_ *Plan, p *ActorGuidancePrior, _ *[]ActorGuidanceObservation, _ *bool) {
			p.State = installstate.Manifest{}
		}},
		{"prior installation", func(_ *Plan, p *ActorGuidancePrior, _ *[]ActorGuidanceObservation, _ *bool) {
			data := bytes.ReplaceAll(mustEncode(t, p.State), []byte(actorAwareInstallationID), []byte("111102030405060708090a0b0c0d0e0f"))
			var err error
			p.State, err = installstate.Decode(data)
			if err != nil {
				t.Fatal(err)
			}
		}},
		{"candidate skill tamper", func(p *Plan, _ *ActorGuidancePrior, _ *[]ActorGuidanceObservation, _ *bool) {
			p.files[0].content[0] ^= 1
		}},
		{"candidate state tamper", func(p *Plan, _ *ActorGuidancePrior, _ *[]ActorGuidanceObservation, _ *bool) { p.stateJSON[0] ^= 1 }},
		{"canonical drift", func(_ *Plan, _ *ActorGuidancePrior, o *[]ActorGuidanceObservation, _ *bool) { (*o)[0].Content[0] ^= 1 }},
		{"v3 effective tamper", func(p *Plan, prior *ActorGuidancePrior, o *[]ActorGuidanceObservation, _ *bool) {
			registered, _, err := BuildActorGuidance(*p, *prior, *o, true)
			if err != nil {
				t.Fatal(err)
			}
			prior.State = registered.InstalledState()
			(*o)[0].Content = bytes.Replace((*o)[0].Content, []byte("untrusted"), []byte("changed"), 1)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, prior, observations := guidanceFixture(t, false)
			adopt := true
			tc.mutate(&p, &prior, &observations, &adopt)
			if got, hashes, err := BuildActorGuidance(p, prior, observations, adopt); err == nil || len(got.Files()) != 0 || hashes != nil {
				t.Fatal("unproved candidate accepted")
			}
		})
	}
}

func TestBuildActorGuidanceUnmarkedOwnedActors(t *testing.T) {
	p, prior, observations := guidanceFixture(t, false)
	for i := range observations {
		for _, f := range p.Files() {
			if f.LogicalID() == observations[i].LogicalID {
				observations[i].Content = f.Content()
			}
		}
	}
	got, _, err := BuildActorGuidance(p, prior, observations, false)
	if err != nil {
		t.Fatal(err)
	}
	for i, f := range got.Files() {
		if f.Role() != "state" && !reflect.DeepEqual(f, p.Files()[i]) {
			t.Fatal("unmarked owned bytes changed")
		}
	}
}
