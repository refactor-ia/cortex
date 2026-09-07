package modelprofile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/refactor-ia/cortex/internal/atomicfile"
	"github.com/refactor-ia/cortex/internal/safepath"
)

// RuntimeRoots are caller-trusted absolute roots; unused roots may be empty.
type RuntimeRoots struct{ Pi, OpenCode string }

// Target is one profile-owned config leaf; arbitrary paths are never accepted.
type Target string

const (
	PiSettings     Target = "pi-settings"
	PiSubagents    Target = "pi-subagents"
	OpenCodeConfig Target = "opencode-config"
)

// Change is a candidate image; Save captures its existing before image and does not apply it.
type Change struct {
	Target    Target
	After     []byte
	AfterMode fs.FileMode
}

// Backup is validated, opaque rollback evidence returned only by Load.
type Backup struct{ entries []backupEntry }
type backupEntry struct {
	Target Target       `json:"target"`
	Root   string       `json:"root"`
	RootID rootIdentity `json:"rootIdentity"`
	Before image        `json:"before"`
	After  image        `json:"after"`
}
type image struct {
	Bytes []byte      `json:"bytes"`
	Mode  fs.FileMode `json:"mode"`
}
type diskBackup struct {
	Version int           `json:"version"`
	Entries []backupEntry `json:"entries"`
}
type restoreOperations struct {
	replace  func(string, string, []byte, fs.FileMode, []byte, fs.FileMode) error
	identity func(string, rootIdentity) error
}

const (
	backupName     = "model-profile-rollback.json"
	backupVersion  = 2
	maxImageBytes  = 32 << 20
	maxBackupBytes = 8*maxImageBytes + 4096
)

// Save durably records before and expected-after images in a new private 0600 record.
// Callers conditionally apply after images and invoke Restore only while they remain exact.
func Save(backupDir string, roots RuntimeRoots, changes []Change) error {
	entries := make([]backupEntry, len(changes))
	seen := map[Target]bool{}
	for i, change := range changes {
		if seen[change.Target] || !validImage(image{change.After, change.AfterMode}) {
			return errors.New("model profile restore: invalid change")
		}
		seen[change.Target] = true
		root, leaf, err := targetPath(roots, change.Target)
		if err != nil {
			return err
		}
		identity, err := rootIdentityFor(root)
		if err != nil {
			return err
		}
		before, err := readImage(root, leaf)
		if err != nil {
			return fmt.Errorf("model profile restore: capture selected leaf: %w", err)
		}
		entries[i] = backupEntry{change.Target, root, identity, before, image{append([]byte(nil), change.After...), change.AfterMode}}
	}
	if !validEntries(entries) {
		return errors.New("model profile restore: invalid change set")
	}
	data, err := json.Marshal(diskBackup{backupVersion, entries})
	if err != nil {
		return errors.New("model profile restore: encode backup")
	}
	if err := atomicfile.CreateIfAbsent(backupDir, backupName, data, 0o600); err != nil {
		return fmt.Errorf("model profile restore: save backup: %w", err)
	}
	return nil
}

// Load reads and validates Save's private backup without logging image contents.
func Load(backupDir string) (Backup, error) {
	path, err := safepath.Resolve(backupDir, backupName)
	if err != nil {
		return Backup{}, errors.New("model profile restore: invalid backup directory")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return Backup{}, errors.New("model profile restore: backup is missing or unsafe")
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) > maxBackupBytes {
		return Backup{}, errors.New("model profile restore: backup cannot be read")
	}
	var stored diskBackup
	if json.Unmarshal(data, &stored) != nil || stored.Version != backupVersion || !validEntries(stored.Entries) {
		return Backup{}, errors.New("model profile restore: malformed backup")
	}
	canonical, _ := json.Marshal(stored)
	if !bytes.Equal(data, canonical) {
		return Backup{}, errors.New("model profile restore: malformed backup")
	}
	return Backup{entries: cloneEntries(stored.Entries)}, nil
}

// Restore preflights every selected leaf against its expected-after image before
// changing any leaf, then conditionally restores exact before images.
func Restore(roots RuntimeRoots, backup Backup) error {
	return restoreWith(roots, backup, restoreOperations{replace: atomicfile.ReplaceIfMatches, identity: rootMatches})
}
func restoreWith(roots RuntimeRoots, backup Backup, operations restoreOperations) error {
	if operations.identity == nil {
		operations.identity = rootMatches
	}
	if operations.replace == nil || !validEntries(backup.entries) {
		return errors.New("model profile restore: malformed backup")
	}
	for _, entry := range backup.entries {
		root, leaf, err := targetPath(roots, entry.Target)
		if err != nil || root != entry.Root || operations.identity(root, entry.RootID) != nil {
			return errors.New("model profile restore: runtime roots do not match backup")
		}
		actual, err := readImage(root, leaf)
		if err != nil || !sameImage(actual, entry.After) {
			return errors.New("model profile restore: selected leaf changed; rollback refused")
		}
	}
	done := backup.entries[:0]
	for _, entry := range backup.entries {
		root, leaf, _ := targetPath(roots, entry.Target)
		err := operations.identity(root, entry.RootID)
		if err == nil {
			err = operations.replace(root, leaf, entry.After.Bytes, entry.After.Mode, entry.Before.Bytes, entry.Before.Mode)
		}
		if err != nil {
			for i := len(done) - 1; i >= 0; i-- {
				e := done[i]
				r, l, _ := targetPath(roots, e.Target)
				if operations.identity(r, e.RootID) != nil || operations.replace(r, l, e.Before.Bytes, e.Before.Mode, e.After.Bytes, e.After.Mode) != nil {
					return errors.New("model profile restore: rollback failed; intervention required")
				}
			}
			return errors.New("model profile restore: rollback failed")
		}
		done = append(done, entry)
	}
	return nil
}

func targetPath(roots RuntimeRoots, target Target) (string, string, error) {
	var root, leaf string
	switch target {
	case PiSettings:
		root, leaf = roots.Pi, "settings.json"
	case PiSubagents:
		root, leaf = roots.Pi, "subagents.json"
	case OpenCodeConfig:
		root, leaf = roots.OpenCode, "opencode.json"
	default:
		return "", "", errors.New("model profile restore: unsupported target")
	}
	if _, err := rootIdentityFor(root); err != nil {
		return "", "", err
	}
	return root, leaf, nil
}
func readImage(root, leaf string) (image, error) {
	path, err := safepath.Resolve(root, leaf)
	if err != nil {
		return image{}, errors.New("unsafe selected leaf")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || !validMode(info.Mode().Perm()) {
		return image{}, errors.New("selected leaf must be an existing regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) > maxImageBytes {
		return image{}, errors.New("selected leaf cannot be read")
	}
	return image{data, info.Mode().Perm()}, nil
}
func validEntries(entries []backupEntry) bool {
	if len(entries) == 0 || len(entries) > 3 {
		return false
	}
	seen := map[Target]bool{}
	piRoot, openRoot := "", ""
	for _, entry := range entries {
		identity, err := rootIdentityFor(entry.Root)
		if seen[entry.Target] || !knownTarget(entry.Target) || err != nil || identity != entry.RootID || !validImage(entry.Before) || !validImage(entry.After) {
			return false
		}
		if entry.Target == OpenCodeConfig {
			if openRoot != "" && openRoot != entry.Root {
				return false
			}
			openRoot = entry.Root
		} else if piRoot != "" && piRoot != entry.Root {
			return false
		} else {
			piRoot = entry.Root
		}
		seen[entry.Target] = true
	}
	return true
}
func knownTarget(target Target) bool {
	return target == PiSettings || target == PiSubagents || target == OpenCodeConfig
}
func validImage(value image) bool     { return len(value.Bytes) <= maxImageBytes && validMode(value.Mode) }
func validMode(mode fs.FileMode) bool { return mode&^fs.FileMode(0o777) == 0 }
func sameImage(left, right image) bool {
	return left.Mode == right.Mode && bytes.Equal(left.Bytes, right.Bytes)
}
func cloneEntries(entries []backupEntry) []backupEntry {
	out := make([]backupEntry, len(entries))
	for i, entry := range entries {
		out[i] = backupEntry{entry.Target, entry.Root, entry.RootID, image{append([]byte(nil), entry.Before.Bytes...), entry.Before.Mode}, image{append([]byte(nil), entry.After.Bytes...), entry.After.Mode}}
	}
	return out
}
