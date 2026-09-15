package installobserve

import (
	"bytes"
	"errors"
	"io/fs"
	"os"

	"github.com/refactor-ia/cortex/internal/installstate"
	"github.com/refactor-ia/cortex/internal/runtimematrix"
	"github.com/refactor-ia/cortex/internal/skilldest"
)

// UninstallRoot identifies a caller-trusted runtime root without a desired install plan.
// It is an observation input only and is never returned in uninstall results.
type UninstallRoot struct {
	runtimeID runtimematrix.RuntimeID
	rootKind  skilldest.RootKind
	rootPath  string
}

// NewUninstallRoot validates the runtime identity and absolute trusted root supplied
// by the caller. ObserveUninstall additionally requires that root to exist as a
// real directory before reading its canonical state file.
func NewUninstallRoot(runtimeID runtimematrix.RuntimeID, rootKind skilldest.RootKind, rootPath string) (UninstallRoot, error) {
	root := UninstallRoot{runtimeID: runtimeID, rootKind: rootKind, rootPath: rootPath}
	if !validUninstallRoot(root) {
		return UninstallRoot{}, uninstallInvalid()
	}
	return root, nil
}

// UninstallStatus is a neutral classification of one prior-state logical record.
type UninstallStatus string

const (
	UninstallAbsent   UninstallStatus = "absent"
	UninstallRemove   UninstallStatus = "remove"
	UninstallConflict UninstallStatus = "conflict"
)

// UninstallRecord reports only path-neutral logical evidence. It carries no path,
// timestamp, file content, or permission to mutate a filesystem.
type UninstallRecord struct {
	LogicalID string
	Status    UninstallStatus
	SHA256    string
}

// RemovalEvidence is detached transaction-only evidence for one authorized uninstall.
// It exposes only a canonical relative destination and cloned observed file data.
type RemovalEvidence struct {
	destination string
	exact       ExactFile
}

// Destination returns the canonical relative destination recorded in prior state.
func (evidence RemovalEvidence) Destination() string { return evidence.destination }

// Bytes returns a detached copy of the observed file bytes.
func (evidence RemovalEvidence) Bytes() []byte { return evidence.exact.Bytes() }

// Mode returns the observed file permission mode.
func (evidence RemovalEvidence) Mode() fs.FileMode { return evidence.exact.Mode() }

func (evidence RemovalEvidence) clone() RemovalEvidence {
	evidence.exact = evidence.exact.clone()
	return evidence
}

// UninstallObservation is detached, bounded evidence from canonical prior state.
type UninstallObservation struct {
	runtimeID runtimematrix.RuntimeID
	rootPath  string
	records   []UninstallRecord
	exact     map[string]ExactFile
	removals  map[string]RemovalEvidence
	ready     bool
}

// Records returns detached logical records in canonical prior-state order, with the
// state file record always final when state exists.
func (observation UninstallObservation) Records() []UninstallRecord {
	return append(make([]UninstallRecord, 0, len(observation.records)), observation.records...)
}

// Ready reports whether no prior owned file drifted. It grants no touch authority.
func (observation UninstallObservation) Ready() bool { return observation.ready }

// MatchesRoot reports whether this observation is bound to the given trusted root.
func (observation UninstallObservation) MatchesRoot(root string) bool {
	return root != "" && root == observation.rootPath
}

// MatchesRuntime reports whether this observation is bound to the given runtime.
func (observation UninstallObservation) MatchesRuntime(runtimeID runtimematrix.RuntimeID) bool {
	return runtimeID != "" && runtimeID == observation.runtimeID
}

// RemovalCandidates returns detached records whose removal is supported by exact
// prior evidence. A conflict globally suppresses every candidate.
func (observation UninstallObservation) RemovalCandidates() []UninstallRecord {
	if !observation.ready {
		return []UninstallRecord{}
	}
	out := make([]UninstallRecord, 0, len(observation.records))
	for _, record := range observation.records {
		if record.Status == UninstallRemove {
			out = append(out, record)
		}
	}
	return out
}

// Exact returns detached transaction-only exact evidence for an observed logical
// record. It never accepts or exposes a filesystem path.
func (observation UninstallObservation) Exact(logicalID string) (ExactFile, bool) {
	file, found := observation.exact[logicalID]
	if !found {
		return ExactFile{}, false
	}
	return file.clone(), true
}

// RemovalEvidence returns detached transaction-only evidence for an authorized
// removal candidate identified by logical ID. Unknown, absent, drifted, and globally
// suppressed candidates have no removal evidence.
func (observation UninstallObservation) RemovalEvidence(logicalID string) (RemovalEvidence, bool) {
	if !observation.ready {
		return RemovalEvidence{}, false
	}
	evidence, found := observation.removals[logicalID]
	if !found {
		return RemovalEvidence{}, false
	}
	return evidence.clone(), true
}

// ObserveUninstall reads the canonical state file and only the relative paths
// explicitly recorded within that canonical state. It does not list, glob,
// recurse, discover unrelated files, or use a desired installation plan.
func ObserveUninstall(root UninstallRoot, options Options) (UninstallObservation, error) {
	if !validOptions(options) || !validUninstallRoot(root) || !existingRoot(root.rootPath) {
		return UninstallObservation{}, uninstallInvalid()
	}
	state, stateMode, present, err := readRegular(root.rootPath, ".cortex/install-state.json", options.MaxStateBytes)
	if err != nil {
		return UninstallObservation{}, uninstallInvalid()
	}
	if !present {
		return UninstallObservation{runtimeID: root.runtimeID, rootPath: root.rootPath, records: []UninstallRecord{}, exact: map[string]ExactFile{}, removals: map[string]RemovalEvidence{}, ready: true}, nil
	}
	manifest, err := decodeCanonicalUninstallState(state, root, options.MaxEntries)
	if err != nil {
		return UninstallObservation{}, uninstallInvalid()
	}

	artifacts := manifest.Artifacts()
	records := make([]UninstallRecord, 0, len(artifacts)+1)
	exact := make(map[string]ExactFile, len(artifacts)+1)
	removals := make(map[string]RemovalEvidence, len(artifacts)+1)
	conflict := false
	for _, artifact := range artifacts {
		data, mode, exists, err := readRegular(root.rootPath, artifact.RelativePath(), options.MaxFileBytes)
		if err != nil {
			return UninstallObservation{}, uninstallInvalid()
		}
		record := UninstallRecord{LogicalID: artifact.LogicalID(), Status: UninstallAbsent}
		if exists {
			record.SHA256 = hash(data)
			exact[record.LogicalID] = ExactFile{bytes: append([]byte{}, data...), mode: mode}
			if record.SHA256 == artifact.SHA256() {
				record.Status = UninstallRemove
				removals[record.LogicalID] = RemovalEvidence{destination: artifact.RelativePath(), exact: exact[record.LogicalID]}
			} else {
				record.Status = UninstallConflict
				conflict = true
			}
		}
		records = append(records, record)
	}
	stateHash := hash(state)
	exact["state/install-state"] = ExactFile{bytes: append([]byte{}, state...), mode: stateMode}
	removals["state/install-state"] = RemovalEvidence{destination: ".cortex/install-state.json", exact: exact["state/install-state"]}
	records = append(records, UninstallRecord{LogicalID: "state/install-state", Status: UninstallRemove, SHA256: stateHash})
	return UninstallObservation{runtimeID: root.runtimeID, rootPath: root.rootPath, records: records, exact: exact, removals: removals, ready: !conflict}, nil
}

func validUninstallRoot(root UninstallRoot) bool {
	if !validPath(root.rootPath) {
		return false
	}
	switch root.runtimeID {
	case runtimematrix.RuntimePi:
		return root.rootKind == skilldest.RootKindPiUserAgent
	case runtimematrix.RuntimeOpenCode:
		return root.rootKind == skilldest.RootKindOpenCodeUserConfig
	case runtimematrix.RuntimeClaudeCode:
		return root.rootKind == skilldest.RootKindClaudeCodeUser
	default:
		return false
	}
}

func existingRoot(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.IsDir() && info.Mode()&fs.ModeSymlink == 0
}

func decodeCanonicalUninstallState(state []byte, root UninstallRoot, maxEntries int) (manifest installstate.Manifest, err error) {
	manifest, err = installstate.Decode(state)
	if err != nil || len(manifest.Artifacts()) > maxEntries || manifest.RuntimeID() != root.runtimeID || manifest.RootKind() != root.rootKind {
		return installstate.Manifest{}, uninstallInvalid()
	}
	encoded, err := installstate.Encode(manifest)
	if err != nil || !bytes.Equal(state, encoded) {
		return installstate.Manifest{}, uninstallInvalid()
	}
	paths := make(map[string]bool, len(manifest.Artifacts()))
	for _, artifact := range manifest.Artifacts() {
		if paths[artifact.RelativePath()] {
			return installstate.Manifest{}, uninstallInvalid()
		}
		paths[artifact.RelativePath()] = true
	}
	return manifest, nil
}

func uninstallInvalid() error { return errors.New("install observe: uninstall observation failed") }
