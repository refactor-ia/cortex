package qaactor

import (
	"errors"
	"fmt"

	"github.com/refactor-ia/cortex/internal/qarole"
	"github.com/refactor-ia/cortex/internal/runtimematrix"
)

// DestinationKindPiActor identifies a Cortex-owned Pi actor asset.
const DestinationKindPiActor = "pi-actor"

// Plan is an immutable set of canonical Pi actor destinations.
type Plan struct {
	destinations []Destination
}

// Destinations returns deep copies in canonical QA role order.
func (plan Plan) Destinations() []Destination {
	destinations := make([]Destination, len(plan.destinations))
	for index, destination := range plan.destinations {
		destinations[index] = cloneDestination(destination)
	}
	return destinations
}

// Destination is one immutable, Cortex-owned Pi actor destination.
type Destination struct {
	logicalID     string
	kind          string
	roleID        qarole.RoleID
	relativePath  string
	actorContract string
	sha256        string
	content       []byte
}

// LogicalID returns the immutable actor identity in the installation catalog.
func (destination Destination) LogicalID() string {
	return destination.logicalID
}

// Kind returns the exact typed actor asset kind.
func (destination Destination) Kind() string {
	return destination.kind
}

// RoleID returns the closed QA role identity.
func (destination Destination) RoleID() qarole.RoleID {
	return destination.roleID
}

// RelativePath returns the canonical agents-only path.
func (destination Destination) RelativePath() string {
	return destination.relativePath
}

// ActorContract returns the exact Pi actor contract version.
func (destination Destination) ActorContract() string {
	return destination.actorContract
}

// SHA256 returns the lowercase SHA-256 hash of Content.
func (destination Destination) SHA256() string {
	return destination.sha256
}

// Content returns a defensive copy of the exact rendered actor bytes.
func (destination Destination) Content() []byte {
	return append([]byte(nil), destination.content...)
}

// PlanDestinations validates one bound Pi projection and derives its six
// canonical agents-only actor destinations without I/O.
func PlanDestinations(binding Binding) (Plan, error) {
	actors, err := validateDestinationBinding(binding)
	if err != nil {
		return Plan{}, fmt.Errorf("plan Pi actor destinations: %w", err)
	}

	destinations := make([]Destination, len(actors))
	for index, actor := range actors {
		roleID := actor.RoleID()
		destinations[index] = Destination{
			logicalID:     actor.LogicalID(),
			kind:          DestinationKindPiActor,
			roleID:        roleID,
			relativePath:  "agents/cortex-" + string(roleID) + ".md",
			actorContract: actor.ActorContract(),
			sha256:        actor.GeneratedSHA256(),
			content:       actor.Content(),
		}
	}
	return Plan{destinations: destinations}, nil
}

func validateDestinationBinding(binding Binding) ([]ProjectedActor, error) {
	if binding.contract != ActorArtifactContract || binding.runtime != runtimematrix.RuntimePi {
		return nil, errors.New("QA actor binding has an unsupported contract or runtime")
	}
	if len(binding.actors) != len(qarole.Catalog()) || !isLowerSHA256(binding.catalogFingerprint) {
		return nil, errors.New("QA actor binding does not contain a valid closed catalog")
	}
	if !isLowerSHA256(binding.bindingSHA256) || binding.bindingSHA256 != bindingSHA256(binding) {
		return nil, errors.New("QA actor binding has an invalid digest")
	}

	actors := binding.Actors()
	if err := validateProjection(actors); err != nil {
		return nil, fmt.Errorf("QA actor binding has an invalid projection: %w", err)
	}
	for _, actor := range actors {
		if actor.CatalogFingerprint() != binding.catalogFingerprint {
			return nil, errors.New("QA actor binding has inconsistent catalog fingerprints")
		}
	}
	return actors, nil
}

func cloneDestination(destination Destination) Destination {
	destination.content = append([]byte(nil), destination.content...)
	return destination
}
