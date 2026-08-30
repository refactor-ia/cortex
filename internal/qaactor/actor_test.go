package qaactor

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/refactor-ia/cortex/internal/catalog"
	"github.com/refactor-ia/cortex/internal/qarole"
)

func TestSourcesReturnsCanonicalSourceSet(t *testing.T) {
	snapshot := testSnapshot(t)
	sources, err := Sources(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if sources.CatalogFingerprint() != snapshot.Fingerprint() {
		t.Fatal("catalog fingerprint differs from snapshot")
	}
	capabilities := qaFamily(t, snapshot).Capabilities()
	for index, contract := range qarole.Catalog() {
		source := sources.Sources()[index]
		capability := capabilities[index]
		if source.RoleID() != contract.ID || source.RoleContractVersion() != contract.ContractVersion || source.Description() != capability.Manifest().Description {
			t.Fatalf("source %d does not match its role contract", index)
		}
		if source.SourceSHA256() != capability.Source().SHA256() || string(source.Body()) != string(capability.Source().Content()) {
			t.Fatalf("source %q does not match the catalog file", contract.ID)
		}
		if err := qarole.ValidateSourceContract(contract, string(source.Body())); err != nil {
			t.Fatalf("source %q has invalid markers: %v", contract.ID, err)
		}
	}
}

func TestSourceSetCopiesBodies(t *testing.T) {
	sources, err := Sources(testSnapshot(t))
	if err != nil {
		t.Fatal(err)
	}
	first := sources.Sources()
	first[0].Body()[0] = 'X'
	if sources.Sources()[0].Body()[0] == 'X' {
		t.Fatal("Sources() returned shared body bytes")
	}
}

func TestSourcesRejectsInvalidCatalogs(t *testing.T) {
	if _, err := Sources(catalog.CatalogSnapshot{}); err == nil {
		t.Fatal("Sources() accepted an empty snapshot")
	}

	for _, tc := range []struct {
		name, path, old, new string
	}{
		{"missing marker", "families/quality-assurance/sources/requirements-analyst.md", "<!-- cortex-qa:role-id=requirements-analyst -->", ""},
		{"reordered roles", "families/quality-assurance/family.json", `"agents": ["requirements-analyst", "test-designer"`, `"agents": ["test-designer", "requirements-analyst"`},
		{"missing role", "families/quality-assurance/family.json", `, "evidence-auditor"]`, "]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := changedSnapshot(t, tc.path, tc.old, tc.new)
			if _, err := Sources(snapshot); err == nil {
				t.Fatal("Sources() accepted an invalid QA catalog")
			}
		})
	}
}

func TestSourcesRetainsChangedCatalogBytes(t *testing.T) {
	snapshot := changedSnapshot(t, "families/quality-assurance/sources/test-runner.md", "# Test Runner", "# Changed Test Runner")
	sources, err := Sources(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := Render(sources)
	if err != nil {
		t.Fatal(err)
	}
	source, actor := sources.Sources()[4], rendered.Actors()[4]
	if source.SourceSHA256() != fmt.Sprintf("%x", sha256.Sum256(source.Body())) || actor.SourceSHA256() != source.SourceSHA256() || actor.GeneratedSHA256() != fmt.Sprintf("%x", sha256.Sum256(actor.Content())) || actor.SourceSHA256() == actor.GeneratedSHA256() {
		t.Fatal("Render did not derive independent source and generated hashes")
	}
	if !strings.Contains(string(source.Body()), "# Changed Test Runner") || !strings.HasSuffix(string(actor.Content()), string(source.Body())) {
		t.Fatal("Render did not preserve the changed admitted source body")
	}
}

func testSnapshot(t *testing.T) catalog.CatalogSnapshot {
	t.Helper()
	return buildSnapshot(t, filepath.Join("..", "..", "catalog"))
}

func changedSnapshot(t *testing.T, path, old, new string) catalog.CatalogSnapshot {
	t.Helper()
	root := t.TempDir()
	if err := os.CopyFS(root, os.DirFS(filepath.Join("..", "..", "catalog"))); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, path)
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(strings.Replace(string(data), old, new, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	return buildSnapshot(t, root)
}

func buildSnapshot(t *testing.T, root string) catalog.CatalogSnapshot {
	t.Helper()
	snapshot, err := catalog.BuildCatalogSnapshot(root, "catalog.json", catalog.AdmissionPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func qaFamily(t *testing.T, snapshot catalog.CatalogSnapshot) catalog.CatalogFamilySnapshot {
	t.Helper()
	for _, family := range snapshot.Families() {
		if family.Manifest().ID == "quality-assurance" {
			return family
		}
	}
	t.Fatal("quality-assurance family is missing")
	return catalog.CatalogFamilySnapshot{}
}

func TestRenderReturnsCanonicalGoldens(t *testing.T) {
	sourceSet, rendered := testRendered(t)
	if rendered.ActorContract() != ActorContractVersion {
		t.Fatalf("actor contract = %q, want %q", rendered.ActorContract(), ActorContractVersion)
	}
	if rendered.CatalogFingerprint() != sourceSet.CatalogFingerprint() {
		t.Fatal("rendered catalog fingerprint differs from source set")
	}
	sources := sourceSet.Sources()
	actors := rendered.Actors()
	if len(actors) != len(sources) {
		t.Fatalf("actor count = %d, want %d", len(actors), len(sources))
	}
	for index, actor := range actors {
		source := sources[index]
		expected, err := os.ReadFile(filepath.Join("testdata", "pi-actors", string(source.RoleID())+".golden"))
		if err != nil {
			t.Fatal(err)
		}
		if actor.RoleID() != source.RoleID() || actor.RoleContractVersion() != source.RoleContractVersion() || actor.Description() != source.Description() {
			t.Fatalf("actor %d does not preserve its source identity", index)
		}
		if actor.SourceSHA256() != source.SourceSHA256() || actor.GeneratedSHA256() != fmt.Sprintf("%x", sha256.Sum256(actor.Content())) {
			t.Fatalf("actor %q has incorrect hashes", actor.RoleID())
		}
		if string(actor.Content()) != string(expected) || !strings.HasSuffix(string(expected), string(source.Body())) {
			t.Fatalf("actor %q differs from its golden or source body", actor.RoleID())
		}
		if strings.HasPrefix(string(expected), "\ufeff") || strings.Count(string(expected), "\n") != 23 || !strings.HasSuffix(string(expected), "\n") {
			t.Fatalf("actor %q golden does not have exact LF-only line shape", actor.RoleID())
		}
	}
}

func TestRenderUsesDeterministicScalarQuoting(t *testing.T) {
	for _, tc := range []struct{ value, want string }{
		{"requires: quoting", `"requires: quoting"`},
		{"true", `"true"`},
		{"null", `"null"`},
		{"123", `"123"`},
	} {
		t.Run(tc.value, func(t *testing.T) {
			if got := yamlScalar(tc.value); got != tc.want {
				t.Fatalf("yamlScalar(%q) = %q, want %q", tc.value, got, tc.want)
			}
		})
	}
}

func TestRenderCopiesValuesAndRejectsUnusableSourceSets(t *testing.T) {
	if _, err := Render(SourceSet{}); err == nil {
		t.Fatal("Render accepted an unusable source set")
	}

	_, rendered := testRendered(t)
	sources := rendered.Sources()
	actors := rendered.Actors()
	content := actors[0].Content()
	sources[0].body[0] = 'X'
	actors[0].content[0] = 'X'
	content[0] = 'X'
	if rendered.Sources()[0].body[0] == 'X' || rendered.Actors()[0].content[0] == 'X' || rendered.Actors()[0].Content()[0] == 'X' {
		t.Fatal("Set returned shared source or actor bytes")
	}
}

func testRendered(t *testing.T) (SourceSet, Set) {
	t.Helper()
	sourceSet, err := Sources(testSnapshot(t))
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := Render(sourceSet)
	if err != nil {
		t.Fatal(err)
	}
	return sourceSet, rendered
}

func TestValidateAcceptsCanonicalRenderedSet(t *testing.T) {
	_, rendered := testRendered(t)
	if err := Validate(rendered); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidateRejectsInvalidSetStructure(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Set)
	}{
		{"actor contract", func(set *Set) { set.actorContract = "other" }},
		{"catalog fingerprint", func(set *Set) { set.catalogFingerprint = "not-a-fingerprint" }},
		{"missing source", func(set *Set) { set.sources = set.sources[:5] }},
		{"extra actor", func(set *Set) { set.actors = append(set.actors, set.actors[0]) }},
		{"duplicate source", func(set *Set) { set.sources[1].roleID = set.sources[0].roleID }},
		{"reordered actor", func(set *Set) { set.actors[0], set.actors[1] = set.actors[1], set.actors[0] }},
		{"source role contract", func(set *Set) { set.sources[0].roleContractVersion = "other" }},
		{"empty source description", func(set *Set) { set.sources[0].description = "" }},
		{"source body hash", func(set *Set) { set.sources[0].sourceSHA256 = "bad" }},
		{"source markers", func(set *Set) {
			set.sources[0].body = []byte(strings.Replace(string(set.sources[0].body), "<!-- cortex-qa:role-id=requirements-analyst -->", "", 1))
			refreshSourceHash(&set.sources[0])
		}},
		{"actor source identity", func(set *Set) { set.actors[0].sourceSHA256 = "bad" }},
		{"actor role contract", func(set *Set) { set.actors[0].roleContractVersion = "other" }},
		{"actor generated hash", func(set *Set) { set.actors[0].generatedSHA256 = "bad" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, rendered := testRendered(t)
			tc.mutate(&rendered)
			if err := Validate(rendered); err == nil {
				t.Fatal("Validate() accepted an invalid actor set")
			}
		})
	}
}

func TestValidateRejectsCanonicalContentDrift(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(string) string
	}{
		{"unknown frontmatter", func(content string) string {
			return strings.Replace(content, "tools:\n", "unknown: value\ntools:\n", 1)
		}},
		{"duplicate frontmatter", func(content string) string {
			return strings.Replace(content, "name: cortex-requirements-analyst\n", "name: cortex-requirements-analyst\nname: cortex-requirements-analyst\n", 1)
		}},
		{"reordered frontmatter", func(content string) string {
			return strings.Replace(content, "name: cortex-requirements-analyst\ndescription:", "description:", 1) + "\nname: cortex-requirements-analyst"
		}},
		{"single mode", func(content string) string {
			return strings.Replace(content, "subagent_mode: task", "subagent_mode: single", 1)
		}},
		{"background mode", func(content string) string {
			return strings.Replace(content, "subagent_mode: task", "subagent_mode: background", 1)
		}},
		{"tool change", func(content string) string { return strings.Replace(content, "  - ls", "  - write", 1) }},
		{"provider branding", func(content string) string {
			return strings.Replace(content, "description:", "provider: nan\ndescription:", 1)
		}},
		{"model branding", func(content string) string {
			return strings.Replace(content, "description:", "model: qwen\ndescription:", 1)
		}},
		{"effort branding", func(content string) string {
			return strings.Replace(content, "description:", "effort: high\ndescription:", 1)
		}},
		{"profile branding", func(content string) string {
			return strings.Replace(content, "description:", "profile: default\ndescription:", 1)
		}},
		{"BOM", func(content string) string { return "\ufeff" + content }},
		{"CRLF", func(content string) string { return strings.ReplaceAll(content, "\n", "\r\n") }},
		{"separator", func(content string) string { return strings.Replace(content, "---\n\n#", "---\n#", 1) }},
		{"body", func(content string) string { return strings.Replace(content, "# Requirements Analyst", "# Other", 1) }},
		{"trailing bytes", func(content string) string { return content + "extra\n" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, rendered := testRendered(t)
			rendered.actors[0].content = []byte(tc.mutate(string(rendered.actors[0].content)))
			refreshActorHash(&rendered.actors[0])
			if err := Validate(rendered); err == nil {
				t.Fatal("Validate() accepted non-canonical actor content")
			}
		})
	}
}

func TestValidateOnlyProvesInternalSourceIntegrity(t *testing.T) {
	_, rendered := testRendered(t)
	replacement := append([]byte(nil), rendered.sources[0].body...)
	replacement = append(replacement, []byte("\nReplacement text with retained markers.\n")...)
	rendered.sources[0].body = replacement
	refreshSourceHash(&rendered.sources[0])

	canonical, err := Render(SourceSet{
		catalogFingerprint: rendered.catalogFingerprint,
		sources:            rendered.Sources(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := Validate(canonical); err != nil {
		t.Fatalf("Validate() rejected internally consistent replacement: %v", err)
	}
}

func refreshSourceHash(source *Source) {
	source.sourceSHA256 = fmt.Sprintf("%x", sha256.Sum256(source.body))
}

func refreshActorHash(actor *Actor) {
	actor.generatedSHA256 = fmt.Sprintf("%x", sha256.Sum256(actor.content))
}
