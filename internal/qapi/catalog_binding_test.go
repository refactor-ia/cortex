package qapi

import (
	"testing"

	"github.com/refactor-ia/cortex/internal/catalog"
	"github.com/refactor-ia/cortex/internal/installobserve"
	"github.com/refactor-ia/cortex/internal/qaactor"
	"github.com/refactor-ia/cortex/internal/qarole"
	"github.com/refactor-ia/cortex/internal/qaroute"
	"github.com/refactor-ia/cortex/internal/runtimematrix"
	"github.com/refactor-ia/cortex/internal/skillprojection"
	"github.com/refactor-ia/cortex/internal/skillrender"
)

func TestCatalogAdmissionBindingUsesDerivedPiAssets(t *testing.T) {
	snapshot := productionCatalogSnapshot(t)
	actors := productionActorBinding(t, snapshot)
	neutral, err := skillrender.Render(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	piSkills, err := skillprojection.Build(runtimematrix.RuntimePi, neutral)
	if err != nil {
		t.Fatal(err)
	}

	for _, role := range []qarole.RoleID{qarole.RequirementsAnalyst, qarole.TestRunner} {
		t.Run(string(role), func(t *testing.T) {
			got, err := CatalogAdmissionBinding(snapshot, role, "pi")
			if err != nil {
				t.Fatalf("CatalogAdmissionBinding() error = %v", err)
			}

			actor := selectedActor(t, actors, role)
			skill := selectedSkill(t, piSkills, role)
			want := installobserve.AdmissionBinding{
				Role:               role,
				Backend:            "pi",
				CatalogFingerprint: snapshot.Fingerprint(),
				ActorSHA256:        actor.GeneratedSHA256(),
				ActorSourceSHA256:  actor.SourceSHA256(),
				ActorBindingSHA256: actors.BindingSHA256(),
				SkillSHA256:        skill.SHA256(),
			}
			if got != want {
				t.Fatalf("CatalogAdmissionBinding() = %#v, want %#v", got, want)
			}
			if got.ActorSHA256 != actor.GeneratedSHA256() || got.ActorSHA256 == got.ActorSourceSHA256 {
				t.Fatal("actor binding did not preserve the generated installed actor hash")
			}

			neutralSkill := selectedRenderedSkill(t, neutral, role)
			if skill.SHA256() != neutralSkill.SHA256() && got.SkillSHA256 == neutralSkill.SHA256() {
				t.Fatal("binding used the neutral skill hash instead of the Pi projection hash")
			}
		})
	}
}

func TestCatalogAdmissionBindingRejectsInvalidInputs(t *testing.T) {
	snapshot := productionCatalogSnapshot(t)
	for _, tc := range []struct {
		name     string
		snapshot catalog.CatalogSnapshot
		role     qarole.RoleID
		backend  string
	}{
		{"zero snapshot", catalog.CatalogSnapshot{}, qarole.RequirementsAnalyst, "pi"},
		{"invalid role", snapshot, qarole.RoleID("other"), "pi"},
		{"non-pi backend", snapshot, qarole.RequirementsAnalyst, "other"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := CatalogAdmissionBinding(tc.snapshot, tc.role, tc.backend)
			if err == nil || got != (installobserve.AdmissionBinding{}) {
				t.Fatalf("CatalogAdmissionBinding() = (%#v, %v), want zero binding and error", got, err)
			}
		})
	}
}

func productionCatalogSnapshot(t *testing.T) catalog.CatalogSnapshot {
	t.Helper()
	snapshot, err := catalog.BuildCatalogSnapshot("../../catalog", "catalog.json", catalog.AdmissionPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func productionActorBinding(t *testing.T, snapshot catalog.CatalogSnapshot) qaactor.Binding {
	t.Helper()
	sources, err := qaactor.Sources(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := qaactor.Render(sources)
	if err != nil {
		t.Fatal(err)
	}
	projection, err := qaactor.ProjectPi(rendered)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := qaactor.Bind(projection)
	if err != nil {
		t.Fatal(err)
	}
	return binding
}

func selectedActor(t *testing.T, binding qaactor.Binding, role qarole.RoleID) qaactor.ProjectedActor {
	t.Helper()
	var selected []qaactor.ProjectedActor
	for _, actor := range binding.Actors() {
		if actor.RoleID() == role {
			selected = append(selected, actor)
		}
	}
	if len(selected) != 1 {
		t.Fatalf("actors for %q = %d, want 1", role, len(selected))
	}
	return selected[0]
}

func selectedSkill(t *testing.T, plan skillprojection.Plan, role qarole.RoleID) skillprojection.ProjectedSkill {
	t.Helper()
	var selected []skillprojection.ProjectedSkill
	for _, skill := range plan.Skills() {
		if skill.CapabilityID() == string(role) && skill.LogicalID() == "skills/"+string(role) {
			selected = append(selected, skill)
		}
	}
	if len(selected) != 1 {
		t.Fatalf("Pi skills for %q = %d, want 1", role, len(selected))
	}
	return selected[0]
}

func selectedRenderedSkill(t *testing.T, set skillrender.Set, role qarole.RoleID) skillrender.RenderedSkill {
	t.Helper()
	var selected []skillrender.RenderedSkill
	for _, skill := range set.Skills() {
		if skill.CapabilityID() == string(role) && skill.LogicalID() == "skills/"+string(role) {
			selected = append(selected, skill)
		}
	}
	if len(selected) != 1 {
		t.Fatalf("neutral skills for %q = %d, want 1", role, len(selected))
	}
	return selected[0]
}

// TestCatalogAdmissionBindingProjectsTheSelectedBackendRuntime pins that the
// skill hash a binding attests is the projection for the runtime that will
// actually load it, not Pi's projection handed to another runtime.
func TestCatalogAdmissionBindingProjectsTheSelectedBackendRuntime(t *testing.T) {
	snapshot := productionCatalogSnapshot(t)
	actors := productionActorBinding(t, snapshot)
	neutral, err := skillrender.Render(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		backend string
		runtime runtimematrix.RuntimeID
	}{
		{"pi", runtimematrix.RuntimePi},
		{"claude", runtimematrix.RuntimeClaudeCode},
		{"opencode", runtimematrix.RuntimeOpenCode},
	} {
		t.Run(tc.backend, func(t *testing.T) {
			plan, err := skillprojection.Build(tc.runtime, neutral)
			if err != nil {
				t.Fatal(err)
			}
			role := qarole.RequirementsAnalyst
			got, err := CatalogAdmissionBinding(snapshot, role, tc.backend)
			if err != nil {
				t.Fatalf("CatalogAdmissionBinding() error = %v", err)
			}
			actor := selectedActor(t, actors, role)
			want := installobserve.AdmissionBinding{
				Role:               role,
				Backend:            tc.backend,
				CatalogFingerprint: snapshot.Fingerprint(),
				ActorSHA256:        actor.GeneratedSHA256(),
				ActorSourceSHA256:  actor.SourceSHA256(),
				ActorBindingSHA256: actors.BindingSHA256(),
				SkillSHA256:        selectedSkill(t, plan, role).SHA256(),
			}
			if got != want {
				t.Fatalf("CatalogAdmissionBinding() = %#v, want %#v", got, want)
			}
		})
	}
}

// TestRuntimeForCoversEveryAdmittedBackend keeps the mapping and the route
// policy from drifting apart: a backend the policy admits but no runtime backs
// would reach the admission path with no skill projection to ask for.
func TestRuntimeForCoversEveryAdmittedBackend(t *testing.T) {
	for _, backend := range []string{"pi", "claude", "opencode"} {
		if _, known := qaroute.RuntimeFor(backend); !known {
			t.Fatalf("qaroute.RuntimeFor(%q) is unknown", backend)
		}
	}
	if _, known := qaroute.RuntimeFor("other"); known {
		t.Fatal("qaroute.RuntimeFor() resolved an unadmitted backend")
	}
}
