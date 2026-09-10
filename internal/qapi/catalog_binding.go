package qapi

import (
	"errors"

	"github.com/refactor-ia/cortex/internal/catalog"
	"github.com/refactor-ia/cortex/internal/installobserve"
	"github.com/refactor-ia/cortex/internal/qaactor"
	"github.com/refactor-ia/cortex/internal/qarole"
	"github.com/refactor-ia/cortex/internal/runtimematrix"
	"github.com/refactor-ia/cortex/internal/skillprojection"
	"github.com/refactor-ia/cortex/internal/skillrender"
)

var errCatalogAdmissionBinding = errors.New("qapi: catalog admission binding is unavailable")

// CatalogAdmissionBinding derives the exact Pi actor and skill hashes for one role
// from an already admitted catalog snapshot. It does not inspect ambient state.
func CatalogAdmissionBinding(snapshot catalog.CatalogSnapshot, role qarole.RoleID, backend string) (installobserve.AdmissionBinding, error) {
	if backend != "pi" || snapshot.Fingerprint() == "" {
		return installobserve.AdmissionBinding{}, errCatalogAdmissionBinding
	}
	if _, err := qarole.ValidateSquad([]qarole.RoleID{role}); err != nil {
		return installobserve.AdmissionBinding{}, errCatalogAdmissionBinding
	}

	actorSources, err := qaactor.Sources(snapshot)
	if err != nil {
		return installobserve.AdmissionBinding{}, errCatalogAdmissionBinding
	}
	renderedActors, err := qaactor.Render(actorSources)
	if err != nil {
		return installobserve.AdmissionBinding{}, errCatalogAdmissionBinding
	}
	actorProjection, err := qaactor.ProjectPi(renderedActors)
	if err != nil {
		return installobserve.AdmissionBinding{}, errCatalogAdmissionBinding
	}
	actorBinding, err := qaactor.Bind(actorProjection)
	if err != nil || actorBinding.CatalogFingerprint() != snapshot.Fingerprint() {
		return installobserve.AdmissionBinding{}, errCatalogAdmissionBinding
	}

	skillSources, err := skillrender.Render(snapshot)
	if err != nil || skillSources.SnapshotFingerprint() != snapshot.Fingerprint() {
		return installobserve.AdmissionBinding{}, errCatalogAdmissionBinding
	}
	piSkills, err := skillprojection.Build(runtimematrix.RuntimePi, skillSources)
	if err != nil || piSkills.Assessment().SnapshotFingerprint() != snapshot.Fingerprint() {
		return installobserve.AdmissionBinding{}, errCatalogAdmissionBinding
	}

	actorSHA256, ok := selectedActorSHA256(actorBinding, role)
	if !ok {
		return installobserve.AdmissionBinding{}, errCatalogAdmissionBinding
	}
	skillSHA256, ok := selectedPiSkillSHA256(piSkills, role)
	if !ok {
		return installobserve.AdmissionBinding{}, errCatalogAdmissionBinding
	}
	return installobserve.AdmissionBinding{
		Role:               role,
		Backend:            backend,
		CatalogFingerprint: snapshot.Fingerprint(),
		ActorSHA256:        actorSHA256,
		SkillSHA256:        skillSHA256,
	}, nil
}

func selectedActorSHA256(binding qaactor.Binding, role qarole.RoleID) (string, bool) {
	selected := ""
	for _, actor := range binding.Actors() {
		if actor.RoleID() != role {
			continue
		}
		if selected != "" || actor.GeneratedSHA256() == "" {
			return "", false
		}
		selected = actor.GeneratedSHA256()
	}
	return selected, selected != ""
}

func selectedPiSkillSHA256(plan skillprojection.Plan, role qarole.RoleID) (string, bool) {
	selected := ""
	logicalID := "skills/" + string(role)
	for _, skill := range plan.Skills() {
		if skill.CapabilityID() != string(role) || skill.LogicalID() != logicalID {
			continue
		}
		if selected != "" || skill.SHA256() == "" {
			return "", false
		}
		selected = skill.SHA256()
	}
	return selected, selected != ""
}
