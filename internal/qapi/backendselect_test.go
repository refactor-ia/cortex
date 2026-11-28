package qapi

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/refactor-ia/cortex/internal/qaadmission"
	"github.com/refactor-ia/cortex/internal/qarole"
)

// TestNewBackendCoversTheAdmittedSet pins that the selector answers for every
// backend the route policy admits and for nothing else. A token that differs
// only in case or whitespace is a different token.
func TestNewBackendCoversTheAdmittedSet(t *testing.T) {
	for _, id := range []string{piBackendID, claudeBackendID, opencodeBackendID} {
		backend, ok := NewBackend(id, nil)
		if !ok || backend == nil || backend.ID() != id {
			t.Fatalf("NewBackend(%q) = %v, %t", id, backend, ok)
		}
	}
	for _, id := range []string{"", "PI", "codex", "pi ", "Claude"} {
		if backend, ok := NewBackend(id, nil); ok || backend != nil {
			t.Fatalf("NewBackend(%q) admitted an unknown backend", id)
		}
	}
}

// TestProbeBackendAvailabilityRejectsUnadmittedInput pins the closed door.
// The caller asks this about ambient runtimes, so an unknown backend, an
// unresolvable role, or a missing context must be a typed refusal reached
// before anything is bound or launched.
func TestProbeBackendAvailabilityRejectsUnadmittedInput(t *testing.T) {
	recorder := &probeRecorder{}
	recorder.install(t)
	for _, test := range []struct {
		name, backend string
		role          qarole.RoleID
		ctx           context.Context
	}{
		{"unknown backend", "codex", qarole.RequirementsAnalyst, context.Background()},
		{"empty backend", "", qarole.RequirementsAnalyst, context.Background()},
		{"unresolvable role", piBackendID, qarole.RoleID("archaeologist"), context.Background()},
		{"nil context", piBackendID, qarole.RequirementsAnalyst, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			verdict := ProbeBackendAvailability(test.ctx, test.backend, test.role, "", availabilityCWD(t), failingPathResolver{})
			if verdict.Available || verdict.Code != qaadmission.CodeUnsupportedRuntime {
				t.Fatalf("verdict = %#v, want unavailable unsupported_runtime", verdict)
			}
		})
	}
	if len(recorder.issued) != 0 {
		t.Fatalf("a refused request still issued probe commands: %v", recorder.issued)
	}
}

// TestProbeBackendAvailabilityReportsUnbindableRuntime pins that a runtime
// which cannot be bound is named unsupported rather than reported as an
// authentication problem it may not have. Binding failure and credential
// failure are different facts.
func TestProbeBackendAvailabilityReportsUnbindableRuntime(t *testing.T) {
	recorder := &probeRecorder{}
	recorder.install(t)
	for _, id := range []string{piBackendID, claudeBackendID, opencodeBackendID} {
		t.Run(id, func(t *testing.T) {
			verdict := ProbeBackendAvailability(context.Background(), id, qarole.RequirementsAnalyst, probeCatalog(t, id), availabilityCWD(t), failingPathResolver{})
			if verdict.Available || verdict.Code != qaadmission.CodeUnsupportedRuntime {
				t.Fatalf("verdict = %#v, want unavailable unsupported_runtime", verdict)
			}
		})
	}
	if len(recorder.issued) != 0 {
		t.Fatalf("an unbindable runtime still issued probe commands: %v", recorder.issued)
	}
}

// probeCatalog writes a catalog root whose default profile routes the probe
// role on one backend, so a probe gets past route resolution.
func probeCatalog(t *testing.T, backend string) string {
	t.Helper()
	root := testProfileRoot(t)
	writeProfile(t, root, []byte(`{"schemaVersion":1,"defaultProfile":"mine","profiles":{"mine":{"routes":{"requirements-analyst":{"`+backend+`":{"provider":"openai-codex","model":"gpt-5.6","effort":"high"}}}}}}`))
	return root
}

// D1 (#458): without a profile route the probe refuses route_incomplete and
// issues nothing; there is no default route to probe.
func TestProbeBackendAvailabilityWithoutARouteIsIncomplete(t *testing.T) {
	recorder := &probeRecorder{}
	recorder.install(t)
	for _, root := range []string{"", testProfileRoot(t)} {
		verdict := ProbeBackendAvailability(context.Background(), piBackendID, qarole.RequirementsAnalyst, root, availabilityCWD(t), failingPathResolver{})
		if verdict.Available || verdict.Code != qaadmission.CodeRouteIncomplete {
			t.Fatalf("root %q verdict = %#v, want route_incomplete", root, verdict)
		}
	}
	if len(recorder.issued) != 0 {
		t.Fatalf("a route-less probe issued commands: %v", recorder.issued)
	}
}

// The probe asks about the route the catalog profile resolves, not a default.
func TestProbeBackendAvailabilityProbesTheProfileRoute(t *testing.T) {
	original := executeVersionCommand
	executeVersionCommand = func(context.Context, string, string) versionCapture { return successfulVersion() }
	t.Cleanup(func() { executeVersionCommand = original })
	// The model table lacks the route, so the verdict names the pair the probe
	// asked about.
	recorder := &probeRecorder{captures: []probeCapture{
		completeCapture([]byte(modelTableHeader+"\nother         other-model          1        1        yes       no\n"), 0),
	}}
	recorder.install(t)
	resolver := &runtimeResolver{path: runtimeBinary(t, "pi")}
	verdict := ProbeBackendAvailability(context.Background(), piBackendID, qarole.RequirementsAnalyst, probeCatalog(t, piBackendID), runtimeDirectory(t), resolver)
	if verdict.Available || verdict.Code != qaadmission.CodeModelUnavailable || verdict.Provider != "openai-codex" || verdict.Model != "gpt-5.6" {
		t.Fatalf("verdict = %#v, want the profile route probed", verdict)
	}
	if len(recorder.issued) == 0 || strings.Join(recorder.issued[0], " ") != "--list-models --no-extensions" {
		t.Fatalf("issued = %v, want the model probe first", recorder.issued)
	}
}

type failingPathResolver struct{}

func (failingPathResolver) Resolve(context.Context) (string, error) {
	return "", errors.New("fixture resolver refuses to resolve")
}
