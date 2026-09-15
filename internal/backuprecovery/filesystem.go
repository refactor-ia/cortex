package backuprecovery

import (
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/refactor-ia/cortex/internal/backupjournal"
)

const filesystemMaxBytes int64 = 32 << 20

type filesystemRoot struct {
	runtime  backupjournal.Runtime
	kind     backupjournal.RootKind
	path     string
	binding  backupjournal.RootBinding
	identity os.FileInfo
}

type filesystemReadOptions struct{ maxBytes int64 }

type filesystemFile interface {
	Stat() (os.FileInfo, error)
	Read([]byte) (int, error)
	Close() error
}

type filesystemOperations struct {
	open                      func(*os.Root, string) (filesystemFile, error)
	afterLeafLstat, afterRead func() error
}

func newFilesystemRoot(runtime backupjournal.Runtime, kind backupjournal.RootKind, root string) (filesystemRoot, error) {
	binding, err := backupjournal.NewRootBinding(runtime, kind, root)
	if err != nil {
		return filesystemRoot{}, invalidFilesystem()
	}
	info, err := os.Lstat(root)
	if err != nil || !realDirectory(info) {
		return filesystemRoot{}, invalidFilesystem()
	}
	return filesystemRoot{runtime: runtime, kind: kind, path: root, binding: binding, identity: info}, nil
}

func defaultFilesystemReadOptions() filesystemReadOptions {
	return filesystemReadOptions{maxBytes: filesystemMaxBytes}
}

func osFilesystemOperations() filesystemOperations {
	return filesystemOperations{open: func(root *os.Root, name string) (filesystemFile, error) { return root.Open(name) }}
}

func observeFilesystemRecovery(handle backupjournal.Handle, roots []filesystemRoot) (recoveryPlan, error) {
	if handle.State() == backupjournal.Committed || handle.State() == backupjournal.Recovered {
		return recoveryPlan{}, terminalFilesystem()
	}
	manifest, ok := handle.Manifest()
	if !ok {
		return recoveryPlan{}, invalidFilesystem()
	}
	rootsByRuntime, ok := rootsForManifest(manifest, roots)
	if !ok {
		return recoveryPlan{}, invalidFilesystem()
	}
	entries := manifest.Entries()
	current := make([]currentEvidence, 0, len(entries))
	blobs := make([]beforeBlob, 0, len(entries))
	for _, entry := range entries {
		root := rootsByRuntime[entry.Runtime]
		evidence, err := readFilesystemEntry(entry, root, defaultFilesystemReadOptions(), osFilesystemOperations())
		if err != nil {
			return recoveryPlan{}, err
		}
		current = append(current, evidence)
		if entry.Existence == backupjournal.Present {
			data, found := handle.Blob(entry.Runtime, entry.RelativePath)
			if !found {
				return recoveryPlan{}, invalidFilesystem()
			}
			blobs = append(blobs, newBeforeBlob(keyFor(entry), data))
		}
	}
	return classify(manifest, blobs, current)
}

func rootsForManifest(manifest backupjournal.Manifest, roots []filesystemRoot) (map[backupjournal.Runtime]filesystemRoot, bool) {
	bindings := manifest.RootBindings()
	if len(bindings) == 0 || len(roots) != len(bindings) {
		return nil, false
	}
	rootsByRuntime := make(map[backupjournal.Runtime]filesystemRoot, len(roots))
	for _, root := range roots {
		if !validFilesystemRoot(root) {
			return nil, false
		}
		if _, duplicate := rootsByRuntime[root.runtime]; duplicate {
			return nil, false
		}
		rootsByRuntime[root.runtime] = root
	}
	for _, binding := range bindings {
		root, found := rootsByRuntime[binding.Runtime()]
		if !found || root.runtime != binding.Runtime() || root.kind != binding.Kind() || root.binding != binding {
			return nil, false
		}
	}
	return rootsByRuntime, true
}

func validFilesystemRoot(root filesystemRoot) bool {
	return root.runtime != "" && root.kind != "" && root.path != "" && root.identity != nil &&
		root.binding.Runtime() == root.runtime && root.binding.Kind() == root.kind && root.binding.MatchesRoot(root.path)
}

func readFilesystemEntry(entry backupjournal.Entry, root filesystemRoot, options filesystemReadOptions, operations filesystemOperations) (currentEvidence, error) {
	key, err := validFilesystemRead(entry, root, options, operations)
	if err != nil {
		return currentEvidence{}, err
	}
	descriptor, err := os.OpenRoot(root.path)
	if err != nil {
		return newCurrentUnsafe(key), nil
	}
	defer descriptor.Close()
	if !matchesFilesystemRoot(root, descriptor) {
		return newCurrentUnsafe(key), nil
	}
	parents, ok := filesystemParents(descriptor, entry.RelativePath, nil)
	if !ok {
		return newCurrentUnsafe(key), nil
	}
	before, leafErr := descriptor.Lstat(entry.RelativePath)
	if operations.afterLeafLstat != nil && operations.afterLeafLstat() != nil {
		return newCurrentUnsafe(key), nil
	}
	if errors.Is(leafErr, os.ErrNotExist) {
		if _, stable := filesystemParents(descriptor, entry.RelativePath, parents); matchesFilesystemRoot(root, descriptor) && stable {
			return newCurrentAbsent(key), nil
		}
		return newCurrentUnsafe(key), nil
	}
	if leafErr != nil || !regularFile(before) {
		return newCurrentUnsafe(key), nil
	}
	file, err := operations.open(descriptor, entry.RelativePath)
	if err != nil {
		return newCurrentUnsafe(key), nil
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !sameFilesystemFile(before, opened) || opened.Size() > options.maxBytes {
		return newCurrentUnsafe(key), nil
	}
	data, err := io.ReadAll(io.LimitReader(file, options.maxBytes+1))
	if err != nil || operations.afterRead != nil && operations.afterRead() != nil || int64(len(data)) != opened.Size() || int64(len(data)) > options.maxBytes {
		return newCurrentUnsafe(key), nil
	}
	afterOpened, err := file.Stat()
	after, pathErr := descriptor.Lstat(entry.RelativePath)
	_, stable := filesystemParents(descriptor, entry.RelativePath, parents)
	if err != nil || pathErr != nil || !sameFilesystemFile(opened, afterOpened) || !sameFilesystemFile(afterOpened, after) || !matchesFilesystemRoot(root, descriptor) || !stable {
		return newCurrentUnsafe(key), nil
	}
	return newCurrentPresent(key, uint32(opened.Mode().Perm()), data), nil
}

func validFilesystemRead(entry backupjournal.Entry, root filesystemRoot, options filesystemReadOptions, operations filesystemOperations) (entryKey, error) {
	if entry.Runtime == "" || entry.Root == "" || entry.Runtime != root.runtime || entry.Root != root.kind ||
		root.runtime == "" || root.kind == "" || root.path == "" || root.identity == nil ||
		root.binding.Runtime() != root.runtime || root.binding.Kind() != root.kind || !root.binding.MatchesRoot(root.path) ||
		!safeRelativePath(entry.RelativePath) || options.maxBytes <= 0 || options.maxBytes > filesystemMaxBytes || operations.open == nil {
		return "", invalidFilesystem()
	}
	return keyFor(entry), nil
}

func matchesFilesystemRoot(root filesystemRoot, descriptor *os.Root) bool {
	current, err := os.Lstat(root.path)
	anchored, anchoredErr := descriptor.Lstat(".")
	return err == nil && anchoredErr == nil && realDirectory(current) && realDirectory(anchored) && sameFilesystemObject(root.identity, current) && sameFilesystemObject(root.identity, anchored)
}

func filesystemParents(root *os.Root, relative string, previous []os.FileInfo) ([]os.FileInfo, bool) {
	parts := strings.Split(relative, "/")
	if previous != nil && len(previous) != len(parts)-1 {
		return nil, false
	}
	parents := make([]os.FileInfo, 0, len(parts)-1)
	for index := range parts[:len(parts)-1] {
		info, err := root.Lstat(strings.Join(parts[:index+1], "/"))
		if err != nil || !realDirectory(info) || previous != nil && !sameFilesystemObject(previous[index], info) {
			return nil, false
		}
		parents = append(parents, info)
	}
	return parents, true
}

func safeRelativePath(value string) bool {
	return value != "" && !strings.Contains(value, "\\") && !filepath.IsAbs(value) && !path.IsAbs(value) && filepath.Clean(value) == value && path.Clean(value) == value && value != "." && !strings.HasPrefix(value, "../")
}
func realDirectory(info os.FileInfo) bool {
	return info != nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0
}
func regularFile(info os.FileInfo) bool {
	return info != nil && info.Mode()&os.ModeSymlink == 0 && info.Mode().IsRegular()
}
func sameFilesystemObject(left, right os.FileInfo) bool {
	return left != nil && right != nil && left.Mode() == right.Mode() && os.SameFile(left, right)
}
func sameFilesystemFile(left, right os.FileInfo) bool {
	return regularFile(left) && regularFile(right) && sameFilesystemObject(left, right) && left.Size() == right.Size() && left.ModTime().Equal(right.ModTime())
}

type invalidFilesystemError struct{}
type terminalFilesystemError struct{}

func (invalidFilesystemError) Error() string  { return "backup recovery: invalid filesystem evidence" }
func (terminalFilesystemError) Error() string { return "backup recovery: terminal journal handle" }
func invalidFilesystem() error                { return invalidFilesystemError{} }
func terminalFilesystem() error               { return terminalFilesystemError{} }
