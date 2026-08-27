// Package filetxn captures verifiable file snapshots for later transactions.
package filetxn

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/refactor-ia/cortex/internal/safepath"
)

const manifestName, manifestVersion = "manifest.json", 1

// Entry describes one source-relative file captured by a snapshot.
type Entry struct {
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
	Mode   uint32 `json:"mode,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
}

// Manifest is the deterministic, unauthenticated record of a snapshot.
type Manifest struct {
	Version int     `json:"version"`
	Entries []Entry `json:"entries"`
}

// Snapshot identifies a captured backup directory and its manifest.
type Snapshot struct {
	Dir      string
	Manifest Manifest
}

type candidate struct {
	path   string
	source string
	info   os.FileInfo
}

// Capture records regular files and absent leaves beneath sourceRoot in backupName.
func Capture(sourceRoot, backupRoot, backupName string, paths []string) (snapshot Snapshot, err error) {
	candidates := make([]candidate, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, raw := range paths {
		source, resolveErr := safepath.Resolve(sourceRoot, raw)
		if resolveErr != nil {
			return Snapshot{}, fmt.Errorf("snapshot candidate: %w", resolveErr)
		}
		path := filepath.ToSlash(filepath.Clean(raw))
		if _, exists := seen[path]; exists {
			return Snapshot{}, fmt.Errorf("snapshot candidate is duplicated: %s", path)
		}
		seen[path] = struct{}{}
		info, statErr := os.Lstat(source)
		if statErr != nil && !os.IsNotExist(statErr) {
			return Snapshot{}, fmt.Errorf("snapshot candidate inspection failed: %s", path)
		}
		if statErr == nil && !info.Mode().IsRegular() {
			return Snapshot{}, fmt.Errorf("snapshot candidate is not a regular file: %s", path)
		}
		candidates = append(candidates, candidate{path: path, source: source, info: info})
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].path < candidates[j].path })

	backupDir, resolveErr := safepath.Resolve(backupRoot, backupName)
	if resolveErr != nil {
		return Snapshot{}, fmt.Errorf("snapshot backup directory: %w", resolveErr)
	}
	if err = os.Mkdir(backupDir, 0o700); err != nil {
		return Snapshot{}, fmt.Errorf("create snapshot backup directory: %w", err)
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(backupDir)
		}
	}()
	if err = os.Mkdir(filepath.Join(backupDir, "payloads"), 0o700); err != nil {
		return Snapshot{}, fmt.Errorf("create snapshot payload directory: %w", err)
	}

	manifest := Manifest{Version: manifestVersion, Entries: make([]Entry, 0, len(candidates))}
	for _, item := range candidates {
		entry := Entry{Path: item.path, Exists: item.info != nil}
		if item.info != nil {
			entry.Mode = uint32(item.info.Mode().Perm())
			payload := backupPayloadPath(backupDir, item.path)
			if copyErr := copyPayload(item.source, payload); copyErr != nil {
				return Snapshot{}, fmt.Errorf("copy snapshot payload failed: %s", item.path)
			}
			entry.SHA256, err = digestFile(payload)
			if err != nil {
				return Snapshot{}, fmt.Errorf("hash snapshot payload failed: %s", item.path)
			}
		}
		manifest.Entries = append(manifest.Entries, entry)
	}
	if err = writeManifest(backupDir, manifest); err != nil {
		return Snapshot{}, err
	}
	return Snapshot{Dir: backupDir, Manifest: manifest}, nil
}

// Verify reloads and verifies backup payloads; it does not cryptographically authenticate the manifest.
func Verify(backupRoot, backupName string) error {
	backupDir, err := safepath.Resolve(backupRoot, backupName)
	if err != nil {
		return fmt.Errorf("snapshot backup directory: %w", err)
	}
	if info, statErr := os.Lstat(backupDir); statErr != nil || !info.IsDir() {
		return fmt.Errorf("snapshot backup directory is missing or invalid")
	}
	payloadDir, resolveErr := safepath.Resolve(backupDir, "payloads")
	if resolveErr != nil {
		return fmt.Errorf("snapshot payload directory is missing or invalid")
	}
	if info, statErr := os.Lstat(payloadDir); statErr != nil || !info.IsDir() {
		return fmt.Errorf("snapshot payload directory is missing or invalid")
	}
	manifest, err := readManifest(backupDir)
	if err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(manifest.Entries))
	for _, entry := range manifest.Entries {
		if !validManifestPath(entry.Path) {
			return fmt.Errorf("snapshot manifest has an invalid path")
		}
		if _, exists := seen[entry.Path]; exists {
			return fmt.Errorf("snapshot manifest has duplicate paths")
		}
		seen[entry.Path] = struct{}{}
		if !entry.Exists {
			if entry.Mode != 0 || entry.SHA256 != "" {
				return fmt.Errorf("snapshot manifest has invalid absent entry")
			}
			continue
		}
		if entry.SHA256 == "" {
			return fmt.Errorf("snapshot manifest has missing checksum")
		}
		payload := backupPayloadPath(backupDir, entry.Path)
		info, statErr := os.Lstat(payload)
		if statErr != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("snapshot payload is missing or invalid: %s", entry.Path)
		}
		digest, hashErr := digestFile(payload)
		if hashErr != nil || digest != entry.SHA256 {
			return fmt.Errorf("snapshot payload checksum mismatch: %s", entry.Path)
		}
	}
	return nil
}

func validManifestPath(path string) bool {
	if path == "" || filepath.IsAbs(path) {
		return false
	}
	clean := filepath.ToSlash(filepath.Clean(path))
	return clean != "." && clean != ".." && !strings.HasPrefix(clean, "../") && clean == path
}

func backupPayloadPath(backupDir, path string) string {
	digest := sha256.Sum256([]byte(path))
	return filepath.Join(backupDir, "payloads", hex.EncodeToString(digest[:]))
}

func copyPayload(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func digestFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func writeManifest(backupDir string, manifest Manifest) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode snapshot manifest: %w", err)
	}
	temporary := filepath.Join(backupDir, ".manifest.json.tmp")
	if err := os.WriteFile(temporary, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write snapshot manifest: %w", err)
	}
	if err := os.Chmod(temporary, 0o600); err != nil {
		return fmt.Errorf("protect snapshot manifest: %w", err)
	}
	if err := os.Rename(temporary, filepath.Join(backupDir, manifestName)); err != nil {
		return fmt.Errorf("store snapshot manifest: %w", err)
	}
	return nil
}

func readManifest(backupDir string) (Manifest, error) {
	if info, err := os.Lstat(filepath.Join(backupDir, manifestName)); err != nil || !info.Mode().IsRegular() {
		return Manifest{}, fmt.Errorf("snapshot manifest is missing or invalid")
	}
	data, err := os.ReadFile(filepath.Join(backupDir, manifestName))
	if err != nil {
		return Manifest{}, fmt.Errorf("read snapshot manifest: %w", err)
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil || manifest.Version != manifestVersion {
		return Manifest{}, fmt.Errorf("snapshot manifest is invalid")
	}
	return manifest, nil
}
