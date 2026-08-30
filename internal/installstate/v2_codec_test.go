package installstate

import (
	"bytes"
	"strings"
	"testing"

	"github.com/refactor-ia/cortex/internal/runtimematrix"
	"github.com/refactor-ia/cortex/internal/skilldest"
)

const expectedSkillJSON = `{"kind":"skill","capabilityId":"alpha","relativePath":"skills/cortex-alpha/SKILL.md","sha256":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","installationId":"000102030405060708090a0b0c0d0e0f"}`

const expectedV2JSON = `{"schemaVersion":2,"owner":"cortex","scope":"user","runtime":"pi","rootKind":"pi-user-agent",` +
	`"snapshotFingerprint":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","installationId":"000102030405060708090a0b0c0d0e0f","artifacts":{` +
	`"actors/adversarial-tester":{"kind":"pi-actor","roleId":"adversarial-tester","actorContractVersion":"cortex.qa.pi-actor.v1","relativePath":"agents/cortex-adversarial-tester.md","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","installationId":"000102030405060708090a0b0c0d0e0f"},` +
	`"actors/evidence-auditor":{"kind":"pi-actor","roleId":"evidence-auditor","actorContractVersion":"cortex.qa.pi-actor.v1","relativePath":"agents/cortex-evidence-auditor.md","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","installationId":"000102030405060708090a0b0c0d0e0f"},` +
	`"actors/exploratory-tester":{"kind":"pi-actor","roleId":"exploratory-tester","actorContractVersion":"cortex.qa.pi-actor.v1","relativePath":"agents/cortex-exploratory-tester.md","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","installationId":"000102030405060708090a0b0c0d0e0f"},` +
	`"actors/requirements-analyst":{"kind":"pi-actor","roleId":"requirements-analyst","actorContractVersion":"cortex.qa.pi-actor.v1","relativePath":"agents/cortex-requirements-analyst.md","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","installationId":"000102030405060708090a0b0c0d0e0f"},` +
	`"actors/test-designer":{"kind":"pi-actor","roleId":"test-designer","actorContractVersion":"cortex.qa.pi-actor.v1","relativePath":"agents/cortex-test-designer.md","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","installationId":"000102030405060708090a0b0c0d0e0f"},` +
	`"actors/test-runner":{"kind":"pi-actor","roleId":"test-runner","actorContractVersion":"cortex.qa.pi-actor.v1","relativePath":"agents/cortex-test-runner.md","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","installationId":"000102030405060708090a0b0c0d0e0f"},` +
	`"skills/alpha":` + expectedSkillJSON + `}}`

func TestV2CodecRoundTrip(t *testing.T) {
	manifest, err := NewV2(
		runtimematrix.RuntimePi,
		skilldest.RootKindPiUserAgent,
		fingerprint,
		installationID,
		validV2Inputs(),
	)
	if err != nil {
		t.Fatal(err)
	}
	encoded := mustEncode(t, manifest)
	if got := string(encoded); got != expectedV2JSON {
		t.Fatalf("Encode() = %q, want %q", got, expectedV2JSON)
	}
	decoded, err := Decode([]byte(expectedV2JSON))
	if err != nil {
		t.Fatalf("Decode(oracle) = %v", err)
	}
	again, err := Encode(decoded)
	if err != nil || string(again) != expectedV2JSON {
		t.Fatalf("Encode(Decode(oracle)) = (%q, %v), want %q", again, err, expectedV2JSON)
	}
}

func TestV2CodecRejectsInvalidWire(t *testing.T) {
	valid := expectedV2JSON
	for _, tc := range []struct {
		name  string
		input []byte
	}{
		{name: "missing required top field", input: []byte(strings.Replace(valid, `"installationId":"`+installationID+`",`, "", 1))},
		{name: "unknown top field", input: []byte(strings.Replace(valid, `"artifacts":{`, `"unknown":true,"artifacts":{`, 1))},
		{name: "null top field", input: []byte(strings.Replace(valid, `"owner":"cortex"`, `"owner":null`, 1))},
		{name: "null artifact", input: []byte(strings.Replace(valid, expectedSkillJSON, `null`, 1))},
		{name: "null common field", input: []byte(strings.Replace(valid, `"relativePath":"skills/cortex-alpha/SKILL.md"`, `"relativePath":null`, 1))},
		{name: "unknown entry field", input: []byte(strings.Replace(valid, `"kind":"skill"`, `"unknown":true,"kind":"skill"`, 1))},
		{name: "missing union field", input: []byte(strings.Replace(valid, `"capabilityId":"alpha",`, "", 1))},
		{name: "skill cross kind field", input: []byte(strings.Replace(valid, `"capabilityId":"alpha"`, `"capabilityId":"alpha","roleId":"test-runner"`, 1))},
		{name: "actor cross kind field", input: []byte(strings.Replace(valid, `"roleId":"test-designer"`, `"capabilityId":"alpha","roleId":"test-designer"`, 1))},
		{name: "unknown kind", input: []byte(strings.Replace(valid, `"kind":"skill"`, `"kind":"other"`, 1))},
		{name: "unsupported version", input: []byte(strings.Replace(valid, `"schemaVersion":2`, `"schemaVersion":3`, 1))},
		{name: "trailing JSON", input: []byte(valid + `{}`)},
		{name: "invalid UTF-8", input: []byte{0xff}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manifest, err := Decode(tc.input)
			if err == nil || !zero(manifest) {
				t.Fatalf("Decode(%q) = (%+v, %v)", tc.input, manifest, err)
			}
		})
	}
}

func TestV1CodecBytePreservation(t *testing.T) {
	encoded := mustEncode(t, mustNew(t))
	decoded, err := Decode(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if got := mustEncode(t, decoded); !bytes.Equal(got, encoded) {
		t.Fatalf("Encode(Decode(v1)) = %q, want %q", got, encoded)
	}
	artifact := decoded.Artifacts()[0]
	if decoded.InstallationID() != "" || artifact.Kind() != "" || artifact.InstallationID() != "" {
		t.Fatalf("Decode(v1) synthesized ownership: %#v", artifact)
	}
}
