package installcoord_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/refactor-ia/cortex/internal/catalog"
	"github.com/refactor-ia/cortex/internal/installcoord"
	"github.com/refactor-ia/cortex/internal/skilldest"
	"github.com/refactor-ia/cortex/internal/skillroot"
)

// TestBuildCandidatesRefusesAnUnownedQAProjection pins the
// skilldest.ValidateQAProjection call site in buildOne. The mutated source below
// still loads, renders, projects, binds and resolves: a dropped QA source marker
// is invisible to every other stage of the pipeline, so this test fails if the
// validator call is removed.
func TestBuildCandidatesRefusesAnUnownedQAProjection(t *testing.T) {
	if err := buildFromCatalog(t, copiedCatalog(t)); err != nil {
		t.Fatalf("BuildCandidates() on the intact catalog copy = %v", err)
	}

	mutated := copiedCatalog(t)
	source := filepath.Join(mutated, "families", "quality-assurance", "sources", "test-runner.md")
	body := string(readFile(t, source))
	stripped := strings.Replace(body, "<!-- cortex-qa:evidence-only-output -->\n", "", 1)
	if stripped == body {
		t.Fatal("QA source fixture no longer carries the evidence-only-output marker")
	}
	must(t, os.WriteFile(source, []byte(stripped), 0o600))

	// The message is asserted, not just the failure: without it the test would
	// also pass on an unrelated error and stop pinning the validator call site.
	err := buildFromCatalog(t, mutated)
	if err == nil || !strings.Contains(err.Error(), "unowned QA destination") {
		t.Fatalf("BuildCandidates() on a QA source that lost a required marker = %v", err)
	}
}

func buildFromCatalog(t *testing.T, root string) error {
	t.Helper()
	snapshot, err := catalog.BuildCatalogSnapshot(root, "catalog.json", catalog.AdmissionPolicy{})
	must(t, err)
	home := t.TempDir()
	_, _, err = installcoord.BuildCandidates(installcoord.CandidateRequest{
		Snapshot:     snapshot,
		Observations: compatible(),
		ResolveRoot: func(plan skilldest.Plan) (skillroot.Plan, error) {
			return skillroot.Resolve(plan, skillroot.Inputs{Home: home})
		},
	})
	return err
}

// copiedCatalog copies the repository catalog into a temporary root so a test
// can mutate one file without touching the working tree.
func copiedCatalog(t *testing.T) string {
	t.Helper()
	origin, destination := filepath.Join("..", "..", "catalog"), t.TempDir()
	must(t, filepath.WalkDir(origin, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(origin, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o600)
	}))
	return destination
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	must(t, err)
	return data
}
