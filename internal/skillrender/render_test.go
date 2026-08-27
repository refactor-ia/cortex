package skillrender

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/refactor-ia/cortex/internal/catalog"
)

func TestRenderCanonicalSourceAndIsolation(t *testing.T) {
	snapshot := renderSnapshot(t, "---\nbody\té")
	set, err := Render(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	alpha := `---
name: "cortex-alpha"
description: "Third \"party\" Résumé"
license: "Apache \"Two\" é"
metadata:
  cortex-family: "services"
  cortex-activation: "dormant"
  cortex-provenance: "third-party"
---
---
body	é`
	zeta := `---
name: "cortex-zeta"
description: "Owned description"
license: "CC-BY-SA-4.0"
metadata:
  cortex-family: "reasoning"
  cortex-activation: "automatic"
  cortex-provenance: "cortex-owned"
---
zeta
`
	skills := set.Skills()
	if set.SnapshotFingerprint() != snapshot.Fingerprint() || len(skills) != 2 || skills[0].CapabilityID() != "alpha" || skills[0].LogicalID() != "skills/alpha" || skills[0].Activation() != catalog.ActivationDormant || skills[1].Activation() != catalog.ActivationAutomatic || string(skills[0].Content()) != alpha || string(skills[1].Content()) != zeta {
		t.Fatalf("unexpected rendered set: %+v", skills)
	}
	for _, skill := range skills {
		if skill.SHA256() != fmt.Sprintf("%x", sha256.Sum256(skill.Content())) || strings.Contains(string(skill.Content()), "\nactivation:") {
			t.Fatal("rendered content is not canonical")
		}
	}
	if strings.Contains(string(skills[0].Content()), "rejected-source") {
		t.Fatal("rejected source was rendered")
	}
	again, err := Render(snapshot)
	if err != nil || !reflect.DeepEqual(set, again) || set.Skills() == nil {
		t.Fatal("rendering was not deterministic")
	}
	copySkills, content := set.Skills(), skills[0].Content()
	copySkills[0] = RenderedSkill{}
	content[0] = 'x'
	if string(set.Skills()[0].Content()) != alpha {
		t.Fatal("accessor mutation changed result")
	}
	source := snapshot.Families()[0].Capabilities()[0].Source().Content()
	source[0] = 'x'
	if string(set.Skills()[1].Content()) != zeta {
		t.Fatal("input mutation changed result")
	}
}

func TestRenderNamesAtRuntimeBoundary(t *testing.T) {
	for _, tc := range []struct {
		name    string
		id      string
		nameLen int
		hash    string
	}{
		{"one-character capability", "a", 8, "a30d4497ac1659565d7581e0bfaa4d65949ed56533268335e5260a4955e083cd"},
		{"maximum-length capability", strings.Repeat("a", 57), 64, "e0df21e3f4fe508913c48f0c79b06e3ae3b0488f1d18f2358fe8b58f0fea1197"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			set, err := Render(renderSnapshot(t, "body\n", tc.id))
			if err != nil {
				t.Fatal(err)
			}
			skill := set.Skills()[0]
			wantContent := strings.Join([]string{
				"---",
				`name: "cortex-` + tc.id + `"`,
				`description: "Third \"party\" Résumé"`,
				`license: "Apache \"Two\" é"`,
				"metadata:",
				`  cortex-family: "services"`,
				`  cortex-activation: "dormant"`,
				`  cortex-provenance: "third-party"`,
				"---",
				"body",
				"",
			}, "\n")
			if got := string(skill.Content()); got != wantContent {
				t.Fatalf("Content() = %q, want %q", got, wantContent)
			}
			if skill.CapabilityID() != tc.id || skill.LogicalID() != "skills/"+tc.id || strings.HasPrefix(skill.LogicalID(), "skills/cortex-") {
				t.Fatalf("identifiers = (%q, %q), want unprefixed capability and logical IDs", skill.CapabilityID(), skill.LogicalID())
			}
			if got := len("cortex-" + tc.id); got != tc.nameLen {
				t.Fatalf("rendered name length = %d", got)
			}
			if got := skill.SHA256(); got != tc.hash {
				t.Fatalf("SHA256() = %q, want %q", got, tc.hash)
			}
		})
	}
}

func TestRenderRejectsInvalidInputWithoutLeaks(t *testing.T) {
	for _, source := range [][]byte{nil, {0xff}} {
		t.Run(fmt.Sprintf("source-%x", source), func(t *testing.T) {
			set, err := Render(renderSnapshot(t, string(source)))
			if err == nil || !reflect.DeepEqual(set, Set{}) || err.Error() != "skill render: invalid input" {
				t.Fatalf("result = (%+v, %v)", set, err)
			}
		})
	}
	set, err := Render(catalog.CatalogSnapshot{})
	if err == nil || !reflect.DeepEqual(set, Set{}) || err.Error() != "skill render: invalid input" {
		t.Fatalf("zero snapshot = (%+v, %v)", set, err)
	}
}

func renderSnapshot(t *testing.T, alphaSource string, alphaIDs ...string) catalog.CatalogSnapshot {
	t.Helper()
	alphaID := "alpha"
	if len(alphaIDs) == 1 {
		alphaID = alphaIDs[0]
	}
	root, paths := t.TempDir(), map[string]string{}
	for _, family := range catalog.ApprovedFamilyIDs() {
		paths[family] = "families/" + family + ".json"
		capabilities := []string{}
		switch family {
		case "reasoning":
			capabilities = []string{"manifests/zeta.json"}
		case "services":
			capabilities = []string{"manifests/" + alphaID + ".json"}
		case "web":
			capabilities = []string{"manifests/rejected.json"}
		}
		writeRenderFile(t, root, paths[family], fmt.Sprintf(`{"schemaVersion":1,"id":%q,"router":%q,"capabilities":%s,"agents":[]}`, family, "routers/"+family+".md", jsonList(capabilities)))
		writeRenderFile(t, root, "routers/"+family+".md", family)
	}
	data, err := json.Marshal(map[string]any{"schemaVersion": 1, "families": paths})
	if err != nil {
		t.Fatal(err)
	}
	writeRenderFile(t, root, "catalog.json", string(data))
	manifest := func(id, family, description, license, provenance string) catalog.CapabilityManifest {
		value := catalog.CapabilityManifest{SchemaVersion: 1, ID: id, Description: description, Family: family, Source: "sources/" + id + ".md", Activation: catalog.ActivationAutomatic, Provenance: provenance, License: license, RedistributionAllowed: true}
		if provenance == catalog.ProvenanceThirdParty {
			value.Activation, value.ProvenanceURL, value.RedistributionURL = catalog.ActivationDormant, "https://example.com/provenance", "https://example.com/redistribution"
		}
		return value
	}
	for _, item := range []struct {
		manifest catalog.CapabilityManifest
		source   string
	}{
		{manifest("zeta", "reasoning", "Owned description", "CC-BY-SA-4.0", catalog.ProvenanceCortexOwned), "zeta\n"},
		{manifest(alphaID, "services", "Third \"party\" Résumé", "Apache \"Two\" é", catalog.ProvenanceThirdParty), alphaSource},
		{manifest("rejected", "web", "Rejected description", "GPL-3.0-only", catalog.ProvenanceThirdParty), "rejected-source"},
	} {
		data, err := json.Marshal(item.manifest)
		if err != nil {
			t.Fatal(err)
		}
		writeRenderFile(t, root, "manifests/"+item.manifest.ID+".json", string(data))
		writeRenderFile(t, root, item.manifest.Source, item.source)
	}
	policy := catalog.AdmissionPolicy{CompatibleThirdPartyLicenses: []string{"Apache \"Two\" é"}}
	snapshot, err := catalog.BuildCatalogSnapshot(root, "catalog.json", policy)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func jsonList(values []string) string { data, _ := json.Marshal(values); return string(data) }
func writeRenderFile(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
