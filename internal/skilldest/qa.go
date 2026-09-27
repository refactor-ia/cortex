package skilldest

import (
	"bytes"
	"errors"
	"strings"

	"github.com/refactor-ia/cortex/internal/catalog"
	"github.com/refactor-ia/cortex/internal/qarole"
	"github.com/refactor-ia/cortex/internal/runtimematrix"
	"github.com/refactor-ia/cortex/internal/skillartifact"
	"github.com/refactor-ia/cortex/internal/skillrender"
)

const (
	qaNoCICapability  = "qa-no-ci"
	qaNoCIDescription = "Perform bounded no-CI QA with report-mode Cortex checks and authorized execution boundaries."
	qaNoCIMarkerText  = "no-ci-report-only cortex-qa-run-report-only claude-pi-background-argv stdin-never-accepted no-inference no-paid-call-without-authorization no-retries authorized-tools-disposable-worktree no-overbroad-results guidance-not-launcher"
)

var qaNoCIMarkers = strings.Fields(qaNoCIMarkerText)

// QAProjectionOwnership binds one generated QA destination to its neutral source.
type QAProjectionOwnership struct {
	RoleID              string
	RuntimeID           runtimematrix.RuntimeID
	SnapshotFingerprint string
	SourceSHA256        string
	Destination         string
	GeneratedSHA256     string
}

// ValidateQAProjection proves all seven QA skills (six actors plus qa-no-ci)
// are owned by their neutral sources on each supported runtime.
func ValidateQAProjection(snapshot catalog.CatalogSnapshot, sources skillrender.Set, binding skillartifact.Binding, destinations Plan) ([]QAProjectionOwnership, error) {
	manifest, manifestOK := binding.Manifest()
	bundle, bundleOK := binding.Bundle()
	if !manifestOK || !bundleOK || sources.SnapshotFingerprint() != snapshot.Fingerprint() || manifest.SnapshotFingerprint() != snapshot.Fingerprint() || destinations.SnapshotFingerprint() != snapshot.Fingerprint() || manifest.RuntimeID() != destinations.RuntimeID() || !supportedQARuntime(destinations.RuntimeID()) {
		return nil, invalidQAProjection()
	}

	family, found := qaFamily(snapshot)
	capabilityIDs := qaCapabilityIDs()
	if !found || !validQAFamilyLayout(family, capabilityIDs) {
		return nil, invalidQAProjection()
	}

	rendered := make(map[string]skillrender.RenderedSkill, len(sources.Skills()))
	for _, source := range sources.Skills() {
		if _, exists := rendered[source.LogicalID()]; exists {
			return nil, invalidQAProjection()
		}
		rendered[source.LogicalID()] = source
	}
	artifacts := make(map[string]string, len(manifest.Artifacts()))
	for _, artifact := range manifest.Artifacts() {
		if _, exists := artifacts[artifact.LogicalID()]; exists {
			return nil, invalidQAProjection()
		}
		artifacts[artifact.LogicalID()] = artifact.SHA256()
	}
	payloads := make(map[string][]byte, len(bundle.Artifacts()))
	for _, payload := range bundle.Artifacts() {
		if _, exists := payloads[payload.LogicalID()]; exists {
			return nil, invalidQAProjection()
		}
		payloads[payload.LogicalID()] = payload.Content()
	}
	planned := make(map[string]Destination, len(destinations.Destinations()))
	for _, destination := range destinations.Destinations() {
		if _, exists := planned[destination.LogicalID()]; exists {
			return nil, invalidQAProjection()
		}
		planned[destination.LogicalID()] = destination
	}

	contracts := qarole.Catalog()
	ownership := make([]QAProjectionOwnership, 0, len(capabilityIDs))
	for index, capabilityID := range capabilityIDs {
		logicalID := "skills/" + capabilityID
		capability, source, found := qaCapability(family, capabilityID)
		capabilityManifest, sourceContent := capability.Manifest(), source.Content()
		expectedSource := "families/quality-assurance/sources/" + capabilityID + ".md"
		if !found || capabilityManifest.ID != capabilityID || capabilityManifest.Family != "quality-assurance" || capabilityManifest.Source != expectedSource || capabilityManifest.Description == "" || capabilityManifest.Provenance != catalog.ProvenanceCortexOwned || capabilityManifest.License != "CC-BY-SA-4.0" || !capabilityManifest.RedistributionAllowed || source.Path() != expectedSource || source.SHA256() == "" || len(sourceContent) == 0 {
			return nil, invalidQAProjection()
		}
		if capabilityID == qaNoCICapability && (capabilityManifest.Description != qaNoCIDescription || capabilityManifest.Activation != catalog.ActivationAutomatic) {
			return nil, invalidQAProjection()
		}
		if capabilityID == qaNoCICapability {
			if !validQANoCISource(string(sourceContent)) {
				return nil, invalidQAProjection()
			}
		} else if index >= len(contracts) || qarole.ValidateSourceContract(contracts[index], string(sourceContent)) != nil {
			return nil, invalidQAProjection()
		}
		renderedSkill, renderedOK := rendered[logicalID]
		destination, destinationOK := planned[logicalID]
		artifactSHA, artifactOK := artifacts[logicalID]
		payload, payloadOK := payloads[logicalID]
		if !renderedOK || !destinationOK || !artifactOK || !payloadOK || renderedSkill.CapabilityID() != capabilityID || renderedSkill.LogicalID() != logicalID || !bytes.Contains(renderedSkill.Content(), sourceContent) || !bytes.Contains(destination.Content(), renderedSkill.Content()) || destination.RelativePath() != "skills/cortex-"+capabilityID+"/SKILL.md" || destination.SHA256() != renderedSkill.SHA256() || artifactSHA != destination.SHA256() || !bytes.Equal(payload, destination.Content()) {
			return nil, invalidQAProjection()
		}
		ownership = append(ownership, QAProjectionOwnership{capabilityID, destinations.RuntimeID(), snapshot.Fingerprint(), source.SHA256(), destination.RelativePath(), destination.SHA256()})
	}
	return ownership, nil
}

func qaCapabilityIDs() []string {
	contracts := qarole.Catalog()
	ids := make([]string, 0, len(contracts)+1)
	for _, contract := range contracts {
		ids = append(ids, string(contract.ID))
	}
	return append(ids, qaNoCICapability)
}

func validQAFamilyLayout(family catalog.CatalogFamilySnapshot, capabilityIDs []string) bool {
	manifest := family.Manifest()
	contracts := qarole.Catalog()
	if manifest.ID != "quality-assurance" || len(manifest.Agents) != len(contracts) || len(manifest.Capabilities) != len(capabilityIDs) {
		return false
	}
	for index, contract := range contracts {
		if manifest.Agents[index] != string(contract.ID) {
			return false
		}
	}
	for index, capabilityID := range capabilityIDs {
		want := "families/quality-assurance/capabilities/" + capabilityID + ".json"
		if manifest.Capabilities[index] != want {
			return false
		}
	}
	return true
}

func validQANoCISource(source string) bool {
	lower := strings.ToLower(source)
	for _, marker := range qaNoCIMarkers {
		if !strings.Contains(source, "<!-- cortex-qa:"+marker+" -->") {
			return false
		}
	}
	for _, phrase := range []string{
		"cortex qa run", "report-only", "pi -p", "</dev/null", "timeout", "standard input", "untested", "paid", "retry", "disposable worktree", "automatic launcher",
	} {
		if !strings.Contains(lower, phrase) {
			return false
		}
	}
	return true
}

func qaFamily(snapshot catalog.CatalogSnapshot) (catalog.CatalogFamilySnapshot, bool) {
	for _, family := range snapshot.Families() {
		if family.Manifest().ID == "quality-assurance" {
			return family, true
		}
	}
	return catalog.CatalogFamilySnapshot{}, false
}

func qaCapability(family catalog.CatalogFamilySnapshot, roleID string) (catalog.CatalogCapabilitySnapshot, catalog.CatalogFileSnapshot, bool) {
	for _, capability := range family.Capabilities() {
		if capability.Manifest().ID == roleID {
			return capability, capability.Source(), true
		}
	}
	return catalog.CatalogCapabilitySnapshot{}, catalog.CatalogFileSnapshot{}, false
}

// supportedQARuntime names the runtimes the quality-assurance family projects
// to. It admits every runtime in runtimematrix because the family follows the
// three-runtime parity stated in docs/architecture/overview.md:52: Cortex
// targets Pi, OpenCode, and Claude Code with contract and function parity. The
// helper stays as the single place that statement is encoded, so a future
// runtime is admitted here deliberately rather than by silence.
func supportedQARuntime(runtime runtimematrix.RuntimeID) bool {
	return runtime == runtimematrix.RuntimePi || runtime == runtimematrix.RuntimeOpenCode || runtime == runtimematrix.RuntimeClaudeCode
}

// QAFamilyProjected reports whether a snapshot carries a quality-assurance
// family with capabilities to project. Every admitted catalog lists the family,
// because a catalog manifest must name all eleven approved family IDs, but the
// manifest may declare no capabilities. An empty family projects nothing, so
// there is no ownership for ValidateQAProjection to prove and callers skip it.
//
// A partially populated family is not empty: it still reaches the validator and
// still fails there, which is the intended answer for a catalog that ships some
// of the closed fleet.
func QAFamilyProjected(snapshot catalog.CatalogSnapshot) bool {
	family, found := qaFamily(snapshot)
	return found && len(family.Manifest().Capabilities) > 0
}

func invalidQAProjection() error { return errors.New("QA projection: invalid input") }
