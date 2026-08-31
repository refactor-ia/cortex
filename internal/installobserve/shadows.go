package installobserve

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/refactor-ia/cortex/internal/installplan"
	"github.com/refactor-ia/cortex/internal/installstate"
	"github.com/refactor-ia/cortex/internal/ownership"
	"github.com/refactor-ia/cortex/internal/qarole"
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

// ShadowLocation identifies one bounded actor-definition root.
type ShadowLocation uint8

const (
	ShadowLocationGlobalAgents ShadowLocation = iota
	ShadowLocationGlobalSubagents
	ShadowLocationProjectAgents
	ShadowLocationProjectSubagents
)

// ShadowConflict identifies one closed role and one conflicting location.
type ShadowConflict struct {
	role     qarole.RoleID
	location ShadowLocation
}

// RoleID returns the closed conflicting role identity.
func (conflict ShadowConflict) RoleID() qarole.RoleID { return conflict.role }

// Location returns the bounded location that conflicts with the role.
func (conflict ShadowConflict) Location() ShadowLocation { return conflict.location }

// ShadowObservation is a detached shadow-scan result with no touch authority.
type ShadowObservation struct {
	conflicts []ShadowConflict
	success   bool
}

// Clean reports whether a successful scan found no conflicts.
func (observation ShadowObservation) Clean() bool {
	return observation.success && len(observation.conflicts) == 0
}

// Conflicts returns detached role/location conflict records in stable order.
func (observation ShadowObservation) Conflicts() []ShadowConflict {
	return append([]ShadowConflict(nil), observation.conflicts...)
}

// ObserveActorShadows reads only the bounded actor-definition roots for an exact
// v2 candidate and reports any role definition that could shadow it.
func ObserveActorShadows(candidate installplan.Plan, observation FilesystemObservation, cwd string) (ShadowObservation, error) {
	if candidate.InstalledState().SchemaVersion() != 2 || !observation.MatchesCandidate(candidate) {
		return ShadowObservation{}, shadowInvalid()
	}
	classified, err := ClassifyFilesystem(candidate, observation)
	if err != nil {
		return ShadowObservation{}, shadowInvalid()
	}
	targets, ok := shadowTargets(candidate)
	if !ok {
		return ShadowObservation{}, shadowInvalid()
	}
	candidates, err := scanActorRoots(candidate.RootPath(), cwd)
	if err != nil {
		return ShadowObservation{}, shadowInvalid()
	}
	allowed := allowedActorDecisions(classified)
	conflicts := make(map[ShadowConflict]struct{})
	seen := make(map[qarole.RoleID]shadowCandidate)
	for _, scanned := range candidates {
		role, target := targets[scanned.basename]
		if target {
			if shadowAllowed(scanned, role, allowed, observation) {
				seen[role] = scanned
			} else {
				conflicts[ShadowConflict{role: role, location: publicShadowLocation(scanned.location)}] = struct{}{}
			}
			continue
		}
		for _, role := range shadowNames(scanned.bytes, targets) {
			conflicts[ShadowConflict{role: role, location: publicShadowLocation(scanned.location)}] = struct{}{}
		}
	}
	for role := range allowed {
		if scanned, found := seen[role]; !found || !recheckAllowedActor(candidate.RootPath(), role, scanned, observation) {
			return ShadowObservation{}, shadowInvalid()
		}
	}
	return ShadowObservation{conflicts: sortedShadowConflicts(conflicts), success: true}, nil
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

func shadowTargets(candidate installplan.Plan) (map[string]qarole.RoleID, bool) {
	roles := qarole.Catalog()
	targets := make(map[string]qarole.RoleID, len(roles))
	artifacts := make(map[qarole.RoleID]installstate.Artifact, len(roles))
	for _, artifact := range candidate.InstalledState().Artifacts() {
		if artifact.Kind() == installstate.KindPiActor {
			artifacts[artifact.RoleID()] = artifact
		}
	}
	if len(artifacts) != len(roles) {
		return nil, false
	}
	for _, contract := range roles {
		artifact, found := artifacts[contract.ID]
		basename := "cortex-" + string(contract.ID) + ".md"
		if !found || artifact.LogicalID() != "actors/"+string(contract.ID) || artifact.RelativePath() != "agents/"+basename {
			return nil, false
		}
		targets[basename] = contract.ID
	}
	return targets, true
}

func allowedActorDecisions(result Result) map[qarole.RoleID]bool {
	allowed := make(map[qarole.RoleID]bool)
	for _, decision := range result.ArtifactDecisions() {
		if decision.Kind != installstate.KindPiActor {
			continue
		}
		role := qarole.RoleID(strings.TrimPrefix(decision.LogicalID, "actors/"))
		if decision.ObservedOwnership == ownership.CortexOwned && (decision.Action == ownership.Unchanged || decision.Action == ownership.Replace) {
			allowed[role] = true
		}
	}
	return allowed
}

func shadowAllowed(scanned shadowCandidate, role qarole.RoleID, allowed map[qarole.RoleID]bool, observation FilesystemObservation) bool {
	exact, found := observation.Exact("actors/" + string(role))
	return found && scanned.location == globalAgents && !scanned.unsafe && allowed[role] && scanned.mode.Perm() == installplan.CanonicalFileMode && exact.Mode() == installplan.CanonicalFileMode && bytes.Equal(scanned.bytes, exact.Bytes())
}

func recheckAllowedActor(root string, role qarole.RoleID, scanned shadowCandidate, observation FilesystemObservation) bool {
	rechecked, err := readShadowCandidate(filepath.Join(root, "agents"), globalAgents, scanned.basename)
	if err != nil || !shadowAllowed(rechecked, role, map[qarole.RoleID]bool{role: true}, observation) {
		return false
	}
	return rechecked.mode.Perm() == scanned.mode.Perm() && bytes.Equal(rechecked.bytes, scanned.bytes)
}

func shadowNames(data []byte, targets map[string]qarole.RoleID) []qarole.RoleID {
	lines := bytes.Split(data, []byte("\n"))
	if string(bytes.TrimSpace(lines[0])) != "---" {
		return nil
	}
	found := map[qarole.RoleID]bool{}
	for _, line := range lines[1:] {
		if string(bytes.TrimSpace(line)) == "---" {
			break
		}
		text := string(line)
		if text == "" || strings.HasPrefix(text, "#") || text[0] == ' ' || text[0] == '\t' {
			continue
		}
		key, value, hasValue := strings.Cut(text, ":")
		if !hasValue || strings.TrimSpace(key) != "name" {
			continue
		}
		if role, target := targets[normalizedShadowName(value)+".md"]; target {
			found[role] = true
		}
	}
	roles := make([]qarole.RoleID, 0, len(found))
	for role := range found {
		roles = append(roles, role)
	}
	sort.Slice(roles, func(left, right int) bool { return roles[left] < roles[right] })
	return roles
}

func normalizedShadowName(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "\"") {
		if decoded, err := strconv.Unquote(value); err == nil {
			return decoded
		}
	}
	if !strings.HasPrefix(value, "'") {
		value, _, _ = strings.Cut(value, " #")
	}
	return strings.Trim(value, "\"'")
}

func publicShadowLocation(location shadowLocation) ShadowLocation { return ShadowLocation(location) }

func sortedShadowConflicts(conflicts map[ShadowConflict]struct{}) []ShadowConflict {
	result := make([]ShadowConflict, 0, len(conflicts))
	for conflict := range conflicts {
		result = append(result, conflict)
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].role == result[right].role {
			return result[left].location < result[right].location
		}
		return result[left].role < result[right].role
	})
	return result
}

func shadowInvalid() error { return errors.New("install observe: actor shadow observation failed") }
