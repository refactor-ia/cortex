package qaactor

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/refactor-ia/cortex/internal/qarole"
)

// Validate proves that a rendered actor set is a closed, canonical rendering of
// internally consistent QA sources. It does not establish that replaced source
// bodies originated in the catalog when their source hashes and markers agree.
func Validate(set Set) error {
	if set.actorContract != ActorContractVersion {
		return errors.New("QA actor set has an unsupported actor contract")
	}
	if !isLowerSHA256(set.catalogFingerprint) {
		return errors.New("QA actor set has an invalid catalog fingerprint")
	}

	contracts := qarole.Catalog()
	if len(set.sources) != len(contracts) || len(set.actors) != len(contracts) {
		return errors.New("QA actor set does not contain the closed QA catalog")
	}

	for index, contract := range contracts {
		source := set.sources[index]
		actor := set.actors[index]
		if err := validateSource(source, contract); err != nil {
			return err
		}
		if err := validateActor(actor, source, contract); err != nil {
			return err
		}
	}

	canonical, err := Render(SourceSet{
		catalogFingerprint: set.catalogFingerprint,
		sources:            set.Sources(),
	})
	if err != nil {
		return fmt.Errorf("render canonical QA actors: %w", err)
	}
	if !sameActors(set.actors, canonical.actors) {
		return errors.New("QA actor set differs from the canonical rendering")
	}
	return nil
}

func validateSource(source Source, contract qarole.RoleContract) error {
	if source.roleID != contract.ID || source.roleContractVersion != contract.ContractVersion {
		return errors.New("QA actor source has an invalid role contract")
	}
	if source.description == "" || len(source.body) == 0 {
		return errors.New("QA actor source has empty required content")
	}
	if !isSHA256Of(source.sourceSHA256, source.body) {
		return errors.New("QA actor source has an invalid body hash")
	}
	if err := qarole.ValidateSourceContract(contract, string(source.body)); err != nil {
		return fmt.Errorf("QA actor source violates its role contract: %w", err)
	}
	return nil
}

func validateActor(actor Actor, source Source, contract qarole.RoleContract) error {
	if actor.roleID != contract.ID || actor.roleContractVersion != contract.ContractVersion {
		return errors.New("QA actor has an invalid role contract")
	}
	if actor.description != source.description || actor.sourceSHA256 != source.sourceSHA256 {
		return errors.New("QA actor does not carry its source identity")
	}
	if !isSHA256Of(actor.generatedSHA256, actor.content) {
		return errors.New("QA actor has an invalid generated hash")
	}
	return nil
}

func sameActors(actual, canonical []Actor) bool {
	if len(actual) != len(canonical) {
		return false
	}
	for index, actor := range actual {
		want := canonical[index]
		if actor.roleID != want.roleID ||
			actor.roleContractVersion != want.roleContractVersion ||
			actor.description != want.description ||
			actor.sourceSHA256 != want.sourceSHA256 ||
			actor.generatedSHA256 != want.generatedSHA256 ||
			string(actor.content) != string(want.content) {
			return false
		}
	}
	return true
}

func isSHA256Of(value string, content []byte) bool {
	sum := sha256.Sum256(content)
	return value == fmt.Sprintf("%x", sum)
}

func isLowerSHA256(value string) bool {
	if len(value) != sha256.Size*2 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
