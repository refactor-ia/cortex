package catalog

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func catalogJSON(ids []string, paths map[string]string) string {
	entries := make([]string, 0, len(ids))
	for _, id := range ids {
		entries = append(entries, `"`+id+`":`+strconvQuote(paths[id]))
	}
	return `{"schemaVersion":1,"families":{` + strings.Join(entries, ",") + `}}`
}

func strconvQuote(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func catalogPaths() map[string]string {
	paths := make(map[string]string, len(approvedFamilyIDs))
	for _, id := range approvedFamilyIDs {
		paths[id] = "families/" + id + ".json"
	}
	return paths
}

func catalogError(t *testing.T, input string) {
	t.Helper()
	manifest, err := DecodeCatalogManifest([]byte(input))
	if err == nil {
		t.Fatal("DecodeCatalogManifest() error = nil")
	}
	if manifest.SchemaVersion != 0 || manifest.Families != nil {
		t.Fatal("DecodeCatalogManifest() returned non-zero output on error")
	}
}

func TestDecodeCatalogManifestCanonicalOrder(t *testing.T) {
	paths := catalogPaths()
	ids := append([]string(nil), approvedFamilyIDs...)
	for left, right := 0, len(ids)-1; left < right; left, right = left+1, right-1 {
		ids[left], ids[right] = ids[right], ids[left]
	}
	manifest, err := DecodeCatalogManifest([]byte(catalogJSON(ids, paths)))
	if err != nil {
		t.Fatalf("DecodeCatalogManifest() error = %v", err)
	}
	if manifest.SchemaVersion != 1 || manifest.Families == nil || len(manifest.Families) != len(approvedFamilyIDs) {
		t.Fatal("DecodeCatalogManifest() did not return every approved family")
	}
	for index, reference := range manifest.Families {
		if reference.ID != approvedFamilyIDs[index] || reference.ManifestPath != paths[reference.ID] {
			t.Fatalf("Families[%d] = %#v, want canonical approved family reference", index, reference)
		}
	}
}

func TestDecodeCatalogManifestRejectsInvalidInput(t *testing.T) {
	paths := catalogPaths()
	valid := catalogJSON(approvedFamilyIDs, paths)
	cases := []struct{ name, input string }{
		{"root unknown", valid[:len(valid)-1] + `,"other":true}`},
		{"missing schemaVersion", `{"families":{}}`},
		{"missing families", `{"schemaVersion":1}`},
		{"families wrong type", `{"schemaVersion":1,"families":[]}`},
		{"family wrong type", strings.Replace(valid, `"reasoning":"families/reasoning.json"`, `"reasoning":1`, 1)},
		{"wrong version", strings.Replace(valid, `"schemaVersion":1`, `"schemaVersion":2`, 1)},
		{"trailing JSON", valid + ` true`},
		{"family unknown", valid[:len(valid)-2] + `,"unknown":"families/unknown.json"}}`},
	}
	for _, id := range approvedFamilyIDs {
		ids := make([]string, 0, len(approvedFamilyIDs)-1)
		for _, candidate := range approvedFamilyIDs {
			if candidate != id {
				ids = append(ids, candidate)
			}
		}
		cases = append(cases, struct{ name, input string }{"missing family " + id, catalogJSON(ids, paths)})
	}
	for _, path := range []string{"", `families\\reasoning.json`, "/families/reasoning.json", "families/./reasoning.json", "families/../reasoning.json", "families/reasoning.md", "families/reasoning.json\n"} {
		invalidPaths := catalogPaths()
		invalidPaths["reasoning"] = path
		cases = append(cases, struct{ name, input string }{"invalid path", catalogJSON(approvedFamilyIDs, invalidPaths)})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { catalogError(t, tc.input) })
	}
}

func TestDecodeCatalogManifestDoesNotLeakInputOrMutateBytes(t *testing.T) {
	input := []byte(catalogJSON(approvedFamilyIDs, catalogPaths()))
	original := append([]byte(nil), input...)
	if _, err := DecodeCatalogManifest(input); err != nil {
		t.Fatalf("DecodeCatalogManifest() error = %v", err)
	}
	if !bytes.Equal(input, original) {
		t.Fatal("DecodeCatalogManifest() mutated caller bytes")
	}
	paths := catalogPaths()
	paths["reasoning"] = "https://user:password@example.invalid/manifest.json"
	_, err := DecodeCatalogManifest([]byte(catalogJSON(approvedFamilyIDs, paths)))
	if err == nil {
		t.Fatal("DecodeCatalogManifest() error = nil")
	}
	for _, forbidden := range []string{"https", "password", "example.invalid"} {
		if strings.Contains(strings.ToLower(err.Error()), forbidden) {
			t.Fatalf("error leaked input value %q", forbidden)
		}
	}
}

func TestCatalogSchemaStructure(t *testing.T) {
	data, err := os.ReadFile("../../schemas/catalog.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]json.RawMessage
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	if string(schema["$schema"]) != `"https://json-schema.org/draft/2020-12/schema"` || string(schema["$id"]) != `"https://github.com/refactor-ia/cortex/schemas/catalog.schema.json"` {
		t.Fatal("catalog schema draft or ID differs from the contract")
	}
	var root struct {
		Type                 string          `json:"type"`
		AdditionalProperties *bool           `json:"additionalProperties"`
		Required             []string        `json:"required"`
		Properties           json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(data, &root); err != nil || len(schema) != 8 || root.Type != "object" || root.AdditionalProperties == nil || *root.AdditionalProperties || strings.Join(root.Required, ",") != "schemaVersion,families" {
		t.Fatal("catalog schema root differs from the contract")
	}
	for _, key := range []string{"$schema", "$id", "title", "type", "additionalProperties", "required", "properties", "$defs"} {
		if _, exists := schema[key]; !exists {
			t.Fatalf("catalog schema missing %s", key)
		}
	}
	var families struct {
		AdditionalProperties *bool                      `json:"additionalProperties"`
		Required             []string                   `json:"required"`
		Properties           map[string]json.RawMessage `json:"properties"`
	}
	var properties map[string]json.RawMessage
	var schemaVersion struct {
		Type  string `json:"type"`
		Const *int   `json:"const"`
	}
	if err := json.Unmarshal(root.Properties, &properties); err != nil || len(properties) != 2 || json.Unmarshal(properties["schemaVersion"], &schemaVersion) != nil || json.Unmarshal(properties["families"], &families) != nil {
		t.Fatal("catalog properties schema is invalid")
	}
	if schemaVersion.Type != "integer" || schemaVersion.Const == nil || *schemaVersion.Const != 1 || families.AdditionalProperties == nil || *families.AdditionalProperties || strings.Join(families.Required, ",") != strings.Join(approvedFamilyIDs, ",") || len(families.Properties) != len(approvedFamilyIDs) {
		t.Fatal("catalog families schema differs from the approved IDs contract")
	}
	for _, id := range approvedFamilyIDs {
		var property map[string]string
		if err := json.Unmarshal(families.Properties[id], &property); err != nil || len(property) != 1 || property["$ref"] != "#/$defs/familyManifestPath" {
			t.Fatalf("catalog family property %q differs from the contract", id)
		}
	}
	var defs map[string]struct {
		Pattern string `json:"pattern"`
	}
	if err := json.Unmarshal(schema["$defs"], &defs); err != nil {
		t.Fatal(err)
	}
	pattern := defs["familyManifestPath"].Pattern
	if strings.Contains(pattern, "$") || strings.Count(pattern, `(?![\s\S])`) < 2 || !strings.Contains(pattern, `.json(?![\s\S])`) {
		t.Fatalf("familyManifestPath must use true end-of-input assertions: %q", pattern)
	}
}
