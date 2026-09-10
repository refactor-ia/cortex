package qapi

import (
	"testing"

	"github.com/refactor-ia/cortex/internal/catalog"
	"github.com/refactor-ia/cortex/internal/installobserve"
	"github.com/refactor-ia/cortex/internal/qaactor"
	"github.com/refactor-ia/cortex/internal/qarole"
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
				SkillSHA256:        skill.SHA256(),
			}
			if got != want {
				t.Fatalf("CatalogAdmissionBinding() = %#v, want %#v", got, want)
			}
			if actor.GeneratedSHA256() == actor.SourceSHA256() {
				t.Fatal("actor binding used its source hash instead of generated actor bytes")
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
