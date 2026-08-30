package backuprecovery

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"

	"github.com/refactor-ia/cortex/internal/backupjournal"
)

type (
	entryKey       string
	currentState   uint8
	classification uint8
)

const (
	currentAbsent currentState = iota
	currentPresent
	currentUnsafe
	atBefore classification = 0
	atAfter  classification = 1
	drifted  classification = 2
	unsafe   classification = 3
)

type currentEvidence struct {
	key    entryKey
	state  currentState
	mode   uint32
	length int64
	hash   string
	bytes  []byte
}
type beforeBlob struct {
	key   entryKey
	bytes []byte
}
type recoveryEntry struct {
	key          entryKey
	runtime      backupjournal.Runtime
	relativePath string
	state        classification
	before       currentEvidence
	after        currentEvidence
	current      currentEvidence
}
type recoveryPlan struct {
	entries []recoveryEntry
	ready   bool
}
type invalidModelError struct{}

func (invalidModelError) Error() string { return "backup recovery: invalid model evidence" }
func invalidModel() error               { return invalidModelError{} }
func keyFor(entry backupjournal.Entry) entryKey {
	return entryKey(string(entry.Runtime) + "\x00" + entry.RelativePath)
}
func newCurrentAbsent(key entryKey) currentEvidence {
	return currentEvidence{key: key, state: currentAbsent}
}
func newCurrentUnsafe(key entryKey) currentEvidence {
	return currentEvidence{key: key, state: currentUnsafe}
}
func newCurrentPresent(key entryKey, mode uint32, data []byte) currentEvidence {
	copyData := cloneBytes(data)
	sum := sha256.Sum256(copyData)
	return currentEvidence{key: key, state: currentPresent, mode: mode, length: int64(len(copyData)), hash: hex.EncodeToString(sum[:]), bytes: copyData}
}
func newBeforeBlob(key entryKey, data []byte) beforeBlob {
	return beforeBlob{key: key, bytes: cloneBytes(data)}
}
func cloneBytes(value []byte) []byte {
	copyValue := make([]byte, len(value))
	copy(copyValue, value)
	return copyValue
}
func classify(manifest backupjournal.Manifest, blobs []beforeBlob, current []currentEvidence) (recoveryPlan, error) {
	entries := manifest.Entries()
	if !manifest.Recoverable() || manifest.State() != backupjournal.Prepared || len(entries) == 0 {
		return recoveryPlan{}, invalidModel()
	}
	blobsByKey := make(map[entryKey]beforeBlob, len(blobs))
	for _, blob := range blobs {
		if blob.key == "" {
			return recoveryPlan{}, invalidModel()
		}
		if _, duplicate := blobsByKey[blob.key]; duplicate {
			return recoveryPlan{}, invalidModel()
		}
		blobsByKey[blob.key] = newBeforeBlob(blob.key, blob.bytes)
	}
	currentByKey := make(map[entryKey]currentEvidence, len(current))
	for _, evidence := range current {
		if !validCurrent(evidence) || evidence.key == "" {
			return recoveryPlan{}, invalidModel()
		}
		if _, duplicate := currentByKey[evidence.key]; duplicate {
			return recoveryPlan{}, invalidModel()
		}
		currentByKey[evidence.key] = copyCurrent(evidence)
	}
	if len(currentByKey) != len(entries) {
		return recoveryPlan{}, invalidModel()
	}
	plan := recoveryPlan{entries: make([]recoveryEntry, len(entries)), ready: true}
	for index, entry := range entries {
		key := keyFor(entry)
		before := journalCurrent(key, entry.Existence, entry.Mode, entry.Length, entry.SHA256, nil)
		afterEvidence := entry.AfterEvidence()
		after := journalCurrent(key, afterEvidence.Existence, afterEvidence.Mode, afterEvidence.Length, afterEvidence.SHA256, nil)
		if key == "" || !validJournal(before) || !validJournal(after) || sameJournal(before, after) {
			return recoveryPlan{}, invalidModel()
		}
		if before.state == currentPresent {
			blob, ok := blobsByKey[key]
			if !ok {
				return recoveryPlan{}, invalidModel()
			}
			candidate := newCurrentPresent(key, before.mode, blob.bytes)
			if !sameJournal(before, candidate) {
				return recoveryPlan{}, invalidModel()
			}
			before.bytes = candidate.bytes
			delete(blobsByKey, key)
		}
		evidence, ok := currentByKey[key]
		if !ok {
			return recoveryPlan{}, invalidModel()
		}
		delete(currentByKey, key)
		state := classifyCurrent(evidence, before, after)
		plan.entries[index] = recoveryEntry{
			key:          key,
			runtime:      entry.Runtime,
			relativePath: entry.RelativePath,
			state:        state,
			before:       copyCurrent(before),
			after:        copyCurrent(after),
			current:      copyCurrent(evidence),
		}
		plan.ready = plan.ready && (state == atBefore || state == atAfter)
	}
	if len(blobsByKey) != 0 || len(currentByKey) != 0 {
		return recoveryPlan{}, invalidModel()
	}
	return plan, nil
}
func journalCurrent(key entryKey, existence backupjournal.Existence, mode uint32, length int64, hash string, data []byte) currentEvidence {
	state := currentAbsent
	if existence == backupjournal.Present {
		state = currentPresent
	}
	return currentEvidence{key: key, state: state, mode: mode, length: length, hash: hash, bytes: cloneBytes(data)}
}
func copyCurrent(value currentEvidence) currentEvidence {
	value.bytes = cloneBytes(value.bytes)
	return value
}
func validCurrent(value currentEvidence) bool {
	if value.state == currentAbsent || value.state == currentUnsafe {
		return value.mode == 0 && value.length == 0 && value.hash == "" && len(value.bytes) == 0
	}
	return value.state == currentPresent && value.mode <= 0777 && value.length >= 0 && validHash(value.hash) && int64(len(value.bytes)) == value.length && hashBytes(value.bytes) == value.hash
}
func validJournal(value currentEvidence) bool {
	return (value.state == currentAbsent && value.mode == 0 && value.length == 0 && value.hash == "" && len(value.bytes) == 0) ||
		(value.state == currentPresent && (value.mode == 0600 || value.mode == 0644) && value.length >= 0 && validHash(value.hash))
}
func validHash(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if !(character >= '0' && character <= '9' || character >= 'a' && character <= 'f') {
			return false
		}
	}
	return true
}
func hashBytes(value []byte) string { sum := sha256.Sum256(value); return hex.EncodeToString(sum[:]) }
func sameJournal(left, right currentEvidence) bool {
	return left.state == right.state && left.mode == right.mode && left.length == right.length && left.hash == right.hash
}
func matchesJournal(want, got currentEvidence, exactBytes bool) bool {
	return sameJournal(want, got) && (!exactBytes || bytes.Equal(want.bytes, got.bytes))
}
func classifyCurrent(current, before, after currentEvidence) classification {
	if current.state == currentUnsafe {
		return unsafe
	}
	if matchesJournal(before, current, true) {
		return atBefore
	}
	if matchesJournal(after, current, false) {
		return atAfter
	}
	return drifted
}
