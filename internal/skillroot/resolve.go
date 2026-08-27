// Package skillroot resolves symbolic skill destinations without filesystem access.
// It assumes configured roots are trusted and not concurrently mutated; descriptor
// hardening is deferred to a materialization boundary.
package skillroot

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/refactor-ia/cortex/internal/runtimematrix"
	"github.com/refactor-ia/cortex/internal/skilldest"
)

// Inputs are caller-owned values needed for deterministic root resolution.
type Inputs struct {
	Home             string
	PiCodingAgentDir string
	ClaudeConfigDir  string
}

// UninstallRoot binds one supported runtime to its canonical Cortex-owned user root.
// Its values are immutable, and ResolveUninstallRoots returns a detached slice.
type UninstallRoot struct {
	runtimeID runtimematrix.RuntimeID
	rootKind  skilldest.RootKind
	rootPath  string
}

// RuntimeID returns the bound supported runtime.
func (root UninstallRoot) RuntimeID() runtimematrix.RuntimeID { return root.runtimeID }

// RootKind returns the approved symbolic user root.
func (root UninstallRoot) RootKind() skilldest.RootKind { return root.rootKind }

// RootPath returns the canonical absolute user root without materializing it.
func (root UninstallRoot) RootPath() string { return root.rootPath }

// Plan is an immutable resolved skill destination plan.
type Plan struct {
	runtimeID, snapshotFingerprint string
	rootKind                       skilldest.RootKind
	rootPath                       string
	targets                        []Target
}

func (plan Plan) RuntimeID() runtimematrix.RuntimeID { return runtimematrix.RuntimeID(plan.runtimeID) }
func (plan Plan) RootKind() skilldest.RootKind       { return plan.rootKind }
func (plan Plan) SnapshotFingerprint() string        { return plan.snapshotFingerprint }
func (plan Plan) RootPath() string                   { return plan.rootPath }
func (plan Plan) Targets() []Target {
	targets := make([]Target, len(plan.targets))
	for i, target := range plan.targets {
		targets[i] = target.clone()
	}
	return targets
}

// Target is one absolute, non-materialized destination.
type Target struct {
	logicalID, absolutePath, sha256 string
	content                         []byte
}

func (target Target) LogicalID() string    { return target.logicalID }
func (target Target) AbsolutePath() string { return target.absolutePath }
func (target Target) SHA256() string       { return target.sha256 }
func (target Target) Content() []byte      { return append([]byte{}, target.content...) }
func (target Target) clone() Target        { target.content = target.Content(); return target }

// Resolve binds a symbolic plan to caller-owned user roots without I/O.
func Resolve(symbolic skilldest.Plan, inputs Inputs) (Plan, error) {
	if !validPath(inputs.Home) || !validSymbolic(symbolic) {
		return Plan{}, invalid()
	}
	root, ok := resolveRoot(symbolic.RuntimeID(), inputs)
	if !ok || !validPath(root) {
		return Plan{}, invalid()
	}
	destinations := symbolic.Destinations()
	targets := make([]Target, len(destinations))
	for i, destination := range destinations {
		relative := filepath.FromSlash(destination.RelativePath())
		absolute := filepath.Join(root, relative)
		rel, err := filepath.Rel(root, absolute)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || !validPath(absolute) {
			return Plan{}, invalid()
		}
		targets[i] = Target{destination.LogicalID(), absolute, destination.SHA256(), destination.Content()}
	}
	return Plan{runtimeID: string(symbolic.RuntimeID()), rootKind: symbolic.RootKind(), snapshotFingerprint: symbolic.SnapshotFingerprint(), rootPath: root, targets: targets}, nil
}

// ResolveSystem reads only the system home and documented Pi and Claude overrides.
func ResolveSystem(symbolic skilldest.Plan) (Plan, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Plan{}, invalid()
	}
	return Resolve(symbolic, Inputs{Home: home, PiCodingAgentDir: os.Getenv("PI_CODING_AGENT_DIR"), ClaudeConfigDir: os.Getenv("CLAUDE_CONFIG_DIR")})
}

// ResolveUninstallRoots resolves all supported runtime roots without a desired plan or I/O.
// It returns detached descriptors in canonical Pi, OpenCode, Claude Code order.
func ResolveUninstallRoots(inputs Inputs) ([]UninstallRoot, error) {
	if !validPath(inputs.Home) {
		return nil, invalid()
	}
	runtimes := []runtimematrix.RuntimeID{
		runtimematrix.RuntimePi,
		runtimematrix.RuntimeOpenCode,
		runtimematrix.RuntimeClaudeCode,
	}
	roots := make([]UninstallRoot, 0, len(runtimes))
	for _, runtime := range runtimes {
		rootPath, ok := resolveRoot(runtime, inputs)
		rootKind := expectedRoot(runtime)
		if !ok || rootKind == "" || !validPath(rootPath) {
			return nil, invalid()
		}
		roots = append(roots, UninstallRoot{runtimeID: runtime, rootKind: rootKind, rootPath: rootPath})
	}
	return roots, nil
}

// ResolveSystemUninstallRoots reads only the system home and documented Pi and Claude overrides.
func ResolveSystemUninstallRoots() ([]UninstallRoot, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, invalid()
	}
	return ResolveUninstallRoots(Inputs{Home: home, PiCodingAgentDir: os.Getenv("PI_CODING_AGENT_DIR"), ClaudeConfigDir: os.Getenv("CLAUDE_CONFIG_DIR")})
}

func resolveRoot(runtime runtimematrix.RuntimeID, in Inputs) (string, bool) {
	switch runtime {
	case runtimematrix.RuntimePi:
		if in.PiCodingAgentDir == "" {
			return filepath.Join(in.Home, ".pi", "agent"), true
		}
		if in.PiCodingAgentDir == "~" {
			return in.Home, true
		}
		prefix := "~" + string(filepath.Separator)
		if strings.HasPrefix(in.PiCodingAgentDir, prefix) {
			tail := strings.TrimPrefix(in.PiCodingAgentDir, prefix)
			if tail == "" || tail == "." || tail == ".." || strings.HasPrefix(tail, ".."+string(filepath.Separator)) || filepath.IsAbs(tail) || filepath.Clean(tail) != tail || strings.ContainsRune(tail, 0) || badSeparator(tail) {
				return "", false
			}
			return filepath.Join(in.Home, tail), true
		}
		if strings.Contains(in.PiCodingAgentDir, "~") || !validPath(in.PiCodingAgentDir) {
			return "", false
		}
		return in.PiCodingAgentDir, true
	case runtimematrix.RuntimeOpenCode:
		return filepath.Join(in.Home, ".config", "opencode"), true
	case runtimematrix.RuntimeClaudeCode:
		if in.ClaudeConfigDir == "" {
			return filepath.Join(in.Home, ".claude"), true
		}
		if strings.Contains(in.ClaudeConfigDir, "~") || !validPath(in.ClaudeConfigDir) {
			return "", false
		}
		return in.ClaudeConfigDir, true
	}
	return "", false
}

func validSymbolic(plan skilldest.Plan) bool {
	if !validHash(plan.SnapshotFingerprint()) || expectedRoot(plan.RuntimeID()) != plan.RootKind() {
		return false
	}
	destinations := plan.Destinations()
	if len(destinations) == 0 {
		return false
	}
	for i, destination := range destinations {
		id := destination.LogicalID()
		if !validID(id) || destination.RelativePath() != "skills/cortex-"+strings.TrimPrefix(id, "skills/")+"/SKILL.md" || !validHash(destination.SHA256()) || len(destination.Content()) == 0 || hash(destination.Content()) != destination.SHA256() || (i > 0 && destinations[i-1].LogicalID() >= id) {
			return false
		}
	}
	return true
}

func expectedRoot(runtime runtimematrix.RuntimeID) skilldest.RootKind {
	switch runtime {
	case runtimematrix.RuntimePi:
		return skilldest.RootKindPiUserAgent
	case runtimematrix.RuntimeOpenCode:
		return skilldest.RootKindOpenCodeUserConfig
	case runtimematrix.RuntimeClaudeCode:
		return skilldest.RootKindClaudeCodeUser
	}
	return ""
}
func validID(id string) bool {
	name := strings.TrimPrefix(id, "skills/")
	if !strings.HasPrefix(id, "skills/") || strings.Count(id, "/") != 1 || len(name) == 0 || len(name) > 57 || name[0] == '-' || name[len(name)-1] == '-' || strings.Contains(name, "--") || strings.HasPrefix(name, "cortex-") {
		return false
	}
	for _, c := range name {
		if c != '-' && (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}
func validPath(path string) bool {
	return path != "" && !strings.ContainsRune(path, 0) && !badSeparator(path) && filepath.IsAbs(path) && filepath.Clean(path) == path && filepath.Dir(path) != path
}
func badSeparator(path string) bool {
	if filepath.Separator == '/' {
		return strings.Contains(path, "\\")
	}
	return strings.Contains(path, "/")
}
func validHash(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, c := range value {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}
func hash(content []byte) string { sum := sha256.Sum256(content); return hex.EncodeToString(sum[:]) }
func invalid() error             { return errors.New("skill root: invalid plan") }
