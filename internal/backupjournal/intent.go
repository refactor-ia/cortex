package backupjournal

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
)

const transitionIntentVersion = 1
const transitionIntentFile = "transition-intent.json"
const maxIntentLength int64 = 3 * maxLength

type transitionIntentError struct{}

func (transitionIntentError) Error() string { return "backup journal: invalid transition intent" }

type transitionIntent struct {
	transactionID              string
	prepared, target, encoded  []byte
	preparedHash, targetHash   string
	preparedState, targetState State
}
type transitionIntentWire struct {
	Version          int    `json:"version"`
	TransactionID    string `json:"transactionID"`
	PreparedState    State  `json:"preparedState"`
	PreparedSHA256   string `json:"preparedSHA256"`
	PreparedManifest []byte `json:"preparedManifest"`
	TargetState      State  `json:"targetState"`
	TargetSHA256     string `json:"targetSHA256"`
	TargetManifest   []byte `json:"targetManifest"`
}

func newTransitionIntent(transactionID string, prepared, target []byte) (transitionIntent, error) {
	if !validHash(transactionID) || int64(len(prepared)) > maxLength || int64(len(target)) > maxLength {
		return transitionIntent{}, transitionIntentError{}
	}
	preparedManifest, err := Parse(prepared)
	if err != nil || preparedManifest.State() != Prepared || preparedManifest.TransactionID() != transactionID {
		return transitionIntent{}, transitionIntentError{}
	}
	targetManifest, err := Parse(target)
	if err != nil || (targetManifest.State() != Committed && targetManifest.State() != Recovered) || targetManifest.TransactionID() != transactionID || targetManifest.CandidateFingerprint() != preparedManifest.CandidateFingerprint() || !sameEntries(preparedManifest.entries, targetManifest.entries) {
		return transitionIntent{}, transitionIntentError{}
	}
	value := transitionIntent{transactionID: transactionID, prepared: append([]byte(nil), prepared...), target: append([]byte(nil), target...), preparedHash: sha256Hex(prepared), targetHash: sha256Hex(target), preparedState: Prepared, targetState: targetManifest.State()}
	data, err := json.Marshal(transitionIntentWire{transitionIntentVersion, value.transactionID, value.preparedState, value.preparedHash, value.prepared, value.targetState, value.targetHash, value.target})
	if err != nil || int64(len(data)) > maxIntentLength {
		return transitionIntent{}, transitionIntentError{}
	}
	value.encoded = data
	return value, nil
}
func parseTransitionIntent(data []byte) (transitionIntent, error) {
	if len(data) == 0 || int64(len(data)) > maxIntentLength {
		return transitionIntent{}, transitionIntentError{}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var wire transitionIntentWire
	if decoder.Decode(&wire) != nil || decoder.Decode(&struct{}{}) != io.EOF || wire.Version != transitionIntentVersion {
		return transitionIntent{}, transitionIntentError{}
	}
	intent, err := newTransitionIntent(wire.TransactionID, wire.PreparedManifest, wire.TargetManifest)
	if err != nil || wire.PreparedState != intent.preparedState || wire.TargetState != intent.targetState || wire.PreparedSHA256 != intent.preparedHash || wire.TargetSHA256 != intent.targetHash || !bytes.Equal(data, intent.encoded) {
		return transitionIntent{}, transitionIntentError{}
	}
	return intent, nil
}
func createTransitionIntent(directory, transactionID string, prepared, target []byte) (transitionIntent, error) {
	intent, err := newTransitionIntent(transactionID, prepared, target)
	if err != nil || !intentDirectory(directory) {
		return transitionIntent{}, transitionIntentError{}
	}
	name := filepath.Join(directory, transitionIntentFile)
	file, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return transitionIntent{}, transitionIntentError{}
	}
	info, err := file.Stat()
	if err != nil || !regular0600(info) || !writeSyncClose(file, intent.encoded) {
		file.Close()
		removeTransitionFile(directory, name, info)
		return transitionIntent{}, transitionIntentError{}
	}
	current, err := os.Lstat(name)
	if err != nil || !os.SameFile(info, current) || !regular0600(current) || syncDirectory(directory) != nil {
		removeTransitionFile(directory, name, info)
		return transitionIntent{}, transitionIntentError{}
	}
	readback, readInfo, err := readTransitionFile(name, maxIntentLength)
	if err != nil || !os.SameFile(info, readInfo) || !bytes.Equal(readback, intent.encoded) {
		removeTransitionFile(directory, name, info)
		return transitionIntent{}, transitionIntentError{}
	}
	stored, err := parseTransitionIntent(readback)
	if err != nil || !bytes.Equal(stored.encoded, intent.encoded) {
		removeTransitionFile(directory, name, info)
		return transitionIntent{}, transitionIntentError{}
	}
	return intent, nil
}
func readTransitionIntent(directory string) (transitionIntent, error) {
	if !intentDirectory(directory) {
		return transitionIntent{}, transitionIntentError{}
	}
	data, _, err := readTransitionFile(filepath.Join(directory, transitionIntentFile), maxIntentLength)
	if err != nil {
		return transitionIntent{}, transitionIntentError{}
	}
	return parseTransitionIntent(data)
}
func removeTransitionIntent(directory string, intent transitionIntent) error {
	valid, err := parseTransitionIntent(intent.encoded)
	if err != nil || !intentDirectory(directory) || !bytes.Equal(valid.encoded, intent.encoded) {
		return transitionIntentError{}
	}
	name := filepath.Join(directory, transitionIntentFile)
	data, info, err := readTransitionFile(name, maxIntentLength)
	current, statErr := os.Lstat(name)
	if err != nil || statErr != nil || !bytes.Equal(data, intent.encoded) || !regular0600(current) || !os.SameFile(info, current) || os.Remove(name) != nil || syncDirectory(directory) != nil {
		return transitionIntentError{}
	}
	return nil
}
func replaceManifest(directory string, intent transitionIntent) error {
	valid, err := parseTransitionIntent(intent.encoded)
	if err != nil || !intentDirectory(directory) || !bytes.Equal(valid.encoded, intent.encoded) {
		return transitionIntentError{}
	}
	name := filepath.Join(directory, manifestFile)
	current, _, err := readTransitionFile(name, maxLength)
	if err != nil || !bytes.Equal(current, intent.prepared) {
		return transitionIntentError{}
	}
	temporary := manifestTempName(directory, intent)
	file, err := os.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return transitionIntentError{}
	}
	info, err := file.Stat()
	if err != nil || !regular0600(info) || !writeSyncClose(file, intent.target) {
		file.Close()
		removeTransitionFile(directory, temporary, info)
		return transitionIntentError{}
	}
	data, temporaryInfo, err := readTransitionFile(temporary, maxLength)
	if err != nil || !os.SameFile(info, temporaryInfo) || !bytes.Equal(data, intent.target) {
		removeTransitionFile(directory, temporary, info)
		return transitionIntentError{}
	}
	current, _, err = readTransitionFile(name, maxLength)
	if err != nil || !bytes.Equal(current, intent.prepared) || os.Rename(temporary, name) != nil || syncDirectory(directory) != nil {
		removeTransitionFile(directory, temporary, info)
		return transitionIntentError{}
	}
	data, _, err = readTransitionFile(name, maxLength)
	if err != nil || !bytes.Equal(data, intent.target) {
		return transitionIntentError{}
	}
	return nil
}
func manifestTempName(directory string, intent transitionIntent) string {
	return filepath.Join(directory, ".manifest-"+intent.targetHash+".tmp")
}
func intentDirectory(directory string) bool {
	return filepath.IsAbs(directory) && realDir(directory, true)
}
func writeSyncClose(file *os.File, data []byte) bool {
	for len(data) > 0 {
		count, err := file.Write(data)
		if err != nil || count == 0 {
			return false
		}
		data = data[count:]
	}
	return file.Sync() == nil && file.Close() == nil
}
func readTransitionFile(name string, limit int64) ([]byte, os.FileInfo, error) {
	if limit < 0 || limit > maxIntentLength {
		return nil, nil, transitionIntentError{}
	}
	before, err := os.Lstat(name)
	if err != nil || !regular0600(before) || before.Size() < 0 || before.Size() > limit {
		return nil, nil, transitionIntentError{}
	}
	file, err := os.Open(name)
	if err != nil {
		return nil, nil, transitionIntentError{}
	}
	opened, err := file.Stat()
	if err != nil || !regular0600(opened) || !os.SameFile(before, opened) {
		file.Close()
		return nil, nil, transitionIntentError{}
	}
	data, readErr := io.ReadAll(io.LimitReader(file, limit+1))
	closeErr := file.Close()
	final, statErr := os.Lstat(name)
	if readErr != nil || closeErr != nil || statErr != nil || !regular0600(final) || !os.SameFile(before, final) || int64(len(data)) != before.Size() || int64(len(data)) > limit {
		return nil, nil, transitionIntentError{}
	}
	return data, before, nil
}
func removeTransitionFile(directory, name string, owned os.FileInfo) {
	current, err := os.Lstat(name)
	if err == nil && owned != nil && regular0600(current) && os.SameFile(owned, current) && os.Remove(name) == nil {
		_ = syncDirectory(directory)
	}
}
func sameEntries(left, right []Entry) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
