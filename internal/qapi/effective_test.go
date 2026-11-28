package qapi

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/refactor-ia/cortex/internal/qarole"
	"github.com/refactor-ia/cortex/internal/qaroute"
)

func effectiveRoot(t *testing.T, profile string) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if profile != "" {
		if err := os.MkdirAll(filepath.Join(root, "cortex"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "cortex", "qa-profiles.json"), []byte(profile), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestEffectiveRoutesMatchesRunResolutionForEveryRoleAndBackend(t *testing.T) {
	root := effectiveRoot(t, `{"schemaVersion":1,"defaultProfile":"mine","profiles":{"mine":{"routes":{"test-runner":{"claude":{"provider":"acme","model":"m1","effort":"low"}}}}}}`)
	routes, failure := EffectiveRoutes(root, "mine")
	if failure.Code != "" {
		t.Fatalf("failure %q", failure.Code)
	}
	roles := qarole.Catalog()
	if len(routes) != len(roles)*3 {
		t.Fatalf("got %d rows", len(routes))
	}
	index := 0
	for _, role := range roles {
		for _, backend := range qaroute.Backends() {
			row := routes[index]
			index++
			if row.Role != role.ID || row.Backend != backend {
				t.Fatalf("row %d = %s/%s", index, row.Role, row.Backend)
			}
			want, wantFailure := resolveProfileRoute(root, AdmissionRequest{Role: role.ID, Backend: backend, Profile: "mine"})
			if wantFailure.Code != "" {
				if row.Source != "unset" || row.Provider != "" {
					t.Fatalf("%s/%s = %+v, want unset", role.ID, backend, row)
				}
				continue
			}
			if row.Source != "profile:mine" || row.Provider != want.Provider || row.Model != want.Model || row.Effort != want.Effort {
				t.Fatalf("%s/%s = %+v, want %+v", role.ID, backend, row, want)
			}
		}
	}
}

func TestEffectiveRoutesWithoutProfileReportsResolverSource(t *testing.T) {
	root := effectiveRoot(t, "")
	routes, failure := EffectiveRoutes(root, "")
	if failure.Code != "" || len(routes) == 0 {
		t.Fatalf("failure %q rows %d", failure.Code, len(routes))
	}
	for _, row := range routes {
		want, wantFailure := resolveProfileRoute(root, AdmissionRequest{Role: row.Role, Backend: row.Backend})
		wantSource := want.ProfileID
		if wantFailure.Code != "" {
			wantSource = "unset"
		}
		if row.Source != wantSource || row.Provider != want.Provider || row.Model != want.Model {
			t.Fatalf("%+v want %+v", row, want)
		}
	}
}

func TestEffectiveRoutesInvalidProfileAborts(t *testing.T) {
	root := effectiveRoot(t, `{"schemaVersion":1,"defaultProfile":"mine","profiles":{"mine":{"routes":{}}}}`)
	routes, failure := EffectiveRoutes(root, "other")
	if failure.Code != "profile_invalid" || routes != nil {
		t.Fatalf("failure %q rows %d", failure.Code, len(routes))
	}
}
