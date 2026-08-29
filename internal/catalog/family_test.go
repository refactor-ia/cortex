package catalog

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func familyJSON(id, router, capabilities, agents string) string {
	return `{"schemaVersion":1,"id":"` + id + `","router":"` + router + `","capabilities":` + capabilities + `,"agents":` + agents + `}`
}

func TestDecodeFamilyManifestValid(t *testing.T) {
	for _, manifest := range []string{
		familyJSON("reasoning", "routers/reasoning.md", `[]`, `[]`),
		familyJSON("web", "routers/web.md", `["capabilities/web.json"]`, `["test-runner","security-audit"]`),
	} {
		if _, err := DecodeFamilyManifest([]byte(manifest)); err != nil {
			t.Fatalf("DecodeFamilyManifest() error = %v", err)
		}
	}
}

func TestDecodeFamilyManifestApprovedValues(t *testing.T) {
	for _, id := range ApprovedFamilyIDs() {
		if _, err := DecodeFamilyManifest([]byte(familyJSON(id, "router.md", `[]`, `[]`))); err != nil {
			t.Errorf("family %q rejected: %v", id, err)
		}
	}
	for _, agent := range ApprovedAgentIDs() {
		if _, err := DecodeFamilyManifest([]byte(familyJSON("reasoning", "router.md", `[]`, `["`+agent+`"]`))); err != nil {
			t.Errorf("agent %q rejected: %v", agent, err)
		}
	}
}

func TestDecodeFamilyManifestRejectsInvalidInput(t *testing.T) {
	valid := familyJSON("reasoning", "router.md", `["capability.json"]`, `["test-runner"]`)
	cases := []struct{ name, input string }{
		{"unknown", valid[:len(valid)-1] + `,"other":true}`},
		{"trailing", valid + ` true`},
		{"missing", `{"schemaVersion":1}`},
		{"extra", valid[:len(valid)-1] + `,"other":true}`},
		{"version", strings.Replace(valid, `"schemaVersion":1`, `"schemaVersion":2`, 1)},
		{"family", strings.Replace(valid, `"reasoning"`, `"unknown"`, 1)},
		{"agent", strings.Replace(valid, `"test-runner"`, `"unknown"`, 1)},
		{"duplicate capabilities", strings.Replace(valid, `["capability.json"]`, `["capability.json","capability.json"]`, 1)},
		{"duplicate agents", strings.Replace(valid, `["test-runner"]`, `["test-runner","test-runner"]`, 1)},
	}
	for _, path := range []string{"", ".", "/router.md", `router\\x.md`, "a/../router.md", "a//router.md", "router.json", "router.md\\n"} {
		cases = append(cases, struct{ name, input string }{"router " + path, familyJSON("reasoning", path, `[]`, `[]`)})
	}
	for _, path := range []string{"", ".", "/capability.json", `capability\\x.json`, "a/../capability.json", "a//capability.json", "capability.md", "capability.json\\n"} {
		cases = append(cases, struct{ name, input string }{"capability " + path, familyJSON("reasoning", "router.md", `["`+path+`"]`, `[]`)})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := DecodeFamilyManifest([]byte(tc.input)); err == nil {
				t.Fatal("DecodeFamilyManifest() error = nil")
			}
		})
	}
}

func TestGeneralCoreAgentAllowlistStaysSynchronizedWithSchema(t *testing.T) {
	want := []string{
		"requirements-analyst",
		"test-designer",
		"exploratory-tester",
		"adversarial-tester",
		"test-runner",
		"evidence-auditor",
	}
	approved := ApprovedAgentIDs()
	for _, id := range want {
		if !contains(approved, id) {
			t.Errorf("ApprovedAgentIDs() is missing %q", id)
		}
	}

	data, err := os.ReadFile("../../schemas/family.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range want {
		if !strings.Contains(string(data), `"`+id+`"`) {
			t.Errorf("family schema agents enum is missing %q", id)
		}
	}
}

func TestFamilySchemaStructure(t *testing.T) {
	data, err := os.ReadFile("../../schemas/family.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Required             []string                   `json:"required"`
		AdditionalProperties *bool                      `json:"additionalProperties"`
		Properties           map[string]json.RawMessage `json:"properties"`
		Defs                 map[string]struct {
			Pattern string `json:"pattern"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	if schema.AdditionalProperties == nil || *schema.AdditionalProperties || strings.Join(schema.Required, ",") != "schemaVersion,id,router,capabilities,agents" {
		t.Fatal("schema required fields or additionalProperties differ from the manifest contract")
	}
	for property, values := range map[string][]string{"id": ApprovedFamilyIDs(), "agents": ApprovedAgentIDs()} {
		if !strings.Contains(string(schema.Properties[property]), `"enum"`) {
			t.Errorf("%s enum missing", property)
		}
		for _, value := range values {
			if !strings.Contains(string(schema.Properties[property]), `"`+value+`"`) {
				t.Errorf("%s enum missing %q", property, value)
			}
		}
	}
	for _, property := range []string{"capabilities", "agents"} {
		if !strings.Contains(string(schema.Properties[property]), `"uniqueItems": true`) {
			t.Errorf("%s uniqueItems missing", property)
		}
	}
	for name, suffix := range map[string]string{"routerPath": ".md", "capabilityPath": ".json"} {
		pattern := schema.Defs[name].Pattern
		if strings.Contains(pattern, "$") {
			t.Errorf("%s pattern contains a $ anchor: %q", name, pattern)
		}
		if strings.Count(pattern, `(?![\s\S])`) < 2 || !strings.Contains(pattern, suffix+`(?![\s\S])`) {
			t.Errorf("%s pattern must use true end-of-input assertions for dot segments and the %s suffix: %q", name, suffix, pattern)
		}
	}
}
