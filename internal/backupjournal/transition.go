package backupjournal

import (
	"bytes"
	"io/fs"
	"os"
	"path"
	"path/filepath"
)

// TransitionError classifies transition failures without exposing storage details.
type TransitionError string

func (err TransitionError) Error() string { return string(err) }

const (
	ErrTransitionBusy        TransitionError = "backup journal: transition busy"
	ErrTransitionInvalid     TransitionError = "backup journal: invalid transition"
	ErrTransitionCleanup     TransitionError = "backup journal: transition cleanup failed"
	ErrTransitionCorrupt     TransitionError = "backup journal: corrupt transition state"
	ErrTransitionInterrupted TransitionError = "backup journal: transition interrupted"
)

type transitionHooks struct {
	afterIntent, afterManifest func() error
	removeIntent               func(string, transitionIntent) error
}

// Transition durably moves a prepared journal to a single terminal state.
func Transition(home, transactionID string, target State) (Handle, error) {
	return transitionWithHooks(home, transactionID, target, transitionHooks{})
}

func transitionWithHooks(home, transactionID string, target State, hooks transitionHooks) (Handle, error) {
	if !validTransitionInput(home, transactionID, target) {
		return Handle{}, ErrTransitionInvalid
	}
	handle, err := Open(home, transactionID)
	if err != nil {
		roots, rootErr := openJournalRoots(home, transactionID)
		if rootErr == nil && roots.unchanged() && transitionIntentPresent(roots.transaction) {
			return Handle{}, ErrTransitionBusy
		}
		return Handle{}, ErrTransitionCorrupt
	}
	if handle.State() != Prepared {
		return Handle{}, ErrTransitionInvalid
	}
	manifest, ok := handle.Manifest()
	if !ok {
		return Handle{}, ErrTransitionCorrupt
	}
	targetManifest, err := manifest.Transition(target)
	if err != nil {
		return Handle{}, ErrTransitionInvalid
	}
	prepared, targetData := mustManifestBytes(manifest), mustManifestBytes(targetManifest)
	roots, err := openJournalRoots(home, transactionID)
	if err != nil || !roots.unchanged() {
		return Handle{}, ErrTransitionCorrupt
	}
	intent, err := createTransitionIntent(roots.transaction, transactionID, prepared, targetData)
	if err != nil {
		if transitionIntentPresent(roots.transaction) {
			return Handle{}, ErrTransitionBusy
		}
		return Handle{}, ErrTransitionCorrupt
	}
	if callTransitionHook(hooks.afterIntent) {
		return Handle{}, ErrTransitionInterrupted
	}
	if replaceManifest(roots.transaction, intent) != nil {
		return Handle{}, ErrTransitionCorrupt
	}
	if callTransitionHook(hooks.afterManifest) {
		return Handle{}, ErrTransitionInterrupted
	}
	if !validTransitionTree(roots.transaction, targetManifest, intent, false) || !roots.unchanged() {
		return Handle{}, ErrTransitionCorrupt
	}
	if removeIntentWithHooks(roots.transaction, intent, hooks) != nil {
		return Handle{}, ErrTransitionCleanup
	}
	return strictTerminalOpen(home, transactionID, target)
}

// Reconcile completes only durable journal metadata work left by a transition.
func Reconcile(home, transactionID string) (Handle, error) {
	return reconcileWithHooks(home, transactionID, transitionHooks{})
}

func reconcileWithHooks(home, transactionID string, hooks transitionHooks) (Handle, error) {
	if !filepath.IsAbs(home) || !validHash(transactionID) {
		return Handle{}, ErrTransitionInvalid
	}
	roots, err := openJournalRoots(home, transactionID)
	if err != nil || !roots.unchanged() {
		return Handle{}, ErrTransitionCorrupt
	}
	_, statErr := os.Lstat(filepath.Join(roots.transaction, transitionIntentFile))
	if os.IsNotExist(statErr) {
		handle, openErr := Open(home, transactionID)
		if openErr != nil {
			return Handle{}, ErrTransitionCorrupt
		}
		return handle, nil
	}
	if statErr != nil {
		return Handle{}, ErrTransitionCorrupt
	}
	intent, err := readTransitionIntent(roots.transaction)
	if err != nil || intent.transactionID != transactionID {
		return Handle{}, ErrTransitionCorrupt
	}
	manifest, data, err := currentTransitionManifest(roots.transaction, transactionID)
	if err != nil {
		return Handle{}, ErrTransitionCorrupt
	}
	if bytes.Equal(data, intent.prepared) && manifest.State() == Prepared {
		temporary := manifestTempName(roots.transaction, intent)
		_, tempErr := os.Lstat(temporary)
		hasTemp := tempErr == nil
		if (!hasTemp && !os.IsNotExist(tempErr)) || !validTransitionTree(roots.transaction, manifest, intent, hasTemp) || !roots.unchanged() {
			return Handle{}, ErrTransitionCorrupt
		}
		if hasTemp {
			data, _, tempErr := readTransitionFile(temporary, maxLength)
			if tempErr != nil || !bytes.Equal(data, intent.target) || os.Rename(temporary, filepath.Join(roots.transaction, manifestFile)) != nil || syncDirectory(roots.transaction) != nil {
				return Handle{}, ErrTransitionCorrupt
			}
		} else if replaceManifest(roots.transaction, intent) != nil {
			return Handle{}, ErrTransitionCorrupt
		}
		manifest, data, err = currentTransitionManifest(roots.transaction, transactionID)
		if err != nil || !bytes.Equal(data, intent.target) {
			return Handle{}, ErrTransitionCorrupt
		}
	}
	if !bytes.Equal(data, intent.target) || manifest.State() != intent.targetState || !validTransitionTree(roots.transaction, manifest, intent, false) || !roots.unchanged() {
		return Handle{}, ErrTransitionCorrupt
	}
	if removeIntentWithHooks(roots.transaction, intent, hooks) != nil {
		return Handle{}, ErrTransitionCleanup
	}
	return strictTerminalOpen(home, transactionID, intent.targetState)
}

func validTransitionInput(home, transactionID string, target State) bool {
	return filepath.IsAbs(home) && validHash(transactionID) && (target == Committed || target == Recovered)
}
func mustManifestBytes(manifest Manifest) []byte { data, _ := manifest.MarshalJSON(); return data }
func callTransitionHook(hook func() error) bool  { return hook != nil && hook() != nil }
func removeIntentWithHooks(directory string, intent transitionIntent, hooks transitionHooks) error {
	if hooks.removeIntent != nil {
		return hooks.removeIntent(directory, intent)
	}
	return removeTransitionIntent(directory, intent)
}
func transitionIntentPresent(directory string) bool {
	info, err := os.Lstat(filepath.Join(directory, transitionIntentFile))
	return err == nil && regular0600(info)
}
func strictTerminalOpen(home, transactionID string, state State) (Handle, error) {
	handle, err := Open(home, transactionID)
	if err != nil || handle.State() != state || (state != Committed && state != Recovered) {
		return Handle{}, ErrTransitionCorrupt
	}
	return handle, nil
}
func currentTransitionManifest(directory, transactionID string) (Manifest, []byte, error) {
	data, _, err := readTransitionFile(filepath.Join(directory, manifestFile), maxLength)
	if err != nil {
		return Manifest{}, nil, err
	}
	manifest, err := Parse(data)
	if err != nil || manifest.TransactionID() != transactionID {
		return Manifest{}, nil, ErrTransitionCorrupt
	}
	return manifest, data, nil
}
func validTransitionTree(directory string, manifest Manifest, intent transitionIntent, temporary bool) bool {
	files, directories := map[string]bool{manifestFile: true, transitionIntentFile: true}, map[string]bool{".": true}
	if temporary {
		files[filepath.Base(manifestTempName(directory, intent))] = true
	}
	for _, entry := range manifest.entries {
		if entry.Existence == Absent {
			continue
		}
		files[entry.BlobName] = true
		for name := path.Dir(entry.BlobName); name != "."; name = path.Dir(name) {
			directories[name] = true
		}
		data, err := readOpenFile(filepath.Join(directory, filepath.FromSlash(entry.BlobName)), entry.Length)
		if err != nil || int64(len(data)) != entry.Length || sha256Hex(data) != entry.SHA256 {
			return false
		}
	}
	err := filepath.WalkDir(directory, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relativeName, err := filepath.Rel(directory, name)
		if err != nil {
			return err
		}
		relativeName = filepath.ToSlash(relativeName)
		info, err := os.Lstat(name)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if !directories[relativeName] || !realDirInfo(info, true) {
				return ErrTransitionCorrupt
			}
			delete(directories, relativeName)
			return nil
		}
		if !files[relativeName] || !regular0600(info) {
			return ErrTransitionCorrupt
		}
		delete(files, relativeName)
		return nil
	})
	return err == nil && len(files) == 0 && len(directories) == 0
}
