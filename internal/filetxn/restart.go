package filetxn

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"

	"github.com/refactor-ia/cortex/internal/safepath"
)

func Open(backupRoot, name string) (Snapshot, error) {
	snapshot, err := reloadAndVerify(backupRoot, name)
	if err != nil {
		return Snapshot{}, errors.New("open snapshot failed")
	}
	return snapshot, nil
}

func (snapshot Snapshot) Payload(path string) ([]byte, error) {
	if validateManifest(snapshot.Manifest) != nil || !validManifestPath(path) {
		return nil, errors.New("snapshot payload is unavailable")
	}
	var entry *Entry
	for index := range snapshot.Manifest.Entries {
		candidate := &snapshot.Manifest.Entries[index]
		if candidate.Path != path {
			continue
		}
		if entry != nil || !candidate.Exists {
			return nil, errors.New("snapshot payload is unavailable")
		}
		entry = candidate
	}
	if entry == nil {
		return nil, errors.New("snapshot payload is unavailable")
	}
	payloadDir, err := safepath.Resolve(snapshot.Dir, "payloads")
	if err != nil {
		return nil, errors.New("snapshot payload is unavailable")
	}
	payloadDigest := sha256.Sum256([]byte(path))
	payload, err := safepath.Resolve(payloadDir, hex.EncodeToString(payloadDigest[:]))
	if err != nil {
		return nil, errors.New("snapshot payload is unavailable")
	}
	info, err := os.Lstat(payload)
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("snapshot payload is unavailable")
	}
	data, err := os.ReadFile(payload)
	digest := sha256.Sum256(data)
	if err != nil || hex.EncodeToString(digest[:]) != entry.SHA256 {
		return nil, errors.New("snapshot payload is unavailable")
	}
	return bytes.Clone(data), nil
}

type restartLeaf struct {
	before After
	after  After
}

type restartPlan struct {
	snapshot Snapshot
	leaves   []restartLeaf
}

func planRestartEvidence(snapshot Snapshot, after []After) (restartPlan, error) {
	if validateManifest(snapshot.Manifest) != nil {
		return restartPlan{}, errors.New("restart evidence is invalid")
	}
	snapshot = cloneRestartSnapshot(snapshot)
	byPath := make(map[string]After, len(after))
	for _, value := range after {
		detached, err := NewAfter(value.Path(), value.Exists(), value.Data(), value.Mode())
		if err != nil {
			return restartPlan{}, errors.New("restart evidence is invalid")
		}
		if _, exists := byPath[detached.path]; exists {
			return restartPlan{}, errors.New("restart evidence is invalid")
		}
		byPath[detached.path] = detached
	}
	if len(byPath) != len(snapshot.Manifest.Entries) {
		return restartPlan{}, errors.New("restart evidence is invalid")
	}
	ordered := make([]After, len(snapshot.Manifest.Entries))
	for index, entry := range snapshot.Manifest.Entries {
		value, exists := byPath[entry.Path]
		if !exists {
			return restartPlan{}, errors.New("restart evidence is invalid")
		}
		ordered[index] = value
	}
	leaves := make([]restartLeaf, len(ordered))
	for index, entry := range snapshot.Manifest.Entries {
		before, err := restartBefore(snapshot, entry)
		if err != nil || sameRestartEvidence(before, ordered[index]) {
			return restartPlan{}, errors.New("restart evidence is invalid")
		}
		leaves[index] = restartLeaf{before: before, after: ordered[index]}
	}
	return restartPlan{snapshot: snapshot, leaves: leaves}, nil
}

func cloneRestartSnapshot(snapshot Snapshot) Snapshot {
	manifest := snapshot.Manifest
	manifest.Entries = append([]Entry{}, snapshot.Manifest.Entries...)
	manifest.AbsentDirectories = append([]directoryEntry{}, snapshot.Manifest.AbsentDirectories...)
	return Snapshot{Dir: snapshot.Dir, Manifest: manifest}
}

func restartBefore(snapshot Snapshot, entry Entry) (After, error) {
	if !entry.Exists {
		return NewAfter(entry.Path, false, nil, 0)
	}
	data, err := snapshot.Payload(entry.Path)
	if err != nil {
		return After{}, err
	}
	return NewAfter(entry.Path, true, data, os.FileMode(entry.Mode))
}

func sameRestartEvidence(before, after After) bool {
	return before.exists == after.exists && before.mode == after.mode && bytes.Equal(before.data, after.data)
}

type After struct {
	path   string
	exists bool
	data   []byte
	mode   os.FileMode
}

func NewAfter(path string, exists bool, data []byte, mode os.FileMode) (After, error) {
	if !validManifestPath(path) || mode&^os.FileMode(0o777) != 0 {
		return After{}, errors.New("restart after evidence is invalid")
	}
	if exists {
		if data == nil {
			return After{}, errors.New("restart after evidence is invalid")
		}
	} else if data != nil || mode != 0 {
		return After{}, errors.New("restart after evidence is invalid")
	}
	return After{path: path, exists: exists, data: bytes.Clone(data), mode: mode}, nil
}

func (after After) Path() string      { return after.path }
func (after After) Exists() bool      { return after.exists }
func (after After) Data() []byte      { return bytes.Clone(after.data) }
func (after After) Mode() os.FileMode { return after.mode }
