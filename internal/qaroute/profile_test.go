package qaroute

import (
	"fmt"
	"strings"
	"testing"

	"github.com/refactor-ia/cortex/internal/qarole"
)

func TestResolveRejectsMalformedProfiles(t *testing.T) {
	valid := `{"schemaVersion":1,"defaultProfile":"balanced","profiles":{"balanced":{"routes":{"requirements-analyst":{"pi":{"provider":"nan","model":"qwen3.6","effort":"high"}}}}}}`
	for _, tc := range []struct {
		name string
		body []byte
	}{
		{"invalid-utf8", append([]byte(valid), 0xff)},
		{"duplicate-root", []byte(strings.Replace(valid, `"schemaVersion":1,`, `"schemaVersion":1,"schemaVersion":1,`, 1))},
		{"duplicate-profile", []byte(`{"schemaVersion":1,"defaultProfile":"balanced","profiles":{"balanced":{"routes":{}},"balanced":{"routes":{}}}}`)},
		{"unknown-field", []byte(strings.Replace(valid, `"schemaVersion":1,`, `"schemaVersion":1,"extra":true,`, 1))},
		{"unknown-route-field", []byte(strings.Replace(valid, `"effort":"high"`, `"effort":"high","extra":true`, 1))},
		{"trailing-json", []byte(valid + ` {}`)},
		{"unsupported-schema", []byte(strings.Replace(valid, `"schemaVersion":1`, `"schemaVersion":2`, 1))},
		{"ambiguous-backend", []byte(strings.Replace(valid, `"pi":{`, `"pi":{},"pi":{`, 1))},
		{"oversized", []byte(valid + strings.Repeat(" ", maxProfileBytes-len(valid)+1))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, failure := Resolve(Request{Role: qarole.RequirementsAnalyst, Backend: "pi", ProfileID: "balanced"}, Snapshot{Present: true, Bytes: tc.body})
			if failure.Code != "profile_invalid" {
				t.Fatalf("Resolve() failure = %q, want profile_invalid", failure.Code)
			}
		})
	}
}

func TestResolveUsesOnlyTheExplicitProfile(t *testing.T) {
	body := []byte(`{"schemaVersion":1,"defaultProfile":"first","profiles":{"first":{"routes":{"requirements-analyst":{"pi":{"provider":"nan","model":"qwen3.6","effort":"high"}}}},"second":{"routes":{"requirements-analyst":{"pi":{"provider":"nan","model":"glm5.2","effort":"high"}}}}}}`)
	for _, profileID := range []string{"missing", ""} {
		t.Run(fmt.Sprintf("%q", profileID), func(t *testing.T) {
			got, failure := Resolve(Request{Role: qarole.RequirementsAnalyst, Backend: "pi", ProfileID: profileID}, Snapshot{Present: true, Bytes: body})
			if profileID == "" {
				if failure.Code != "" || got.ProfileID != "role-default" {
					t.Fatalf("Resolve() = (%+v, %q)", got, failure.Code)
				}
				return
			}
			if failure.Code != "profile_invalid" {
				t.Fatalf("Resolve() failure = %q, want profile_invalid", failure.Code)
			}
		})
	}
}
