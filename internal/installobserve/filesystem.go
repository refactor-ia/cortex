package installobserve

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
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
	DefaultMaxEntries         = 1024
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
	prior *PriorState
	slots []SlotObservation
}

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

// Observe reads only the canonical state file and slots named by candidate or
// prior state. It does not discover paths, mutate the filesystem, or infer ownership.
func Observe(candidate installplan.Plan, options Options) (FilesystemObservation, error) {
	if !validOptions(options) || !validFilesystemCandidate(candidate, options.MaxEntries) {
		return FilesystemObservation{}, filesystemInvalid()
	}

	current := candidate.InstalledState()
	paths := make(map[string]string, len(current.Artifacts()))
	for _, artifact := range current.Artifacts() {
		paths[artifact.LogicalID()] = artifact.RelativePath()
	}

	state, present, err := readRegular(candidate.RootPath(), ".cortex/install-state.json", options.MaxStateBytes)
	if err != nil {
		return FilesystemObservation{}, filesystemInvalid()
	}
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
		digest, exists, err := hashRegular(candidate.RootPath(), paths[id], options.MaxFileBytes)
		if err != nil {
			return FilesystemObservation{}, filesystemInvalid()
		}
		slots = append(slots, SlotObservation{LogicalID: id, Present: exists, SHA256: digest})
	}
	return FilesystemObservation{prior: prior, slots: slots}, nil
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
		if file.Role() != "skill" || file.LogicalID() != artifact.LogicalID() || file.RelativePath() != artifact.RelativePath() || file.SHA256() != artifact.SHA256() || file.AbsolutePath() != filepath.Join(candidate.RootPath(), filepath.FromSlash(artifact.RelativePath())) || hash(file.Content()) != file.SHA256() {
			return false
		}
	}
	stateFile := files[len(files)-1]
	return stateFile.Role() == "state" && stateFile.LogicalID() == "state/install-state" && stateFile.RelativePath() == ".cortex/install-state.json" && stateFile.AbsolutePath() == filepath.Join(candidate.RootPath(), ".cortex", "install-state.json") && stateFile.SHA256() == hash(state) && bytes.Equal(stateFile.Content(), state)
}

func readRegular(root, relative string, maximum int64) ([]byte, bool, error) {
	path, exists, err := existingRegular(root, relative)
	if err != nil || !exists {
		return nil, exists, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil || int64(len(data)) > maximum {
		return nil, false, errors.New("read regular file")
	}
	return data, true, nil
}

func hashRegular(root, relative string, maximum int64) (string, bool, error) {
	path, exists, err := existingRegular(root, relative)
	if err != nil || !exists {
		return "", exists, err
	}
	file, err := os.Open(path)
	if err != nil {
		return "", false, err
	}
	defer file.Close()
	digest := sha256.New()
	count, err := io.Copy(digest, io.LimitReader(file, maximum+1))
	if err != nil || count > maximum {
		return "", false, errors.New("hash regular file")
	}
	return hex.EncodeToString(digest.Sum(nil)), true, nil
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
