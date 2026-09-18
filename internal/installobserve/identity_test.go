package installobserve_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/refactor-ia/cortex/internal/installobserve"
	"github.com/refactor-ia/cortex/internal/installplan"
	"github.com/refactor-ia/cortex/internal/installstate"
)

// TestObserveInstallationIDReadsOnlyCanonicalActorAwareState pins the guarantee
// the candidate builder relies on: the recorded identity is returned only when
// the state file proves itself canonical for this exact root, and every other
// shape reports no identity so the caller mints instead of inheriting.
func TestObserveInstallationIDReadsOnlyCanonicalActorAwareState(t *testing.T) {
	actorAware := func(t *testing.T) installplan.Plan { return makeActorAwareCandidate(t) }
	skillOnly := func(t *testing.T) installplan.Plan { plan, _ := makeCandidate(t, "one", "alpha"); return plan }
	for _, tc := range []struct {
		name  string
		build func(t *testing.T) installplan.Plan
		setup func(t *testing.T, candidate installplan.Plan)
		found bool
	}{
		{
			name:  "canonical v2 state yields its recorded identity",
			build: actorAware,
			setup: func(t *testing.T, candidate installplan.Plan) { writeCandidateFiles(t, candidate) },
			found: true,
		},
		{
			name:  "an existing root without state has no identity",
			build: actorAware,
			setup: func(t *testing.T, candidate installplan.Plan) {
				if err := os.MkdirAll(candidate.RootPath(), 0o700); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:  "an absent root has no identity",
			build: actorAware,
			setup: func(*testing.T, installplan.Plan) {},
		},
		{
			name:  "a v1 skill-only installation has no identity",
			build: skillOnly,
			setup: func(t *testing.T, candidate installplan.Plan) { writeCandidateFiles(t, candidate) },
		},
		{
			name:  "unparseable state is rejected, not inherited",
			build: actorAware,
			setup: func(t *testing.T, candidate installplan.Plan) {
				writeCandidateFiles(t, candidate)
				writeStateBytes(t, candidate, []byte("{not json"))
			},
		},
		{
			name:  "non-canonical bytes carrying a valid identity are rejected",
			build: actorAware,
			setup: func(t *testing.T, candidate installplan.Plan) {
				writeCandidateFiles(t, candidate)
				writeStateBytes(t, candidate, append([]byte("  "), candidate.StateJSON()...))
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := tc.build(t)
			tc.setup(t, candidate)
			want := installstate.InstallationID("")
			if tc.found {
				want = candidate.InstalledState().InstallationID()
			}
			got, found := installobserve.ObserveInstallationID(uninstallRoot(t, candidate), installobserve.DefaultOptions())
			if found != tc.found || got != want {
				t.Fatalf("ObserveInstallationID() = (%q, %v), want (%q, %v)", got, found, want, tc.found)
			}
		})
	}
}

func writeStateBytes(t *testing.T, candidate installplan.Plan, data []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(candidate.RootPath(), ".cortex", "install-state.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}
