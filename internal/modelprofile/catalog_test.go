package modelprofile

import (
	"errors"
	"maps"
	"slices"
	"testing"
)

func TestCatalogIdentity(t *testing.T) {
	if !slices.Equal(Profiles(), []Profile{ProfileAnthropic, ProfileMixed, ProfileNAN, ProfileOpenAI}) {
		t.Fatal("Profiles() must be exactly anthropic, mixed, nan, openai (sorted)")
	}
	if !slices.Equal(Runtimes(), []Runtime{RuntimePi, RuntimeOpenCode, RuntimeClaude}) {
		t.Fatal("Runtimes() must be exactly pi, opencode, claude")
	}
}

func TestLookupErrors(t *testing.T) {
	if _, err := SpecialistRoles("nope"); !errors.Is(err, ErrUnknownProfile) {
		t.Fatalf("SpecialistRoles err = %v, want ErrUnknownProfile", err)
	}
	if _, err := ResolvePrimary("nope", RuntimePi); !errors.Is(err, ErrUnknownProfile) {
		t.Fatalf("ResolvePrimary err = %v, want ErrUnknownProfile", err)
	}
	if _, err := ResolveSpecialist(ProfileNAN, RuntimePi, "no-such-role"); !errors.Is(err, ErrUnknownRole) {
		t.Fatalf("err = %v, want ErrUnknownRole", err)
	}
}

func TestResolvePrimary(t *testing.T) {
	want := map[Profile]model{
		ProfileOpenAI:    oaModel("gpt-6-astra"),
		ProfileNAN:       nanModel("glm5.3"),
		ProfileMixed:     oaModel("gpt-6-astra"),
		ProfileAnthropic: {RuntimeClaude: "claude-fable-5"},
	}
	for p, w := range want {
		for _, rt := range Runtimes() {
			got, err := ResolvePrimary(p, rt)
			if w[rt] == "" {
				if !errors.Is(err, ErrUnrepresentable) {
					t.Fatalf("ResolvePrimary(%q,%q) err = %v, want ErrUnrepresentable", p, rt, err)
				}
				continue
			}
			if err != nil || got.ModelID != w[rt] || got.ConversationalLevel != ConversationalLevelMedium {
				t.Fatalf("ResolvePrimary(%q,%q) = (%q,%q) err %v, want %q at level %q", p, rt, got.ModelID, got.ConversationalLevel, err, w[rt], ConversationalLevelMedium)
			}
		}
	}
}

// checkSpecialists pins a profile against its normative approved mapping: exact role set, count, and per-runtime IDs.
func checkSpecialists(t *testing.T, p Profile, count int, groups map[string][]string, want func(string) model) {
	t.Helper()
	expected := map[string]string{}
	for name, roles := range groups {
		for _, role := range roles {
			expected[role] = name
		}
	}
	if len(expected) != count {
		t.Fatalf("approved %q mapping has %d roles, want %d", p, len(expected), count)
	}
	roles, err := SpecialistRoles(p)
	if err != nil || !slices.Equal(roles, slices.Sorted(maps.Keys(expected))) {
		t.Fatalf("SpecialistRoles(%q) = %v (err %v), want exactly the approved roles", p, roles, err)
	}
	for role, name := range expected {
		w := want(name)
		for _, rt := range Runtimes() {
			got, err := ResolveSpecialist(p, rt, role)
			if w[rt] == "" {
				if !errors.Is(err, ErrUnrepresentable) {
					t.Fatalf("ResolveSpecialist(%q,%q,%q) err = %v, want ErrUnrepresentable", p, rt, role, err)
				}
				continue
			}
			if err != nil || got != w[rt] {
				t.Fatalf("ResolveSpecialist(%q,%q,%q) = %q (err %v), want %q", p, rt, role, got, err, w[rt])
			}
		}
	}
}

var wantNan = map[string][]string{
	"glm5.3":            {"sdd-proposal", "sdd-design", "review-risk", "jd-judge-a"},
	"deepseek-v4-flash": {"sdd-apply", "jd-fix-agent", "review-refuter", "jd-judge-b"},
	"glm5.3-flash":      {"gentle-ai-worker", "sdd-spec", "sdd-verify", "review-reliability"},
	"mimo-v2.5":         {"sdd-onboard", "review-resilience"},
	"qwen3.8-flash":     {"sdd-init", "review-validator"},
	"qwen3.6":           {"gentle-ai-explore", "gentle-ai-verify", "sdd-explore", "sdd-tasks", "sdd-research", "sdd-archive", "sdd-sync", "review-readability"},
	"gemma4":            {"sdd-status"},
}

func TestSpecialistMappings(t *testing.T) {
	checkSpecialists(t, ProfileNAN, 25, wantNan, nanModel)
	checkSpecialists(t, ProfileOpenAI, 25, map[string][]string{
		"gpt-5.6-sol":   {"sdd-proposal", "sdd-design", "sdd-onboard", "review-refuter", "jd-judge-a", "sdd-spec", "sdd-verify", "review-validator"},
		"gpt-5.6-terra": {"review-risk", "sdd-apply", "jd-fix-agent", "jd-judge-b", "gentle-ai-worker", "review-reliability", "review-resilience", "sdd-init", "gentle-ai-explore", "sdd-explore", "sdd-tasks", "sdd-research", "review-readability"},
		"gpt-5.6-luna":  {"gentle-ai-verify", "sdd-archive", "sdd-sync", "sdd-status"},
	}, oaModel)
	wantMixed := maps.Clone(wantNan)
	wantMixed["gpt-5.6-sol"] = []string{"sdd-proposal", "sdd-design", "sdd-onboard", "review-refuter", "jd-judge-a"}
	wantMixed["glm5.3"] = []string{"review-risk"}
	wantMixed["deepseek-v4-flash"] = []string{"sdd-apply", "jd-fix-agent", "jd-judge-b"}
	wantMixed["mimo-v2.5"] = []string{"review-resilience"}
	checkSpecialists(t, ProfileMixed, 25, wantMixed, func(name string) model {
		if name == "gpt-5.6-sol" {
			return oaModel(name)
		}
		return nanModel(name)
	})
	checkSpecialists(t, ProfileAnthropic, 19, map[string][]string{
		"opus":   {"jd-fix-agent", "jd-judge-a", "review-refuter", "review-risk", "sdd-design", "sdd-propose", "sdd-verify"},
		"sonnet": {"jd-judge-b", "review-readability", "review-reliability", "review-resilience", "sdd-apply", "sdd-explore", "sdd-init", "sdd-research", "sdd-spec", "sdd-tasks"},
		"haiku":  {"sdd-archive", "sdd-onboard"},
	}, func(alias string) model { return model{RuntimeClaude: alias} })
}

func TestNoExcludedStaleIDs(t *testing.T) {
	stale := map[string]bool{"nan/glm5.2": true, "nan/deepseek-v4-flash-0731": true}
	for _, p := range Profiles() {
		roles, _ := SpecialistRoles(p)
		for _, rt := range Runtimes() {
			if prim, err := ResolvePrimary(p, rt); err == nil && stale[prim.ModelID] {
				t.Fatalf("stale ID %q resolved for %q on %q", prim.ModelID, p, rt)
			}
			for _, role := range roles {
				if id, err := ResolveSpecialist(p, rt, role); err == nil && stale[id] {
					t.Fatalf("stale ID %q resolved for %q/%q/%q", id, p, rt, role)
				}
			}
		}
	}
	if id, err := ResolveSpecialist(ProfileNAN, RuntimePi, "sdd-apply"); err != nil || id != "nan/deepseek-v4-flash" {
		t.Fatalf("got %q (%v), want nan/deepseek-v4-flash", id, err)
	}
}

func TestDefensiveCopies(t *testing.T) {
	profiles := Profiles()
	profiles[0] = "corrupted"
	runtimes := Runtimes()
	runtimes[0] = "corrupted"
	if !slices.Equal(Profiles(), []Profile{ProfileAnthropic, ProfileMixed, ProfileNAN, ProfileOpenAI}) || !slices.Equal(Runtimes(), []Runtime{RuntimePi, RuntimeOpenCode, RuntimeClaude}) {
		t.Fatal("Profiles()/Runtimes() leaked mutable canonical state")
	}
	roles, _ := SpecialistRoles(ProfileNAN)
	roles[0] = "corrupted"
	if fresh, _ := SpecialistRoles(ProfileNAN); slices.Equal(roles, fresh) || !slices.IsSorted(fresh) {
		t.Fatal("SpecialistRoles() leaked mutable canonical state")
	}
}
