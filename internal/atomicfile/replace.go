// Package atomicfile replaces one safe-root-anchored regular file at a time.
package atomicfile

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/refactor-ia/cortex/internal/safepath"
)

// Replace atomically replaces one regular file beneath root with data and mode.
// It revalidates the destination before rename but does not eliminate TOCTOU races.
// After rename, a caller-level transaction owns rollback for any reported error.
func Replace(root, relativePath string, data []byte, mode fs.FileMode) error {
	return replace(root, relativePath, data, mode, nil)
}

// ReplaceIfMatches atomically replaces an existing regular file only when its bytes
// and permission mode match expectedData and expectedMode. It rechecks the target
// immediately before rename to reduce, but not eliminate, TOCTOU exposure.
// After rename, a caller-level transaction owns rollback for any reported error.
func ReplaceIfMatches(root, relativePath string, expectedData []byte, expectedMode fs.FileMode, replacementData []byte, replacementMode fs.FileMode) error {
	if expectedMode&^fs.FileMode(0o777) != 0 {
		return fmt.Errorf("atomic replace: unsupported expected mode")
	}
	return replace(root, relativePath, replacementData, replacementMode, func(destination string) error {
		return destinationMatches(destination, expectedData, expectedMode)
	})
}

func replace(root, relativePath string, data []byte, mode fs.FileMode, matches func(string) error) (resultErr error) {
	if mode&^fs.FileMode(0o777) != 0 {
		return fmt.Errorf("atomic replace: unsupported mode")
	}

	destination, err := validateDestination(root, relativePath)
	if err != nil {
		return err
	}
	if matches != nil {
		if err := matches(destination); err != nil {
			return err
		}
	}
	parent := filepath.Dir(destination)
	temporary, err := os.CreateTemp(parent, ".cortex-replace-")
	if err != nil {
		return fmt.Errorf("atomic replace: create temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	temporaryOpen := true
	defer func() {
		if temporaryOpen {
			if err := temporary.Close(); err != nil {
				resultErr = errors.Join(resultErr, fmt.Errorf("atomic replace: close temporary file: %w", err))
			}
		}
		if temporaryPath != "" {
			if err := os.Remove(temporaryPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
				resultErr = errors.Join(resultErr, fmt.Errorf("atomic replace: remove temporary file: %w", err))
			}
		}
	}()

	if err := temporary.Chmod(mode.Perm()); err != nil {
		return fmt.Errorf("atomic replace: set temporary mode: %w", err)
	}
	written, err := temporary.Write(data)
	if err != nil {
		return fmt.Errorf("atomic replace: write temporary file: %w", err)
	}
	if written != len(data) {
		return fmt.Errorf("atomic replace: write temporary file: %w", io.ErrShortWrite)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("atomic replace: sync temporary file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		temporaryOpen = false
		return fmt.Errorf("atomic replace: close temporary file: %w", err)
	}
	temporaryOpen = false

	destination, err = validateDestination(root, relativePath)
	if err != nil {
		return err
	}
	if matches != nil {
		if err := matches(destination); err != nil {
			return err
		}
	}
	if err := os.Rename(temporaryPath, destination); err != nil {
		return fmt.Errorf("atomic replace: rename temporary file: %w", err)
	}
	temporaryPath = ""

	if err := syncDirectory(parent); err != nil {
		return err
	}
	if err := verifyReplacement(destination, data, mode.Perm()); err != nil {
		return err
	}
	return nil
}

func validateDestination(root, relativePath string) (string, error) {
	destination, err := safepath.Resolve(root, relativePath)
	if err != nil {
		return "", fmt.Errorf("atomic replace: destination is unsafe: %w", err)
	}
	parentInfo, err := os.Lstat(filepath.Dir(destination))
	if err != nil || !parentInfo.IsDir() || parentInfo.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("atomic replace: destination parent is missing or invalid")
	}
	info, err := os.Lstat(destination)
	if os.IsNotExist(err) {
		return destination, nil
	}
	if err != nil {
		return "", fmt.Errorf("atomic replace: inspect destination: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", fmt.Errorf("atomic replace: destination is not a regular file")
	}
	return destination, nil
}

type rootedReplaceReadFile interface {
	Stat() (fs.FileInfo, error)
	Read([]byte) (int, error)
	Seek(int64, int) (int64, error)
	Close() error
}

type rootedReplaceEvidenceOperations struct {
	lstat    func(*os.Root, string) (fs.FileInfo, error)
	openRoot func(*os.Root, string) (*os.Root, error)
	openRead func(*os.Root, string) (rootedReplaceReadFile, error)
	close    func(*os.Root) error
}

var errRootedReplaceParentClose, errRootedReplaceDestinationClose = errors.New("atomic rooted replace evidence: parent close failed"), errors.New("atomic rooted replace evidence: destination close failed")

// rootedReplaceEvidence retains only descriptor-anchored proof for a future
// ReplaceIfMatchesRoot. It must be discarded after the immediate recheck.
type rootedReplaceEvidence struct {
	parent   *os.Root
	basename string
	leaf     fs.FileInfo
	expected []byte
	mode     fs.FileMode
}

func observeRootedReplaceEvidence(root *os.Root, relativePath string, expected []byte, mode fs.FileMode, operations rootedReplaceEvidenceOperations) (evidence rootedReplaceEvidence, resultErr error) {
	if runtime.GOOS == "js" || runtime.GOOS == "plan9" {
		return evidence, errors.New("atomic rooted replace evidence: unsupported platform")
	}
	if root == nil || !validRootedReplacePath(relativePath) || expected == nil || len(expected) > removeExactMaxEvidenceBytes || mode&^fs.FileMode(0o777) != 0 || mode&0o400 == 0 {
		return evidence, errors.New("atomic rooted replace evidence: invalid input")
	}
	detached := make([]byte, len(expected))
	copy(detached, expected)
	expected = detached
	rootInfo, err := rootedReplaceLstat(operations, root, ".")
	if err != nil || rootInfo == nil || !rootInfo.IsDir() || rootInfo.Mode()&fs.ModeSymlink != 0 {
		return evidence, errors.New("atomic rooted replace evidence: invalid root")
	}
	parent, err := rootedReplaceOpenRoot(operations, root, ".")
	if err != nil || parent == nil {
		return evidence, rootedReplaceJoinClose(errors.New("atomic rooted replace evidence: open parent failed"), closeRootedReplace(&parent, operations), errRootedReplaceParentClose)
	}
	defer func() {
		if closeErr := closeRootedReplace(&parent, operations); closeErr != nil {
			evidence = rootedReplaceEvidence{}
			resultErr = rootedReplaceJoinClose(resultErr, closeErr, errRootedReplaceParentClose)
		}
	}()
	anchored, err := rootedReplaceLstat(operations, parent, ".")
	if err != nil || !sameRootedReplaceDirectory(anchored, rootInfo) {
		return evidence, errors.New("atomic rooted replace evidence: parent drifted")
	}
	parts := strings.Split(relativePath, "/")
	for _, component := range parts[:len(parts)-1] {
		info, err := rootedReplaceLstat(operations, parent, component)
		if err != nil || !sameRootedReplaceDirectory(info, info) {
			return evidence, errors.New("atomic rooted replace evidence: parent is missing or invalid")
		}
		next, openErr := rootedReplaceOpenRoot(operations, parent, component)
		if openErr != nil || next == nil {
			return evidence, rootedReplaceJoinClose(errors.New("atomic rooted replace evidence: open parent failed"), closeRootedReplace(&next, operations), errRootedReplaceParentClose)
		}
		anchored, anchorErr := rootedReplaceLstat(operations, next, ".")
		if anchorErr != nil || !sameRootedReplaceDirectory(anchored, info) {
			return evidence, rootedReplaceJoinClose(errors.New("atomic rooted replace evidence: parent drifted"), closeRootedReplace(&next, operations), errRootedReplaceParentClose)
		}
		if closeRootedReplace(&parent, operations) != nil {
			_ = closeRootedReplace(&next, operations)
			return evidence, errRootedReplaceParentClose
		}
		parent = next
	}
	leaf, err := rootedReplaceLeaf(parent, parts[len(parts)-1], expected, mode, operations)
	if err != nil {
		return evidence, err
	}
	evidence = rootedReplaceEvidence{parent: parent, basename: parts[len(parts)-1], leaf: leaf, expected: expected, mode: mode}
	parent = nil
	return evidence, nil
}

func recheckRootedReplaceEvidence(evidence *rootedReplaceEvidence, operations rootedReplaceEvidenceOperations) error {
	if evidence == nil || evidence.parent == nil || evidence.basename == "" || evidence.leaf == nil || evidence.expected == nil {
		return errors.New("atomic rooted replace evidence: invalid evidence")
	}
	leaf, err := rootedReplaceLeaf(evidence.parent, evidence.basename, evidence.expected, evidence.mode, operations)
	if err != nil || !os.SameFile(leaf, evidence.leaf) || !rootedCreateModeOK(leaf.Mode(), evidence.mode, runtime.GOOS == "windows") {
		return errors.New("atomic rooted replace evidence: destination drifted")
	}
	// A replacement after the final Lstat, or a same-inode rewrite after the final
	// byte/path check, remains a residual race for the future Rename caller.
	return nil
}

func discardRootedReplaceEvidence(evidence *rootedReplaceEvidence, operations rootedReplaceEvidenceOperations) error {
	if evidence == nil || evidence.parent == nil {
		return errors.New("atomic rooted replace evidence: invalid evidence")
	}
	parent := evidence.parent
	*evidence = rootedReplaceEvidence{}
	if closeRootedReplace(&parent, operations) != nil {
		return errors.New("atomic rooted replace evidence: parent close failed")
	}
	return nil
}

type rootedReplaceStage struct {
	parent              *os.Root
	basename, temporary string
	leaf, info          fs.FileInfo
	expected, data      []byte
	expectedMode, mode  fs.FileMode
}

type rootedReplaceStageFile interface {
	Chmod(fs.FileMode) error
	Write([]byte) (int, error)
	Sync() error
	Stat() (fs.FileInfo, error)
	Close() error
}

type rootedReplaceStagingOperations struct {
	random      io.Reader
	openFile    func(*os.Root, string, int, fs.FileMode) (rootedReplaceStageFile, error)
	remove      func(*os.Root, string) error
	syncParent  func(*os.Root) error
	closeParent func(*os.Root) error
}

// stageRootedReplace consumes descriptor-anchored replacement evidence and creates a
// private, durable replacement inode. Publication remains the caller's responsibility.
func stageRootedReplace(evidence *rootedReplaceEvidence, replacement []byte, replacementMode fs.FileMode, operations rootedReplaceStagingOperations) (stage rootedReplaceStage, resultErr error) {
	if !validRootedReplaceStageEvidence(evidence) || replacement == nil || len(replacement) > removeExactMaxEvidenceBytes || replacementMode&^fs.FileMode(0o777) != 0 || replacementMode&0o400 == 0 {
		return stage, errors.New("atomic rooted replace: invalid staging input")
	}
	detached := make([]byte, len(replacement))
	copy(detached, replacement)
	stage = rootedReplaceStage{parent: evidence.parent, basename: evidence.basename, leaf: evidence.leaf, expected: evidence.expected, expectedMode: evidence.mode, data: detached, mode: replacementMode}
	*evidence = rootedReplaceEvidence{}
	var (
		file rootedReplaceStageFile
		err  error
	)
	defer func() {
		if resultErr == nil {
			return
		}
		if err := closeRootedReplaceStageFile(&file); err != nil {
			resultErr = errors.Join(resultErr, err)
		}
		if err := cleanupRootedReplaceStage(&stage, operations); err != nil {
			resultErr = errors.Join(resultErr, err)
		}
	}()
	reader := operations.random
	if reader == nil {
		reader = rand.Reader
	}
	for attempts := 0; attempts < 128; attempts++ {
		entropy := make([]byte, 16)
		if _, err := io.ReadFull(reader, entropy); err != nil {
			return stage, errors.New("atomic rooted replace: temporary entropy failed")
		}
		temporary := ".cortex-replace-" + hex.EncodeToString(entropy)
		file, err = rootedReplaceOpenStageFile(operations, stage.parent, temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, fs.ErrExist) {
			if err := closeRootedReplaceStageFile(&file); err != nil {
				return stage, err
			}
			continue
		}
		if err != nil || file == nil {
			openErr := error(errors.New("atomic rooted replace: open temporary failed"))
			if closeErr := closeRootedReplaceStageFile(&file); closeErr != nil {
				openErr = errors.Join(openErr, closeErr)
			}
			return stage, openErr
		}
		stage.temporary = temporary
		break
	}
	if file == nil {
		return stage, errors.New("atomic rooted replace: temporary collision limit reached")
	}
	if err := file.Chmod(replacementMode.Perm()); err != nil {
		return stage, errors.New("atomic rooted replace: set temporary mode failed")
	}
	for remaining := stage.data; len(remaining) > 0; {
		written, err := file.Write(remaining)
		if written < 0 || written > len(remaining) || written == 0 && err == nil {
			return stage, errors.New("atomic rooted replace: write temporary failed")
		}
		remaining = remaining[written:]
		if err != nil {
			return stage, errors.New("atomic rooted replace: write temporary failed")
		}
	}
	if err := file.Sync(); err != nil {
		return stage, errors.New("atomic rooted replace: sync temporary failed")
	}
	info, err := file.Stat()
	if err != nil || info == nil || !info.Mode().IsRegular() || !rootedCreateModeOK(info.Mode(), replacementMode, runtime.GOOS == "windows") || info.Size() != int64(len(stage.data)) {
		return stage, errors.New("atomic rooted replace: validate temporary failed")
	}
	if err := closeRootedReplaceStageFile(&file); err != nil {
		return stage, err
	}
	stage.info = info
	return stage, nil
}

func discardRootedReplaceStage(stage *rootedReplaceStage, operations rootedReplaceStagingOperations) error {
	if stage == nil {
		return errors.New("atomic rooted replace: invalid stage")
	}
	owned := *stage
	*stage = rootedReplaceStage{}
	valid := owned.parent != nil && owned.basename != "" && owned.temporary != "" && owned.leaf != nil && owned.expected != nil && owned.data != nil && owned.info != nil
	result := error(nil)
	if !valid {
		result = errors.New("atomic rooted replace: invalid stage")
	} else {
		if err := rootedReplaceRemove(operations, owned.parent, owned.temporary); err != nil {
			result = errors.Join(result, errors.New("atomic rooted replace: remove temporary failed"))
		}
		if err := rootedReplaceSyncParent(operations, owned.parent); err != nil {
			result = errors.Join(result, errors.New("atomic rooted replace: sync parent failed"))
		}
	}
	if err := rootedReplaceCloseParent(operations, owned.parent); err != nil {
		result = errors.Join(result, errors.New("atomic rooted replace: close parent failed"))
	}
	return result
}

func validRootedReplaceStageEvidence(evidence *rootedReplaceEvidence) bool {
	return evidence != nil && evidence.parent != nil && evidence.basename != "" && evidence.leaf != nil && evidence.expected != nil && evidence.mode&^fs.FileMode(0o777) == 0 && evidence.mode&0o400 != 0 && rootedReplaceFileOK(evidence.leaf, evidence.mode, len(evidence.expected))
}

func cleanupRootedReplaceStage(stage *rootedReplaceStage, operations rootedReplaceStagingOperations) error {
	if stage == nil {
		return nil
	}
	owned := *stage
	*stage = rootedReplaceStage{}
	result := error(nil)
	if owned.temporary != "" {
		if err := rootedReplaceRemove(operations, owned.parent, owned.temporary); err != nil {
			result = errors.Join(result, errors.New("atomic rooted replace: remove temporary failed"))
		}
		if err := rootedReplaceSyncParent(operations, owned.parent); err != nil {
			result = errors.Join(result, errors.New("atomic rooted replace: sync parent failed"))
		}
	}
	if err := rootedReplaceCloseParent(operations, owned.parent); err != nil {
		result = errors.Join(result, errors.New("atomic rooted replace: close parent failed"))
	}
	return result
}

func closeRootedReplaceStageFile(file *rootedReplaceStageFile) error {
	if *file == nil {
		return nil
	}
	owned := *file
	*file = nil
	if err := owned.Close(); err != nil {
		return errors.New("atomic rooted replace: close temporary failed")
	}
	return nil
}

func rootedReplaceOpenStageFile(operations rootedReplaceStagingOperations, parent *os.Root, name string, flag int, mode fs.FileMode) (rootedReplaceStageFile, error) {
	if operations.openFile != nil {
		return operations.openFile(parent, name, flag, mode)
	}
	return parent.OpenFile(name, flag, mode)
}

func rootedReplaceRemove(operations rootedReplaceStagingOperations, parent *os.Root, name string) error {
	if operations.remove != nil {
		return operations.remove(parent, name)
	}
	return parent.Remove(name)
}

func rootedReplaceSyncParent(operations rootedReplaceStagingOperations, parent *os.Root) error {
	if operations.syncParent != nil {
		return operations.syncParent(parent)
	}
	file, err := parent.Open(".")
	if err != nil {
		return err
	}
	return errors.Join(file.Sync(), file.Close())
}

func rootedReplaceCloseParent(operations rootedReplaceStagingOperations, parent *os.Root) error {
	if parent == nil {
		return nil
	}
	if operations.closeParent != nil {
		return operations.closeParent(parent)
	}
	return parent.Close()
}

func validRootedReplacePath(relativePath string) bool {
	return relativePath != "" && !path.IsAbs(relativePath) && !strings.Contains(relativePath, `\`) && path.Clean(relativePath) == relativePath && relativePath != "." && relativePath != ".." && !strings.HasPrefix(relativePath, "../")
}

func rootedReplaceLeaf(root *os.Root, basename string, expected []byte, mode fs.FileMode, operations rootedReplaceEvidenceOperations) (info fs.FileInfo, resultErr error) {
	initial, err := rootedReplaceLstat(operations, root, basename)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, errors.New("atomic rooted replace evidence: destination is missing")
	}
	if err != nil || !rootedReplaceFileOK(initial, mode, len(expected)) {
		return nil, errors.New("atomic rooted replace evidence: destination does not match")
	}
	file, err := rootedReplaceOpenRead(operations, root, basename)
	if err != nil || file == nil {
		resultErr = errors.New("atomic rooted replace evidence: open destination failed")
		if file != nil && file.Close() != nil {
			return nil, errors.Join(resultErr, errRootedReplaceDestinationClose)
		}
		return nil, resultErr
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			info = nil
			resultErr = rootedReplaceJoinClose(resultErr, closeErr, errRootedReplaceDestinationClose)
		}
	}()
	opened, err := file.Stat()
	if err != nil || !rootedReplaceFileOK(opened, mode, len(expected)) || !os.SameFile(initial, opened) {
		return nil, errors.New("atomic rooted replace evidence: destination drifted")
	}
	actual, err := io.ReadAll(io.LimitReader(file, int64(len(expected))+1))
	if err != nil {
		return nil, errors.New("atomic rooted replace evidence: read destination failed")
	}
	if !bytes.Equal(actual, expected) {
		return nil, errors.New("atomic rooted replace evidence: destination bytes do not match")
	}
	late, err := rootedReplaceLstat(operations, root, basename)
	if err != nil || !rootedReplaceFileOK(late, mode, len(expected)) || !os.SameFile(initial, late) || !os.SameFile(opened, late) {
		return nil, errors.New("atomic rooted replace evidence: destination drifted")
	}
	if offset, err := file.Seek(0, io.SeekStart); err != nil || offset != 0 {
		return nil, errors.New("atomic rooted replace evidence: destination drifted")
	}
	actual, err = io.ReadAll(io.LimitReader(file, int64(len(expected))+1))
	final, statErr := file.Stat()
	pathInfo, pathErr := rootedReplaceLstat(operations, root, basename)
	if err != nil || statErr != nil || pathErr != nil || !bytes.Equal(actual, expected) || !rootedReplaceFileOK(final, mode, len(expected)) || !rootedReplaceFileOK(pathInfo, mode, len(expected)) || !os.SameFile(initial, final) || !os.SameFile(initial, pathInfo) || !os.SameFile(final, pathInfo) {
		return nil, errors.New("atomic rooted replace evidence: destination drifted")
	}
	return final, nil
}

func rootedReplaceFileOK(info fs.FileInfo, mode fs.FileMode, size int) bool {
	return info != nil && info.Mode()&fs.ModeSymlink == 0 && info.Mode().IsRegular() && rootedCreateModeOK(info.Mode(), mode, runtime.GOOS == "windows") && info.Size() == int64(size)
}

func sameRootedReplaceDirectory(a, b fs.FileInfo) bool {
	return a != nil && b != nil && a.IsDir() && b.IsDir() && a.Mode()&fs.ModeSymlink == 0 && b.Mode()&fs.ModeSymlink == 0 && os.SameFile(a, b)
}

func rootedReplaceLstat(operations rootedReplaceEvidenceOperations, root *os.Root, name string) (fs.FileInfo, error) {
	if operations.lstat != nil {
		return operations.lstat(root, name)
	}
	return root.Lstat(name)
}

func rootedReplaceOpenRoot(operations rootedReplaceEvidenceOperations, root *os.Root, name string) (*os.Root, error) {
	if operations.openRoot != nil {
		return operations.openRoot(root, name)
	}
	return root.OpenRoot(name)
}

func rootedReplaceOpenRead(operations rootedReplaceEvidenceOperations, root *os.Root, name string) (rootedReplaceReadFile, error) {
	if operations.openRead != nil {
		return operations.openRead(root, name)
	}
	return root.Open(name)
}

func rootedReplaceJoinClose(result, closeErr, category error) error {
	if closeErr != nil {
		return errors.Join(result, category)
	}
	return result
}

func closeRootedReplace(root **os.Root, operations rootedReplaceEvidenceOperations) error {
	if *root == nil {
		return nil
	}
	owned := *root
	*root = nil
	if operations.close != nil {
		return operations.close(owned)
	}
	return owned.Close()
}

func destinationMatches(destination string, expectedData []byte, expectedMode fs.FileMode) error {
	info, err := os.Lstat(destination)
	if os.IsNotExist(err) {
		return fmt.Errorf("atomic replace: destination is missing")
	}
	if err != nil {
		return fmt.Errorf("atomic replace: inspect destination: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("atomic replace: destination is not a regular file")
	}
	actual, err := os.ReadFile(destination)
	if err != nil {
		return fmt.Errorf("atomic replace: read destination: %w", err)
	}
	if !bytes.Equal(actual, expectedData) {
		return fmt.Errorf("atomic replace: destination bytes do not match")
	}
	if info.Mode().Perm() != expectedMode.Perm() {
		return fmt.Errorf("atomic replace: destination mode does not match")
	}
	return nil
}

func syncDirectory(path string) (resultErr error) {
	directory, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("atomic replace: open destination directory: %w", err)
	}
	defer func() {
		if err := directory.Close(); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("atomic replace: close destination directory: %w", err))
		}
	}()
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("atomic replace: sync destination directory: %w", err)
	}
	return nil
}

func verifyReplacement(destination string, data []byte, mode fs.FileMode) error {
	info, err := os.Lstat(destination)
	if err != nil {
		return fmt.Errorf("atomic replace: inspect replacement: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("atomic replace: replacement is not a regular file")
	}
	actual, err := os.ReadFile(destination)
	if err != nil {
		return fmt.Errorf("atomic replace: read replacement: %w", err)
	}
	if !bytes.Equal(actual, data) {
		return fmt.Errorf("atomic replace: replacement bytes do not match")
	}
	if info.Mode().Perm() != mode {
		return fmt.Errorf("atomic replace: replacement mode does not match")
	}
	return nil
}
