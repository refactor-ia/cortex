// Package releasecatalog admits only catalog snapshots explicitly bound by Cortex code.
package releasecatalog

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strconv"
	"strings"

	"github.com/refactor-ia/cortex/internal/catalog"
)

// Resolution is the code-bound identity of an admitted catalog snapshot.
// Its zero value carries no trust evidence.
type Resolution struct {
	id             string
	catalogVersion int
	fingerprint    string
}

// ID returns the detached identity of the admitted snapshot.
func (resolution Resolution) ID() string {
	return resolution.id
}

// CatalogVersion returns the detached catalog schema version of the admitted snapshot.
func (resolution Resolution) CatalogVersion() int {
	return resolution.catalogVersion
}

// Fingerprint returns the detached fingerprint of the admitted snapshot.
func (resolution Resolution) Fingerprint() string {
	return resolution.fingerprint
}

type snapshotEvidence struct {
	id             string
	catalogVersion int
	fingerprint    string
}

type admission struct {
	id             string
	catalogVersion int
	fingerprint    string
}

// source is an immutable set of release catalog admissions.
type source struct {
	admissions []admission
}

var builtInAdmissions = []admission{}

// BuiltInSource returns Cortex's compiled release catalog policy. No catalog
// evidence has been approved yet, so no release snapshots are admitted.
func BuiltInSource() source {
	builtInSource, err := newSource(builtInAdmissions)
	if err != nil {
		panic("release catalog: built-in admissions are invalid")
	}
	return builtInSource
}

// ResolveSnapshot admits a catalog snapshot only when it exactly matches a
// compiled release binding. Its identity is derived from the snapshot itself.
func (source source) ResolveSnapshot(snapshot catalog.CatalogSnapshot) (Resolution, error) {
	evidence, err := evidenceFor(snapshot)
	if err != nil {
		return Resolution{}, err
	}
	admission, err := source.resolveEvidence(evidence)
	if err != nil {
		return Resolution{}, err
	}
	return Resolution{
		id:             admission.id,
		catalogVersion: evidence.catalogVersion,
		fingerprint:    evidence.fingerprint,
	}, nil
}

func newSource(admissions []admission) (source, error) {
	bound := append([]admission(nil), admissions...)
	seenIDs := make(map[string]struct{}, len(bound))
	seenFingerprints := make(map[string]struct{}, len(bound))
	for _, admission := range bound {
		evidence := admission.evidence()
		if !validEvidence(evidence) {
			return source{}, errors.New("release catalog: admission is invalid")
		}
		if _, exists := seenIDs[admission.id]; exists {
			return source{}, errors.New("release catalog: admission identity is duplicate")
		}
		if _, exists := seenFingerprints[admission.fingerprint]; exists {
			return source{}, errors.New("release catalog: admission fingerprint is duplicate")
		}
		seenIDs[admission.id] = struct{}{}
		seenFingerprints[admission.fingerprint] = struct{}{}
	}
	sort.Slice(bound, func(i, j int) bool { return bound[i].id < bound[j].id })
	return source{admissions: bound}, nil
}

func (source source) resolveEvidence(evidence snapshotEvidence) (admission, error) {
	if !validEvidence(evidence) {
		return admission{}, errors.New("release catalog: snapshot evidence is invalid")
	}
	index := sort.Search(len(source.admissions), func(i int) bool {
		return source.admissions[i].id >= evidence.id
	})
	if index == len(source.admissions) || source.admissions[index].evidence() != evidence {
		return admission{}, errors.New("release catalog: snapshot is not admitted")
	}
	return source.admissions[index], nil
}

func evidenceFor(snapshot catalog.CatalogSnapshot) (snapshotEvidence, error) {
	evidence := snapshotEvidence{
		catalogVersion: snapshot.Manifest().SchemaVersion,
		fingerprint:    snapshot.Fingerprint(),
	}
	evidence.id = identityFor(evidence.catalogVersion, evidence.fingerprint)
	if !validEvidence(evidence) {
		return snapshotEvidence{}, errors.New("release catalog: catalog snapshot is invalid")
	}
	return evidence, nil
}

func (admission admission) evidence() snapshotEvidence {
	return snapshotEvidence{
		id:             admission.id,
		catalogVersion: admission.catalogVersion,
		fingerprint:    admission.fingerprint,
	}
}

func validEvidence(evidence snapshotEvidence) bool {
	if !canonicalID(evidence.id) || evidence.id != identityFor(evidence.catalogVersion, evidence.fingerprint) || evidence.catalogVersion < 1 || len(evidence.fingerprint) != sha256.Size*2 || evidence.fingerprint != strings.ToLower(evidence.fingerprint) {
		return false
	}
	_, err := hex.DecodeString(evidence.fingerprint)
	return err == nil
}

func identityFor(catalogVersion int, fingerprint string) string {
	return "catalog." + strconv.Itoa(catalogVersion) + "." + fingerprint
}

func canonicalID(id string) bool {
	if len(id) == 0 || len(id) > 128 {
		return false
	}
	for index, character := range id {
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || (index > 0 && (character == '-' || character == '.')) {
			continue
		}
		return false
	}
	return true
}
