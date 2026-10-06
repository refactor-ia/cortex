package installobserve_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/refactor-ia/cortex/internal/catalog"
	"github.com/refactor-ia/cortex/internal/installobserve"
	"github.com/refactor-ia/cortex/internal/installplan"
	"github.com/refactor-ia/cortex/internal/qapi"
	"github.com/refactor-ia/cortex/internal/qarole"
	"github.com/refactor-ia/cortex/internal/qaroute"
)

func TestActorGuidanceAdmission(t *testing.T) {
	for _, name := range []string{
		"composed v3", "unmarked v3", "v2 regression", "v2 guidance rejected",
		"prefix tamper", "suffix tamper", "registered stale prefix", "registered prefix whitespace", "forged canonical ledger", "stale restored actor",
		"malformed block", "duplicate block", "nested block", "tool marker", "outside suffix drift",
		"wrong state", "noncanonical state", "wrong schema", "wrong catalog", "wrong id", "wrong runtime", "wrong root kind",
		"wrong owner", "duplicate artifact", "wrong actor contract", "wrong actor path", "wrong skill path", "wrong skill capability", "missing canonical identity",
		"actor mode", "state mode", "skill mode", "skill drift", "skill ledger drift", "shadow conflict", "unsafe root", "unsafe cwd", "oversized actor",
	} {
		t.Run(name, func(t *testing.T) {
			f := newGuidanceAdmissionFixture(t, name != "unmarked v3")
			actor, state := bytes.Clone(f.effective), bytes.Clone(f.state)
			changeState := func(old, replacement string) { state = bytes.Replace(state, []byte(old), []byte(replacement), 1) }
			modePath := ""
			switch name {
			case "v2 regression", "v2 guidance rejected":
				state = f.candidate.StateJSON()
				if name == "v2 regression" {
					actor = f.canonical
				}
			case "prefix tamper", "registered stale prefix":
				actor = bytes.Replace(actor, []byte("name: cortex-"), []byte("name: altered-"), 1)
				if bytes.Equal(actor, f.effective) {
					t.Fatal("fixture lacks expected actor header")
				}
				if name == "registered stale prefix" {
					changeState(admissionHash(f.effective), admissionHash(actor))
				}
			case "registered prefix whitespace":
				actor = bytes.Replace(actor, bridgeSuffix, append([]byte(" "), bridgeSuffix...), 1)
				changeState(admissionHash(f.effective), admissionHash(actor))
			case "suffix tamper":
				actor = bytes.Replace(actor, []byte("external guidance"), []byte("tampered guidance"), 1)
			case "forged canonical ledger":
				changeState(f.expected.ActorSHA256, strings.Repeat("a", 64))
			case "stale restored actor":
				actor = f.canonical
			case "malformed block", "duplicate block", "nested block", "tool marker", "outside suffix drift":
				switch name {
				case "malformed block":
					actor = bytes.Replace(actor, []byte("<!-- /gentle-ai:pi-codegraph -->"), []byte("<!-- /gentle-ai:pi-codegraph --"), 1)
				case "duplicate block":
					actor = append(actor, bridgeSuffix...)
				case "nested block":
					actor = bytes.Replace(actor, []byte("external guidance"), []byte("<!-- gentle-ai:pi-codegraph-guidance -->\nexternal guidance"), 1)
				case "tool marker":
					actor = bytes.Replace(actor, []byte("external guidance"), []byte("<!-- gentle-ai:pi-codegraph-tool -->"), 1)
				case "outside suffix drift":
					actor = append(actor, []byte("outside\n")...)
				}
				// Even a matching full-file ledger hash cannot bless invalid structure.
				changeState(admissionHash(f.effective), admissionHash(actor))
			case "wrong state":
				state = []byte("{}")
			case "noncanonical state":
				state = append(state, '\n')
			case "wrong schema":
				changeState(`"schemaVersion":3`, `"schemaVersion":4`)
			case "wrong catalog":
				changeState(f.expected.CatalogFingerprint, strings.Repeat("a", 64))
			case "wrong id":
				changeState(f.installationID, "f0e1d2c3b4a5968778695a4b3c2d1e0f")
			case "wrong runtime":
				changeState(`"runtime":"pi"`, `"runtime":"opencode"`)
			case "wrong root kind":
				changeState(`"rootKind":"pi-user-agent"`, `"rootKind":"opencode-user-config"`)
			case "wrong owner":
				changeState(`"owner":"cortex"`, `"owner":"other"`)
			case "duplicate artifact":
				changeState(`"actors/test-designer":`, `"actors/requirements-analyst":`)
			case "wrong actor contract":
				changeState("cortex.qa.pi-actor.v1", "cortex.qa.pi-actor.v2")
			case "wrong actor path":
				changeState("agents/cortex-test-designer.md", "agents/other.md")
			case "wrong skill path":
				changeState("skills/cortex-test-designer/SKILL.md", "skills/other/SKILL.md")
			case "wrong skill capability":
				changeState(`"capabilityId":"test-designer"`, `"capabilityId":"other"`)
			case "missing canonical identity":
				changeState(`"canonicalSha256":"`+f.expected.ActorSHA256+`",`, "")
			case "actor mode":
				modePath = f.actorPath
			case "state mode":
				modePath = f.statePath
			case "skill mode":
				modePath = f.skillPath
			case "skill drift":
				writeAdmissionFile(t, f.skillPath, []byte("drift"), 0o600)
			case "skill ledger drift":
				changeState(f.expected.SkillSHA256, admissionHash([]byte("drift")))
				writeAdmissionFile(t, f.skillPath, []byte("drift"), 0o600)
			case "shadow conflict":
				writeAdmissionFile(t, filepath.Join(f.cwd, ".pi", "agents", "other.md"), []byte("---\nname: cortex-test-designer\n---\n"), 0o600)
			case "unsafe root":
				f.root = admissionSymlink(t, f.root)
			case "unsafe cwd":
				f.cwd = admissionSymlink(t, f.cwd)
			case "oversized actor":
				actor = bytes.Repeat([]byte("x"), int(installobserve.DefaultMaxFileBytes)+1)
				changeState(admissionHash(f.effective), admissionHash(actor))
			}
			if name != "composed v3" && name != "unmarked v3" && name != "v2 regression" && modePath == "" &&
				bytes.Equal(state, f.state) && bytes.Equal(actor, f.effective) && strings.HasPrefix(name, "wrong ") {
				t.Fatal("state mutation did not exercise fixture")
			}
			writeAdmissionFile(t, f.actorPath, actor, 0o600)
			writeAdmissionFile(t, f.statePath, state, 0o600)
			if modePath != "" {
				if err := os.Chmod(modePath, 0o640); err != nil {
					t.Fatal(err)
				}
			}
			assets, err := installobserve.ObserveAdmissionAssets(f.root, f.cwd, f.expected)
			valid := name == "composed v3" || name == "unmarked v3" || name == "v2 regression"
			if (err == nil) != valid {
				t.Fatalf("admission error = %v, want valid=%t", err, valid)
			}
			if !valid {
				if assets != (installobserve.AdmissionAssets{}) {
					t.Fatal("failed admission leaked assets")
				}
				return
			}
			if assets.ActorSHA256() != admissionHash(actor) || assets.ActorPath() != f.actorPath || assets.ActorProvenance() != installobserve.ActorProvenanceInstalled ||
				assets.ActorSourceSHA256() != f.expected.ActorSourceSHA256 || assets.ActorBindingSHA256() != f.expected.ActorBindingSHA256 ||
				assets.SkillSHA256() != f.expected.SkillSHA256 || admissionHash([]byte(assets.SkillText())) != assets.SkillSHA256() ||
				string(assets.InstallationID()) != f.installationID || assets.CatalogFingerprint() != f.expected.CatalogFingerprint {
				t.Fatal("admission facts do not describe observed effective assets")
			}
			if name == "composed v3" && assets.ActorSHA256() == f.expected.ActorSHA256 {
				t.Fatal("composed effective identity was mislabeled as canonical")
			}
			// Pure qapi contracts must carry the effective hash and actual actor path.
			route, failure := qaroute.Resolve(qaroute.Request{Role: f.expected.Role, Backend: "pi"}, qaroute.Snapshot{})
			if failure.Code != "" {
				t.Fatal(failure.Code)
			}
			frame, err := qapi.EncodeReportInput(route, assets.ActorSHA256(), assets.SkillSHA256(), []byte("inspect fixture"))
			if err != nil || !bytes.Contains(frame, []byte("actor_sha256 "+admissionHash(actor))) {
				t.Fatalf("effective frame identity: %v", err)
			}
			paths, err := qapi.BindInvocationPaths(f.expected.Role, filepath.Join(f.root, "fixture-pi"), assets.ActorPath(), assets.SkillPath(), f.cwd)
			if err != nil {
				t.Fatal(err)
			}
			invocation, err := qapi.BuildInvocation(route, paths)
			if err != nil || invocation.Argv()[5] != assets.ActorPath() {
				t.Fatalf("effective invocation path: %v", err)
			}
		})
	}
}

type guidanceAdmissionFixture struct {
	admissionFixture
	candidate                   installplan.Plan
	canonical, effective, state []byte
}

func newGuidanceAdmissionFixture(t *testing.T, marked bool) guidanceAdmissionFixture {
	t.Helper()
	candidate := makeActorAwareCandidate(t)
	var observations []installplan.ActorGuidanceObservation
	var canonical, effective []byte
	for _, file := range candidate.Files() {
		if file.Role() != "actor" {
			continue
		}
		content := file.Content()
		if marked {
			content = append(bytes.TrimRight(content, "\n"), bridgeSuffix...)
		}
		observations = append(observations, installplan.ActorGuidanceObservation{LogicalID: file.LogicalID(), RootPath: candidate.RootPath(), RelativePath: file.RelativePath(), AbsolutePath: file.AbsolutePath(), Mode: file.DesiredMode(), Content: content})
		if file.LogicalID() == "actors/test-designer" {
			canonical, effective = file.Content(), content
		}
	}
	composed, _, err := installplan.BuildActorGuidance(candidate, installplan.ActorGuidancePrior{RootPath: candidate.RootPath(), State: candidate.InstalledState()}, observations, marked)
	if err != nil {
		t.Fatal(err)
	}
	writeCandidateFiles(t, composed)
	snapshot, err := catalog.BuildCatalogSnapshot(filepath.Join("..", "..", "catalog"), "catalog.json", catalog.AdmissionPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	expected, err := qapi.CatalogAdmissionBinding(snapshot, qarole.TestDesigner, "pi")
	if err != nil || expected.ActorSHA256 != admissionHash(canonical) {
		t.Fatalf("independent catalog binding: %v", err)
	}
	root := candidate.RootPath()
	return guidanceAdmissionFixture{admissionFixture: admissionFixture{
		root: root, cwd: admissionTempDir(t), statePath: filepath.Join(root, ".cortex", "install-state.json"),
		actorPath: filepath.Join(root, "agents", "cortex-test-designer.md"), skillPath: filepath.Join(root, "skills", "cortex-test-designer", "SKILL.md"),
		installationID: string(candidate.InstalledState().InstallationID()), expected: expected,
	}, candidate: candidate, canonical: canonical, effective: effective, state: composed.StateJSON()}
}
