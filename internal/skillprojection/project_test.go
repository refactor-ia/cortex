package skillprojection

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/refactor-ia/cortex/internal/catalog"
	"github.com/refactor-ia/cortex/internal/projection"
	"github.com/refactor-ia/cortex/internal/runtimematrix"
	"github.com/refactor-ia/cortex/internal/skillrender"
)

const translatedDisclosure = "Dormant skills add disable-model-invocation: true to preserve explicit-only invocation."

func TestValidIDLengthContract(t *testing.T) {
	for _, tc := range []struct {
		name  string
		id    string
		valid bool
	}{
		{"one character", "a", true},
		{"maximum length", strings.Repeat("a", 57), true},
		{"over maximum length", strings.Repeat("a", 58), false},
		{"prior grammar rejection", "invalid_id", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := validID(tc.id); got != tc.valid {
				t.Errorf("validID(%q) = %t, want %t", tc.id, got, tc.valid)
			}
		})
	}
}

func TestBuildExactAutomaticSets(t *testing.T) {
	snapshot, sources := projectionSources(t, false)
	for _, runtime := range []runtimematrix.RuntimeID{runtimematrix.RuntimePi, runtimematrix.RuntimeOpenCode, runtimematrix.RuntimeClaudeCode} {
		t.Run(string(runtime), func(t *testing.T) {
			plan, err := Build(runtime, sources)
			if err != nil || plan.ReasonCode() != "" || plan.Assessment().Result() != projection.Exact || plan.Assessment().TranslationDisclosure() != "" || projection.ValidateBinding(snapshot, runtime, plan.Assessment()) != nil {
				t.Fatalf("Build() = (%+v, %v)", plan, err)
			}
			assertSameSources(t, plan.Skills(), sources.Skills())
		})
	}
}

func TestBuildMixedDormantProjection(t *testing.T) {
	snapshot, sources := projectionSources(t, true)
	for _, runtime := range []runtimematrix.RuntimeID{runtimematrix.RuntimePi, runtimematrix.RuntimeClaudeCode} {
		t.Run(string(runtime), func(t *testing.T) {
			plan, err := Build(runtime, sources)
			if err != nil || plan.Assessment().Result() != projection.Translated || plan.Assessment().TranslationDisclosure() != translatedDisclosure || plan.ReasonCode() != "" || projection.ValidateBinding(snapshot, runtime, plan.Assessment()) != nil {
				t.Fatalf("Build() = (%+v, %v)", plan, err)
			}
			skills := plan.Skills()
			if len(skills) != 2 || skills[0].LogicalID() != "skills/alpha" || skills[1].LogicalID() != "skills/zeta" || string(skills[1].Content()) != string(sources.Skills()[1].Content()) {
				t.Fatal("automatic skill changed or projected order is not lexical")
			}
			want := strings.Replace(string(sources.Skills()[0].Content()), "metadata:\n", "disable-model-invocation: true\nmetadata:\n", 1)
			if string(skills[0].Content()) != want || skills[0].SHA256() != hash(skills[0].Content()) || !strings.Contains(string(skills[0].Content()), "body metadata:\ndisable-model-invocation: true\n") {
				t.Fatal("dormant translation changed bytes outside the canonical header")
			}
		})
	}
	plan, err := Build(runtimematrix.RuntimeOpenCode, sources)
	if err != nil || plan.Assessment().Result() != projection.Unrepresentable || plan.Assessment().TranslationDisclosure() != "" || plan.ReasonCode() != "dormant_enforcement_unavailable" || plan.Skills() == nil || len(plan.Skills()) != 0 || projection.ValidateBinding(snapshot, runtimematrix.RuntimeOpenCode, plan.Assessment()) != nil {
		t.Fatalf("OpenCode Build() = (%+v, %v)", plan, err)
	}
}

func TestBuildEmptyDeterminismAndIsolation(t *testing.T) {
	_, empty := projectionSources(t, false, true)
	for _, runtime := range []runtimematrix.RuntimeID{runtimematrix.RuntimePi, runtimematrix.RuntimeOpenCode, runtimematrix.RuntimeClaudeCode} {
		plan, err := Build(runtime, empty)
		if err != nil || plan.Assessment().Result() != projection.Exact || plan.Skills() == nil || len(plan.Skills()) != 0 {
			t.Fatalf("empty Build(%q) = (%+v, %v)", runtime, plan, err)
		}
	}
	_, sources := projectionSources(t, true)
	first, err := Build(runtimematrix.RuntimePi, sources)
	second, again := Build(runtimematrix.RuntimePi, sources)
	if err != nil || again != nil || !reflect.DeepEqual(first, second) {
		t.Fatal("Build must be deterministic")
	}
	skills, content := first.Skills(), first.Skills()[0].Content()
	skills[0] = ProjectedSkill{}
	content[0] = 'x'
	if first.Skills()[0].LogicalID() != "skills/alpha" || first.Skills()[0].Content()[0] == 'x' {
		t.Fatal("accessors must return detached copies")
	}
}

func TestBuildRejectsZeroInputAndUnknownRuntime(t *testing.T) {
	for _, test := range []struct {
		name string
		run  func() (Plan, error)
	}{
		{"zero set", func() (Plan, error) { return Build(runtimematrix.RuntimePi, skillrender.Set{}) }},
		{"unknown runtime", func() (Plan, error) { _, set := projectionSources(t, false); return Build("unknown", set) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan, err := test.run()
			if err == nil || !reflect.DeepEqual(plan, Plan{}) || err.Error() != "skill projection: invalid input" {
				t.Fatalf("Build() = (%+v, %v)", plan, err)
			}
		})
	}
}

func assertSameSources(t *testing.T, got []ProjectedSkill, want []skillrender.RenderedSkill) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatal("unexpected skill count")
	}
	for index := range want {
		if got[index].CapabilityID() != want[index].CapabilityID() || got[index].LogicalID() != want[index].LogicalID() || got[index].Activation() != want[index].Activation() || got[index].SHA256() != want[index].SHA256() || got[index].SHA256() != hash(got[index].Content()) || string(got[index].Content()) != string(want[index].Content()) {
			t.Fatal("exact projection changed a skill")
		}
	}
}

func projectionSources(t *testing.T, dormant bool, empty ...bool) (catalog.CatalogSnapshot, skillrender.Set) {
	t.Helper()
	root, families := t.TempDir(), map[string]string{}
	for _, family := range catalog.ApprovedFamilyIDs() {
		families[family] = "families/" + family + ".json"
		capabilities := []string{}
		if len(empty) == 0 {
			switch family {
			case "reasoning":
				capabilities = []string{"manifests/zeta.json"}
			case "services":
				capabilities = []string{"manifests/alpha.json"}
			}
		}
		writeJSON(t, root, families[family], map[string]any{"schemaVersion": 1, "id": family, "router": "routers/" + family + ".md", "capabilities": capabilities, "agents": []string{}})
		write(t, root, "routers/"+family+".md", family)
	}
	for _, capability := range []struct{ id, family, activation, body string }{
		{"zeta", "reasoning", catalog.ActivationAutomatic, "zeta\n"},
		{"alpha", "services", map[bool]string{true: catalog.ActivationDormant, false: catalog.ActivationAutomatic}[dormant], "body metadata:\ndisable-model-invocation: true\n"},
	} {
		writeJSON(t, root, "manifests/"+capability.id+".json", catalog.CapabilityManifest{SchemaVersion: 1, ID: capability.id, Description: capability.id, Family: capability.family, Source: "sources/" + capability.id + ".md", Activation: capability.activation, Provenance: catalog.ProvenanceCortexOwned, License: "CC-BY-SA-4.0", RedistributionAllowed: true})
		write(t, root, "sources/"+capability.id+".md", capability.body)
	}
	writeJSON(t, root, "catalog.json", map[string]any{"schemaVersion": 1, "families": families})
	snapshot, err := catalog.BuildCatalogSnapshot(root, "catalog.json", catalog.AdmissionPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	set, err := skillrender.Render(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot, set
}

func writeJSON(t *testing.T, root, name string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	write(t, root, name, string(data))
}
func write(t *testing.T, root, name, value string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
}
func hash(content []byte) string {
	value := sha256.Sum256(content)
	return hex.EncodeToString(value[:])
}
