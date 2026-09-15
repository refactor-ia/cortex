// Package qaactor extracts immutable QA actor source values from an admitted catalog.
package qaactor

import (
	"errors"

	"github.com/refactor-ia/cortex/internal/catalog"
	"github.com/refactor-ia/cortex/internal/qarole"
)

const qualityAssuranceFamily = "quality-assurance"

// Source is one immutable role source extracted from the admitted QA catalog.
type Source struct {
	roleID              qarole.RoleID
	roleContractVersion string
	description         string
	sourceSHA256        string
	body                []byte
}

// RoleID returns the closed QA role identity.
func (source Source) RoleID() qarole.RoleID {
	return source.roleID
}

// RoleContractVersion returns the version of the role contract.
func (source Source) RoleContractVersion() string {
	return source.roleContractVersion
}

// Description returns the exact capability description.
func (source Source) Description() string {
	return source.description
}

// SourceSHA256 returns the lowercase SHA-256 hash of Body.
func (source Source) SourceSHA256() string {
	return source.sourceSHA256
}

// Body returns a copy of the exact catalog source bytes.
func (source Source) Body() []byte {
	return append([]byte(nil), source.body...)
}

// SourceSet is the immutable six-role QA catalog source set.
type SourceSet struct {
	catalogFingerprint string
	sources            []Source
}

// CatalogFingerprint returns the exact admitted catalog fingerprint.
func (set SourceSet) CatalogFingerprint() string {
	return set.catalogFingerprint
}

// Sources returns deep copies in the canonical QA role order.
func (set SourceSet) Sources() []Source {
	copies := make([]Source, len(set.sources))
	for index, source := range set.sources {
		copies[index] = cloneSource(source)
	}
	return copies
}

// Sources extracts and validates the exact admitted six-role QA catalog source set.
func Sources(snapshot catalog.CatalogSnapshot) (SourceSet, error) {
	if snapshot.Fingerprint() == "" {
		return SourceSet{}, errors.New("QA actor source set has an empty catalog fingerprint")
	}
	contracts := qarole.Catalog()
	family, found := qualityAssurance(snapshot.Families())
	if !found || !matchesRoles(family.Manifest().Agents, contracts) {
		return SourceSet{}, errors.New("QA actor source set has an invalid role catalog")
	}

	capabilities := family.Capabilities()
	if len(capabilities) != len(contracts) {
		return SourceSet{}, errors.New("QA actor source set has an invalid capability catalog")
	}

	sources := make([]Source, len(contracts))
	for index, contract := range contracts {
		capability := capabilities[index]
		manifest := capability.Manifest()
		expectedSource := "families/quality-assurance/sources/" + string(contract.ID) + ".md"
		if manifest.ID != string(contract.ID) || manifest.Family != qualityAssuranceFamily || manifest.Source != expectedSource || manifest.Description == "" {
			return SourceSet{}, errors.New("QA actor source set has an invalid capability")
		}

		file := capability.Source()
		if file.Path() != expectedSource || file.SHA256() == "" || len(file.Content()) == 0 || qarole.ValidateSourceContract(contract, string(file.Content())) != nil {
			return SourceSet{}, errors.New("QA actor source set has an invalid source")
		}
		sources[index] = Source{
			roleID:              contract.ID,
			roleContractVersion: contract.ContractVersion,
			description:         manifest.Description,
			sourceSHA256:        file.SHA256(),
			body:                file.Content(),
		}
	}

	return SourceSet{catalogFingerprint: snapshot.Fingerprint(), sources: sources}, nil
}

func qualityAssurance(families []catalog.CatalogFamilySnapshot) (catalog.CatalogFamilySnapshot, bool) {
	for _, family := range families {
		if family.Manifest().ID == qualityAssuranceFamily {
			return family, true
		}
	}
	return catalog.CatalogFamilySnapshot{}, false
}

func matchesRoles(ids []string, contracts []qarole.RoleContract) bool {
	if len(ids) != len(contracts) {
		return false
	}
	for index, contract := range contracts {
		if ids[index] != string(contract.ID) {
			return false
		}
	}
	return true
}

func cloneSource(source Source) Source {
	source.body = append([]byte(nil), source.body...)
	return source
}
