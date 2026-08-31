package atomicfile

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/refactor-ia/cortex/internal/safepath"
)

const removeExactMaxEvidenceBytes = 32 << 20

type exactRemovalOperations struct {
	resolve       func(string, string) (string, error)
	lstat         func(string) (fs.FileInfo, error)
	open          func(string) (*os.File, error)
	remove        func(string) error
	syncDirectory func(string) error
}

// RemoveIfExact durably removes a contained regular file only when its bytes and
// permission mode exactly match recovery evidence. Missing, mismatched, and
// drifting destinations fail closed. It uses Lstat, open/read/Fstat, and a final
// Lstat to reduce TOCTOU exposure; portable primitives leave a residual path-swap
// race between that final check and removal.
func RemoveIfExact(root, relativePath string, expectedBytes []byte, expectedMode fs.FileMode) error {
	return removeIfExact(root, relativePath, expectedBytes, expectedMode, exactRemovalOperations{
		resolve:       safepath.Resolve,
		lstat:         os.Lstat,
		open:          os.Open,
		remove:        os.Remove,
		syncDirectory: syncDirectory,
	})
}

func removeIfExact(root, relativePath string, expectedBytes []byte, expectedMode fs.FileMode, operations exactRemovalOperations) error {
	if err := validateExactRemoval(root, relativePath, expectedBytes, expectedMode); err != nil {
		return err
	}
	destination, err := operations.resolve(root, relativePath)
	if err != nil {
		return errors.New("atomic remove exact: destination is unsafe")
	}
	if err := exactRemovalMatches(destination, expectedBytes, expectedMode, operations); err != nil {
		return err
	}
	destination, err = operations.resolve(root, relativePath)
	if err != nil {
		return errors.New("atomic remove exact: destination is unsafe")
	}
	if err := exactRemovalMatches(destination, expectedBytes, expectedMode, operations); err != nil {
		return err
	}
	if err := operations.remove(destination); err != nil {
		return errors.New("atomic remove exact: remove destination failed")
	}
	if err := operations.syncDirectory(filepath.Dir(destination)); err != nil {
		return errors.New("atomic remove exact: parent directory sync failed")
	}
	info, err := operations.lstat(destination)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil || info != nil {
		return errors.New("atomic remove exact: absence verification failed")
	}
	return errors.New("atomic remove exact: absence verification failed")
}

func validateExactRemoval(root, relativePath string, expectedBytes []byte, expectedMode fs.FileMode) error {
	if root == "" {
		return errors.New("atomic remove exact: invalid root")
	}
	if relativePath == "" || filepath.IsAbs(relativePath) || strings.Contains(relativePath, `\`) {
		return errors.New("atomic remove exact: invalid relative path")
	}
	clean := filepath.Clean(relativePath)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return errors.New("atomic remove exact: invalid relative path")
	}
	for _, component := range strings.Split(relativePath, string(filepath.Separator)) {
		if component == "." || component == ".." {
			return errors.New("atomic remove exact: invalid relative path")
		}
	}
	if expectedBytes == nil || len(expectedBytes) > removeExactMaxEvidenceBytes {
		return errors.New("atomic remove exact: invalid expected bytes")
	}
	if expectedMode&^fs.FileMode(0o777) != 0 {
		return errors.New("atomic remove exact: invalid expected mode")
	}
	return nil
}

type rootedExactEvidenceOperations struct {
	lstat func(*os.Root, string) (fs.FileInfo, error)
}
type rootedParentEvidence struct {
	path string
	info fs.FileInfo
}

type rootedExactEvidence struct {
	parents []rootedParentEvidence
	leaf    fs.FileInfo
}

func observeRootedExact(root *os.Root, relativePath string, expectedBytes []byte, expectedMode fs.FileMode, operations rootedExactEvidenceOperations) (rootedExactEvidence, error) {
	if err := validateRootedExactEvidence(root, relativePath, expectedBytes, expectedMode, operations); err != nil {
		return rootedExactEvidence{}, err
	}
	firstParents, err := rootedEvidenceParents(root, relativePath, operations)
	if err != nil {
		return rootedExactEvidence{}, err
	}
	firstLeaf, err := rootedEvidenceLeaf(root, relativePath, expectedBytes, expectedMode, operations)
	if err != nil {
		return rootedExactEvidence{}, err
	}
	secondParents, err := rootedEvidenceParents(root, relativePath, operations)
	if err != nil || !sameRootedParents(firstParents, secondParents) {
		return rootedExactEvidence{}, errors.New("atomic rooted exact evidence: parent drifted")
	}
	secondLeaf, err := rootedEvidenceLeaf(root, relativePath, expectedBytes, expectedMode, operations)
	if err != nil || firstLeaf.Mode() != secondLeaf.Mode() || !os.SameFile(firstLeaf, secondLeaf) {
		return rootedExactEvidence{}, errors.New("atomic rooted exact evidence: destination drifted")
	}
	parent := secondParents[len(secondParents)-1]
	parentRoot, err := root.OpenRoot(parent.path)
	if err != nil {
		return rootedExactEvidence{}, errors.New("atomic rooted exact evidence: parent drifted")
	}
	anchoredParent, anchorErr := rootedEvidenceLstat(operations, parentRoot, ".")
	if anchorErr != nil || anchoredParent.Mode() != parent.info.Mode() || !os.SameFile(anchoredParent, parent.info) {
		_ = parentRoot.Close()
		return rootedExactEvidence{}, errors.New("atomic rooted exact evidence: parent drifted")
	}
	anchoredLeaf, leafErr := rootedEvidenceLeaf(parentRoot, relativePath[strings.LastIndex(relativePath, "/")+1:], expectedBytes, expectedMode, operations)
	closeErr := parentRoot.Close()
	if leafErr != nil || closeErr != nil || anchoredLeaf.Mode() != secondLeaf.Mode() || !os.SameFile(anchoredLeaf, secondLeaf) {
		return rootedExactEvidence{}, errors.New("atomic rooted exact evidence: destination drifted")
	}
	return rootedExactEvidence{parents: secondParents, leaf: anchoredLeaf}, nil
}
func validateRootedExactEvidence(root *os.Root, relativePath string, expectedBytes []byte, expectedMode fs.FileMode, operations rootedExactEvidenceOperations) error {
	if root == nil {
		return errors.New("atomic rooted exact evidence: invalid root")
	}
	info, err := rootedEvidenceLstat(operations, root, ".")
	if err != nil || info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("atomic rooted exact evidence: invalid root")
	}
	if relativePath == "" || filepath.IsAbs(relativePath) || strings.Contains(relativePath, `\`) || filepath.Clean(relativePath) != relativePath {
		return errors.New("atomic rooted exact evidence: invalid relative path")
	}
	if relativePath == "." || relativePath == ".." || strings.HasPrefix(relativePath, "../") {
		return errors.New("atomic rooted exact evidence: invalid relative path")
	}
	if expectedBytes == nil || len(expectedBytes) > removeExactMaxEvidenceBytes {
		return errors.New("atomic rooted exact evidence: invalid expected bytes")
	}
	if expectedMode&^fs.FileMode(0o777) != 0 {
		return errors.New("atomic rooted exact evidence: invalid expected mode")
	}
	return nil
}
func rootedEvidenceParents(root *os.Root, relativePath string, operations rootedExactEvidenceOperations) ([]rootedParentEvidence, error) {
	components := strings.Split(relativePath, "/")
	parents := make([]rootedParentEvidence, 0, len(components))
	for i := range components {
		name := "."
		if i > 0 {
			name = strings.Join(components[:i], "/")
		}
		info, err := rootedEvidenceLstat(operations, root, name)
		if err != nil || info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() {
			return nil, errors.New("atomic rooted exact evidence: parent is missing or invalid")
		}
		parents = append(parents, rootedParentEvidence{path: name, info: info})
	}
	return parents, nil
}

func rootedEvidenceLeaf(root *os.Root, relativePath string, expectedBytes []byte, expectedMode fs.FileMode, operations rootedExactEvidenceOperations) (fs.FileInfo, error) {
	initial, err := rootedEvidenceLstat(operations, root, relativePath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, errors.New("atomic rooted exact evidence: destination is missing")
	}
	if err != nil {
		return nil, errors.New("atomic rooted exact evidence: inspect destination failed")
	}
	if initial.Mode()&fs.ModeSymlink != 0 || !initial.Mode().IsRegular() {
		return nil, errors.New("atomic rooted exact evidence: destination is not a regular file")
	}
	if initial.Mode() != expectedMode {
		return nil, errors.New("atomic rooted exact evidence: destination mode does not match")
	}
	file, err := root.Open(relativePath)
	if err != nil {
		return nil, errors.New("atomic rooted exact evidence: open destination failed")
	}
	opened, statErr := file.Stat()
	if statErr != nil || !opened.Mode().IsRegular() || opened.Mode() != expectedMode || !os.SameFile(initial, opened) {
		_ = file.Close()
		return nil, errors.New("atomic rooted exact evidence: destination drifted")
	}
	actual, readErr := io.ReadAll(io.LimitReader(file, int64(len(expectedBytes))+1))
	if readErr != nil || !bytes.Equal(actual, expectedBytes) {
		_ = file.Close()
		if readErr != nil {
			return nil, errors.New("atomic rooted exact evidence: read destination failed")
		}
		return nil, errors.New("atomic rooted exact evidence: destination bytes do not match")
	}
	final, err := rootedEvidenceLstat(operations, root, relativePath)
	if err != nil || final.Mode() != initial.Mode() || !os.SameFile(initial, final) || !os.SameFile(opened, final) {
		_ = file.Close()
		return nil, errors.New("atomic rooted exact evidence: destination drifted")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		_ = file.Close()
		return nil, errors.New("atomic rooted exact evidence: destination drifted")
	}
	actual, readErr = io.ReadAll(io.LimitReader(file, int64(len(expectedBytes))+1))
	after, statErr := file.Stat()
	closeErr := file.Close()
	if readErr != nil || statErr != nil || closeErr != nil || !bytes.Equal(actual, expectedBytes) || !after.Mode().IsRegular() || after.Mode() != opened.Mode() || after.Mode() != final.Mode() || !os.SameFile(after, opened) || !os.SameFile(after, final) {
		return nil, errors.New("atomic rooted exact evidence: destination drifted")
	}
	// A same-inode rewrite can still race after this final byte read.
	return after, nil
}

func sameRootedParents(first, second []rootedParentEvidence) bool {
	if len(first) != len(second) {
		return false
	}
	for i := range first {
		if first[i].path != second[i].path || first[i].info.Mode() != second[i].info.Mode() || !os.SameFile(first[i].info, second[i].info) {
			return false
		}
	}
	return true
}

func rootedEvidenceLstat(operations rootedExactEvidenceOperations, root *os.Root, name string) (fs.FileInfo, error) {
	if operations.lstat != nil {
		return operations.lstat(root, name)
	}
	return root.Lstat(name)
}

var (
	errRootedExactRemoveUnsupported = errors.New("atomic rooted exact remove: unsupported platform")
	errRootedExactRemoveFailed      = errors.New("atomic rooted exact remove: remove failed")
	errRootedExactRemoveSyncFailed  = errors.New("atomic rooted exact remove: parent directory sync failed")
	errRootedExactRemoveAbsent      = errors.New("atomic rooted exact remove: absence verification failed")
	errRootedExactRemoveCloseFailed = errors.New("atomic rooted exact remove: parent close failed")
)

type rootedExactRemovalOperations struct {
	rootedExactEvidenceOperations
	remove   func(*os.Root, string) error
	sync     func(*os.Root) error
	readback func(*os.Root, string) (fs.FileInfo, error)
	close    func(*os.Root) error
}

// RemoveIfExactRoot durably removes a regular file only when exact evidence
// matches it beneath root. Before Remove is invoked, an error proves that no
// intended publication occurred. Once Remove is invoked, callers must treat
// every result as potentially published and compensate from the exact evidence.
// Portable primitives leave a residual same-parent race between final evidence and removal.
func RemoveIfExactRoot(root *os.Root, relativePath string, expectedBytes []byte, expectedMode fs.FileMode) error {
	if runtime.GOOS == "js" || runtime.GOOS == "plan9" {
		return errRootedExactRemoveUnsupported
	}
	return removeIfExactRoot(root, relativePath, expectedBytes, expectedMode, rootedExactRemovalOperations{})
}

func removeIfExactRoot(root *os.Root, relativePath string, expectedBytes []byte, expectedMode fs.FileMode, operations rootedExactRemovalOperations) error {
	if runtime.GOOS == "js" || runtime.GOOS == "plan9" {
		return errRootedExactRemoveUnsupported
	}
	evidence, err := observeRootedExact(root, relativePath, expectedBytes, expectedMode, operations.rootedExactEvidenceOperations)
	if err != nil {
		return err
	}
	parent := evidence.parents[len(evidence.parents)-1]
	parentRoot, err := root.OpenRoot(parent.path)
	if err != nil {
		return errors.New("atomic rooted exact remove: parent drifted")
	}
	parentInfo, parentErr := parentRoot.Lstat(".")
	if parentErr != nil || parentInfo.Mode() != parent.info.Mode() || !os.SameFile(parentInfo, parent.info) {
		_ = closeRootedExactRemoval(parentRoot, operations)
		return errors.New("atomic rooted exact remove: parent drifted")
	}
	basename := relativePath[strings.LastIndex(relativePath, "/")+1:]
	leaf, leafErr := rootedEvidenceLeaf(parentRoot, basename, expectedBytes, expectedMode, operations.rootedExactEvidenceOperations)
	if leafErr != nil || leaf.Mode() != evidence.leaf.Mode() || !os.SameFile(leaf, evidence.leaf) {
		_ = closeRootedExactRemoval(parentRoot, operations)
		return errors.New("atomic rooted exact remove: destination drifted")
	}

	var failures []error
	if operations.remove != nil {
		err = operations.remove(parentRoot, basename)
	} else {
		err = parentRoot.Remove(basename)
	}
	if err != nil {
		failures = append(failures, errRootedExactRemoveFailed)
	}
	if err := syncRootedExactRemoval(parentRoot, operations); err != nil {
		failures = append(failures, errRootedExactRemoveSyncFailed)
	}
	info, readbackErr := readbackRootedExactRemoval(parentRoot, basename, operations)
	if info != nil || !errors.Is(readbackErr, fs.ErrNotExist) {
		failures = append(failures, errRootedExactRemoveAbsent)
	}
	if err := closeRootedExactRemoval(parentRoot, operations); err != nil {
		failures = append(failures, errRootedExactRemoveCloseFailed)
	}
	return errors.Join(failures...)
}

func syncRootedExactRemoval(root *os.Root, operations rootedExactRemovalOperations) error {
	if operations.sync != nil {
		return operations.sync(root)
	}
	directory, err := root.Open(".")
	if err != nil {
		return err
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	return errors.Join(syncErr, closeErr)
}

func readbackRootedExactRemoval(root *os.Root, basename string, operations rootedExactRemovalOperations) (fs.FileInfo, error) {
	if operations.readback != nil {
		return operations.readback(root, basename)
	}
	return root.Lstat(basename)
}

func closeRootedExactRemoval(root *os.Root, operations rootedExactRemovalOperations) error {
	if operations.close != nil {
		return operations.close(root)
	}
	return root.Close()
}

func exactRemovalMatches(destination string, expectedBytes []byte, expectedMode fs.FileMode, operations exactRemovalOperations) error {
	initial, err := operations.lstat(destination)
	if errors.Is(err, fs.ErrNotExist) {
		return errors.New("atomic remove exact: destination is missing")
	}
	if err != nil {
		return errors.New("atomic remove exact: inspect destination failed")
	}
	if initial.Mode()&fs.ModeSymlink != 0 || !initial.Mode().IsRegular() {
		return errors.New("atomic remove exact: destination is not a regular file")
	}
	if initial.Mode() != expectedMode {
		return errors.New("atomic remove exact: destination mode does not match")
	}

	file, err := operations.open(destination)
	if err != nil {
		return errors.New("atomic remove exact: open destination failed")
	}
	opened, statErr := file.Stat()
	if statErr != nil {
		_ = file.Close()
		return errors.New("atomic remove exact: inspect opened destination failed")
	}
	if opened.Mode()&fs.ModeSymlink != 0 || !opened.Mode().IsRegular() || opened.Mode() != expectedMode || !os.SameFile(initial, opened) {
		_ = file.Close()
		return errors.New("atomic remove exact: destination drifted")
	}
	actual, readErr := io.ReadAll(io.LimitReader(file, int64(len(expectedBytes))+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil {
		return errors.New("atomic remove exact: read destination failed")
	}
	if !bytes.Equal(actual, expectedBytes) {
		return errors.New("atomic remove exact: destination bytes do not match")
	}

	final, err := operations.lstat(destination)
	if err != nil {
		return errors.New("atomic remove exact: destination drifted")
	}
	if final.Mode()&fs.ModeSymlink != 0 || !final.Mode().IsRegular() || final.Mode() != expectedMode || !os.SameFile(initial, final) || !os.SameFile(opened, final) {
		return errors.New("atomic remove exact: destination drifted")
	}
	return nil
}

// RemoveIfMatches removes a regular file beneath root only when its bytes exactly
// match expected. A missing leaf succeeds without mutation. It revalidates the
// path and bytes before removal to reduce, but not eliminate, TOCTOU races.
func RemoveIfMatches(root, relativePath string, expected []byte) error {
	destination, exists, err := removalDestination(root, relativePath)
	if err != nil || !exists {
		return err
	}
	matches, err := removalMatches(destination, expected)
	if err != nil || !matches {
		return err
	}

	destination, exists, err = removalDestination(root, relativePath)
	if err != nil || !exists {
		return err
	}
	matches, err = removalMatches(destination, expected)
	if err != nil || !matches {
		return err
	}
	if err := os.Remove(destination); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("atomic remove: remove destination: %w", err)
	}
	if err := syncDirectory(filepath.Dir(destination)); err != nil {
		return fmt.Errorf("atomic remove: removal succeeded but directory sync or absence verification failed; caller intervention may be needed: %w", err)
	}
	info, err := os.Lstat(destination)
	if err == nil && info != nil {
		return fmt.Errorf("atomic remove: removal succeeded but directory sync or absence verification failed; caller intervention may be needed: destination remains")
	}
	if !os.IsNotExist(err) {
		return fmt.Errorf("atomic remove: removal succeeded but directory sync or absence verification failed; caller intervention may be needed: inspect destination: %w", err)
	}
	return nil
}

func removalDestination(root, relativePath string) (string, bool, error) {
	destination, err := safepath.Resolve(root, relativePath)
	if err != nil {
		return "", false, fmt.Errorf("atomic remove: destination is unsafe: %w", err)
	}
	parentInfo, err := os.Lstat(filepath.Dir(destination))
	if err != nil || !parentInfo.IsDir() || parentInfo.Mode()&os.ModeSymlink != 0 {
		return "", false, fmt.Errorf("atomic remove: destination parent is missing or invalid")
	}
	info, err := os.Lstat(destination)
	if os.IsNotExist(err) {
		return destination, false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("atomic remove: inspect destination: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", false, fmt.Errorf("atomic remove: destination is not a regular file")
	}
	return destination, true, nil
}

func removalMatches(destination string, expected []byte) (bool, error) {
	info, err := os.Lstat(destination)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("atomic remove: inspect destination: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return false, fmt.Errorf("atomic remove: destination is not a regular file")
	}
	actual, err := os.ReadFile(destination)
	if err != nil {
		return false, fmt.Errorf("atomic remove: read destination: %w", err)
	}
	if !bytes.Equal(actual, expected) {
		return false, fmt.Errorf("atomic remove: destination bytes do not match")
	}
	return true, nil
}
