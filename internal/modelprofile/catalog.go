// Package modelprofile provides an inert, validated, in-memory catalog of named
// model profiles, resolving primary and specialist model resources to
// runtime-specific IDs. Specialist assignments carry model pointers only; no
// effort, reasoning, or variant is encoded. No I/O, probing, or mutable state.
package modelprofile

import (
	"errors"
	"fmt"
	"maps"
	"slices"
)

// Runtime identifies a supported agent runtime; Profile a named catalog profile.
type Runtime string
type Profile string

const (
	RuntimePi       Runtime = "pi"
	RuntimeOpenCode Runtime = "opencode"
	RuntimeClaude   Runtime = "claude"

	ProfileOpenAI    Profile = "openai"
	ProfileNAN       Profile = "nan"
	ProfileMixed     Profile = "mixed"
	ProfileAnthropic Profile = "anthropic"
)

// ConversationalLevelLow is the user-approved conversational level for primary models.
const ConversationalLevelLow = "low"

// Errors reported when a lookup yields no resolvable model.
var (
	ErrUnrepresentable = errors.New("unrepresentable profile/runtime pair")
	ErrUnknownProfile  = errors.New("unknown model profile")
	ErrUnknownRole     = errors.New("unknown specialist role")
)

// Primary is a primary conversational model assignment.
type Primary struct{ ModelID, ConversationalLevel string }

// model is a named model resource keyed by runtime; a missing key means the
// model is unrepresentable on that runtime.
type model map[Runtime]string

var oaModel = func(name string) model {
	return model{RuntimePi: "openai-codex/" + name, RuntimeOpenCode: "openai/" + name}
}
var nanModel = func(name string) model { return model{RuntimePi: "nan/" + name, RuntimeOpenCode: "nan/" + name} }

func modelsWith(build func(string) model, names ...string) map[string]model {
	m := make(map[string]model, len(names))
	for _, n := range names {
		m[n] = build(n)
	}
	return m
}

// models is the resource registry keyed by name; anthropic specialist pointers
// are the approved alias names exactly as issued (versioned IDs out of scope).
var models = func() map[string]model {
	m := modelsWith(oaModel, "gpt-6-astra", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna")
	maps.Copy(m, modelsWith(nanModel, "glm5.3", "deepseek-v4-flash", "glm5.3-flash", "mimo-v2.5", "qwen3.8-flash", "qwen3.6", "gemma4"))
	for _, e := range [][2]string{{"claude-fable-5", "claude-fable-5"}, {"opus", "opus"}, {"sonnet", "sonnet"}, {"haiku", "haiku"}} {
		m[e[0]] = model{RuntimeClaude: e[1]}
	}
	return m
}()

// spec is the declared source data for one profile; roleGroups maps a model
// name to every specialist role pointing at it.
type spec struct {
	primary     string
	roleGroups  map[string][]string
	specialists map[string]string // derived: role -> model name
}

// nanGroups is the approved NaN specialist mapping; mixed reuses all of it except Sol-reassigned roles.
var nanGroups = map[string][]string{
	"glm5.3":            {"sdd-proposal", "sdd-design", "review-risk", "jd-judge-a"},
	"deepseek-v4-flash": {"sdd-apply", "jd-fix-agent", "review-refuter", "jd-judge-b"},
	"glm5.3-flash":      {"gentle-ai-worker", "sdd-spec", "sdd-verify", "review-reliability"},
	"mimo-v2.5":         {"sdd-onboard", "review-resilience"},
	"qwen3.8-flash":     {"sdd-init", "review-validator"},
	"qwen3.6":           {"gentle-ai-explore", "gentle-ai-verify", "sdd-explore", "sdd-tasks", "sdd-research", "sdd-archive", "sdd-sync", "review-readability"},
	"gemma4":            {"sdd-status"},
}

// mixedGroups is the approved mixed mapping: Sol for exactly five roles, everything else as NaN.
func mixedGroups() map[string][]string {
	g := maps.Clone(nanGroups)
	g["gpt-5.6-sol"] = []string{"sdd-proposal", "sdd-design", "sdd-onboard", "review-refuter", "jd-judge-a"}
	g["glm5.3"] = []string{"review-risk"}
	g["deepseek-v4-flash"] = []string{"sdd-apply", "jd-fix-agent", "jd-judge-b"}
	g["mimo-v2.5"] = []string{"review-resilience"}
	return g
}

var specs = map[Profile]spec{
	ProfileOpenAI: {
		primary: "gpt-6-astra",
		roleGroups: map[string][]string{
			"gpt-5.6-sol":   {"sdd-proposal", "sdd-design", "sdd-onboard", "review-refuter", "jd-judge-a", "sdd-spec", "sdd-verify", "review-validator"},
			"gpt-5.6-terra": {"review-risk", "sdd-apply", "jd-fix-agent", "jd-judge-b", "gentle-ai-worker", "review-reliability", "review-resilience", "sdd-init", "gentle-ai-explore", "sdd-explore", "sdd-tasks", "sdd-research", "review-readability"},
			"gpt-5.6-luna":  {"gentle-ai-verify", "sdd-archive", "sdd-sync", "sdd-status"},
		},
	},
	ProfileNAN:   {primary: "glm5.3", roleGroups: nanGroups},
	ProfileMixed: {primary: "gpt-6-astra", roleGroups: mixedGroups()},
	ProfileAnthropic: {
		primary: "claude-fable-5",
		roleGroups: map[string][]string{
			"opus":   {"jd-fix-agent", "jd-judge-a", "review-refuter", "review-risk", "sdd-design", "sdd-propose", "sdd-verify"},
			"sonnet": {"jd-judge-b", "review-readability", "review-reliability", "review-resilience", "sdd-apply", "sdd-explore", "sdd-init", "sdd-research", "sdd-spec", "sdd-tasks"},
			"haiku":  {"sdd-archive", "sdd-onboard"},
		},
	},
}

// catalog is the validated, derived form of specs: every specialist role is
// indexed exactly once and every primary is representable on some runtime.
var catalog = func() map[Profile]spec {
	for p, s := range specs {
		if len(models[s.primary]) == 0 {
			panic(fmt.Sprintf("modelprofile: primary %q of %q has no runtime IDs", s.primary, p))
		}
		specialists := map[string]string{}
		for name, roles := range s.roleGroups {
			for _, role := range roles {
				if _, dup := specialists[role]; dup {
					panic(fmt.Sprintf("modelprofile: profile %q duplicates role %q", p, role))
				}
				specialists[role] = name
			}
		}
		s.specialists = specialists
		specs[p] = s
	}
	return specs
}()

func get(p Profile) (spec, error) {
	s, ok := catalog[p]
	if !ok {
		return spec{}, fmt.Errorf("%w: %q", ErrUnknownProfile, p)
	}
	return s, nil
}

// Profiles returns the exact set of catalog profile names, sorted.
func Profiles() []Profile { return slices.Sorted(maps.Keys(catalog)) }

// Runtimes returns the exact set of supported runtimes in declared order.
func Runtimes() []Runtime { return []Runtime{RuntimePi, RuntimeOpenCode, RuntimeClaude} }

// SpecialistRoles returns a fresh sorted copy of a profile's specialist roles.
func SpecialistRoles(p Profile) ([]string, error) {
	s, err := get(p)
	if err != nil {
		return nil, err
	}
	return slices.Sorted(maps.Keys(s.specialists)), nil
}

// ResolvePrimary resolves the profile's primary conversational model to a
// runtime-specific ID with its approved conversational level, or an error
// wrapping ErrUnrepresentable when no primary model exists on the runtime.
func ResolvePrimary(p Profile, r Runtime) (Primary, error) {
	if s, err := get(p); err != nil {
		return Primary{}, err
	} else if id := models[s.primary][r]; id != "" {
		return Primary{ModelID: id, ConversationalLevel: ConversationalLevelLow}, nil
	}
	return Primary{}, fmt.Errorf("%w: primary of %q on %q", ErrUnrepresentable, p, r)
}

// ResolveSpecialist resolves a specialist role's model pointer to a
// runtime-specific ID, or an error wrapping ErrUnrepresentable when the pointed
// model does not exist on that runtime. It never fabricates an ID.
func ResolveSpecialist(p Profile, r Runtime, role string) (string, error) {
	if s, err := get(p); err != nil {
		return "", err
	} else if name, ok := s.specialists[role]; !ok {
		return "", fmt.Errorf("%w: %q in profile %q", ErrUnknownRole, role, p)
	} else if id := models[name][r]; id != "" {
		return id, nil
	}
	return "", fmt.Errorf("%w: role %q of %q on %q", ErrUnrepresentable, role, p, r)
}
