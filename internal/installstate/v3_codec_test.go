package installstate

import (
	"bytes"
	"strings"
	"testing"

	"github.com/refactor-ia/cortex/internal/runtimematrix"
	"github.com/refactor-ia/cortex/internal/skilldest"
)

func v3Oracle() string {
	wire := strings.Replace(expectedV2JSON, `"schemaVersion":2`, `"schemaVersion":3`, 1)
	return strings.ReplaceAll(wire, `"sha256":"`+fingerprint+`"`, `"sha256":"`+fingerprint+`","canonicalSha256":"`+hash+`"`)
}

func TestV3CodecRoundTrip(t *testing.T) {
	wire := []byte(v3Oracle())
	manifest, err := Decode(wire)
	if err != nil || manifest.SchemaVersion() != 3 {
		t.Fatalf("Decode(valid v3) = (%+v, %v)", manifest, err)
	}
	for _, artifact := range manifest.Artifacts() {
		if artifact.Kind() == KindPiActor && (artifact.CanonicalSHA256() != hash || artifact.SHA256() != fingerprint) {
			t.Fatalf("canonical/effective identity conflated: %+v", artifact)
		}
		if artifact.Kind() == KindSkill && artifact.CanonicalSHA256() != "" {
			t.Fatal("skill acquired canonical metadata")
		}
	}
	if got := mustEncode(t, manifest); !bytes.Equal(got, wire) {
		t.Fatalf("roundtrip = %s, want %s", got, wire)
	}
}

func TestV3ModelIdentityBoundary(t *testing.T) {
	inputs := make([]V3ArtifactInput, 0)
	for _, input := range validV2Inputs() {
		canonical := ""
		if input.Kind == KindPiActor {
			canonical = hash
		}
		inputs = append(inputs, V3ArtifactInput{input, canonical})
	}
	manifest, err := NewV3(runtimematrix.RuntimePi, skilldest.RootKindPiUserAgent, fingerprint, installationID, inputs)
	if err != nil || string(mustEncode(t, manifest)) != v3Oracle() {
		t.Fatalf("NewV3() = (%+v, %v)", manifest, err)
	}
	inputs[0].CanonicalSHA256 = "bad"
	if string(mustEncode(t, manifest)) != v3Oracle() {
		t.Fatal("constructor retained caller-owned inputs")
	}
	if _, err := NewV3(runtimematrix.RuntimePi, skilldest.RootKindPiUserAgent, fingerprint, installationID, inputs); err == nil {
		t.Fatal("constructor accepted invalid canonical metadata")
	}
	for _, version := range []int{1, 2} {
		older := manifest
		older.schemaVersion = version
		if version == 1 {
			older = mustNew(t)
			older.artifacts[0].canonicalSHA256 = hash
		}
		if _, err := Encode(older); err == nil {
			t.Fatalf("schema %d encoded canonical metadata", version)
		}
	}
}

func TestV3CodecRejectsInvalidIdentity(t *testing.T) {
	wire := v3Oracle()
	canonical := `"canonicalSha256":"` + hash + `"`
	for _, tc := range []struct{ name, wire string }{
		{"missing canonical", strings.Replace(wire, ","+canonical, "", 1)},
		{"null canonical", strings.Replace(wire, canonical, `"canonicalSha256":null`, 1)},
		{"empty canonical", strings.Replace(wire, canonical, `"canonicalSha256":""`, 1)},
		{"malformed canonical", strings.Replace(wire, canonical, `"canonicalSha256":"bad"`, 1)},
		{"uppercase canonical", strings.Replace(wire, canonical, `"canonicalSha256":"`+strings.ToUpper(hash)+`"`, 1)},
		{"nonstring canonical", strings.Replace(wire, canonical, `"canonicalSha256":42`, 1)},
		{"skill canonical", strings.Replace(wire, `"kind":"skill"`, `"kind":"skill",`+canonical, 1)},
		{"skill null canonical", strings.Replace(wire, `"kind":"skill"`, `"kind":"skill","canonicalSha256":null`, 1)},
		{"v2 canonical", strings.Replace(wire, `"schemaVersion":3`, `"schemaVersion":2`, 1)},
		{"unknown field", strings.Replace(wire, `"canonicalSha256":`, `"unknown":true,"canonicalSha256":`, 1)},
		{"trailing JSON", wire + `{}`},
		{"duplicate artifact ID", strings.Replace(wire, `"skills/alpha":`+expectedSkillJSON, `"skills/alpha":`+expectedSkillJSON+`,"skills/alpha":`+expectedSkillJSON, 1)},
		{"non Pi runtime", strings.Replace(wire, `"runtime":"pi"`, `"runtime":"opencode"`, 1)},
		{"wrong root", strings.Replace(wire, `"rootKind":"pi-user-agent"`, `"rootKind":"claude-code-user"`, 1)},
		{"wrong actor path", strings.Replace(wire, `agents/cortex-test-runner.md`, `agents/other.md`, 1)},
		{"wrong actor role", strings.Replace(wire, `"roleId":"test-runner"`, `"roleId":"unknown"`, 1)},
		{"wrong actor contract", strings.ReplaceAll(wire, `cortex.qa.pi-actor.v1`, `other`)},
		{"malformed effective digest", strings.Replace(wire, `"sha256":"`+fingerprint+`"`, `"sha256":"bad"`, 1)},
		{"installation mismatch", strings.Replace(wire, `"installationId":"`+installationID+`"`, `"installationId":"111102030405060708090a0b0c0d0e0f"`, 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manifest, err := Decode([]byte(tc.wire))
			if err == nil || !zero(manifest) {
				t.Fatalf("invalid identity accepted: (%+v, %v)", manifest, err)
			}
		})
	}
	v1 := strings.Replace(string(mustEncode(t, mustNew(t))), `"sha256":`, canonical+`,"sha256":`, 1)
	if _, err := Decode([]byte(v1)); err == nil {
		t.Fatal("v1 accepted canonical metadata")
	}
}
