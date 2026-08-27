package installstate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
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
	if schema["$schema"] != "https://json-schema.org/draft/2020-12/schema" || schema["additionalProperties"] != false || !strings.Contains(string(data), `"required": ["schemaVersion", "owner", "scope", "runtime", "rootKind", "snapshotFingerprint", "artifacts"]`) || !strings.Contains(string(data), `"minProperties": 1`) || !strings.Contains(string(data), `"allOf"`) {
		t.Fatalf("schema contract = %s", data)
	}
	for _, value := range []string{"install-state.schema.json", `"maxLength": 64`, `"maxLength": 80`, "relativePath", "sha256", `"additionalProperties": false`, "pi-user-agent", "opencode-user-config", "claude-code-user"} {
		if !strings.Contains(string(data), value) {
			t.Errorf("schema missing %q", value)
		}
	}
	properties := schema["properties"].(map[string]any)
	artifacts := properties["artifacts"].(map[string]any)
	if artifacts["minProperties"] != float64(1) {
		t.Fatal("artifacts lacks minProperties")
	}
}

func ExampleNew_candidateOnly() {
	_, _ = New(runtimematrix.RuntimePi, skilldest.RootKindPiUserAgent, fingerprint, inputs()[:1])
	fmt.Print("")
}
