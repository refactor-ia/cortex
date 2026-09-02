package filetxn

import (
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
	return append([]byte(nil), data...), nil
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
	return After{path: path, exists: exists, data: append([]byte(nil), data...), mode: mode}, nil
}

func (after After) Path() string      { return after.path }
func (after After) Exists() bool      { return after.exists }
func (after After) Data() []byte      { return append([]byte(nil), after.data...) }
func (after After) Mode() os.FileMode { return after.mode }
