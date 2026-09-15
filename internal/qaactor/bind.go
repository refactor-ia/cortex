package qaactor

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"

	"github.com/refactor-ia/cortex/internal/qarole"
	"github.com/refactor-ia/cortex/internal/runtimematrix"
)

// ActorArtifactContract identifies the versioned Pi actor binding contract.
const ActorArtifactContract = "cortex.qa.pi-actor-artifact.v1"

// Binding is the immutable artifact identity for one canonical Pi projection.
type Binding struct {
	contract           string
	runtime            runtimematrix.RuntimeID
	catalogFingerprint string
	actors             []ProjectedActor
	bindingSHA256      string
}

// Contract returns the versioned artifact contract.
func (binding Binding) Contract() string {
	return binding.contract
}

// Runtime returns the only runtime supported by this binding.
func (binding Binding) Runtime() runtimematrix.RuntimeID {
	return binding.runtime
}

// CatalogFingerprint returns the common projected catalog fingerprint.
func (binding Binding) CatalogFingerprint() string {
	return binding.catalogFingerprint
}

// Actors returns deep copies in canonical QA role order.
func (binding Binding) Actors() []ProjectedActor {
	actors := make([]ProjectedActor, len(binding.actors))
	for index, actor := range binding.actors {
		actors[index] = cloneProjectedActor(actor)
	}
	return actors
}

// BindingSHA256 returns the lowercase framed binding digest.
func (binding Binding) BindingSHA256() string {
	return binding.bindingSHA256
}

// Bind validates and immutably binds one canonical Pi projection without I/O.
func Bind(projection Projection) (Binding, error) {
	actors := projection.Actors()
	if err := validateProjection(actors); err != nil {
		return Binding{}, fmt.Errorf("bind Pi actors: %w", err)
	}

	binding := Binding{
		contract:           ActorArtifactContract,
		runtime:            runtimematrix.RuntimePi,
		catalogFingerprint: actors[0].CatalogFingerprint(),
		actors:             actors,
	}
	binding.bindingSHA256 = bindingSHA256(binding)
	return binding, nil
}

func validateProjection(actors []ProjectedActor) error {
	contracts := qarole.Catalog()
	if len(actors) != len(contracts) {
		return errors.New("QA actor projection does not contain the closed catalog")
	}

	catalogFingerprint := ""
	for index, contract := range contracts {
		actor := actors[index]
		if actor.RoleID() != contract.ID ||
			actor.RoleContractVersion() != contract.ContractVersion ||
			actor.LogicalID() != "actors/"+string(contract.ID) ||
			actor.Name() != "cortex-"+string(contract.ID) ||
			actor.ActorContract() != ActorContractVersion ||
			!isLowerSHA256(actor.SourceSHA256()) ||
			!isLowerSHA256(actor.GeneratedSHA256()) ||
			len(actor.Content()) == 0 ||
			!isSHA256Of(actor.GeneratedSHA256(), actor.Content()) {
			return errors.New("QA actor projection is not canonical")
		}
		if index == 0 {
			catalogFingerprint = actor.CatalogFingerprint()
			if !isLowerSHA256(catalogFingerprint) {
				return errors.New("QA actor projection has an invalid catalog fingerprint")
			}
		} else if actor.CatalogFingerprint() != catalogFingerprint {
			return errors.New("QA actor projection has inconsistent catalog fingerprints")
		}
	}
	return nil
}

func bindingSHA256(binding Binding) string {
	hasher := sha256.New()
	writeBindingFrame(hasher, []byte(binding.Contract()))
	writeBindingFrame(hasher, []byte(binding.CatalogFingerprint()))
	writeBindingFrame(hasher, []byte(binding.actors[0].RoleContractVersion()))
	writeBindingFrame(hasher, []byte(ActorContractVersion))
	writeBindingUint64(hasher, uint64(len(binding.actors)))
	for _, actor := range binding.actors {
		writeBindingFrame(hasher, []byte(actor.RoleID()))
		writeBindingFrame(hasher, []byte(actor.SourceSHA256()))
		writeBindingFrame(hasher, []byte(actor.GeneratedSHA256()))
		writeBindingFrame(hasher, []byte("agents/"+actor.Name()+".md"))
	}
	return fmt.Sprintf("%x", hasher.Sum(nil))
}

func writeBindingUint64(hasher hash.Hash, value uint64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], value)
	writeBindingFrame(hasher, encoded[:])
}

func writeBindingFrame(hasher hash.Hash, value []byte) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = hasher.Write(length[:])
	_, _ = hasher.Write(value)
}
