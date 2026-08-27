package artifact

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/refactor-ia/cortex/internal/projection"
	"github.com/refactor-ia/cortex/internal/runtimematrix"
)

const fingerprint = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const hash = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

func manifestJSON(runtime, result, disclosure, artifacts string) string {
	disclosureField := ""
	if disclosure != "" {
		disclosureField = fmt.Sprintf(`,"translationDisclosure":%q`, disclosure)
	}
	return fmt.Sprintf(`{"schemaVersion":1,"owner":"cortex","snapshotFingerprint":%q,"runtime":%q,"projectionResult":%q%s,"artifacts":%s}`,
		fingerprint, runtime, result, disclosureField, artifacts)
}

func TestDecodeManifestValidAndSorted(t *testing.T) {
	for _, runtime := range []runtimematrix.RuntimeID{runtimematrix.RuntimePi, runtimematrix.RuntimeOpenCode, runtimematrix.RuntimeClaudeCode} {
		for _, result := range []projection.Result{projection.Exact, projection.Translated} {
			t.Run(string(runtime)+"/"+string(result), func(t *testing.T) {
				disclosure := ""
				if result == projection.Translated {
					disclosure = "Equivalent runtime representation"
				}
				input := []byte(manifestJSON(string(runtime), string(result), disclosure, `{"families/reasoning/router":"`+hash+`","capabilities/tear":"`+fingerprint+`"}`))
				original := append([]byte(nil), input...)
				manifest, err := DecodeManifest(input)
				if err != nil || !bytes.Equal(input, original) || manifest.SchemaVersion() != 1 || manifest.Owner() != "cortex" || manifest.SnapshotFingerprint() != fingerprint || manifest.RuntimeID() != runtime || manifest.ProjectionResult() != result || manifest.TranslationDisclosure() != disclosure {
					t.Fatalf("DecodeManifest() = (%+v, %v)", manifest, err)
				}
				artifacts := manifest.Artifacts()
				if len(artifacts) != 2 || artifacts[0].LogicalID() != "capabilities/tear" || artifacts[1].LogicalID() != "families/reasoning/router" || artifacts[0].SHA256() != fingerprint {
					t.Fatalf("Artifacts() = %+v", artifacts)
				}
				artifacts[0] = Artifact{}
				if manifest.Artifacts()[0].LogicalID() != "capabilities/tear" {
					t.Fatal("Artifacts() exposed manifest state")
				}
			})
		}
	}
}

func zeroManifest(manifest Manifest) bool {
	return manifest.SchemaVersion() == 0 && manifest.Owner() == "" && manifest.SnapshotFingerprint() == "" && manifest.RuntimeID() == "" && manifest.ProjectionResult() == "" && manifest.TranslationDisclosure() == "" && manifest.Artifacts() == nil
}

func TestDecodeManifestRejectsInvalidInputWithoutLeakage(t *testing.T) {
	valid := manifestJSON("pi", "exact", "", `{"families/reasoning/router":"`+hash+`"}`)
	for _, tc := range []struct{ name, input string }{
		{"exact disclosure", manifestJSON("pi", "exact", "secret", `{"families/router":"`+hash+`"}`)},
		{"exact empty disclosure", strings.Replace(valid, `,"artifacts":`, `,"translationDisclosure":"","artifacts":`, 1)},
		{"translated missing disclosure", manifestJSON("pi", "translated", "", `{"families/router":"`+hash+`"}`)},
		{"translated whitespace disclosure", manifestJSON("pi", "translated", " ", `{"families/router":"`+hash+`"}`)},
		{"translated newline disclosure", manifestJSON("pi", "translated", "line\nbreak", `{"families/router":"`+hash+`"}`)},
		{"translated CR disclosure", manifestJSON("pi", "translated", "line\rbreak", `{"families/router":"`+hash+`"}`)},
		{"translated Unicode line disclosure", manifestJSON("pi", "translated", "line\u2028break", `{"families/router":"`+hash+`"}`)},
		{"translated paragraph disclosure", manifestJSON("pi", "translated", "line\u2029break", `{"families/router":"`+hash+`"}`)},
		{"unrepresentable", manifestJSON("pi", "unrepresentable", "", `{"families/router":"`+hash+`"}`)},
		{"wrong version", strings.Replace(valid, `"schemaVersion":1`, `"schemaVersion":2`, 1)},
		{"missing version", strings.Replace(valid, `"schemaVersion":1,`, "", 1)},
		{"wrong owner", strings.Replace(valid, `"owner":"cortex"`, `"owner":"other"`, 1)},
		{"missing owner", strings.Replace(valid, `"owner":"cortex",`, "", 1)},
		{"wrong runtime", strings.Replace(valid, `"runtime":"pi"`, `"runtime":"other"`, 1)},
		{"missing runtime", strings.Replace(valid, `"runtime":"pi",`, "", 1)},
		{"wrong result", strings.Replace(valid, `"projectionResult":"exact"`, `"projectionResult":"other"`, 1)},
		{"missing result", strings.Replace(valid, `"projectionResult":"exact",`, "", 1)},
		{"missing artifacts", strings.Replace(valid, `,"artifacts":{`, `,"artifact":{`, 1)},
		{"empty artifacts", strings.Replace(valid, `{"families/reasoning/router":"`+hash+`"}`, `{}`, 1)},
		{"unknown field", strings.Replace(valid, `}`, `,"secret":"private"}`, 1)},
		{"trailing JSON", valid + ` {}`},
		{"bad fingerprint", strings.Replace(valid, fingerprint, "short", 1)},
		{"uppercase fingerprint", strings.Replace(valid, fingerprint, strings.ToUpper(fingerprint), 1)},
		{"newline fingerprint", strings.Replace(valid, fingerprint, fingerprint+"\n", 1)},
		{"bad hash", strings.Replace(valid, hash, "short", 1)},
		{"uppercase hash", strings.Replace(valid, hash, strings.ToUpper(hash), 1)},
		{"newline hash", strings.Replace(valid, hash, hash+"\n", 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DecodeManifest([]byte(tc.input))
			if err == nil || !zeroManifest(got) || strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "secret") {
				t.Fatalf("DecodeManifest() = (%+v, %v)", got, err)
			}
		})
	}
}

func TestDecodeManifestRejectsLogicalIDGrammar(t *testing.T) {
	for _, id := range []string{"/families", "families/", "families//router", ".", "..", "families/./router", "families/../router", "Families/router", "families/router_name", `families\router`, "families /router", "families/\nrouter", "families/\u2028router", "families/-router", "families/router-", "families/router--leaf"} {
		t.Run(fmt.Sprintf("%q", id), func(t *testing.T) {
			input := manifestJSON("pi", "exact", "", fmt.Sprintf(`{%q:%q}`, id, hash))
			if got, err := DecodeManifest([]byte(input)); err == nil || !zeroManifest(got) {
				t.Fatalf("DecodeManifest() = (%+v, %v)", got, err)
			}
		})
	}
}

func TestArtifactSchemaContract(t *testing.T) {
	data, err := os.ReadFile("../../schemas/artifact.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"https://json-schema.org/draft/2020-12/schema", "https://github.com/refactor-ia/cortex/schemas/artifact.schema.json", "schemaVersion", "snapshotFingerprint", "projectionResult", "translationDisclosure", "propertyNames", "minProperties", "allOf", `"additionalProperties": false`, `(?![\\s\\S])`} {
		if !strings.Contains(string(data), expected) {
			t.Errorf("schema missing %q", expected)
		}
	}
	if schema["$schema"] != "https://json-schema.org/draft/2020-12/schema" || schema["$id"] != "https://github.com/refactor-ia/cortex/schemas/artifact.schema.json" || schema["type"] != "object" || schema["additionalProperties"] != false {
		t.Fatalf("root contract = %#v", schema)
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok || len(properties) != 7 {
		t.Fatalf("root properties = %#v", schema["properties"])
	}
	for _, name := range []string{"schemaVersion", "owner", "snapshotFingerprint", "runtime", "projectionResult", "translationDisclosure", "artifacts"} {
		if _, ok := properties[name]; !ok {
			t.Errorf("missing root property %q", name)
		}
	}
	required, ok := schema["required"].([]any)
	if !ok || len(required) != 6 {
		t.Fatalf("root required = %#v", schema["required"])
	}
	for _, name := range []string{"schemaVersion", "owner", "snapshotFingerprint", "runtime", "projectionResult", "artifacts"} {
		found := false
		for _, value := range required {
			found = found || value == name
		}
		if !found {
			t.Errorf("missing required field %q", name)
		}
	}
	for _, forbidden := range []string{"path", "content", "media", "mode", "size"} {
		if strings.Contains(string(data), `"`+forbidden+`"`) {
			t.Errorf("schema contains forbidden field %q", forbidden)
		}
	}
}
