package qaroute

import (
	"crypto/sha256"
	"fmt"
	"reflect"
	"testing"

	"github.com/refactor-ia/cortex/internal/qarole"
)

func TestResolveDefaults(t *testing.T) {
	for _, tc := range []struct {
		role   qarole.RoleID
		model  string
		effort string
	}{
		{qarole.RequirementsAnalyst, "qwen3.6", "high"},
		{qarole.TestDesigner, "qwen3.6", "high"},
		{qarole.ExploratoryTester, "glm5.2", "high"},
		{qarole.AdversarialTester, "deepseek-v4-flash", "high"},
		{qarole.TestRunner, "qwen3.6", "low"},
		{qarole.EvidenceAuditor, "glm5.2", "high"},
	} {
		t.Run(string(tc.role), func(t *testing.T) {
			got, failure := Resolve(Request{Role: tc.role, Backend: "pi"}, Snapshot{})
			if failure.Code != "" {
				t.Fatalf("Resolve() failure = %q", failure.Code)
			}
			if got.Provider != "nan" || got.Model != tc.model || got.Effort != tc.effort || got.ProfileID != "role-default" || got.ProfileSHA256 != "" {
				t.Fatalf("Resolve() = %+v", got)
			}
		})
	}
}

func TestResolveProfileOverrideShapesAndOriginalDigest(t *testing.T) {
	snapshot := []byte("{\n  \"schemaVersion\": 1, \"defaultProfile\": \"balanced\", \"profiles\": {\"balanced\": {\"routes\": {\"test-runner\": {\"pi\": {\"provider\": \"nan\", \"model\": \"qwen3.6\", \"effort\": \"low\"}}}}}}\n")
	digest := fmt.Sprintf("%x", sha256.Sum256(snapshot))
	for _, tc := range []struct {
		name     string
		override Override
		fields   []string
	}{
		{"provider", Override{Provider: "nan"}, []string{"provider"}},
		{"model", Override{Model: "qwen3.6"}, []string{"model"}},
		{"effort", Override{Effort: "low"}, []string{"effort"}},
		{"provider-model", Override{Provider: "nan", Model: "qwen3.6"}, []string{"provider", "model"}},
		{"provider-effort", Override{Provider: "nan", Effort: "low"}, []string{"provider", "effort"}},
		{"model-effort", Override{Model: "qwen3.6", Effort: "low"}, []string{"model", "effort"}},
		{"complete", Override{Provider: "nan", Model: "qwen3.6", Effort: "low"}, []string{"provider", "model", "effort"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, failure := Resolve(Request{Role: qarole.TestRunner, Backend: "pi", ProfileID: "balanced", Override: tc.override}, Snapshot{Present: true, Bytes: snapshot})
			if failure.Code != "" {
				t.Fatalf("Resolve() failure = %q", failure.Code)
			}
			if got.ProfileSHA256 != digest || !reflect.DeepEqual(got.OverrideFields, tc.fields) || got.Provider != "nan" || got.Model != "qwen3.6" || got.Effort != "low" {
				t.Fatalf("Resolve() = %+v", got)
			}
		})
	}
}

func TestResolveCompleteOverrideCanReplaceAnIncompleteSelectedRoute(t *testing.T) {
	snapshot := []byte(`{"schemaVersion":1,"defaultProfile":"selected","profiles":{"selected":{"routes":{"test-runner":{"pi":{}}}}}}`)
	got, failure := Resolve(Request{
		Role: qarole.TestRunner, Backend: "pi", ProfileID: "selected",
		Override: Override{Provider: "nan", Model: "qwen3.6", Effort: "low"},
	}, Snapshot{Present: true, Bytes: snapshot})
	if failure.Code != "" || got.Model != "qwen3.6" || got.Effort != "low" {
		t.Fatalf("Resolve() = (%+v, %q)", got, failure.Code)
	}
}

func TestResolveRejectsTerminalRoutes(t *testing.T) {
	profile := func(provider, model, effort string) []byte {
		return []byte(fmt.Sprintf(`{"schemaVersion":1,"defaultProfile":"selected","profiles":{"selected":{"routes":{"requirements-analyst":{"pi":{"provider":%q,"model":%q,"effort":%q}}}}}}`, provider, model, effort))
	}
	for _, tc := range []struct {
		name     string
		request  Request
		snapshot Snapshot
		code     string
	}{
		{"missing-profile", Request{Role: qarole.RequirementsAnalyst, Backend: "pi", ProfileID: "selected"}, Snapshot{}, "profile_invalid"},
		{"incomplete-route", Request{Role: qarole.RequirementsAnalyst, Backend: "pi", ProfileID: "selected"}, Snapshot{Present: true, Bytes: []byte(`{"schemaVersion":1,"defaultProfile":"selected","profiles":{"selected":{"routes":{"requirements-analyst":{"pi":{"provider":"nan","model":"qwen3.6"}}}}}}`)}, "route_incomplete"},
		{"non-nan-precedes-disallowed", Request{Role: qarole.RequirementsAnalyst, Backend: "pi", ProfileID: "selected"}, Snapshot{Present: true, Bytes: profile("other", "qwen3.6", "high")}, "non_nan_route"},
		{"disallowed-route", Request{Role: qarole.RequirementsAnalyst, Backend: "pi", ProfileID: "selected"}, Snapshot{Present: true, Bytes: profile("nan", "glm5.2", "high")}, "route_disallowed"},
		{"disallowed-backend", Request{Role: qarole.RequirementsAnalyst, Backend: "other"}, Snapshot{}, "route_disallowed"},
		{"disallowed-role", Request{Role: "unknown", Backend: "pi"}, Snapshot{}, "route_disallowed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, failure := Resolve(tc.request, tc.snapshot)
			if failure.Code != tc.code {
				t.Fatalf("Resolve() failure = %q, want %q", failure.Code, tc.code)
			}
		})
	}
}
