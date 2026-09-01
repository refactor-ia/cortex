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
)

// CreateIfAbsent durably creates a regular file beneath root only when the leaf
// is absent. It stages and syncs the data before publishing it with a hard link,
// which cannot overwrite a concurrently created path, then verifies the result.
// Filesystems that do not support hard links return an error: there is no portable
// fallback that preserves both atomic publication and create-if-absent semantics.
// After publication, a caller-level transaction owns rollback for any error.
// The final absence check immediately before Link is mandatory: Link alone cannot
// establish rooted-path evidence.
func CreateIfAbsent(root, relativePath string, data []byte, mode fs.FileMode) (resultErr error) {
	if mode&^fs.FileMode(0o777) != 0 {
		return fmt.Errorf("atomic create: unsupported mode")
	}
	destination, err := validateDestination(root, relativePath)
	if err != nil {
		return err
	}
	if err := destinationAbsent(destination); err != nil {
		return err
	}
	parent := filepath.Dir(destination)
	temporary, err := os.CreateTemp(parent, ".cortex-create-")
	if err != nil {
		return fmt.Errorf("atomic create: create temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	temporaryOpen := true
	defer func() {
		if temporaryOpen {
			if err := temporary.Close(); err != nil {
				resultErr = errors.Join(resultErr, fmt.Errorf("atomic create: close temporary file: %w", err))
			}
		}
		if temporaryPath != "" {
			if err := os.Remove(temporaryPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
				resultErr = errors.Join(resultErr, fmt.Errorf("atomic create: remove temporary file: %w", err))
			}
		}
	}()
	if err := temporary.Chmod(mode.Perm()); err != nil {
		return fmt.Errorf("atomic create: set temporary mode: %w", err)
	}
	written, err := temporary.Write(data)
	if err != nil {
		return fmt.Errorf("atomic create: write temporary file: %w", err)
	}
	if written != len(data) {
		return fmt.Errorf("atomic create: write temporary file: %w", io.ErrShortWrite)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("atomic create: sync temporary file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("atomic create: close temporary file: %w", err)
	}
	temporaryOpen = false

	destination, err = validateDestination(root, relativePath)
	if err != nil {
		return err
	}
	if err := destinationAbsent(destination); err != nil {
		return err
	}
	if err := os.Link(temporaryPath, destination); err != nil {
		if absentErr := destinationAbsent(destination); absentErr != nil {
			return absentErr
		}
		return fmt.Errorf("atomic create: link temporary file: %w", err)
	}
	if err := os.Remove(temporaryPath); err != nil {
		return fmt.Errorf("atomic create: remove temporary file: %w", err)
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

type rootedAbsentEvidence struct {
	parent   *os.Root
	basename string
	mode     fs.FileMode
}

type rootedAbsentEvidenceOperations struct {
	lstat    func(*os.Root, string) (fs.FileInfo, error)
	openRoot func(*os.Root, string) (*os.Root, error)
	close    func(*os.Root) error
}

// observeRootedAbsent captures a rooted parent whose named leaf was absent. The
// caller owns evidence.parent and must close it after its immediate pre-Link
// absence recheck.
func observeRootedAbsent(root *os.Root, relativePath string, mode fs.FileMode, operations rootedAbsentEvidenceOperations) (rootedAbsentEvidence, error) {
	if runtime.GOOS == "js" || runtime.GOOS == "plan9" {
		return rootedAbsentEvidence{}, errors.New("atomic rooted absent evidence: unsupported platform")
	}
	if root == nil || mode&^fs.FileMode(0o777) != 0 || !validRootedAbsentPath(relativePath) {
		return rootedAbsentEvidence{}, errors.New("atomic rooted absent evidence: invalid input")
	}
	info, err := rootedAbsentLstat(operations, root, ".")
	if err != nil || info == nil || info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() {
		return rootedAbsentEvidence{}, errors.New("atomic rooted absent evidence: invalid root")
	}
	parent, err := rootedAbsentOpenRoot(operations, root, ".")
	if err != nil || parent == nil {
		closeRootedAbsent(&parent, operations)
		return rootedAbsentEvidence{}, errors.New("atomic rooted absent evidence: open parent failed")
	}
	defer closeRootedAbsent(&parent, operations)

	parts := strings.Split(relativePath, "/")
	for _, component := range parts[:len(parts)-1] {
		info, err := rootedAbsentLstat(operations, parent, component)
		if err != nil || info == nil || info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() {
			return rootedAbsentEvidence{}, errors.New("atomic rooted absent evidence: parent is missing or invalid")
		}
		next, openErr := rootedAbsentOpenRoot(operations, parent, component)
		if openErr != nil || next == nil {
			closeRootedAbsent(&next, operations)
			return rootedAbsentEvidence{}, errors.New("atomic rooted absent evidence: open parent failed")
		}
		anchored, anchorErr := rootedAbsentLstat(operations, next, ".")
		if anchorErr != nil || anchored == nil || anchored.Mode() != info.Mode() || !os.SameFile(anchored, info) {
			closeRootedAbsent(&next, operations)
			return rootedAbsentEvidence{}, errors.New("atomic rooted absent evidence: parent drifted")
		}
		if closeErr := closeRootedAbsent(&parent, operations); closeErr != nil {
			closeRootedAbsent(&next, operations)
			return rootedAbsentEvidence{}, errors.New("atomic rooted absent evidence: parent close failed")
		}
		parent = next
	}
	info, err = rootedAbsentLstat(operations, parent, parts[len(parts)-1])
	if info != nil || !errors.Is(err, fs.ErrNotExist) {
		return rootedAbsentEvidence{}, errors.New("atomic rooted absent evidence: destination is not absent")
	}
	evidence := rootedAbsentEvidence{parent: parent, basename: parts[len(parts)-1], mode: mode}
	parent = nil
	return evidence, nil
}

func validRootedAbsentPath(relativePath string) bool {
	return relativePath != "" && !path.IsAbs(relativePath) && !strings.Contains(relativePath, `\`) && path.Clean(relativePath) == relativePath && relativePath != "." && relativePath != ".." && !strings.HasPrefix(relativePath, "../")
}

func rootedAbsentLstat(operations rootedAbsentEvidenceOperations, root *os.Root, name string) (fs.FileInfo, error) {
	if operations.lstat != nil {
		return operations.lstat(root, name)
	}
	return root.Lstat(name)
}

func rootedAbsentOpenRoot(operations rootedAbsentEvidenceOperations, root *os.Root, name string) (*os.Root, error) {
	if operations.openRoot != nil {
		return operations.openRoot(root, name)
	}
	return root.OpenRoot(name)
}

func closeRootedAbsent(root **os.Root, operations rootedAbsentEvidenceOperations) error {
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

type rootedCreateStage struct {
	parent              *os.Root
	basename, temporary string
	info                fs.FileInfo
	mode                fs.FileMode
	data                []byte
}
type rootedCreateStageFile interface {
	Chmod(fs.FileMode) error
	Write([]byte) (int, error)
	Sync() error
	Stat() (fs.FileInfo, error)
	Close() error
}
type rootedCreateStagingOperations struct {
	rootedAbsentEvidenceOperations
	random      io.Reader
	openFile    func(*os.Root, string, int, fs.FileMode) (rootedCreateStageFile, error)
	remove      func(*os.Root, string) error
	syncParent  func(*os.Root) error
	closeParent func(*os.Root) error
}

func rootedCreateModeOK(a, b fs.FileMode, windows bool) bool {
	return !windows && a == b || windows && a&0o200 == b&0o200
}
func stageRootedCreate(root *os.Root, relativePath string, data []byte, mode fs.FileMode, operations rootedCreateStagingOperations) (stage rootedCreateStage, resultErr error) {
	evidence, err := observeRootedAbsent(root, relativePath, mode, operations.rootedAbsentEvidenceOperations)
	if err != nil {
		return stage, errors.New("atomic rooted create: absent evidence failed")
	}
	stage.parent, stage.basename, stage.mode = evidence.parent, evidence.basename, evidence.mode
	var file rootedCreateStageFile
	defer func() {
		if resultErr == nil {
			return
		}
		if err := closeRootedCreateFile(&file); err != nil {
			resultErr = errors.Join(resultErr, err)
		}
		if err := discardRootedCreateStage(&stage, operations); err != nil {
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
			return stage, errors.New("atomic rooted create: temporary entropy failed")
		}
		temporary := ".cortex-create-" + hex.EncodeToString(entropy)
		file, err = rootedCreateOpenFile(operations, stage.parent, temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, fs.ErrExist) {
			if err := closeRootedCreateFile(&file); err != nil {
				return stage, err
			}
			continue
		}
		if err != nil || file == nil {
			return stage, errors.New("atomic rooted create: open temporary failed")
		}
		stage.temporary = temporary
		break
	}
	if file == nil {
		return stage, errors.New("atomic rooted create: temporary collision limit reached")
	}
	if err := file.Chmod(mode.Perm()); err != nil {
		return stage, errors.New("atomic rooted create: set temporary mode failed")
	}
	for remaining := data; len(remaining) > 0; {
		written, err := file.Write(remaining)
		if written < 0 || written > len(remaining) || (written == 0 && err == nil) {
			return stage, errors.New("atomic rooted create: write temporary failed")
		}
		remaining = remaining[written:]
		if err != nil {
			return stage, errors.New("atomic rooted create: write temporary failed")
		}
	}
	if err := file.Sync(); err != nil {
		return stage, errors.New("atomic rooted create: sync temporary failed")
	}
	info, err := file.Stat()
	if err != nil || info == nil || !info.Mode().IsRegular() || !rootedCreateModeOK(info.Mode(), mode, runtime.GOOS == "windows") {
		return stage, errors.New("atomic rooted create: validate temporary failed")
	}
	if err := closeRootedCreateFile(&file); err != nil {
		return stage, err
	}
	stage.info, stage.data = info, append([]byte(nil), data...)
	return stage, nil
}
func discardRootedCreateStage(stage *rootedCreateStage, operations rootedCreateStagingOperations) error {
	if stage == nil {
		return nil
	}
	parent, temporary := stage.parent, stage.temporary
	*stage = rootedCreateStage{}
	var result error
	if temporary != "" {
		if err := rootedCreateRemove(operations, parent, temporary); err != nil {
			result = errors.Join(result, errors.New("atomic rooted create: remove temporary failed"))
		}
		if err := rootedCreateSyncParent(operations, parent); err != nil {
			result = errors.Join(result, errors.New("atomic rooted create: sync parent failed"))
		}
	}
	if err := rootedCreateCloseParent(operations, parent); err != nil {
		result = errors.Join(result, errors.New("atomic rooted create: close parent failed"))
	}
	return result
}
func closeRootedCreateFile(file *rootedCreateStageFile) error {
	if *file == nil {
		return nil
	}
	owned := *file
	*file = nil
	if err := owned.Close(); err != nil {
		return errors.New("atomic rooted create: close temporary failed")
	}
	return nil
}
func rootedCreateOpenFile(operations rootedCreateStagingOperations, parent *os.Root, name string, flag int, mode fs.FileMode) (rootedCreateStageFile, error) {
	if operations.openFile != nil {
		return operations.openFile(parent, name, flag, mode)
	}
	return parent.OpenFile(name, flag, mode)
}
func rootedCreateRemove(operations rootedCreateStagingOperations, parent *os.Root, name string) error {
	if operations.remove != nil {
		return operations.remove(parent, name)
	}
	return parent.Remove(name)
}
func rootedCreateSyncParent(operations rootedCreateStagingOperations, parent *os.Root) error {
	if operations.syncParent != nil {
		return operations.syncParent(parent)
	}
	file, err := parent.Open(".")
	if err != nil {
		return err
	}
	syncErr, closeErr := file.Sync(), file.Close()
	return errors.Join(syncErr, closeErr)
}
func rootedCreateCloseParent(operations rootedCreateStagingOperations, parent *os.Root) error {
	if parent == nil {
		return nil
	}
	if operations.closeParent != nil {
		return operations.closeParent(parent)
	}
	return parent.Close()
}

// ErrCreateIfAbsentRootPublicationAttempted reports that CreateIfAbsentRoot invoked
// its publication Link. A result with this marker but without
// ErrCreateIfAbsentRootPublicationVerified is ambiguous or rival and must not be
// compensated by a later executor.
var ErrCreateIfAbsentRootPublicationAttempted = errors.New("atomic rooted create: publication attempted")

// ErrCreateIfAbsentRootPublicationVerified reports that CreateIfAbsentRoot proved
// the linked destination before returning. It is returned with
// ErrCreateIfAbsentRootPublicationAttempted; this pair permits a future exact
// removal rollback despite cleanup errors.
var ErrCreateIfAbsentRootPublicationVerified = errors.New("atomic rooted create: publication verified")

type rootedCreateOperations struct {
	rootedCreateFinalizationOperations
	link func(*os.Root, string, string) error
}

// CreateIfAbsentRoot durably creates a regular file at relativePath only if it is
// absent beneath root. It retains descriptor-anchored parents and publishes with
// Root.Link, never falling back to path-based publication. Modes must include
// owner-read permission because final verification reads the staged file. An error
// marked ErrCreateIfAbsentRootPublicationAttempted without
// ErrCreateIfAbsentRootPublicationVerified is ambiguous or rival and must not be
// compensated; both markers permit future exact-removal rollback.
func CreateIfAbsentRoot(root *os.Root, relativePath string, data []byte, mode fs.FileMode) error {
	return createIfAbsentRoot(root, relativePath, data, mode, rootedCreateOperations{})
}

func createIfAbsentRoot(root *os.Root, relativePath string, data []byte, mode fs.FileMode, operations rootedCreateOperations) error {
	if mode&^fs.FileMode(0o777) != 0 || mode&0o400 == 0 {
		return errors.New("atomic rooted create: invalid mode")
	}
	stage, err := stageRootedCreate(root, relativePath, data, mode, operations.rootedCreateFinalizationOperations.rootedCreateStagingOperations)
	if err != nil {
		return err
	}
	info, err := rootedCreateReadbackLstat(operations.rootedCreateFinalizationOperations, stage.parent, stage.basename)
	if info != nil || !errors.Is(err, fs.ErrNotExist) {
		return errors.Join(
			errors.New("atomic rooted create: pre-publication failed"),
			errors.New("atomic rooted create: final absence recheck failed"),
			discardRootedCreateStage(&stage, operations.rootedCreateFinalizationOperations.rootedCreateStagingOperations),
		)
	}
	linkErr := rootedCreateLink(operations, stage.parent, stage.temporary, stage.basename)
	verified, finalizeErr := finalizeRootedCreateStage(&stage, operations.rootedCreateFinalizationOperations)
	if linkErr == nil && verified && finalizeErr == nil {
		return nil
	}
	result := error(ErrCreateIfAbsentRootPublicationAttempted)
	if verified {
		result = errors.Join(result, ErrCreateIfAbsentRootPublicationVerified)
	}
	if linkErr != nil {
		result = errors.Join(result, errors.New("atomic rooted create: link failed"))
	}
	if finalizeErr != nil {
		result = errors.Join(result, finalizeErr)
	}
	return result
}

func rootedCreateLink(operations rootedCreateOperations, parent *os.Root, temporary, basename string) error {
	if operations.link != nil {
		return operations.link(parent, temporary, basename)
	}
	return parent.Link(temporary, basename)
}

type rootedCreateReadbackFile interface {
	Stat() (fs.FileInfo, error)
	Read([]byte) (int, error)
	Seek(int64, int) (int64, error)
	Close() error
}

type rootedCreateFinalizationOperations struct {
	rootedCreateStagingOperations
	openRead func(*os.Root, string) (rootedCreateReadbackFile, error)
}

// finalizeRootedCreateStage consumes a staged file after its caller has linked it.
// It never publishes or otherwise changes the destination. A future exported
// CreateIfAbsentRoot must reject modes without owner-read permission before
// staging: this finalizer cannot reopen an unreadable stage. Replacement after
// its final Lstat remains outside the verified interval.
func finalizeRootedCreateStage(stage *rootedCreateStage, operations rootedCreateFinalizationOperations) (verified bool, resultErr error) {
	if stage == nil {
		return false, errors.New("atomic rooted create: invalid stage")
	}
	owned := *stage
	*stage = rootedCreateStage{}
	if owned.parent == nil || owned.temporary == "" || owned.basename == "" || owned.info == nil {
		resultErr = errors.New("atomic rooted create: invalid stage")
		if err := rootedCreateCloseParent(operations.rootedCreateStagingOperations, owned.parent); err != nil {
			resultErr = errors.Join(resultErr, errors.New("atomic rooted create: close parent failed"))
		}
		return false, resultErr
	}
	if err := rootedCreateRemove(operations.rootedCreateStagingOperations, owned.parent, owned.temporary); err != nil {
		resultErr = errors.Join(resultErr, errors.New("atomic rooted create: remove temporary failed"))
	}
	if err := rootedCreateSyncParent(operations.rootedCreateStagingOperations, owned.parent); err != nil {
		resultErr = errors.Join(resultErr, errors.New("atomic rooted create: sync parent failed"))
	}
	readbackVerified, readbackErr := verifyRootedCreateReadback(owned, operations)
	verified = readbackVerified
	resultErr = errors.Join(resultErr, readbackErr)
	if err := rootedCreateCloseParent(operations.rootedCreateStagingOperations, owned.parent); err != nil {
		resultErr = errors.Join(resultErr, errors.New("atomic rooted create: close parent failed"))
	}
	return verified, resultErr
}

func verifyRootedCreateReadback(stage rootedCreateStage, operations rootedCreateFinalizationOperations) (verified bool, resultErr error) {
	if stage.mode&0o400 == 0 {
		return false, errors.New("atomic rooted create: readback failed")
	}
	first, err := rootedCreateReadbackLstat(operations, stage.parent, stage.basename)
	if err != nil || !rootedCreateReadbackInfoOK(first, stage) {
		return false, errors.New("atomic rooted create: readback failed")
	}
	file, err := rootedCreateOpenRead(operations, stage.parent, stage.basename)
	if err != nil || file == nil {
		resultErr = errors.New("atomic rooted create: readback failed")
		if file != nil {
			if closeErr := file.Close(); closeErr != nil {
				resultErr = errors.Join(resultErr, errors.New("atomic rooted create: read close failed"))
			}
		}
		return false, resultErr
	}
	defer func() {
		if err := file.Close(); err != nil {
			resultErr = errors.Join(resultErr, errors.New("atomic rooted create: read close failed"))
		}
	}()
	info, err := file.Stat()
	if err != nil || !rootedCreateReadbackInfoOK(info, stage) {
		return false, errors.New("atomic rooted create: readback failed")
	}
	if data, err := rootedCreateReadbackBytes(file, len(stage.data)); err != nil || !bytes.Equal(data, stage.data) {
		return false, errors.New("atomic rooted create: readback failed")
	}
	second, err := rootedCreateReadbackLstat(operations, stage.parent, stage.basename)
	if err != nil || !rootedCreateReadbackInfoOK(second, stage) {
		return false, errors.New("atomic rooted create: readback failed")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return false, errors.New("atomic rooted create: readback failed")
	}
	if data, err := rootedCreateReadbackBytes(file, len(stage.data)); err != nil || !bytes.Equal(data, stage.data) {
		return false, errors.New("atomic rooted create: readback failed")
	}
	info, err = file.Stat()
	if err != nil || !rootedCreateReadbackInfoOK(info, stage) {
		return false, errors.New("atomic rooted create: readback failed")
	}
	last, err := rootedCreateReadbackLstat(operations, stage.parent, stage.basename)
	if err != nil || !rootedCreateReadbackInfoOK(last, stage) {
		return false, errors.New("atomic rooted create: readback failed")
	}
	return true, nil
}

func rootedCreateReadbackInfoOK(info fs.FileInfo, stage rootedCreateStage) bool {
	return info != nil && info.Mode().IsRegular() && rootedCreateModeOK(info.Mode(), stage.mode, runtime.GOOS == "windows") && info.Size() == int64(len(stage.data)) && os.SameFile(info, stage.info)
}

func rootedCreateReadbackBytes(file rootedCreateReadbackFile, length int) ([]byte, error) {
	return io.ReadAll(io.LimitReader(file, int64(length)+1))
}

func rootedCreateReadbackLstat(operations rootedCreateFinalizationOperations, parent *os.Root, name string) (fs.FileInfo, error) {
	return rootedAbsentLstat(operations.rootedAbsentEvidenceOperations, parent, name)
}

func rootedCreateOpenRead(operations rootedCreateFinalizationOperations, parent *os.Root, name string) (rootedCreateReadbackFile, error) {
	if operations.openRead != nil {
		return operations.openRead(parent, name)
	}
	return parent.Open(name)
}

func destinationAbsent(destination string) error {
	info, err := os.Lstat(destination)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("atomic create: inspect destination: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("atomic create: destination is not a regular file")
	}
	return fmt.Errorf("atomic create: destination already exists")
}
