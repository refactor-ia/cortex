package backupjournal

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path"
	"path/filepath"
)

const manifestFile = "manifest.json"

// BlobInput binds caller-owned bytes to one present manifest destination.
type BlobInput struct {
	Runtime      Runtime
	RelativePath string
	Bytes        []byte
	Mode         uint32
}

// CreateResult describes a prepared durable journal without storage authority.
type CreateResult struct {
	transactionID         string
	state                 State
	entryCount, blobCount int
}

func (result CreateResult) TransactionID() string { return result.transactionID }
func (result CreateResult) State() State          { return result.state }
func (result CreateResult) EntryCount() int       { return result.entryCount }
func (result CreateResult) BlobCount() int        { return result.blobCount }

var (
	ErrCreateCleanup    = errors.New("backup journal: cleanup failed")
	errCreateFilesystem = errors.New("backup journal: create filesystem failure")
	createSeam          = func(string, string) error { return nil }
)

// Create writes one prepared manifest and its exact before-image blob set.
func Create(home string, manifest Manifest, inputs []BlobInput) (CreateResult, error) {
	prepared, data, blobs, err := validateCreate(manifest, inputs)
	if err != nil {
		return CreateResult{}, err
	}
	base, attempt, err := ensureJournalBase(home)
	if err != nil {
		return CreateResult{}, joinCreateFailure(err, attempt.cleanup())
	}
	transaction, err := attempt.reserve(base, prepared.TransactionID())
	if err != nil {
		return CreateResult{}, joinCreateFailure(err, attempt.cleanup())
	}
	fail := func(cause error) (CreateResult, error) {
		return CreateResult{}, joinCreateFailure(cause, attempt.cleanup())
	}
	for index, blob := range blobs {
		name, createErr := attempt.createFile(transaction, blobName(blob.Runtime, blob.RelativePath))
		if createErr != nil {
			return fail(createErr)
		}
		if createErr = durableWrite(&attempt, name, blob.Bytes); createErr != nil {
			return fail(createErr)
		}
		if index == 0 {
			if createErr = createSeam("after-first-blob", transaction); createErr != nil {
				return fail(createErr)
			}
		}
	}
	name, err := attempt.createFile(transaction, manifestFile)
	if err != nil {
		return fail(err)
	}
	if err = durableWrite(&attempt, name, data); err != nil {
		return fail(err)
	}
	if err = createSeam("after-manifest-write", transaction); err != nil {
		return fail(err)
	}
	if err = createSeam("before-tree-verify", transaction); err != nil {
		return fail(err)
	}
	if err = verifyJournalTree(transaction, prepared); err != nil {
		return fail(err)
	}
	if err = syncDirectory(transaction); err != nil {
		return fail(err)
	}
	if err = syncDirectory(base); err != nil {
		return fail(err)
	}
	return CreateResult{prepared.TransactionID(), Prepared, len(prepared.entries), len(blobs)}, nil
}

func joinCreateFailure(primary, cleanup error) error {
	var pathErr *os.PathError
	if errors.As(primary, &pathErr) {
		primary = errCreateFilesystem
	}
	if cleanup != nil {
		cleanup = ErrCreateCleanup
	}
	return errors.Join(primary, cleanup)
}

func validateCreate(manifest Manifest, inputs []BlobInput) (Manifest, []byte, []BlobInput, error) {
	data, err := json.Marshal(manifest)
	if err != nil || len(data) > int(maxLength) {
		return Manifest{}, nil, nil, errors.New("backup journal: invalid prepared manifest")
	}
	prepared, err := Parse(data)
	if err != nil || prepared.State() != Prepared {
		return Manifest{}, nil, nil, errors.New("backup journal: invalid prepared manifest")
	}
	provided := make(map[string]BlobInput, len(inputs))
	for _, input := range inputs {
		key := blobKey(input.Runtime, input.RelativePath)
		if runtimeRank(input.Runtime) < 0 || !relative(input.RelativePath) || provided[key].Runtime != "" {
			return Manifest{}, nil, nil, errors.New("backup journal: invalid blob input")
		}
		input.Bytes = append([]byte(nil), input.Bytes...)
		provided[key] = input
	}
	blobs := make([]BlobInput, 0, len(inputs))
	for _, entry := range prepared.entries {
		key := blobKey(entry.Runtime, entry.RelativePath)
		input, found := provided[key]
		if entry.Existence == Absent {
			if found {
				return Manifest{}, nil, nil, errors.New("backup journal: blob for absent entry")
			}
			continue
		}
		if !found || input.Mode != entry.Mode || int64(len(input.Bytes)) != entry.Length || sha256Hex(input.Bytes) != entry.SHA256 {
			return Manifest{}, nil, nil, errors.New("backup journal: blob does not match manifest")
		}
		delete(provided, key)
		blobs = append(blobs, input)
	}
	if len(provided) != 0 {
		return Manifest{}, nil, nil, errors.New("backup journal: extra blob input")
	}
	return prepared, data, blobs, nil
}

func verifyJournalTree(root string, manifest Manifest) error {
	expectedFiles := map[string]bool{manifestFile: true}
	expectedDirs := map[string]bool{".": true}
	for _, entry := range manifest.entries {
		if entry.Existence != Present {
			continue
		}
		expectedFiles[entry.BlobName] = true
		for directory := path.Dir(entry.BlobName); directory != "."; directory = path.Dir(directory) {
			expectedDirs[directory] = true
		}
	}
	err := filepath.WalkDir(root, func(name string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relativeName, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		relativeName = filepath.ToSlash(relativeName)
		info, err := pathLstat(name)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if !expectedDirs[relativeName] || !realDirInfo(info, true) {
				return errors.New("backup journal: unexpected journal directory")
			}
			delete(expectedDirs, relativeName)
			return nil
		}
		if !expectedFiles[relativeName] || !regular0600(info) {
			return errors.New("backup journal: unexpected journal file")
		}
		delete(expectedFiles, relativeName)
		return nil
	})
	if err != nil {
		return err
	}
	if len(expectedFiles) != 0 || len(expectedDirs) != 0 {
		return errors.New("backup journal: incomplete journal tree")
	}
	return nil
}

func blobKey(runtime Runtime, relativePath string) string {
	return string(runtime) + "\x00" + relativePath
}
func sha256Hex(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
