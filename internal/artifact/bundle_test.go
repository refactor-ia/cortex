package artifact

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/refactor-ia/cortex/internal/adapterplan"
	"github.com/refactor-ia/cortex/internal/projection"
	"github.com/refactor-ia/cortex/internal/runtimematrix"
)

func bundleHash(content []byte) string { return stringHash(sha256.Sum256(content)) }

func stringHash(sum [sha256.Size]byte) string {
	const hex = "0123456789abcdef"
	output := make([]byte, sha256.Size*2)
	for i, value := range sum {
		output[i*2], output[i*2+1] = hex[value>>4], hex[value&15]
	}
	return string(output)
}

func bundleManifest(t *testing.T, runtime runtimematrix.RuntimeID, result projection.Result, disclosure string, contents map[string][]byte) Manifest {
	t.Helper()
	hashes := make(map[string]string, len(contents))
	for id, content := range contents {
		hashes[id] = bundleHash(content)
	}
	artifacts, err := json.Marshal(hashes)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := DecodeManifest([]byte(manifestJSON(string(runtime), string(result), disclosure, string(artifacts))))
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}

func bundlePlan(t *testing.T, runtime runtimematrix.RuntimeID, result projection.Result, disclosure string) projection.Plan {
	t.Helper()
	observations := []runtimematrix.Observation{
		{ID: runtimematrix.RuntimePi, Present: runtime == runtimematrix.RuntimePi, Version: versionFor(runtime == runtimematrix.RuntimePi), Compatibility: compatibilityFor(runtime == runtimematrix.RuntimePi)},
		{ID: runtimematrix.RuntimeOpenCode, Present: runtime == runtimematrix.RuntimeOpenCode, Version: versionFor(runtime == runtimematrix.RuntimeOpenCode), Compatibility: compatibilityFor(runtime == runtimematrix.RuntimeOpenCode)},
		{ID: runtimematrix.RuntimeClaudeCode, Present: runtime == runtimematrix.RuntimeClaudeCode, Version: versionFor(runtime == runtimematrix.RuntimeClaudeCode), Compatibility: compatibilityFor(runtime == runtimematrix.RuntimeClaudeCode)},
	}
	base, err := adapterplan.Build(fingerprint, observations)
	if err != nil {
		t.Fatal(err)
	}
	assessment, err := projection.NewAssessment(runtime, fingerprint, result, disclosure)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := projection.BuildPlan(base, []projection.Assessment{assessment})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func versionFor(present bool) string {
	if present {
		return "1"
	}
	return ""
}

func compatibilityFor(present bool) runtimematrix.Compatibility {
	if present {
		return runtimematrix.Compatible
	}
	return runtimematrix.CompatibilityUnknown
}

func zeroBundle(bundle Bundle) bool {
	return bundle.Manifest().SchemaVersion() == 0 && len(bundle.Artifacts()) == 0
}

func TestBindOrdersAndPreservesProjection(t *testing.T) {
	contents := map[string][]byte{"families/reasoning/router": []byte("router"), "capabilities/tear": []byte("tear")}
	for _, tc := range []struct {
		name, disclosure string
		result           projection.Result
	}{
		{"exact", "", projection.Exact},
		{"translated", "Equivalent runtime representation", projection.Translated},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manifest := bundleManifest(t, runtimematrix.RuntimePi, tc.result, tc.disclosure, contents)
			bundle, err := Bind(manifest, bundlePlan(t, runtimematrix.RuntimePi, tc.result, tc.disclosure), []PayloadInput{{LogicalID: "families/reasoning/router", Content: contents["families/reasoning/router"]}, {LogicalID: "capabilities/tear", Content: contents["capabilities/tear"]}})
			if err != nil {
				t.Fatal(err)
			}
			artifacts := bundle.Artifacts()
			if len(artifacts) != 2 || artifacts[0].LogicalID() != "capabilities/tear" || artifacts[1].LogicalID() != "families/reasoning/router" || !bytes.Equal(artifacts[0].Content(), contents[artifacts[0].LogicalID()]) || bundle.Manifest().TranslationDisclosure() != tc.disclosure {
				t.Fatalf("Bind() = %#v", bundle)
			}
		})
	}
}

// TestBindAcceptsOnlyAdmittedUncertifiedTargets pins the provenance fence: a
// runtime admitted by a plan can bind, and a runtime the plan never admitted
// cannot.
func TestBindAcceptsOnlyAdmittedUncertifiedTargets(t *testing.T) {
	contents := map[string][]byte{"families/router": []byte("local")}
	manifest := bundleManifest(t, runtimematrix.RuntimePi, projection.Exact, "", contents)
	observations := []runtimematrix.Observation{
		{ID: runtimematrix.RuntimePi, Present: true, Version: "9.9.9", Compatibility: runtimematrix.CompatibilityUnknown},
		{ID: runtimematrix.RuntimeOpenCode, Present: false, Compatibility: runtimematrix.CompatibilityUnknown},
		{ID: runtimematrix.RuntimeClaudeCode, Present: false, Compatibility: runtimematrix.CompatibilityUnknown},
	}
	assessment, err := projection.NewAssessment(runtimematrix.RuntimePi, fingerprint, projection.Exact, "")
	if err != nil {
		t.Fatal(err)
	}
	localBase, err := adapterplan.BuildLocalUpdate(fingerprint, observations, runtimematrix.RuntimePi)
	if err != nil {
		t.Fatal(err)
	}
	local, err := projection.BuildPlan(localBase, []projection.Assessment{assessment})
	if err != nil {
		t.Fatal(err)
	}
	if bundle, err := Bind(manifest, local, []PayloadInput{{LogicalID: "families/router", Content: contents["families/router"]}}); err != nil || zeroBundle(bundle) {
		t.Fatalf("local Bind() = (%#v, %v)", bundle, err)
	}
	// A present runtime whose version could not be identified is never admitted,
	// so no plan carries provenance for it and Bind must refuse.
	unidentifiedBase, err := adapterplan.Build(fingerprint, []runtimematrix.Observation{
		{ID: runtimematrix.RuntimePi, Present: true, Compatibility: runtimematrix.CompatibilityUnknown},
		{ID: runtimematrix.RuntimeOpenCode, Present: false, Compatibility: runtimematrix.CompatibilityUnknown},
		{ID: runtimematrix.RuntimeClaudeCode, Present: false, Compatibility: runtimematrix.CompatibilityUnknown},
	})
	if err != nil {
		t.Fatal(err)
	}
	unidentified, err := projection.BuildPlan(unidentifiedBase, nil)
	if err != nil {
		t.Fatal(err)
	}
	if bundle, err := Bind(manifest, unidentified, []PayloadInput{{LogicalID: "families/router", Content: contents["families/router"]}}); err == nil || !zeroBundle(bundle) {
		t.Fatalf("unidentified Bind() = (%#v, %v)", bundle, err)
	}
}

func TestBindRejectsInvalidBindingsWithoutLeaks(t *testing.T) {
	contents := map[string][]byte{"families/router": []byte("safe")}
	manifest := bundleManifest(t, runtimematrix.RuntimePi, projection.Exact, "", contents)
	plan := bundlePlan(t, runtimematrix.RuntimePi, projection.Exact, "")
	unrepresentable := bundlePlan(t, runtimematrix.RuntimePi, projection.Unrepresentable, "")
	mismatchedFingerprint := manifest
	mismatchedFingerprint.snapshotFingerprint = strings.Repeat("c", 64)
	invalidManifest := manifest
	invalidManifest.owner = "other"
	translatedManifest := bundleManifest(t, runtimematrix.RuntimePi, projection.Translated, "disclosed", contents)
	for _, tc := range []struct {
		name     string
		manifest Manifest
		plan     projection.Plan
		payloads []PayloadInput
	}{
		{"zero manifest", Manifest{}, plan, nil},
		{"zero plan", manifest, projection.Plan{}, nil},
		{"invalid manifest metadata", invalidManifest, plan, []PayloadInput{{LogicalID: "families/router", Content: []byte("safe")}}},
		{"fingerprint mismatch", mismatchedFingerprint, plan, []PayloadInput{{LogicalID: "families/router", Content: []byte("safe")}}},
		{"runtime mismatch", bundleManifest(t, runtimematrix.RuntimeOpenCode, projection.Exact, "", contents), plan, []PayloadInput{{LogicalID: "families/router", Content: []byte("safe")}}},
		{"result mismatch", manifest, bundlePlan(t, runtimematrix.RuntimePi, projection.Translated, "disclosed"), []PayloadInput{{LogicalID: "families/router", Content: []byte("safe")}}},
		{"disclosure mismatch", translatedManifest, bundlePlan(t, runtimematrix.RuntimePi, projection.Translated, "other"), []PayloadInput{{LogicalID: "families/router", Content: []byte("safe")}}},
		{"unrepresentable non-target", manifest, unrepresentable, []PayloadInput{{LogicalID: "families/router", Content: []byte("safe")}}},
		{"missing", manifest, plan, nil},
		{"extra", manifest, plan, []PayloadInput{{LogicalID: "families/router", Content: []byte("safe")}, {LogicalID: "families/extra", Content: []byte("extra")}}},
		{"duplicate", manifest, plan, []PayloadInput{{LogicalID: "families/router", Content: []byte("safe")}, {LogicalID: "families/router", Content: []byte("safe")}}},
		{"invalid logical ID", manifest, plan, []PayloadInput{{LogicalID: "families_router", Content: []byte("safe")}}},
		{"wrong hash", manifest, plan, []PayloadInput{{LogicalID: "families/router", Content: []byte("other")}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Bind(tc.manifest, tc.plan, tc.payloads)
			if err == nil || !zeroBundle(got) || err.Error() != "artifact bundle: invalid input" || strings.Contains(err.Error(), "safe") || strings.Contains(err.Error(), "disclosed") {
				t.Fatalf("Bind() = (%#v, %v)", got, err)
			}
		})
	}
}

func TestBindCopiesContentAndInputs(t *testing.T) {
	contents := map[string][]byte{"families/router": []byte("safe")}
	manifest := bundleManifest(t, runtimematrix.RuntimePi, projection.Exact, "", contents)
	plan := bundlePlan(t, runtimematrix.RuntimePi, projection.Exact, "")
	payloads := []PayloadInput{{LogicalID: "families/router", Content: []byte("safe")}}
	originalManifest, originalPlan, originalPayloads := manifest, plan, append([]PayloadInput(nil), payloads...)
	originalPayloads[0].Content = append([]byte(nil), payloads[0].Content...)
	bundle, err := Bind(manifest, plan, payloads)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(payloads, originalPayloads) {
		t.Fatal("Bind() changed payload inputs")
	}
	payloads[0].LogicalID, payloads[0].Content[0] = "families/changed", 'x'
	artifacts := bundle.Artifacts()
	artifacts[0] = BundledArtifact{}
	content := bundle.Artifacts()[0].Content()
	content[0] = 'x'
	returnedManifest := bundle.Manifest()
	returnedManifest.owner = "changed"
	if got := bundle.Artifacts()[0]; got.LogicalID() != "families/router" || got.SHA256() != bundleHash([]byte("safe")) || string(got.Content()) != "safe" || returnedManifest.Owner() != "changed" || bundle.Manifest().Owner() != "cortex" || !reflect.DeepEqual(manifest, originalManifest) || !reflect.DeepEqual(plan, originalPlan) || !reflect.DeepEqual(payloads[0].Content, []byte("xafe")) || !reflect.DeepEqual(originalPayloads[0].Content, []byte("safe")) {
		t.Fatalf("Bind() exposed or changed input state")
	}
}

func TestBindAcceptsEmptyPayloadAndIsDeterministic(t *testing.T) {
	contents := map[string][]byte{"families/empty": {}}
	manifest := bundleManifest(t, runtimematrix.RuntimePi, projection.Exact, "", contents)
	plan := bundlePlan(t, runtimematrix.RuntimePi, projection.Exact, "")
	payloads := []PayloadInput{{LogicalID: "families/empty", Content: []byte{}}}
	first, err := Bind(manifest, plan, payloads)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Bind(manifest, plan, payloads)
	if err != nil || len(first.Artifacts()) != 1 || first.Artifacts()[0].Content() == nil || !reflect.DeepEqual(first, second) {
		t.Fatalf("Bind() = (%#v, %#v, %v)", first, second, err)
	}
}
