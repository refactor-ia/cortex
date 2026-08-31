package installobserve

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	shadowMaxEntries       = 1024
	shadowMaxFileBytes     = 256 << 10
	shadowMaxRetainedBytes = 8 << 20
)

type shadowLocation uint8

const (
	globalAgents shadowLocation = iota
	globalSubagents
	projectAgents
	projectSubagents
)

type shadowCandidate struct {
	location shadowLocation
	basename string
	bytes    []byte
	mode     fs.FileMode
	unsafe   bool
}

func scanActorRoots(piRoot, cwd string) ([]shadowCandidate, error) {
	if err := validateShadowRoot(piRoot); err != nil {
		return nil, err
	}
	if err := validateShadowRoot(cwd); err != nil {
		return nil, err
	}
	roots := []struct {
		location shadowLocation
		base     string
		parts    []string
	}{
		{globalAgents, piRoot, []string{"agents"}},
		{globalSubagents, piRoot, []string{"subagents"}},
		{projectAgents, cwd, []string{".pi", "agents"}},
		{projectSubagents, cwd, []string{".pi", "subagents"}},
	}
	var candidates []shadowCandidate
	retained := 0
	for _, root := range roots {
		directory, found, err := openShadowDirectory(root.base, root.parts...)
		if err != nil {
			return nil, err
		}
		if !found {
			continue
		}
		entries, err := readShadowEntries(directory)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			name := entry.Name()
			if !strings.HasSuffix(name, ".md") {
				continue
			}
			candidate, err := readShadowCandidate(directory.Name(), root.location, name)
			if err != nil {
				return nil, err
			}
			if !candidate.unsafe {
				if retained > shadowMaxRetainedBytes-len(candidate.bytes) {
					return nil, errors.New("shadow scan retained-byte limit exceeded")
				}
				retained += len(candidate.bytes)
			}
			candidates = append(candidates, candidate)
		}
	}
	return cloneShadowCandidates(candidates), nil
}

func validateShadowRoot(root string) error {
	if root == "" || !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return errors.New("shadow scan root is not canonical and absolute")
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return fmt.Errorf("shadow scan root is unavailable: %w", err)
	}
	info, err := os.Stat(root)
	if err != nil || resolved != root || !info.IsDir() {
		return errors.New("shadow scan root is unsafe")
	}
	return nil
}

func openShadowDirectory(base string, parts ...string) (*os.File, bool, error) {
	path := base
	var before fs.FileInfo
	for _, part := range parts {
		path = filepath.Join(path, part)
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		if err != nil {
			return nil, false, fmt.Errorf("shadow scan directory is unavailable: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return nil, false, errors.New("shadow scan directory is unsafe")
		}
		before = info
	}
	directory, err := os.Open(path)
	if err != nil {
		return nil, false, fmt.Errorf("shadow scan directory cannot be opened: %w", err)
	}
	opened, err := directory.Stat()
	if err != nil || !opened.IsDir() || !os.SameFile(before, opened) {
		directory.Close()
		return nil, false, errors.New("shadow scan directory changed while opening")
	}
	return directory, true, nil
}

func readShadowEntries(directory *os.File) ([]os.DirEntry, error) {
	entries, err := directory.ReadDir(shadowMaxEntries + 1)
	opened, statErr := directory.Stat()
	final, finalErr := os.Lstat(directory.Name())
	closeErr := directory.Close()
	if closeErr != nil {
		return nil, fmt.Errorf("shadow scan directory cannot be closed: %w", closeErr)
	}
	if statErr != nil || finalErr != nil || !final.IsDir() || !os.SameFile(opened, final) || opened.Mode() != final.Mode() {
		return nil, errors.New("shadow scan directory changed while reading")
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("shadow scan directory cannot be read: %w", err)
	}
	if len(entries) > shadowMaxEntries {
		return nil, errors.New("shadow scan entry limit exceeded")
	}
	sort.Slice(entries, func(left, right int) bool { return entries[left].Name() < entries[right].Name() })
	return entries, nil
}

func readShadowCandidate(directory string, location shadowLocation, basename string) (shadowCandidate, error) {
	path := filepath.Join(directory, basename)
	before, err := os.Lstat(path)
	if err != nil {
		return shadowCandidate{}, fmt.Errorf("shadow scan candidate is unavailable: %w", err)
	}
	candidate := shadowCandidate{location: location, basename: basename, mode: before.Mode()}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() || before.Size() > shadowMaxFileBytes {
		candidate.unsafe = true
		return candidate, nil
	}

	file, err := os.Open(path)
	if err != nil {
		candidate.unsafe = true
		return candidate, nil
	}
	opened, statErr := file.Stat()
	if statErr != nil || !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		file.Close()
		return shadowCandidate{}, errors.New("shadow scan candidate changed while opening")
	}
	contents, readErr := io.ReadAll(io.LimitReader(file, shadowMaxFileBytes+1))
	closeErr := file.Close()
	if closeErr != nil {
		return shadowCandidate{}, fmt.Errorf("shadow scan candidate cannot be closed: %w", closeErr)
	}
	final, finalErr := os.Lstat(path)
	if finalErr != nil || !final.Mode().IsRegular() || !os.SameFile(before, final) || final.Mode() != before.Mode() || final.Size() != before.Size() {
		return shadowCandidate{}, errors.New("shadow scan candidate changed while reading")
	}
	if readErr != nil || len(contents) > shadowMaxFileBytes {
		candidate.unsafe = true
		return candidate, nil
	}
	candidate.bytes = append([]byte(nil), contents...)
	return candidate, nil
}

func cloneShadowCandidates(candidates []shadowCandidate) []shadowCandidate {
	result := make([]shadowCandidate, len(candidates))
	for index, candidate := range candidates {
		result[index] = candidate
		result[index].bytes = append([]byte(nil), candidate.bytes...)
	}
	return result
}
