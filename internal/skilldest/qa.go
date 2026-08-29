package skilldest

import (
	"bytes"
	"errors"

	"github.com/refactor-ia/cortex/internal/catalog"
	"github.com/refactor-ia/cortex/internal/qarole"
	"github.com/refactor-ia/cortex/internal/runtimematrix"
	"github.com/refactor-ia/cortex/internal/skillartifact"
	"github.com/refactor-ia/cortex/internal/skillrender"
)

// QAProjectionOwnership binds one generated QA destination to its neutral source.
type QAProjectionOwnership struct {
	RoleID              string
	RuntimeID           runtimematrix.RuntimeID
	SnapshotFingerprint string
	SourceSHA256        string
	Destination         string
	GeneratedSHA256     string
}

// ValidateQAProjection proves that the closed QA fleet is projected from its neutral sources.
func ValidateQAProjection(snapshot catalog.CatalogSnapshot, sources skillrender.Set, binding skillartifact.Binding, destinations Plan) ([]QAProjectionOwnership, error) {
	manifest, manifestOK := binding.Manifest()
	bundle, bundleOK := binding.Bundle()
	if !manifestOK || !bundleOK || sources.SnapshotFingerprint() != snapshot.Fingerprint() || manifest.SnapshotFingerprint() != snapshot.Fingerprint() || destinations.SnapshotFingerprint() != snapshot.Fingerprint() || manifest.RuntimeID() != destinations.RuntimeID() || !supportedQARuntime(destinations.RuntimeID()) {
		return nil, invalidQAProjection()
	}

	family, found := qaFamily(snapshot)
	contracts := qarole.Catalog()
	if !found || len(family.Manifest().Capabilities) != len(contracts) || len(family.Manifest().Agents) != len(contracts) {
		return nil, invalidQAProjection()
	}

	rendered := make(map[string]skillrender.RenderedSkill, len(sources.Skills()))
	for _, source := range sources.Skills() {
		rendered[source.LogicalID()] = source
	}
	artifacts := make(map[string]string, len(manifest.Artifacts()))
	for _, artifact := range manifest.Artifacts() {
		artifacts[artifact.LogicalID()] = artifact.SHA256()
	}
	payloads := make(map[string][]byte, len(bundle.Artifacts()))
	for _, payload := range bundle.Artifacts() {
		payloads[payload.LogicalID()] = payload.Content()
	}
	planned := make(map[string]Destination, len(destinations.Destinations()))
	for _, destination := range destinations.Destinations() {
		planned[destination.LogicalID()] = destination
	}

	ownership := make([]QAProjectionOwnership, 0, len(contracts))
	for index, contract := range contracts {
		roleID, logicalID := string(contract.ID), "skills/"+string(contract.ID)
		if family.Manifest().Agents[index] != roleID {
			return nil, invalidQAProjection()
		}
		capability, source, found := qaCapability(family, roleID)
		if !found || capability.Manifest().Family != "quality-assurance" || qarole.ValidateSourceContract(contract, string(source.Content())) != nil {
			return nil, invalidQAProjection()
		}
		renderedSkill, renderedOK := rendered[logicalID]
		destination, destinationOK := planned[logicalID]
		if !renderedOK || !destinationOK || renderedSkill.CapabilityID() != roleID || !bytes.Contains(renderedSkill.Content(), source.Content()) || !bytes.Contains(destination.Content(), renderedSkill.Content()) || destination.RelativePath() != "skills/cortex-"+roleID+"/SKILL.md" || destination.SHA256() != renderedSkill.SHA256() || artifacts[logicalID] != destination.SHA256() || !bytes.Equal(payloads[logicalID], destination.Content()) {
			return nil, invalidQAProjection()
		}
		ownership = append(ownership, QAProjectionOwnership{roleID, destinations.RuntimeID(), snapshot.Fingerprint(), source.SHA256(), destination.RelativePath(), destination.SHA256()})
	}
	return ownership, nil
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

func supportedQARuntime(runtime runtimematrix.RuntimeID) bool {
	return runtime == runtimematrix.RuntimePi || runtime == runtimematrix.RuntimeOpenCode
}

func invalidQAProjection() error { return errors.New("QA projection: invalid input") }
