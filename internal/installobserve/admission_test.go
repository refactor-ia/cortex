package installobserve_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/refactor-ia/cortex/internal/installobserve"
	"github.com/refactor-ia/cortex/internal/installstate"
	"github.com/refactor-ia/cortex/internal/qarole"
	"github.com/refactor-ia/cortex/internal/runtimematrix"
	"github.com/refactor-ia/cortex/internal/skilldest"
)

func TestAdmissionAssets(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, fixture admissionFixture)
		valid bool
	}{
		{name: "canonical selected assets bind", valid: true},
		{name: "missing selected skill fails closed", setup: func(t *testing.T, fixture admissionFixture) {
			if err := os.Remove(fixture.skillPath); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "missing canonical state fails closed", setup: func(t *testing.T, fixture admissionFixture) {
			if err := os.Remove(fixture.statePath); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "malformed canonical state fails closed", setup: func(t *testing.T, fixture admissionFixture) {
			if err := os.WriteFile(fixture.statePath, []byte("not state"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "actor digest drift fails closed", setup: func(t *testing.T, fixture admissionFixture) {
			if err := os.WriteFile(fixture.actorPath, []byte("drift"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "skill mode drift fails closed", setup: func(t *testing.T, fixture admissionFixture) {
			if err := os.Chmod(fixture.skillPath, 0o640); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "actor symlink fails closed", setup: func(t *testing.T, fixture admissionFixture) {
			if err := os.Remove(fixture.actorPath); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("elsewhere", fixture.actorPath); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "state cross installation ID fails closed", setup: func(t *testing.T, fixture admissionFixture) {
			state, err := os.ReadFile(fixture.statePath)
			if err != nil {
				t.Fatal(err)
			}
			state = []byte(strings.Replace(string(state), fixture.installationID, "f0e1d2c3b4a5968778695a4b3c2d1e0f", 1))
			if err := os.WriteFile(fixture.statePath, state, 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "project shadow fails closed", setup: func(t *testing.T, fixture admissionFixture) {
			writeAdmissionFile(t, filepath.Join(fixture.cwd, ".pi", "agents", "other.md"), []byte("---\nname: cortex-test-designer\n---\n"), 0o600)
		}},
		{name: "project symlink target shadow fails closed", setup: func(t *testing.T, fixture admissionFixture) {
			path := filepath.Join(fixture.cwd, ".pi", "agents", "cortex-test-designer.md")
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(fixture.actorPath, path); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "duplicate actor name fails closed", setup: func(t *testing.T, fixture admissionFixture) {
			writeAdmissionFile(t, filepath.Join(fixture.root, "agents", "duplicate.md"), []byte("---\nname: cortex-test-designer\n---\n"), 0o600)
		}},
		{name: "unrelated foreign content is ignored", valid: true, setup: func(t *testing.T, fixture admissionFixture) {
			writeAdmissionFile(t, filepath.Join(fixture.root, "agents", "other.md"), []byte("not an actor"), 0o600)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newAdmissionFixture(t)
			if tc.setup != nil {
				tc.setup(t, fixture)
			}
			assets, err := installobserve.ObserveAdmissionAssets(fixture.root, fixture.cwd, fixture.expected)
			if (err == nil) != tc.valid {
				t.Fatalf("ObserveAdmissionAssets() error = %v, want valid=%t", err, tc.valid)
			}
			if !tc.valid {
				return
			}
			if assets.InstallationID() != installstate.InstallationID(fixture.installationID) || assets.RoleID() != qarole.TestDesigner || assets.Backend() != "pi" || assets.CatalogFingerprint() != fixture.expected.CatalogFingerprint || assets.ActorSHA256() != fixture.expected.ActorSHA256 || assets.SkillSHA256() != fixture.expected.SkillSHA256 || assets.ActorPath() != fixture.actorPath || assets.SkillPath() != fixture.skillPath {
				t.Fatalf("ObserveAdmissionAssets() = %#v", assets)
			}
		})
	}
}

func TestAdmissionAssetsExposeOnlyImmutableTypedFacts(t *testing.T) {
	typeOf := reflect.TypeOf(installobserve.AdmissionAssets{})
	for index := 0; index < typeOf.NumField(); index++ {
		if typeOf.Field(index).IsExported() {
			t.Fatalf("AdmissionAssets exposes %q", typeOf.Field(index).Name)
		}
	}
	for _, name := range []string{"Bytes", "Content", "RootPath", "State", "SetActorPath"} {
		if _, found := typeOf.MethodByName(name); found {
			t.Fatalf("AdmissionAssets exposes %s", name)
		}
	}
}

func TestAdmissionAssetsRejectInvalidExplicitBindingAndRoots(t *testing.T) {
	fixture := newAdmissionFixture(t)
	for _, tc := range []struct {
		name      string
		root, cwd string
		expected  installobserve.AdmissionBinding
	}{
		{name: "alternate backend", root: fixture.root, cwd: fixture.cwd, expected: installobserve.AdmissionBinding{Role: qarole.TestDesigner, Backend: "other", CatalogFingerprint: fixture.expected.CatalogFingerprint, ActorSHA256: fixture.expected.ActorSHA256, SkillSHA256: fixture.expected.SkillSHA256}},
		{name: "wrong expected actor binding", root: fixture.root, cwd: fixture.cwd, expected: installobserve.AdmissionBinding{Role: qarole.TestDesigner, Backend: "pi", CatalogFingerprint: fixture.expected.CatalogFingerprint, ActorSHA256: strings.Repeat("a", 64), SkillSHA256: fixture.expected.SkillSHA256}},
		{name: "symlink root", root: admissionSymlink(t, fixture.root), cwd: fixture.cwd, expected: fixture.expected},
		{name: "symlink cwd", root: fixture.root, cwd: admissionSymlink(t, fixture.cwd), expected: fixture.expected},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := installobserve.ObserveAdmissionAssets(tc.root, tc.cwd, tc.expected); err == nil {
				t.Fatal("ObserveAdmissionAssets() succeeded")
			}
		})
	}
}

type admissionFixture struct {
	root, cwd, statePath, actorPath, skillPath, installationID string
	expected                                                   installobserve.AdmissionBinding
}

func newAdmissionFixture(t *testing.T) admissionFixture {
	t.Helper()
	root, cwd := admissionTempDir(t), admissionTempDir(t)
	actor, skill := []byte("actor bytes"), []byte("skill bytes")
	installationID := "000102030405060708090a0b0c0d0e0f"
	catalog := strings.Repeat("c", 64)
	inputs := []installstate.V2ArtifactInput{{LogicalID: "skills/test-designer", Kind: installstate.KindSkill, CapabilityID: "test-designer", RelativePath: "skills/cortex-test-designer/SKILL.md", SHA256: admissionHash(skill), InstallationID: installstate.InstallationID(installationID)}}
	for _, role := range qarole.Catalog() {
		content := []byte("unselected actor " + string(role.ID))
		if role.ID == qarole.TestDesigner {
			content = actor
		}
		inputs = append(inputs, installstate.V2ArtifactInput{LogicalID: "actors/" + string(role.ID), Kind: installstate.KindPiActor, RoleID: role.ID, ActorContractVersion: "cortex.qa.pi-actor.v1", RelativePath: "agents/cortex-" + string(role.ID) + ".md", SHA256: admissionHash(content), InstallationID: installstate.InstallationID(installationID)})
	}
	state, err := installstate.NewV2(runtimematrix.RuntimePi, skilldest.RootKindPiUserAgent, catalog, installstate.InstallationID(installationID), inputs)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := installstate.Encode(state)
	if err != nil {
		t.Fatal(err)
	}
	fixture := admissionFixture{
		root: root, cwd: cwd, statePath: filepath.Join(root, ".cortex", "install-state.json"),
		actorPath: filepath.Join(root, "agents", "cortex-test-designer.md"), skillPath: filepath.Join(root, "skills", "cortex-test-designer", "SKILL.md"), installationID: installationID,
		expected: installobserve.AdmissionBinding{Role: qarole.TestDesigner, Backend: "pi", CatalogFingerprint: catalog, ActorSHA256: admissionHash(actor), SkillSHA256: admissionHash(skill)},
	}
	writeAdmissionFile(t, fixture.statePath, encoded, 0o600)
	writeAdmissionFile(t, fixture.actorPath, actor, 0o600)
	writeAdmissionFile(t, fixture.skillPath, skill, 0o600)
	return fixture
}

func writeAdmissionFile(t *testing.T, path string, contents []byte, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, contents, mode); err != nil {
		t.Fatal(err)
	}
}

func admissionTempDir(t *testing.T) string {
	t.Helper()
	path, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func admissionSymlink(t *testing.T, target string) string {
	t.Helper()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	return link
}

func admissionHash(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}
