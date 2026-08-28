package backupjournal

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
)

const maxJournalLength int64 = maxEntries * maxLength

var errOpenJournal = errors.New("backup journal: invalid journal")

// Handle is a detached, read-only view of one validated backup journal.
type Handle struct {
	manifest              []byte
	blobs                 map[string][]byte
	transactionID         string
	state                 State
	entryCount, blobCount int
}

func (handle Handle) TransactionID() string { return handle.transactionID }
func (handle Handle) State() State          { return handle.state }
func (handle Handle) EntryCount() int       { return handle.entryCount }
func (handle Handle) BlobCount() int        { return handle.blobCount }

// Manifest returns a detached parsed manifest when this handle is initialized.
func (handle Handle) Manifest() (Manifest, bool) {
	manifest, err := Parse(handle.manifest)
	return manifest, err == nil
}

// Blob returns a detached before-image for a present manifest entry.
func (handle Handle) Blob(runtime Runtime, relativePath string) ([]byte, bool) {
	if runtimeRank(runtime) < 0 || !relative(relativePath) {
		return nil, false
	}
	data, found := handle.blobs[blobKey(runtime, relativePath)]
	return append([]byte(nil), data...), found
}

type openRoots struct {
	home, cortex, base, transaction string
	infos                           []os.FileInfo
}

// Open validates and reads a durable journal without modifying the filesystem.
// Portable Go lacks descriptor-relative traversal, so it fails closed on Lstat,
// opened-file, and final Lstat drift rather than claiming stronger containment.
func Open(home, transactionID string) (Handle, error) {
	return openWithLimit(home, transactionID, maxJournalLength)
}

func openWithLimit(home, transactionID string, totalLimit int64) (Handle, error) {
	roots, err := openJournalRoots(home, transactionID)
	if err != nil {
		return Handle{}, errOpenJournal
	}
	manifestData, err := readOpenFile(filepath.Join(roots.transaction, manifestFile), maxLength)
	if err != nil {
		return Handle{}, errOpenJournal
	}
	manifest, err := Parse(manifestData)
	if err != nil || manifest.TransactionID() != transactionID {
		return Handle{}, errOpenJournal
	}
	if err = verifyOpenTree(roots.transaction, manifest); err != nil {
		return Handle{}, errOpenJournal
	}
	blobs := make(map[string][]byte)
	var total int64
	for _, entry := range manifest.entries {
		if entry.Existence == Absent {
			continue
		}
		if entry.Length > totalLimit-total {
			return Handle{}, errOpenJournal
		}
		data, readErr := readOpenFile(filepath.Join(roots.transaction, filepath.FromSlash(entry.BlobName)), entry.Length)
		if readErr != nil || int64(len(data)) != entry.Length || sha256Hex(data) != entry.SHA256 {
			return Handle{}, errOpenJournal
		}
		total += int64(len(data))
		blobs[blobKey(entry.Runtime, entry.RelativePath)] = data
	}
	finalManifest, err := readOpenFile(filepath.Join(roots.transaction, manifestFile), maxLength)
	if err != nil || !bytes.Equal(manifestData, finalManifest) || verifyOpenTree(roots.transaction, manifest) != nil || !roots.unchanged() {
		return Handle{}, errOpenJournal
	}
	return Handle{manifestData, blobs, manifest.TransactionID(), manifest.State(), len(manifest.entries), len(blobs)}, nil
}

func openJournalRoots(home, transactionID string) (openRoots, error) {
	if !filepath.IsAbs(home) || !validHash(transactionID) {
		return openRoots{}, errOpenJournal
	}
	names := []string{home, filepath.Join(home, ".cortex"), filepath.Join(home, ".cortex", "transactions"), filepath.Join(home, ".cortex", "transactions", transactionID)}
	roots := openRoots{home: names[0], cortex: names[1], base: names[2], transaction: names[3]}
	for index, name := range names {
		info, err := os.Lstat(name)
		if err != nil || !realDirInfo(info, index != 0) {
			return openRoots{}, errOpenJournal
		}
		roots.infos = append(roots.infos, info)
	}
	return roots, nil
}

func (roots openRoots) unchanged() bool {
	for index, name := range []string{roots.home, roots.cortex, roots.base, roots.transaction} {
		info, err := os.Lstat(name)
		if err != nil || !realDirInfo(info, index != 0) || !os.SameFile(roots.infos[index], info) {
			return false
		}
	}
	return true
}

func verifyOpenTree(root string, manifest Manifest) error {
	files, directories := map[string]bool{manifestFile: true}, map[string]bool{".": true}
	for _, entry := range manifest.entries {
		if entry.Existence == Absent {
			continue
		}
		files[entry.BlobName] = true
		for directory := path.Dir(entry.BlobName); directory != "."; directory = path.Dir(directory) {
			directories[directory] = true
		}
	}
	err := filepath.WalkDir(root, func(name string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return errOpenJournal
		}
		relativeName, err := filepath.Rel(root, name)
		if err != nil {
			return errOpenJournal
		}
		relativeName = filepath.ToSlash(relativeName)
		info, err := os.Lstat(name)
		if err != nil {
			return errOpenJournal
		}
		if entry.IsDir() {
			if !directories[relativeName] || !realDirInfo(info, true) {
				return errOpenJournal
			}
			delete(directories, relativeName)
			return nil
		}
		if !files[relativeName] || !regular0600(info) {
			return errOpenJournal
		}
		delete(files, relativeName)
		return nil
	})
	if err != nil || len(files) != 0 || len(directories) != 0 {
		return errOpenJournal
	}
	return nil
}

func readOpenFile(name string, limit int64) ([]byte, error) {
	if limit < 0 || limit > maxLength {
		return nil, errOpenJournal
	}
	before, err := os.Lstat(name)
	if err != nil || !regular0600(before) || before.Size() < 0 || before.Size() > limit {
		return nil, errOpenJournal
	}
	file, err := os.Open(name)
	if err != nil {
		return nil, errOpenJournal
	}
	opened, err := file.Stat()
	if err != nil || !regular0600(opened) || !os.SameFile(before, opened) {
		file.Close()
		return nil, errOpenJournal
	}
	data, readErr := io.ReadAll(io.LimitReader(file, limit+1))
	closeErr := file.Close()
	final, statErr := os.Lstat(name)
	if readErr != nil || closeErr != nil || statErr != nil || !regular0600(final) || !os.SameFile(before, final) || int64(len(data)) != before.Size() || int64(len(data)) > limit {
		return nil, errOpenJournal
	}
	return data, nil
}
