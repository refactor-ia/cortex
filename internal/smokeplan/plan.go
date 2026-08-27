// Package smokeplan defines immutable, provider-free real-smoke workloads.
package smokeplan

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/refactor-ia/cortex/internal/catalog"
)

// RuntimeID identifies an approved smoke runtime.
type RuntimeID string

const (
	RuntimePi       RuntimeID = "pi"
	RuntimeOpenCode RuntimeID = "opencode"
	RuntimeClaude   RuntimeID = "claude"
)

var canonicalRuntimes = [...]RuntimeID{RuntimePi, RuntimeOpenCode, RuntimeClaude}

var (
	ErrMissingRuntime           = errors.New("missing runtime")
	ErrUnknownRuntime           = errors.New("unknown runtime")
	ErrDuplicateRuntime         = errors.New("duplicate runtime")
	ErrDuplicateVersion         = errors.New("duplicate runtime version")
	ErrMalformedVersion         = errors.New("malformed runtime version")
	ErrNoncanonicalRuntimeOrder = errors.New("noncanonical runtime order")
	ErrMissingFamily            = errors.New("missing family")
	ErrUnknownFamily            = errors.New("unknown family")
	ErrDuplicateFamily          = errors.New("duplicate family")
	ErrNoncanonicalFamilyOrder  = errors.New("noncanonical family order")
)

// FamilyID identifies a catalog-approved model family.
type FamilyID string

// ApprovedCatalog carries the caller's copy of the catalog family selection.
type ApprovedCatalog struct {
	Families []FamilyID
}

// CandidateRuntime binds one runtime to its exact normalized candidate version.
type CandidateRuntime struct {
	Runtime RuntimeID
	Version string
}

// Input supplies only catalog-approved families and candidate runtime versions.
type Input struct {
	Catalog    ApprovedCatalog
	Families   []FamilyID
	Candidates []CandidateRuntime
}

// Status is the lifecycle state of a planned provider invocation.
type Status string

// Pending is the only status a newly constructed plan can contain.
const Pending Status = "pending"

// Slot is one deterministic runtime/family invocation reservation.
type Slot struct {
	Ordinal int
	Runtime RuntimeID
	Version string
	Family  FamilyID
	Status  Status
}

// Forecast reports the bounded prerequisites without authorizing execution.
type Forecast struct {
	Invocations                                    int
	RequiresExplicitBillingCredentialAuthorization bool
	RequiresRealCatalogContent                     bool
}

// Plan is an immutable matrix suitable for later evidence binding.
type Plan struct {
	slots    []Slot
	forecast Forecast
}

// CanonicalRuntimeOrder returns Pi, OpenCode, and Claude in immutable order.
func CanonicalRuntimeOrder() []RuntimeID {
	return append([]RuntimeID(nil), canonicalRuntimes[:]...)
}

// Build validates the catalog selection and returns its 33 pending slots.
func Build(input Input) (Plan, error) {
	families := canonicalFamilies()
	if err := validateFamilies(input.Catalog.Families, families); err != nil {
		return Plan{}, err
	}
	if err := validateFamilies(input.Families, families); err != nil {
		return Plan{}, err
	}
	if err := validateCandidates(input.Candidates); err != nil {
		return Plan{}, err
	}

	slots := make([]Slot, 0, len(canonicalRuntimes)*len(families))
	ordinal := 1
	for _, candidate := range input.Candidates {
		for _, family := range input.Families {
			slots = append(slots, Slot{
				Ordinal: ordinal,
				Runtime: candidate.Runtime,
				Version: candidate.Version,
				Family:  family,
				Status:  Pending,
			})
			ordinal++
		}
	}
	return Plan{
		slots: slots,
		forecast: Forecast{
			Invocations: len(slots),
			RequiresExplicitBillingCredentialAuthorization: true,
			RequiresRealCatalogContent:                     true,
		},
	}, nil
}

// Slots returns a defensive copy of the deterministic pending matrix.
func (p Plan) Slots() []Slot {
	return append([]Slot(nil), p.slots...)
}

// Forecast returns the fixed execution prerequisite forecast.
func (p Plan) Forecast() Forecast {
	return p.forecast
}

func canonicalFamilies() []FamilyID {
	approved := catalog.ApprovedFamilyIDs()
	families := make([]FamilyID, len(approved))
	for index, family := range approved {
		families[index] = FamilyID(family)
	}
	return families
}

func validateFamilies(families, catalog []FamilyID) error {
	if len(families) < len(catalog) {
		return ErrMissingFamily
	}
	if len(families) > len(catalog) {
		return fmt.Errorf("%w: expected %d", ErrUnknownFamily, len(catalog))
	}
	approved := make(map[FamilyID]struct{}, len(catalog))
	for _, family := range catalog {
		approved[family] = struct{}{}
	}
	seen := make(map[FamilyID]struct{}, len(catalog))
	for _, family := range families {
		if family == "" {
			return ErrMissingFamily
		}
		if _, exists := approved[family]; !exists {
			return fmt.Errorf("%w: %q", ErrUnknownFamily, family)
		}
		if _, exists := seen[family]; exists {
			return fmt.Errorf("%w: %q", ErrDuplicateFamily, family)
		}
		seen[family] = struct{}{}
	}
	for index, family := range families {
		if family != catalog[index] {
			return ErrNoncanonicalFamilyOrder
		}
	}
	return nil
}

func validateCandidates(candidates []CandidateRuntime) error {
	if len(candidates) < len(canonicalRuntimes) {
		return ErrMissingRuntime
	}
	if len(candidates) > len(canonicalRuntimes) {
		return fmt.Errorf("%w: expected %d", ErrUnknownRuntime, len(canonicalRuntimes))
	}
	seenRuntimes := make(map[RuntimeID]struct{}, len(canonicalRuntimes))
	seenVersions := make(map[string]struct{}, len(canonicalRuntimes))
	for _, candidate := range candidates {
		if !knownRuntime(candidate.Runtime) {
			return fmt.Errorf("%w: %q", ErrUnknownRuntime, candidate.Runtime)
		}
		if _, exists := seenRuntimes[candidate.Runtime]; exists {
			return fmt.Errorf("%w: %q", ErrDuplicateRuntime, candidate.Runtime)
		}
		if !normalizedVersion(candidate.Version) {
			return fmt.Errorf("%w: %q", ErrMalformedVersion, candidate.Version)
		}
		if _, exists := seenVersions[candidate.Version]; exists {
			return fmt.Errorf("%w: %q", ErrDuplicateVersion, candidate.Version)
		}
		seenRuntimes[candidate.Runtime] = struct{}{}
		seenVersions[candidate.Version] = struct{}{}
	}
	for index, candidate := range candidates {
		if candidate.Runtime != canonicalRuntimes[index] {
			return ErrNoncanonicalRuntimeOrder
		}
	}
	return nil
}

func knownRuntime(runtime RuntimeID) bool {
	for _, approved := range canonicalRuntimes {
		if runtime == approved {
			return true
		}
	}
	return false
}

func normalizedVersion(version string) bool {
	parts := strings.Split(version, ".")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if part == "" || (len(part) > 1 && part[0] == '0') {
			return false
		}
		if _, err := strconv.ParseUint(part, 10, 64); err != nil {
			return false
		}
	}
	return true
}
