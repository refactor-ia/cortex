package installstate

import (
	"bytes"
	"testing"

	"github.com/refactor-ia/cortex/internal/qarole"
	"github.com/refactor-ia/cortex/internal/runtimematrix"
	"github.com/refactor-ia/cortex/internal/skilldest"
)

const installationID = "000102030405060708090a0b0c0d0e0f"

var v2Roles = []qarole.RoleID{
	"requirements-analyst",
	"test-designer",
	"exploratory-tester",
	"adversarial-tester",
	"test-runner",
	"evidence-auditor",
}

func TestInstallationID(t *testing.T) {
	entropy := append([]byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}, 16)
	reader := bytes.NewReader(entropy)
	got, err := NewInstallationIDGenerator(reader).Generate()
	if err != nil || got != installationID || reader.Len() != 1 {
		t.Fatalf("InstallationIDGenerator.Generate() = (%q, %v), %d bytes remain", got, err, reader.Len())
	}
	if DefaultInstallationIDGenerator() == nil {
		t.Fatal("DefaultInstallationIDGenerator() returned nil")
	}
	for _, tc := range []struct {
		name   string
		reader interface{ Read([]byte) (int, error) }
	}{
		{name: "nil", reader: nil},
		{name: "short", reader: bytes.NewReader([]byte{1})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, err := NewInstallationIDGenerator(tc.reader).Generate(); err == nil || got != "" {
				t.Fatalf("InstallationIDGenerator.Generate() = (%q, %v)", got, err)
			}
		})
	}
}

func TestV2Model(t *testing.T) {
	inputs := validV2Inputs()
	manifest, err := NewV2(runtimematrix.RuntimePi, skilldest.RootKindPiUserAgent, fingerprint, installationID, inputs)
	if err != nil || manifest.SchemaVersion() != 2 || manifest.InstallationID() != installationID {
		t.Fatalf("NewV2() = (%+v, %v)", manifest, err)
	}
	inputs[0].CapabilityID = "mutated"
	artifacts := manifest.Artifacts()
	if len(artifacts) != 7 || artifacts[0].LogicalID() != "actors/adversarial-tester" || artifacts[0].Kind() != KindPiActor || artifacts[0].CapabilityID() != "" || artifacts[0].RoleID() != "adversarial-tester" || artifacts[0].ActorContractVersion() != "cortex.qa.pi-actor.v1" || artifacts[0].InstallationID() != installationID {
		t.Fatalf("Artifacts() = %#v", artifacts)
	}
	artifacts[0] = Artifact{}
	if manifest.Artifacts()[0].LogicalID() != "actors/adversarial-tester" {
		t.Fatal("Artifacts exposed state")
	}
	if _, err := Encode(manifest); err == nil {
		t.Fatal("Encode() accepted v2 before the v2 codec exists")
	}
}

func TestV2ModelRejectsInvalidOwnership(t *testing.T) {
	for _, tc := range []struct {
		name    string
		runtime runtimematrix.RuntimeID
		root    skilldest.RootKind
		id      InstallationID
		mutate  func([]V2ArtifactInput)
	}{
		{name: "runtime", runtime: runtimematrix.RuntimeOpenCode, root: skilldest.RootKindPiUserAgent, id: installationID},
		{name: "root", runtime: runtimematrix.RuntimePi, root: skilldest.RootKindClaudeCodeUser, id: installationID},
		{name: "top ID", runtime: runtimematrix.RuntimePi, root: skilldest.RootKindPiUserAgent, id: "upper"},
		{name: "entry ID", runtime: runtimematrix.RuntimePi, root: skilldest.RootKindPiUserAgent, id: installationID, mutate: func(in []V2ArtifactInput) { in[0].InstallationID = "ffffffffffffffffffffffffffffffff" }},
		{name: "duplicate logical ID", runtime: runtimematrix.RuntimePi, root: skilldest.RootKindPiUserAgent, id: installationID, mutate: func(in []V2ArtifactInput) { in[1].LogicalID = in[0].LogicalID }},
		{name: "duplicate path", runtime: runtimematrix.RuntimePi, root: skilldest.RootKindPiUserAgent, id: installationID, mutate: func(in []V2ArtifactInput) { in[1].RelativePath = in[0].RelativePath }},
		{name: "unknown kind", runtime: runtimematrix.RuntimePi, root: skilldest.RootKindPiUserAgent, id: installationID, mutate: func(in []V2ArtifactInput) { in[0].Kind = Kind("other") }},
		{name: "missing skill", runtime: runtimematrix.RuntimePi, root: skilldest.RootKindPiUserAgent, id: installationID, mutate: func(in []V2ArtifactInput) {
			in[0].Kind = KindPiActor
			in[0].CapabilityID = ""
			in[0].RoleID = v2Roles[0]
			in[0].ActorContractVersion = "cortex.qa.pi-actor.v1"
			in[0].LogicalID = "actors/" + string(v2Roles[0])
			in[0].RelativePath = "agents/cortex-" + string(v2Roles[0]) + ".md"
		}},
		{name: "missing role", runtime: runtimematrix.RuntimePi, root: skilldest.RootKindPiUserAgent, id: installationID, mutate: func(in []V2ArtifactInput) { in[6].RoleID = "unknown" }},
		{name: "skill union leak", runtime: runtimematrix.RuntimePi, root: skilldest.RootKindPiUserAgent, id: installationID, mutate: func(in []V2ArtifactInput) { in[0].RoleID = v2Roles[0] }},
		{name: "actor union leak", runtime: runtimematrix.RuntimePi, root: skilldest.RootKindPiUserAgent, id: installationID, mutate: func(in []V2ArtifactInput) { in[1].CapabilityID = "alpha" }},
		{name: "actor contract", runtime: runtimematrix.RuntimePi, root: skilldest.RootKindPiUserAgent, id: installationID, mutate: func(in []V2ArtifactInput) { in[1].ActorContractVersion = "other" }},
		{name: "noncanonical path", runtime: runtimematrix.RuntimePi, root: skilldest.RootKindPiUserAgent, id: installationID, mutate: func(in []V2ArtifactInput) { in[1].RelativePath = `agents\cortex-test-designer.md` }},
		{name: "absolute path", runtime: runtimematrix.RuntimePi, root: skilldest.RootKindPiUserAgent, id: installationID, mutate: func(in []V2ArtifactInput) { in[1].RelativePath = "/agents/cortex-test-designer.md" }},
		{name: "traversal", runtime: runtimematrix.RuntimePi, root: skilldest.RootKindPiUserAgent, id: installationID, mutate: func(in []V2ArtifactInput) { in[1].RelativePath = "agents/../cortex-test-designer.md" }},
		{name: "logical ID", runtime: runtimematrix.RuntimePi, root: skilldest.RootKindPiUserAgent, id: installationID, mutate: func(in []V2ArtifactInput) { in[1].LogicalID = "actors/other" }},
		{name: "hash", runtime: runtimematrix.RuntimePi, root: skilldest.RootKindPiUserAgent, id: installationID, mutate: func(in []V2ArtifactInput) {
			in[1].SHA256 = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inputs := validV2Inputs()
			if tc.mutate != nil {
				tc.mutate(inputs)
			}
			if got, err := NewV2(tc.runtime, tc.root, fingerprint, tc.id, inputs); err == nil || !zero(got) {
				t.Fatalf("NewV2() = (%+v, %v)", got, err)
			}
		})
	}
}

func TestV1DoesNotCarryV2Ownership(t *testing.T) {
	manifest := mustNew(t)
	artifact := manifest.Artifacts()[0]
	if manifest.InstallationID() != "" || artifact.Kind() != "" || artifact.CapabilityID() != "" || artifact.RoleID() != "" || artifact.ActorContractVersion() != "" || artifact.InstallationID() != "" {
		t.Fatalf("v1 exposed v2 ownership: %#v", artifact)
	}
	manifest.artifacts[0].kind = "skill"
	if _, err := Encode(manifest); err == nil {
		t.Fatal("Encode() discarded hidden v2 ownership")
	}
}

func validV2Inputs() []V2ArtifactInput {
	inputs := []V2ArtifactInput{{
		LogicalID:      "skills/alpha",
		Kind:           "skill",
		CapabilityID:   "alpha",
		RelativePath:   "skills/cortex-alpha/SKILL.md",
		SHA256:         hash,
		InstallationID: installationID,
	}}
	for _, role := range v2Roles {
		inputs = append(inputs, V2ArtifactInput{
			LogicalID:            "actors/" + string(role),
			Kind:                 KindPiActor,
			RoleID:               role,
			ActorContractVersion: "cortex.qa.pi-actor.v1",
			RelativePath:         "agents/cortex-" + string(role) + ".md",
			SHA256:               fingerprint,
			InstallationID:       installationID,
		})
	}
	return inputs
}
