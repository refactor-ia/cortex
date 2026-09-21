package qaroute

import (
	"crypto/sha256"
	"fmt"

	"github.com/refactor-ia/cortex/internal/runtimematrix"
)

// allowedBackends is the closed set of execution backends the route policy
// admits. Route resolution is the only place that decides which backend a role
// may run on, so a backend missing from this set can never be resolved,
// invoked, or recorded in a receipt, no matter what adapters exist elsewhere.
// Adding an entry is therefore a deliberate policy change, never a side effect
// of a new adapter landing in another package.
var allowedBackends = map[string]bool{"pi": true, "claude": true, "opencode": true}

// Admits reports whether the route policy admits one execution backend. It is
// the single source every other package asks instead of carrying its own copy
// of the set, so the policy stays one decision in one place.
func Admits(backend string) bool {
	return allowedBackends[backend]
}

// backendRuntimes maps one admitted execution backend to the runtime it runs
// on. The two vocabularies are kept apart on purpose: this package names the
// execution policy token ("claude") and runtimematrix names the installation
// target ("claude-code"), and nothing guarantees they stay spelled alike. It
// lives beside the admitted set so a backend can never be admitted for
// execution while no runtime is known to install its assets.
var backendRuntimes = map[string]runtimematrix.RuntimeID{
	"pi":       runtimematrix.RuntimePi,
	"claude":   runtimematrix.RuntimeClaudeCode,
	"opencode": runtimematrix.RuntimeOpenCode,
}

// RuntimeFor reports the runtime one admitted execution backend runs on, and
// whose installed assets and skill projection therefore belong to it.
func RuntimeFor(backend string) (runtimematrix.RuntimeID, bool) {
	runtimeID, known := backendRuntimes[backend]
	return runtimeID, known && allowedBackends[backend]
}

// BindsInstalledActor reports whether one backend's runtime reads the actor
// from a file it is handed, which is the only reason an installed actor
// artifact is required for it.
//
// Pi takes the actor as a path in argv, so its admission must observe that
// exact file. Claude Code has no flag that takes an actor file and OpenCode's
// --agent selects from the operator's own configuration, which is the ambient
// state the adapters neutralize; on both the actor reaches the run inside the
// bounded input frame, and install ships none. This is a property of the
// runtime's own interface, not a policy knob.
func BindsInstalledActor(backend string) bool {
	return backend == "pi"
}

// InlinesSkillText reports whether one backend's input frame must carry the
// skill's own text instead of asking the runtime to load it.
//
// The opening line `/skill:cortex-<role>` means two different things depending
// on who reads it. Pi loads the installed skill from it. Claude Code and
// OpenCode implement it as a slash command and answer it with a tool call —
// which a QA run forbids on purpose and which would spend its single turn
// anyway — so on those runtimes the reference is not merely useless, it ends
// the run before the role produces anything. They receive the skill as text,
// which executes nothing. Like BindsInstalledActor this is a property of the
// runtime's own interface, not a policy knob.
func InlinesSkillText(backend string) bool {
	return Admits(backend) && backend != "pi"
}

func Resolve(request Request, snapshot Snapshot) (ResolvedRoute, Failure) {
	base, allowed := defaults[request.Role]
	if !allowed || !allowedBackends[request.Backend] {
		return ResolvedRoute{}, Failure{Code: "route_disallowed"}
	}
	profileID, digest := "role-default", ""
	if request.ProfileID != "" {
		if !snapshot.Present || !profileIDPattern.MatchString(request.ProfileID) {
			return ResolvedRoute{}, Failure{Code: "profile_invalid"}
		}
		profiles, valid := decodeProfiles(snapshot.Bytes)
		if !valid {
			return ResolvedRoute{}, Failure{Code: "profile_invalid"}
		}
		selected, exists := profiles[request.ProfileID]
		if !exists {
			return ResolvedRoute{}, Failure{Code: "profile_invalid"}
		}
		var found bool
		base, found = selected.routes[request.Role]
		if !found {
			return ResolvedRoute{}, Failure{Code: "route_incomplete"}
		}
		profileID = request.ProfileID
		digest = fmt.Sprintf("%x", sha256.Sum256(snapshot.Bytes))
	}
	fields := make([]string, 0, 3)
	if request.Override.Provider != "" {
		base.provider = request.Override.Provider
		fields = append(fields, "provider")
	}
	if request.Override.Model != "" {
		base.model = request.Override.Model
		fields = append(fields, "model")
	}
	if request.Override.Effort != "" {
		base.effort = request.Override.Effort
		fields = append(fields, "effort")
	}
	if base.provider == "" || base.model == "" || base.effort == "" {
		return ResolvedRoute{}, Failure{Code: "route_incomplete"}
	}
	if base.provider != "nan" {
		return ResolvedRoute{}, Failure{Code: "non_nan_route"}
	}
	if base != defaults[request.Role] {
		return ResolvedRoute{}, Failure{Code: "route_disallowed"}
	}
	return ResolvedRoute{
		PolicyVersion: PolicyVersion, Role: request.Role, Backend: request.Backend,
		Provider: base.provider, Model: base.model, Effort: base.effort,
		ProfileID: profileID, ProfileSHA256: digest, OverrideFields: fields,
	}, Failure{}
}
