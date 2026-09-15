package installstate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/refactor-ia/cortex/internal/runtimematrix"
	"github.com/refactor-ia/cortex/internal/skilldest"
)

const fingerprint = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const hash = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

func inputs() []ArtifactInput {
	return []ArtifactInput{{"skills/zeta", "skills/cortex-zeta/SKILL.md", hash}, {"skills/alpha", "skills/cortex-alpha/SKILL.md", fingerprint}}
}

func TestNewEncodeDecodeRoundTrip(t *testing.T) {
	in := inputs()
	manifest, err := New(runtimematrix.RuntimePi, skilldest.RootKindPiUserAgent, fingerprint, in)
	in[0].LogicalID, in[1] = "skills/mutated", ArtifactInput{"skills/replaced", "skills/cortex-replaced/SKILL.md", hash}
	if err != nil || manifest.SchemaVersion() != 1 || manifest.Owner() != "cortex" || manifest.Scope() != "user" || manifest.RuntimeID() != runtimematrix.RuntimePi || manifest.RootKind() != skilldest.RootKindPiUserAgent || manifest.SnapshotFingerprint() != fingerprint {
		t.Fatalf("New() = (%+v, %v)", manifest, err)
	}
	artifacts := manifest.Artifacts()
	if len(artifacts) != 2 || artifacts[0].LogicalID() != "skills/alpha" || artifacts[0].RelativePath() != "skills/cortex-alpha/SKILL.md" {
		t.Fatalf("Artifacts() = %#v", artifacts)
	}
	artifacts[0] = Artifact{}
	if manifest.Artifacts()[0].LogicalID() != "skills/alpha" {
		t.Fatal("Artifacts exposed state")
	}
	encoded, err := Encode(manifest)
	want := `{"schemaVersion":1,"owner":"cortex","scope":"user","runtime":"pi","rootKind":"pi-user-agent","snapshotFingerprint":"` + fingerprint + `","artifacts":{"skills/alpha":{"relativePath":"skills/cortex-alpha/SKILL.md","sha256":"` + fingerprint + `"},"skills/zeta":{"relativePath":"skills/cortex-zeta/SKILL.md","sha256":"` + hash + `"}}}`
	if err != nil || string(encoded) != want {
		t.Fatalf("Encode() = %q, %v", encoded, err)
	}
	encoded[0] = 'x'
	again, _ := Encode(manifest)
	decoded, err := Decode(again)
	if err != nil || !bytes.Equal(again, mustEncode(t, decoded)) || decoded.Artifacts()[1].LogicalID() != "skills/zeta" {
		t.Fatalf("Decode() = (%+v, %v)", decoded, err)
	}
}

func mustEncode(t *testing.T, manifest Manifest) []byte {
	t.Helper()
	data, err := Encode(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestNewValidRuntimeRootPairs(t *testing.T) {
	for _, tc := range []struct {
		runtime runtimematrix.RuntimeID
		root    skilldest.RootKind
	}{{runtimematrix.RuntimePi, skilldest.RootKindPiUserAgent}, {runtimematrix.RuntimeOpenCode, skilldest.RootKindOpenCodeUserConfig}, {runtimematrix.RuntimeClaudeCode, skilldest.RootKindClaudeCodeUser}} {
		t.Run(string(tc.runtime), func(t *testing.T) {
			if _, err := New(tc.runtime, tc.root, fingerprint, inputs()[:1]); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestNewRejectsInvalidStateWithoutLeakage(t *testing.T) {
	long := strings.Repeat("a", 58)
	for _, tc := range []struct {
		name     string
		runtime  runtimematrix.RuntimeID
		root     skilldest.RootKind
		snapshot string
		in       []ArtifactInput
	}{
		{"mismatch", runtimematrix.RuntimePi, skilldest.RootKindClaudeCodeUser, fingerprint, inputs()[:1]}, {"zero runtime", "", skilldest.RootKindPiUserAgent, fingerprint, inputs()[:1]}, {"unknown root", runtimematrix.RuntimePi, "other", fingerprint, inputs()[:1]}, {"snapshot", runtimematrix.RuntimePi, skilldest.RootKindPiUserAgent, "secret", inputs()[:1]}, {"nil", runtimematrix.RuntimePi, skilldest.RootKindPiUserAgent, fingerprint, nil}, {"duplicate", runtimematrix.RuntimePi, skilldest.RootKindPiUserAgent, fingerprint, append(inputs()[:1], inputs()[:1]...)}, {"logical", runtimematrix.RuntimePi, skilldest.RootKindPiUserAgent, fingerprint, []ArtifactInput{{"secret", "skills/cortex-alpha/SKILL.md", hash}}}, {"58 id", runtimematrix.RuntimePi, skilldest.RootKindPiUserAgent, fingerprint, []ArtifactInput{{"skills/" + long, "skills/cortex-" + long + "/SKILL.md", hash}}}, {"path", runtimematrix.RuntimePi, skilldest.RootKindPiUserAgent, fingerprint, []ArtifactInput{{"skills/alpha", "/secret", hash}}}, {"traversal", runtimematrix.RuntimePi, skilldest.RootKindPiUserAgent, fingerprint, []ArtifactInput{{"skills/alpha", "skills/../secret", hash}}}, {"dot segment", runtimematrix.RuntimePi, skilldest.RootKindPiUserAgent, fingerprint, []ArtifactInput{{"skills/alpha", "skills/./cortex-a/SKILL.md", hash}}}, {"slash", runtimematrix.RuntimePi, skilldest.RootKindPiUserAgent, fingerprint, []ArtifactInput{{"skills/alpha", `skills\cortex-alpha\SKILL.md`, hash}}}, {"suffix", runtimematrix.RuntimePi, skilldest.RootKindPiUserAgent, fingerprint, []ArtifactInput{{"skills/alpha", "skills/cortex-alpha/other", hash}}}, {"hash", runtimematrix.RuntimePi, skilldest.RootKindPiUserAgent, fingerprint, []ArtifactInput{{"skills/alpha", "skills/cortex-alpha/SKILL.md", "secret"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := New(tc.runtime, tc.root, tc.snapshot, tc.in)
			if err == nil || !zero(got) || strings.Contains(err.Error(), "secret") {
				t.Fatalf("New() = (%+v, %v)", got, err)
			}
		})
	}
	if _, err := New(runtimematrix.RuntimePi, skilldest.RootKindPiUserAgent, fingerprint, []ArtifactInput{{"skills/" + strings.Repeat("a", 57), "skills/cortex-" + strings.Repeat("a", 57) + "/SKILL.md", hash}}); err != nil {
		t.Fatal(err)
	}
}

func TestDecodeIsStrict(t *testing.T) {
	valid := string(mustEncode(t, mustNew(t)))
	for _, input := range []string{`{}`, strings.Replace(valid, `"owner":"cortex"`, `"owner":"other"`, 1), strings.Replace(valid, `"scope":"user"`, `"scope":"other"`, 1), strings.Replace(valid, `"schemaVersion":1`, `"schemaVersion":2`, 1), strings.Replace(valid, `"artifacts":{`, `"unknown":1,"artifacts":{`, 1), strings.Replace(valid, `"sha256":`, `"unknown":1,"sha256":`, 1), strings.Replace(valid, `"artifacts":{`, `"artifacts":{}`, 1), valid + ` {}`, valid + `{}`} {
		got, err := Decode([]byte(input))
		if err == nil || !zero(got) || strings.Contains(err.Error(), "other") {
			t.Fatalf("Decode(%q) = (%+v, %v)", input, got, err)
		}
	}
	got, err := Decode([]byte{0xff})
	if err == nil || !zero(got) {
		t.Fatalf("invalid UTF-8 = (%+v, %v)", got, err)
	}
}

func mustNew(t *testing.T) Manifest {
	t.Helper()
	manifest, err := New(runtimematrix.RuntimePi, skilldest.RootKindPiUserAgent, fingerprint, inputs())
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}
func zero(manifest Manifest) bool {
	return manifest.SchemaVersion() == 0 && manifest.Owner() == "" && manifest.Scope() == "" && manifest.RuntimeID() == "" && manifest.RootKind() == "" && manifest.SnapshotFingerprint() == "" && manifest.Artifacts() != nil && len(manifest.Artifacts()) == 0
}

func TestSchemaContract(t *testing.T) {
	data, err := os.ReadFile("../../schemas/install-state.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	if schema["$schema"] != "https://json-schema.org/draft/2020-12/schema" {
		t.Fatalf("$schema = %q", schema["$schema"])
	}
	if schema["$comment"] != "Decode remains authoritative for raw duplicate artifact keys; NewV2 remains authoritative for installation-ID equality, unique paths, skill key-to-field/path correlation, and the desired skill set." {
		t.Fatalf("schema limitation comment = %q", schema["$comment"])
	}

	variants := schema["oneOf"].([]any)
	if len(variants) != 2 {
		t.Fatalf("root oneOf variants = %d, want 2", len(variants))
	}
	definitions := schemaObject(t, schema["$defs"])
	v1 := schemaObject(t, definitions["v1Manifest"])
	v2 := schemaObject(t, definitions["v2Manifest"])
	assertSchemaRequired(t, v1, "schemaVersion", "owner", "scope", "runtime", "rootKind", "snapshotFingerprint", "artifacts")
	assertSchemaRequired(t, v2, "schemaVersion", "owner", "scope", "runtime", "rootKind", "snapshotFingerprint", "installationId", "artifacts")
	if v1["additionalProperties"] != false || v2["additionalProperties"] != false {
		t.Fatal("manifest variants must be closed")
	}
	v1Pairs := v1["allOf"].([]any)
	if len(v1Pairs) != 1 || schemaObject(t, v1Pairs[0])["$ref"] != "#/$defs/runtimeRootPair" {
		t.Fatal("v1 must preserve runtime/root pairing")
	}

	v2Properties := schemaObject(t, v2["properties"])
	if schemaObject(t, v2Properties["artifacts"])["$ref"] != "#/$defs/v2Artifacts" {
		t.Fatal("v2 must use the exact artifact definition")
	}
	artifacts := schemaObject(t, definitions["v2Artifacts"])
	if artifacts["minProperties"] != float64(7) || artifacts["additionalProperties"] != false {
		t.Fatalf("v2 artifacts boundary = %#v", artifacts)
	}
	assertSchemaRequired(t, artifacts, "actors/requirements-analyst", "actors/test-designer", "actors/exploratory-tester", "actors/adversarial-tester", "actors/test-runner", "actors/evidence-auditor")
	if _, ok := schemaObject(t, artifacts["patternProperties"])[`^skills/[a-z0-9]+(?:-[a-z0-9]+)*$`]; !ok {
		t.Fatal("v2 artifacts lacks canonical skill-key pattern")
	}
	for _, role := range []string{"requirements-analyst", "test-designer", "exploratory-tester", "adversarial-tester", "test-runner", "evidence-auditor"} {
		actorRef := schemaObject(t, schemaObject(t, artifacts["properties"])["actors/"+role])["$ref"].(string)
		actor := schemaObject(t, definitions[strings.TrimPrefix(actorRef, "#/$defs/")])
		allOf := actor["allOf"].([]any)
		if len(allOf) != 2 {
			t.Fatalf("actor %q is not an exact actor schema", role)
		}
		properties := schemaObject(t, schemaObject(t, allOf[1])["properties"])
		if got := schemaObject(t, properties["roleId"])["const"]; got != role {
			t.Errorf("actor %q roleId const = %q", role, got)
		}
		if got := schemaObject(t, properties["relativePath"])["const"]; got != "agents/cortex-"+role+".md" {
			t.Errorf("actor %q relativePath const = %q", role, got)
		}
	}
	for _, name := range []string{"hash", "installationID", "v1SkillArtifact", "skillArtifact", "actorArtifact", "runtimeRootPair"} {
		if _, ok := definitions[name]; !ok {
			t.Errorf("schema lacks reusable definition %q", name)
		}
	}
}

func schemaObject(t *testing.T, value any) map[string]any {
	t.Helper()
	object, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("schema value is %T, want object", value)
	}
	return object
}

func assertSchemaRequired(t *testing.T, schema map[string]any, want ...string) {
	t.Helper()
	got, ok := schema["required"].([]any)
	if !ok {
		t.Fatalf("schema required = %T, want array", schema["required"])
	}
	actual := make([]string, len(got))
	for index, value := range got {
		actual[index] = value.(string)
	}
	if !reflect.DeepEqual(actual, want) {
		t.Fatalf("schema required = %#v, want %#v", actual, want)
	}
}

func ExampleNew_candidateOnly() {
	_, _ = New(runtimematrix.RuntimePi, skilldest.RootKindPiUserAgent, fingerprint, inputs()[:1])
	fmt.Print("")
}
