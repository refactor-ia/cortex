package installcoord

import (
	"fmt"

	"github.com/refactor-ia/cortex/internal/adapterplan"
	"github.com/refactor-ia/cortex/internal/catalog"
	"github.com/refactor-ia/cortex/internal/installplan"
	"github.com/refactor-ia/cortex/internal/installstate"
	"github.com/refactor-ia/cortex/internal/projection"
	"github.com/refactor-ia/cortex/internal/qaactor"
	"github.com/refactor-ia/cortex/internal/runtimematrix"
	"github.com/refactor-ia/cortex/internal/skillartifact"
	"github.com/refactor-ia/cortex/internal/skilldest"
	"github.com/refactor-ia/cortex/internal/skillprojection"
	"github.com/refactor-ia/cortex/internal/skillrender"
	"github.com/refactor-ia/cortex/internal/skillroot"
)

// CandidateRequest carries every axis on which one caller's projection differs
// from another's. Each field is a decision the caller must state; none of them
// defaults silently. The fields exist because two hand-written copies of this
// pipeline had already drifted apart on admission without either copy reading
// as wrong on its own.
type CandidateRequest struct {
	// Snapshot is the already-loaded catalog to project.
	Snapshot catalog.CatalogSnapshot

	// Observations are the probed runtimes the transaction may target.
	Observations []runtimematrix.Observation

	// LocalTarget selects the single-runtime local update mode. The zero value
	// selects the cross-runtime install mode over every admitted runtime.
	LocalTarget runtimematrix.RuntimeID

	// Admit gates the snapshot before any projection work. A nil Admit means
	// this caller deliberately performs no admission check; it is never an
	// oversight the reader has to detect by comparing call sites.
	Admit func(catalog.CatalogSnapshot) error

	// ResolveRoot turns a symbolic destination plan into concrete paths.
	ResolveRoot func(skilldest.Plan) (skillroot.Plan, error)

	// Actors binds the Pi actor projection. A nil Actors produces skill-only
	// candidates for every runtime, including Pi.
	Actors func(catalog.CatalogSnapshot) (qaactor.Binding, error)

	// NewInstallationID mints the identity an actor-aware candidate carries. It
	// is required when Actors is set and unused otherwise.
	NewInstallationID func() (installstate.InstallationID, error)
}

// Candidate is one runtime's installable plan.
type Candidate struct {
	RuntimeID runtimematrix.RuntimeID
	Plan      installplan.Plan
}

// BuildCandidates projects one catalog snapshot into installable candidates.
//
// It is the single implementation of render -> adapter plan -> assessment ->
// projection -> binding -> destination -> root -> plan. Callers differ only
// through CandidateRequest, so a stage added here reaches all of them by
// construction rather than by remembering to copy it.
//
// The returned projection plan carries the per-runtime results the caller
// reports, including runtimes excluded from the transaction.
func BuildCandidates(request CandidateRequest) ([]Candidate, projection.Plan, error) {
	if request.ResolveRoot == nil {
		return nil, projection.Plan{}, ErrInvalid
	}
	if request.Actors != nil && request.NewInstallationID == nil {
		return nil, projection.Plan{}, ErrInvalid
	}
	if request.Admit != nil {
		if err := request.Admit(request.Snapshot); err != nil {
			return nil, projection.Plan{}, err
		}
	}
	sources, err := skillrender.Render(request.Snapshot)
	if err != nil {
		return nil, projection.Plan{}, err
	}
	base, err := buildAdapterPlan(request)
	if err != nil {
		return nil, projection.Plan{}, err
	}
	projected := make(map[runtimematrix.RuntimeID]skillprojection.Plan, len(base.TransactionTargets))
	assessments := make([]projection.Assessment, 0, len(base.TransactionTargets))
	for _, id := range base.TransactionTargets {
		plan, err := skillprojection.Build(id, sources)
		if err != nil {
			return nil, projection.Plan{}, err
		}
		projected[id] = plan
		assessments = append(assessments, plan.Assessment())
	}
	final, err := projection.BuildPlan(base, assessments)
	if err != nil {
		return nil, projection.Plan{}, err
	}
	candidates := make([]Candidate, 0, len(final.TransactionTargets()))
	for _, id := range final.TransactionTargets() {
		candidate, err := buildOne(request, final, projected[id], id)
		if err != nil {
			return nil, projection.Plan{}, err
		}
		candidates = append(candidates, Candidate{RuntimeID: id, Plan: candidate})
	}
	return candidates, final, nil
}

// buildAdapterPlan selects the cross-runtime or single-runtime adapter mode.
func buildAdapterPlan(request CandidateRequest) (adapterplan.Plan, error) {
	if request.LocalTarget == "" {
		return adapterplan.Build(request.Snapshot.Fingerprint(), request.Observations)
	}
	return adapterplan.BuildLocalUpdate(request.Snapshot.Fingerprint(), request.Observations, request.LocalTarget)
}

// buildOne derives one runtime's candidate from an already-validated plan.
func buildOne(request CandidateRequest, final projection.Plan, projected skillprojection.Plan, id runtimematrix.RuntimeID) (installplan.Plan, error) {
	binding, err := skillartifact.Build(projected, final)
	if err != nil {
		return installplan.Plan{}, err
	}
	bundle, bound := binding.Bundle()
	if !bound {
		return installplan.Plan{}, fmt.Errorf("catalog projection has no artifacts for %s", id)
	}
	symbolic, err := skilldest.Build(binding)
	if err != nil {
		return installplan.Plan{}, err
	}
	resolved, err := request.ResolveRoot(symbolic)
	if err != nil {
		return installplan.Plan{}, err
	}
	skills, err := installplan.BuildWithBundle(resolved, bundle)
	if err != nil {
		return installplan.Plan{}, err
	}
	// Only Pi carries actors. Every other runtime keeps the skill-only plan
	// rather than acquiring an empty actor set it would have to describe.
	if request.Actors == nil || id != runtimematrix.RuntimePi {
		return skills, nil
	}
	actors, err := request.Actors(request.Snapshot)
	if err != nil {
		return installplan.Plan{}, err
	}
	installationID, err := request.NewInstallationID()
	if err != nil {
		return installplan.Plan{}, err
	}
	return installplan.BuildActorAware(skills, actors, installationID)
}
