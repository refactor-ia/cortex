package catalog

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func capabilityJSON(family, activation, provenance, license, urls string) string {
	return `{"schemaVersion":1,"id":"test-capability","description":"Professional capability description.","family":"` + family + `","source":"capabilities/test.md","activation":"` + activation + `","provenance":"` + provenance + `","license":"` + license + `","redistributionAllowed":true` + urls + `}`
}

func TestDecodeCapabilityManifestValid(t *testing.T) {
	cases := []string{
		capabilityJSON("reasoning", "automatic", "cortex-owned", "CC-BY-SA-4.0", ""),
		capabilityJSON("documentation", "dormant", "cortex-owned", "CC-BY-SA-4.0", ""),
		capabilityJSON("web", "automatic", "third-party", "MIT", `,"provenanceUrl":"https://example.com/source","redistributionUrl":"https://example.com/license"`),
	}
	for _, input := range cases {
		if _, err := DecodeCapabilityManifest([]byte(input)); err != nil {
			t.Fatalf("DecodeCapabilityManifest() error = %v", err)
		}
	}
}

func TestDecodeCapabilityManifestIDLengthContract(t *testing.T) {
	valid := capabilityJSON("reasoning", "automatic", "cortex-owned", "CC-BY-SA-4.0", "")
	for _, tc := range []struct {
		name  string
		id    string
		valid bool
	}{
		{"one character", "a", true},
		{"maximum length", strings.Repeat("a", 57), true},
		{"over maximum length", strings.Repeat("a", 58), false},
		{"prior grammar rejection", "test_capability", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := strings.Replace(valid, "test-capability", tc.id, 1)
			manifest, err := DecodeCapabilityManifest([]byte(input))
			if tc.valid {
				if err != nil || manifest.ID != tc.id {
					t.Fatalf("DecodeCapabilityManifest() = (%+v, %v)", manifest, err)
				}
				return
			}
			if err == nil || manifest != (CapabilityManifest{}) {
				t.Fatalf("DecodeCapabilityManifest() = (%+v, %v), want zero value and error", manifest, err)
			}
			if strings.Contains(err.Error(), tc.id) {
				t.Errorf("error leaked ID: %q", err)
			}
		})
	}
}

func TestCanonicalEvidenceURLGrammar(t *testing.T) {
	cases := []struct {
		name  string
		value string
		valid bool
	}{
		{"escaped path segment", "https://example.com/source%2Fdetail", true},
		{"malformed percent escape", "https://example.com/source%ZZ", false},
		{"userinfo", "https://user@example.com/source", false},
		{"non-numeric port", "https://example.com:abc/source", false},
		{"host ending in hyphen", "https://example-/source", false},
		{"terminal newline", "https://example.com/source\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := validHTTPSURL(tc.value); got != tc.valid {
				t.Errorf("validHTTPSURL(%q) = %t, want %t", tc.value, got, tc.valid)
			}
		})
	}
}

func TestDecodeCapabilityManifestLicenseWhitespace(t *testing.T) {
	thirdParty := capabilityJSON("web", "automatic", "third-party", "MIT", `,"provenanceUrl":"https://example.com/source","redistributionUrl":"https://example.com/license"`)
	if _, err := DecodeCapabilityManifest([]byte(strings.Replace(thirdParty, `"MIT"`, `"MIT OR Apache-2.0"`, 1))); err != nil {
		t.Fatalf("internal-space license rejected: %v", err)
	}
	for _, value := range []string{`"MIT\rOR"`, `"MIT\nOR"`, `"MIT\u2028OR"`, `"MIT\u2029OR"`} {
		t.Run(value, func(t *testing.T) {
			if _, err := DecodeCapabilityManifest([]byte(strings.Replace(thirdParty, `"MIT"`, value, 1))); err == nil {
				t.Fatal("DecodeCapabilityManifest() error = nil")
			}
		})
	}
}

func TestDecodeCapabilityManifestDescriptionContract(t *testing.T) {
	valid := capabilityJSON("reasoning", "automatic", "cortex-owned", "CC-BY-SA-4.0", "")
	replaceDescription := func(value string) string {
		return strings.Replace(valid, `"description":"Professional capability description."`, `"description":`+value, 1)
	}
	for _, tc := range []struct {
		name, value string
	}{
		{"empty", `""`},
		{"whitespace only", `"\u00a0"`},
		{"leading whitespace", `" professional description"`},
		{"leading Unicode whitespace", `"\u2000professional description"`},
		{"trailing whitespace", `"professional description "`},
		{"trailing ECMAScript whitespace", `"professional description\ufeff"`},
		{"carriage return", `"professional\rdescription"`},
		{"line feed", `"professional\ndescription"`},
		{"line separator", `"professional\u2028description"`},
		{"paragraph separator", `"professional\u2029description"`},
		{"terminal newline", `"professional description\n"`},
		{"too long", `"` + strings.Repeat("界", 1025) + `"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manifest, err := DecodeCapabilityManifest([]byte(replaceDescription(tc.value)))
			if err == nil || manifest != (CapabilityManifest{}) {
				t.Fatalf("DecodeCapabilityManifest() = (%+v, %v), want zero value and error", manifest, err)
			}
			if strings.Contains(err.Error(), "professional") {
				t.Errorf("error leaked description: %q", err)
			}
		})
	}
	for _, description := range []string{"界", strings.Repeat("界", 1024), "Professional\tcapability description"} {
		t.Run("valid", func(t *testing.T) {
			encoded, err := json.Marshal(description)
			if err != nil {
				t.Fatal(err)
			}
			manifest, err := DecodeCapabilityManifest([]byte(replaceDescription(string(encoded))))
			if err != nil {
				t.Fatal(err)
			}
			if manifest.Description != description {
				t.Errorf("Description = %q, want %q", manifest.Description, description)
			}
		})
	}
	missing := strings.Replace(valid, `,"description":"Professional capability description."`, "", 1)
	manifest, err := DecodeCapabilityManifest([]byte(missing))
	if err == nil || manifest != (CapabilityManifest{}) {
		t.Fatalf("missing description = (%+v, %v), want zero value and error", manifest, err)
	}
	privateDescription := "private capability description\n"
	encoded, err := json.Marshal(privateDescription)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err = DecodeCapabilityManifest([]byte(replaceDescription(string(encoded))))
	if err == nil || manifest != (CapabilityManifest{}) || strings.Contains(err.Error(), privateDescription) {
		t.Fatalf("private invalid description = (%+v, %v), want zero value and generic error", manifest, err)
	}
	if validDescription(string([]byte{0xff})) {
		t.Fatal("validDescription() accepted invalid UTF-8")
	}
}

func TestDecodeCapabilityManifestDescriptionUTF8(t *testing.T) {
	valid := capabilityJSON("reasoning", "automatic", "cortex-owned", "CC-BY-SA-4.0", "")

	t.Run("valid replacement character", func(t *testing.T) {
		input := strings.Replace(valid, "Professional capability description.", "Professional � capability description.", 1)
		manifest, err := DecodeCapabilityManifest([]byte(input))
		if err != nil {
			t.Fatal(err)
		}
		if manifest.Description != "Professional � capability description." {
			t.Errorf("Description = %q, want valid replacement character preserved", manifest.Description)
		}
	})

	t.Run("malformed UTF-8", func(t *testing.T) {
		replacement := append([]byte(`"description":"Professional `), 0xff)
		replacement = append(replacement, []byte(` capability description."`)...)
		input := bytes.Replace([]byte(valid), []byte(`"description":"Professional capability description."`), replacement, 1)

		manifest, err := DecodeCapabilityManifest(input)
		if err == nil && manifest.Description != "Professional � capability description." {
			t.Errorf("Description = %q, want JSON decoder replacement behavior", manifest.Description)
		}
		if err == nil || manifest != (CapabilityManifest{}) {
			t.Fatalf("DecodeCapabilityManifest() = (%+v, %v), want zero value and error", manifest, err)
		}
		if strings.Contains(err.Error(), "Professional") {
			t.Errorf("error leaked description: %q", err)
		}
	})
}

func TestDecodeCapabilityManifestApprovedFamilies(t *testing.T) {
	for _, family := range ApprovedFamilyIDs() {
		if _, err := DecodeCapabilityManifest([]byte(capabilityJSON(family, "automatic", "cortex-owned", "CC-BY-SA-4.0", ""))); err != nil {
			t.Errorf("family %q rejected: %v", family, err)
		}
	}
}

func TestDecodeCapabilityManifestRejectsInvalidInput(t *testing.T) {
	valid := capabilityJSON("reasoning", "automatic", "cortex-owned", "CC-BY-SA-4.0", "")
	thirdParty := capabilityJSON("web", "automatic", "third-party", "MIT", `,"provenanceUrl":"https://example.com/source","redistributionUrl":"https://example.com/license"`)
	cases := []struct{ name, input string }{
		{"unknown", valid[:len(valid)-1] + `,"other":true}`}, {"extra", valid[:len(valid)-1] + `,"extra":true}`}, {"trailing", valid + ` true`}, {"missing", `{}`},
		{"activation", strings.Replace(valid, "automatic", "manual", 1)}, {"provenance", strings.Replace(valid, "cortex-owned", "unknown", 1)},
		{"redistribution", strings.Replace(valid, `"redistributionAllowed":true`, `"redistributionAllowed":false`, 1)},
		{"blank license", strings.Replace(thirdParty, `"MIT"`, `" "`, 1)},
		{"third-party terminal newline", strings.Replace(thirdParty, "https://example.com/source", "https://example.com/source\\n", 1)},
		{"third-party relative url", strings.Replace(thirdParty, "https://example.com/source", "/source", 1)},
		{"owned license", strings.Replace(valid, "CC-BY-SA-4.0", "MIT", 1)}, {"owned url", valid[:len(valid)-1] + `,"provenanceUrl":"https://example.com"}`},
		{"third-party missing url", strings.Replace(thirdParty, `,"redistributionUrl":"https://example.com/license"`, "", 1)},
		{"third-party http", strings.Replace(thirdParty, "https://example.com/source", "http://example.com/source", 1)},
		{"third-party userinfo", strings.Replace(thirdParty, "https://example.com/source", "https://user@example.com/source", 1)},
		{"third-party whitespace", strings.Replace(thirdParty, "https://example.com/source", "https://example.com/source ", 1)},
	}
	for _, value := range []string{"", "Test", "test_capability", "test-capability\n"} {
		cases = append(cases, struct{ name, input string }{"id " + value, strings.Replace(valid, "test-capability", value, 1)})
	}
	for _, value := range []string{"", "/capabilities/test.md", "capabilities/../test.md", `capabilities\\test.md`, "capabilities/test.json", "capabilities/test.md\n"} {
		cases = append(cases, struct{ name, input string }{"source " + value, strings.Replace(valid, "capabilities/test.md", value, 1)})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := DecodeCapabilityManifest([]byte(tc.input)); err == nil {
				t.Fatal("DecodeCapabilityManifest() error = nil")
			}
		})
	}
}

func TestCapabilitySchemaStructure(t *testing.T) {
	data, err := os.ReadFile("../../schemas/capability.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Required             []string                   `json:"required"`
		AdditionalProperties *bool                      `json:"additionalProperties"`
		Properties           map[string]json.RawMessage `json:"properties"`
		AllOf                json.RawMessage            `json:"allOf"`
	}
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	if schema.AdditionalProperties == nil || *schema.AdditionalProperties || strings.Join(schema.Required, ",") != "schemaVersion,id,description,family,source,activation,provenance,license,redistributionAllowed" {
		t.Fatal("schema required fields or additionalProperties differ from the manifest contract")
	}
	for property, value := range map[string]string{"schemaVersion": `"const": 1`, "id": `"maxLength": 57`, "description": `"maxLength": 1024`, "source": `.md(?![\\s\\S])`, "activation": `"automatic"`, "provenance": `"third-party"`, "redistributionAllowed": `"const": true`} {
		if !strings.Contains(string(schema.Properties[property]), value) {
			t.Errorf("%s contract missing %q", property, value)
		}
	}
	if !strings.Contains(string(schema.Properties["id"]), `(?![\\s\\S])`) {
		t.Error("id true-end grammar differs from the manifest contract")
	}
	const canonicalEvidenceURLPattern = `^https://[A-Za-z0-9](?:[A-Za-z0-9.-]*[A-Za-z0-9])?(?::[0-9]+)?(?:[/?#](?:[^\u0009-\u000D\u0020\u00A0\u1680\u2000-\u200A\u2028\u2029\u202F\u205F\u3000\uFEFF%]|%[0-9A-Fa-f]{2})*)?(?![\s\S])`
	const canonicalLicensePattern = `^[^\u0009-\u000D\u0020\u00A0\u1680\u2000-\u200A\u2028\u2029\u202F\u205F\u3000\uFEFF](?:[^\r\n\u2028\u2029]*[^\u0009-\u000D\u0020\u00A0\u1680\u2000-\u200A\u2028\u2029\u202F\u205F\u3000\uFEFF])?(?![\s\S])`
	for _, property := range []string{"provenanceUrl", "redistributionUrl", "license", "description"} {
		var definition struct {
			Pattern string `json:"pattern"`
		}
		if err := json.Unmarshal(schema.Properties[property], &definition); err != nil {
			t.Fatalf("decode %s schema: %v", property, err)
		}
		want := canonicalEvidenceURLPattern
		if property == "license" || property == "description" {
			want = canonicalLicensePattern
		}
		if definition.Pattern != want {
			t.Errorf("%s pattern = %q, want %q", property, definition.Pattern, want)
		}
	}
	for _, family := range ApprovedFamilyIDs() {
		if !strings.Contains(string(schema.Properties["family"]), `"`+family+`"`) {
			t.Errorf("family enum missing %q", family)
		}
	}
	if !strings.Contains(string(schema.Properties["activation"]), `"dormant"`) || !strings.Contains(string(schema.Properties["provenance"]), `"cortex-owned"`) || !strings.Contains(string(schema.AllOf), `"cortex-owned"`) || !strings.Contains(string(schema.AllOf), `"third-party"`) || !strings.Contains(string(schema.AllOf), `"provenanceUrl"`) || !strings.Contains(string(schema.AllOf), `"redistributionUrl"`) || !strings.Contains(string(schema.AllOf), `"not"`) {
		t.Error("schema enum or conditional contract missing")
	}
}
