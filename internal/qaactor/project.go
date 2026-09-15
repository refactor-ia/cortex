package qaactor

import (
	"fmt"

	"github.com/refactor-ia/cortex/internal/qarole"
)

// Projection is the immutable Pi-only projection of a validated actor set.
type Projection struct {
	actors []ProjectedActor
}

// Actors returns deep copies in canonical QA role order.
func (projection Projection) Actors() []ProjectedActor {
	actors := make([]ProjectedActor, len(projection.actors))
	for index, actor := range projection.actors {
		actors[index] = cloneProjectedActor(actor)
	}
	return actors
}

// ProjectedActor is one immutable Pi actor projection.
type ProjectedActor struct {
	roleID              qarole.RoleID
	roleContractVersion string
	logicalID           string
	name                string
	actorContract       string
	catalogFingerprint  string
	sourceSHA256        string
	generatedSHA256     string
	content             []byte
}

// RoleID returns the closed QA role identity.
func (actor ProjectedActor) RoleID() qarole.RoleID {
	return actor.roleID
}

// RoleContractVersion returns the exact source role contract version.
func (actor ProjectedActor) RoleContractVersion() string {
	return actor.roleContractVersion
}

// LogicalID returns the immutable actor identity in the installation catalog.
func (actor ProjectedActor) LogicalID() string {
	return actor.logicalID
}

// Name returns the exact Pi actor name.
func (actor ProjectedActor) Name() string {
	return actor.name
}

// ActorContract returns the exact Pi actor contract version.
func (actor ProjectedActor) ActorContract() string {
	return actor.actorContract
}

// CatalogFingerprint returns the admitted catalog identity.
func (actor ProjectedActor) CatalogFingerprint() string {
	return actor.catalogFingerprint
}

// SourceSHA256 returns the exact source content hash.
func (actor ProjectedActor) SourceSHA256() string {
	return actor.sourceSHA256
}

// GeneratedSHA256 returns the exact rendered actor hash.
func (actor ProjectedActor) GeneratedSHA256() string {
	return actor.generatedSHA256
}

// Content returns a defensive copy of the exact rendered actor bytes.
func (actor ProjectedActor) Content() []byte {
	return append([]byte(nil), actor.content...)
}

// ProjectPi validates and projects the exact six-role set for Pi only.
func ProjectPi(set Set) (Projection, error) {
	if err := Validate(set); err != nil {
		return Projection{}, fmt.Errorf("project Pi actors: %w", err)
	}

	rendered := set.Actors()
	actors := make([]ProjectedActor, len(rendered))
	for index, actor := range rendered {
		actors[index] = ProjectedActor{
			roleID:              actor.RoleID(),
			roleContractVersion: actor.RoleContractVersion(),
			logicalID:           "actors/" + string(actor.RoleID()),
			name:                "cortex-" + string(actor.RoleID()),
			actorContract:       set.ActorContract(),
			catalogFingerprint:  set.CatalogFingerprint(),
			sourceSHA256:        actor.SourceSHA256(),
			generatedSHA256:     actor.GeneratedSHA256(),
			content:             actor.Content(),
		}
	}

	return Projection{actors: actors}, nil
}

func cloneProjectedActor(actor ProjectedActor) ProjectedActor {
	actor.content = append([]byte(nil), actor.content...)
	return actor
}
