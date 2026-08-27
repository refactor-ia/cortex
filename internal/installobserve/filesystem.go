package installobserve

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/refactor-ia/cortex/internal/installplan"
	"github.com/refactor-ia/cortex/internal/installstate"
	"github.com/refactor-ia/cortex/internal/safepath"
)

const (
	DefaultMaxStateBytes int64 = 1 << 20
	DefaultMaxEntries          = 1024
	DefaultMaxFileBytes  int64 = 8 << 20
)

// Options bounds the data read during filesystem observation.
type Options struct {
	MaxStateBytes int64
	MaxEntries    int
	MaxFileBytes  int64
}

// DefaultOptions returns explicit conservative filesystem-observation bounds.
func DefaultOptions() Options {
	return Options{
		MaxStateBytes: DefaultMaxStateBytes,
		MaxEntries:    DefaultMaxEntries,
		MaxFileBytes:  DefaultMaxFileBytes,
	}
}

// FilesystemObservation is neutral input for Classify. It grants no authority
// to touch a filesystem path.
type FilesystemObservation struct {
	prior   *PriorState
	slots   []SlotObservation
	exact   map[string]ExactFile
	binding [sha256.Size]byte
	bound   bool
}

// ExactFile is transaction-only evidence for one present canonical logical file.
// Its bytes are never serialized or persisted, and it grants no touch authority.
type ExactFile struct {
	bytes []byte
	mode  fs.FileMode
}

// Bytes returns a detached copy of the observed bytes.
func (file ExactFile) Bytes() []byte { return append([]byte{}, file.bytes...) }

// Mode returns the observed permission mode.
func (file ExactFile) Mode() fs.FileMode { return file.mode }

func (file ExactFile) clone() ExactFile { file.bytes = file.Bytes(); return file }

// PriorState returns a detached prior state, or nil when no state file exists.
func (observation FilesystemObservation) PriorState() *PriorState {
	if observation.prior == nil {
		return nil
	}
	prior := *observation.prior
	return &prior
}

// Slots returns detached current slot observations in logical-ID order.
func (observation FilesystemObservation) Slots() []SlotObservation {
	return append(make([]SlotObservation, 0, len(observation.slots)), observation.slots...)
}

// Exact returns detached transaction-only evidence for a canonical logical ID.
// Arbitrary filesystem paths and absent files have no exact evidence.
func (observation FilesystemObservation) Exact(logicalID string) (ExactFile, bool) {
	file, found := observation.exact[logicalID]
	if !found {
		return ExactFile{}, false
	}
	return file.clone(), true
}

// MatchesCandidate reports whether this successful observation is bound to the
// exact validated candidate. The private binding never exposes path or digest data.
func (observation FilesystemObservation) MatchesCandidate(candidate installplan.Plan) bool {
	binding, valid := candidateBinding(candidate)
	return observation.bound && valid && observation.binding == binding
}

// Observe reads only the canonical state file and slots named by candidate or
// prior state. It does not discover paths, mutate the filesystem, or infer ownership.
func Observe(candidate installplan.Plan, options Options) (FilesystemObservation, error) {
	binding, valid := candidateBinding(candidate)
	if !validOptions(options) || !valid || len(candidate.InstalledState().Artifacts()) > options.MaxEntries {
		return FilesystemObservation{}, filesystemInvalid()
	}

	current := candidate.InstalledState()
	paths := make(map[string]string, len(current.Artifacts()))
	for _, artifact := range current.Artifacts() {
		paths[artifact.LogicalID()] = artifact.RelativePath()
	}

	state, stateMode, present, err := readRegular(candidate.RootPath(), ".cortex/install-state.json", options.MaxStateBytes)
	if err != nil {
		return FilesystemObservation{}, filesystemInvalid()
	}
	exact := make(map[string]ExactFile, len(current.Artifacts())+1)
	var prior *PriorState
	if present {
		manifest, err := installstate.Decode(state)
		if err != nil || len(manifest.Artifacts()) > options.MaxEntries || manifest.RuntimeID() != candidate.RuntimeID() || manifest.RootKind() != candidate.RootKind() {
			return FilesystemObservation{}, filesystemInvalid()
		}
		for _, artifact := range manifest.Artifacts() {
			if path, found := paths[artifact.LogicalID()]; found && path != artifact.RelativePath() {
				return FilesystemObservation{}, filesystemInvalid()
			}
			paths[artifact.LogicalID()] = artifact.RelativePath()
		}
		prior = &PriorState{Manifest: manifest, StateSHA256: hash(state)}
		exact["state/install-state"] = ExactFile{bytes: append([]byte{}, state...), mode: stateMode}
	}
	if len(paths) > options.MaxEntries {
		return FilesystemObservation{}, filesystemInvalid()
	}

	ids := make([]string, 0, len(paths))
	for id := range paths {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	slots := make([]SlotObservation, 0, len(ids))
	for _, id := range ids {
		data, mode, exists, err := readRegular(candidate.RootPath(), paths[id], options.MaxFileBytes)
		if err != nil {
			return FilesystemObservation{}, filesystemInvalid()
		}
		digest := ""
		if exists {
			digest = hash(data)
			exact[id] = ExactFile{bytes: append([]byte{}, data...), mode: mode}
		}
		slots = append(slots, SlotObservation{LogicalID: id, Present: exists, SHA256: digest})
	}
	return FilesystemObservation{prior: prior, slots: slots, exact: exact, binding: binding, bound: true}, nil
}

func validOptions(options Options) bool {
	return options.MaxStateBytes > 0 && options.MaxEntries > 0 && options.MaxFileBytes > 0
}

func validFilesystemCandidate(candidate installplan.Plan, maxEntries int) bool {
	manifest, state := candidate.InstalledState(), candidate.StateJSON()
	encoded, err := installstate.Encode(manifest)
	if err != nil || !bytes.Equal(encoded, state) || candidate.RootPath() == "" || !filepath.IsAbs(candidate.RootPath()) || filepath.Clean(candidate.RootPath()) != candidate.RootPath() || len(manifest.Artifacts()) == 0 || len(manifest.Artifacts()) > maxEntries {
		return false
	}
	files := candidate.Files()
	if len(files) != len(manifest.Artifacts())+1 {
		return false
	}
	for index, artifact := range manifest.Artifacts() {
		file := files[index]
		if file.Role() != "skill" || file.LogicalID() != artifact.LogicalID() || file.RelativePath() != artifact.RelativePath() || file.SHA256() != artifact.SHA256() || file.DesiredMode() != installplan.CanonicalFileMode || file.AbsolutePath() != filepath.Join(candidate.RootPath(), filepath.FromSlash(artifact.RelativePath())) || hash(file.Content()) != file.SHA256() {
			return false
		}
	}
	stateFile := files[len(files)-1]
	return stateFile.Role() == "state" && stateFile.LogicalID() == "state/install-state" && stateFile.RelativePath() == ".cortex/install-state.json" && stateFile.DesiredMode() == installplan.CanonicalFileMode && stateFile.AbsolutePath() == filepath.Join(candidate.RootPath(), ".cortex", "install-state.json") && stateFile.SHA256() == hash(state) && bytes.Equal(stateFile.Content(), state)
}

func candidateBinding(candidate installplan.Plan) ([sha256.Size]byte, bool) {
	var zero [sha256.Size]byte
	manifest := candidate.InstalledState()
	if !validFilesystemCandidate(candidate, len(manifest.Artifacts())) {
		return zero, false
	}

	binding := sha256.New()
	bindingField(binding, []byte("cortex/installobserve/candidate/v1"))
	bindingString(binding, string(candidate.RuntimeID()))
	bindingString(binding, string(candidate.RootKind()))
	bindingString(binding, candidate.RootPath())
	bindingString(binding, candidate.SnapshotFingerprint())
	bindingField(binding, candidate.StateJSON())
	files := candidate.Files()
	bindingUint(binding, uint64(len(files)))
	for _, file := range files {
		bindingString(binding, file.Role())
		bindingString(binding, file.LogicalID())
		bindingString(binding, file.RelativePath())
		bindingString(binding, file.AbsolutePath())
		bindingString(binding, file.SHA256())
		bindingUint(binding, uint64(file.DesiredMode()))
		bindingField(binding, file.Content())
	}

	bundle, present := candidate.Bundle()
	if !present {
		bindingField(binding, []byte{0})
	} else {
		bindingField(binding, []byte{1})
		bundleManifest := bundle.Manifest()
		bindingUint(binding, uint64(bundleManifest.SchemaVersion()))
		bindingString(binding, bundleManifest.Owner())
		bindingString(binding, bundleManifest.SnapshotFingerprint())
		bindingString(binding, string(bundleManifest.RuntimeID()))
		bindingString(binding, string(bundleManifest.ProjectionResult()))
		bindingString(binding, bundleManifest.TranslationDisclosure())
		manifestArtifacts := bundleManifest.Artifacts()
		bindingUint(binding, uint64(len(manifestArtifacts)))
		for _, artifact := range manifestArtifacts {
			bindingString(binding, artifact.LogicalID())
			bindingString(binding, artifact.SHA256())
		}
		bundleArtifacts := bundle.Artifacts()
		bindingUint(binding, uint64(len(bundleArtifacts)))
		for _, artifact := range bundleArtifacts {
			bindingString(binding, artifact.LogicalID())
			bindingString(binding, artifact.SHA256())
			bindingField(binding, artifact.Content())
		}
	}
	var result [sha256.Size]byte
	copy(result[:], binding.Sum(nil))
	return result, true
}

type bindingHasher interface {
	Write([]byte) (int, error)
}

func bindingString(binding bindingHasher, value string) { bindingField(binding, []byte(value)) }

func bindingUint(binding bindingHasher, value uint64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], value)
	bindingField(binding, encoded[:])
}

func bindingField(binding bindingHasher, value []byte) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = binding.Write(length[:])
	_, _ = binding.Write(value)
}

func readRegular(root, relative string, maximum int64) ([]byte, fs.FileMode, bool, error) {
	path, exists, err := existingRegular(root, relative)
	if err != nil || !exists {
		return nil, 0, exists, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, false, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, 0, false, errors.New("read regular file")
	}
	data, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil || int64(len(data)) > maximum {
		return nil, 0, false, errors.New("read regular file")
	}
	return data, info.Mode().Perm(), true, nil
}

func existingRegular(root, relative string) (string, bool, error) {
	current := ""
	for index, part := range strings.Split(relative, "/") {
		if part == "" || part == "." || part == ".." {
			return "", false, errors.New("invalid relative path")
		}
		current = filepath.Join(current, part)
		path, err := safepath.Resolve(root, current)
		if err != nil {
			return "", false, err
		}
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			return "", false, nil
		}
		if err != nil {
			return "", false, err
		}
		if info.Mode()&os.ModeSymlink != 0 || (index < len(strings.Split(relative, "/"))-1 && !info.IsDir()) || (index == len(strings.Split(relative, "/"))-1 && !info.Mode().IsRegular()) {
			return "", false, errors.New("unsafe file type")
		}
	}
	return filepath.Join(root, filepath.FromSlash(relative)), true, nil
}

func filesystemInvalid() error { return errors.New("install observe: filesystem observation failed") }
