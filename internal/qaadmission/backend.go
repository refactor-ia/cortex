package qaadmission

import (
	"regexp"

	"github.com/refactor-ia/cortex/internal/qaroute"
)

// BackendIdentityContract versions the backend-identity block. It is the
// receipt's own contract for that block and moves independently of the
// per-adapter contracts below.
const BackendIdentityContract = "cortex.qa.backend-identity.v1"

// The two model relationships a QA backend can stand in to the work it is
// reviewing. They describe the backend's contract, not an observation of one
// run, and they are what lets a consumer tell a cross-model verdict from a
// same-model one without inferring it from the backend's name.
//
// ModelRelationshipRouted: the backend executes the provider and model the
// route resolved, so the model that answered is the one policy chose. On such
// a backend a role can be routed to a model that did not produce the work
// under review, and internal/qaroute exists to express exactly that.
//
// ModelRelationshipRuntimeFixed: the backend runs its own model whatever the
// route says. `claude -p` runs Claude, so a role invoked there is Claude
// reviewing Claude's output, and no implementation choice changes which model
// answers. The resolved route still describes what the role asked for; it does
// not describe what ran.
//
// What this field claims: which of those two contracts the executing backend
// has. That is a property of the adapter, known before the run, and it is
// recorded rather than derived so a consumer never has to keep its own table
// of backend names.
//
// What it does not claim, and must not be read as claiming: that a routed
// verdict is independent, that a runtime-fixed verdict is biased, or anything
// at all about the model that produced the work under review — the receipt
// does not observe that and cannot. It records a relationship; it does not
// measure independence. Deciding what a same-model verdict is worth is the
// consumer's judgment, and this field exists so that judgment is made on a
// recorded fact instead of on a guess.
const (
	ModelRelationshipRouted       = "routed"
	ModelRelationshipRuntimeFixed = "runtime-fixed"
)

// BackendIdentity records which runtime executed the role and how the model
// that answered relates to the model the route asked for.
type BackendIdentity struct {
	Contract          string
	Backend           string
	ModelRelationship string
}

// Contracts is the per-adapter half of the receipt contract. Every field was a
// Pi-named literal before three adapters existed; keeping them per-backend is
// what stops one adapter's receipt from reading as though another adapter's
// contract had been satisfied.
type Contracts struct {
	Adapter, Binary, Input, Probe, Skill, Result, Request string
}

// modelRelationships is the closed table of backend contracts. A backend the
// route policy admits but that is absent here has no receipt contract, which
// fails closed rather than defaulting to the more flattering relationship.
var modelRelationships = map[string]string{
	"pi":       ModelRelationshipRouted,
	"claude":   ModelRelationshipRuntimeFixed,
	"opencode": ModelRelationshipRouted,
}

// ContractsFor derives the per-adapter contracts of one admitted backend. The
// names are derived rather than tabulated so a fourth adapter cannot land with
// half its contracts silently pointing at Pi's.
func ContractsFor(backend string) (Contracts, bool) {
	if !KnownBackend(backend) {
		return Contracts{}, false
	}
	return Contracts{
		Adapter: "cortex.qa." + backend + "-admission.v1",
		Binary:  "cortex.qa." + backend + "-binary.v1",
		Input:   "cortex.qa." + backend + "-input.v1",
		Probe:   "cortex.qa." + backend + "-probe/v1",
		Skill:   "cortex.qa." + backend + "-skill.v1",
		Result:  "cortex.qa." + backend + "-result.v1",
		Request: "cortex.qa." + backend + "-admission-request.v1",
	}, true
}

// NewBackendIdentity builds the receipt's backend-identity block for one
// admitted backend.
func NewBackendIdentity(backend string) (BackendIdentity, bool) {
	relationship, known := modelRelationships[backend]
	if !KnownBackend(backend) {
		return BackendIdentity{}, false
	}
	if !known {
		return BackendIdentity{}, false
	}
	return BackendIdentity{Contract: BackendIdentityContract, Backend: backend, ModelRelationship: relationship}, true
}

// KnownBackend reports whether one backend is admitted by route policy and has
// a receipt contract. Both halves must hold: route policy owns what may run,
// and this package owns what may be recorded.
func KnownBackend(backend string) bool {
	_, recorded := modelRelationships[backend]
	return qaroute.Admits(backend) && recorded
}

// runtimeVersion is the shape every backend's recorded runtime version must
// have. No backend pins one exact build: the stream a backend produces is
// validated structurally at parse time, so pinning a version rejects working
// installations without gaining any evidence, and three adapters carrying two
// different runtime-trust contracts while every receipt read as equally strong
// was the defect. The version is required to be well formed, and recorded.
var runtimeVersion = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
