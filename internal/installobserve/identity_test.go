package installobserve_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/refactor-ia/cortex/internal/catalog"
	"github.com/refactor-ia/cortex/internal/installcoord"
	"github.com/refactor-ia/cortex/internal/installobserve"
	"github.com/refactor-ia/cortex/internal/installplan"
	"github.com/refactor-ia/cortex/internal/installstate"
	"github.com/refactor-ia/cortex/internal/qaactor"
	"github.com/refactor-ia/cortex/internal/runtimematrix"
	"github.com/refactor-ia/cortex/internal/skilldest"
	"github.com/refactor-ia/cortex/internal/skillroot"
)

// TestObserveInstallationIDReadsOnlyCanonicalActorAwareState pins the guarantee
// the candidate builder relies on: the recorded identity is returned only when
// the state file proves itself canonical for this exact root, and every other
// shape reports no identity so the caller mints instead of inheriting.
func TestObserveInstallationIDReadsOnlyCanonicalActorAwareState(t *testing.T) {
	actorAware := func(t *testing.T) installplan.Plan { return makeActorAwareCandidate(t) }
	skillOnly := func(t *testing.T) installplan.Plan { plan, _ := makeCandidate(t, "one", "alpha"); return plan }
	for _, tc := range []struct {
		name  string
		build func(t *testing.T) installplan.Plan
		setup func(t *testing.T, candidate installplan.Plan)
		found bool
	}{
		{
			name:  "canonical v2 state yields its recorded identity",
			build: actorAware,
			setup: func(t *testing.T, candidate installplan.Plan) { writeCandidateFiles(t, candidate) },
			found: true,
		},
		{
			name:  "an existing root without state has no identity",
			build: actorAware,
			setup: func(t *testing.T, candidate installplan.Plan) {
				if err := os.MkdirAll(candidate.RootPath(), 0o700); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:  "an absent root has no identity",
			build: actorAware,
			setup: func(*testing.T, installplan.Plan) {},
		},
		{
			name:  "a v1 skill-only installation has no identity",
			build: skillOnly,
			setup: func(t *testing.T, candidate installplan.Plan) { writeCandidateFiles(t, candidate) },
		},
		{
			name:  "unparseable state is rejected, not inherited",
			build: actorAware,
			setup: func(t *testing.T, candidate installplan.Plan) {
				writeCandidateFiles(t, candidate)
				writeStateBytes(t, candidate, []byte("{not json"))
			},
		},
		{
			name:  "non-canonical bytes carrying a valid identity are rejected",
			build: actorAware,
			setup: func(t *testing.T, candidate installplan.Plan) {
				writeCandidateFiles(t, candidate)
				writeStateBytes(t, candidate, append([]byte("  "), candidate.StateJSON()...))
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := tc.build(t)
			tc.setup(t, candidate)
			want := installstate.InstallationID("")
			if tc.found {
				want = candidate.InstalledState().InstallationID()
			}
			got, found := installobserve.ObserveInstallationID(uninstallRoot(t, candidate), installobserve.DefaultOptions())
			if found != tc.found || got != want {
				t.Fatalf("ObserveInstallationID() = (%q, %v), want (%q, %v)", got, found, want, tc.found)
			}
		})
	}
}

func TestObserveInstallationIDRetainsV3IdentityWithoutAuthority(t *testing.T) {
	canonical, composed := identityV3Candidate(t)
	writeCandidateFiles(t, composed)
	for _, drift := range []bool{false, true} {
		if drift {
			if err := os.WriteFile(actorFile(t, composed).AbsolutePath(), []byte("actor drift"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		id, found := installobserve.ObserveInstallationID(uninstallRoot(t, composed), installobserve.DefaultOptions())
		if !found || id != composed.InstalledState().InstallationID() {
			t.Fatalf("v3 identity (drift=%v) = (%q, %v)", drift, id, found)
		}
		o, err := installobserve.Observe(canonical, installobserve.DefaultOptions())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := installobserve.ClassifyFilesystem(canonical, o); err == nil {
			t.Fatal("identity reuse widened ordinary update authority")
		}
		removal, err := installobserve.ObserveUninstall(uninstallRoot(t, composed), installobserve.DefaultOptions())
		if err == nil || removal.Ready() || len(removal.RemovalCandidates()) != 0 {
			t.Fatal("identity reuse widened uninstall authority")
		}
	}
}

func TestObserveInstallationIDV3Validation(t *testing.T) {
	_, composed := identityV3Candidate(t)
	actor := actorFile(t, composed)
	var actorMetadata, skillMetadata installstate.Artifact
	for _, a := range composed.InstalledState().Artifacts() {
		if a.LogicalID() == actor.LogicalID() {
			actorMetadata = a
		}
		if a.Kind() == installstate.KindSkill {
			skillMetadata = a
		}
	}
	canonicalField := `"canonicalSha256":"` + actorMetadata.CanonicalSHA256() + `"`
	for _, tc := range []struct {
		name, from, to string
	}{
		{name: "exact limits"},
		{name: "state byte limit"},
		{name: "entry limit"},
		{name: "invalid options"},
		{name: "invalid root"},
		{name: "foreign trusted root"},
		{name: "root symlink"},
		{name: "state ancestor symlink"},
		{name: "state symlink"},
		{name: "state directory"},
		{"malformed", `"schemaVersion":3`, `"schemaVersion":`},
		{"future schema", `"schemaVersion":3`, `"schemaVersion":4`},
		{"foreign runtime", `"runtime":"pi"`, `"runtime":"opencode"`},
		{"foreign root kind", `"rootKind":"pi-user-agent"`, `"rootKind":"opencode-user-config"`},
		{"foreign owner", `"owner":"cortex"`, `"owner":"other"`},
		{"unknown top metadata", `"owner":"cortex"`, `"unknown":true,"owner":"cortex"`},
		{"duplicate top member", `"owner":"cortex"`, `"owner":"cortex","owner":"cortex"`},
		{"unknown actor metadata", canonicalField, canonicalField + `,"unknown":true`},
		{"missing canonical hash", canonicalField + `,`, ""},
		{"null canonical hash", canonicalField, `"canonicalSha256":null`},
		{"invalid canonical hash", canonicalField, `"canonicalSha256":"bad"`},
		{"invalid effective hash", `"sha256":"` + actor.SHA256() + `"`, `"sha256":"bad"`},
		{"canonical metadata on skill", `"capabilityId":"` + skillMetadata.CapabilityID() + `"`, `"capabilityId":"` + skillMetadata.CapabilityID() + `","canonicalSha256":null`},
		{"invalid contract", actorMetadata.ActorContractVersion(), "unsupported"},
		{"invalid installation ID", string(composed.InstalledState().InstallationID()), "bad"},
		{"mismatched artifact ID", canonicalField + `,"installationId":"` + string(actorMetadata.InstallationID()) + `"`, canonicalField + `,"installationId":"ffffffffffffffffffffffffffffffff"`},
		{"duplicate destination", actor.RelativePath(), "agents/cortex-test-runner.md"},
		{name: "noncanonical whitespace"},
		{name: "trailing JSON"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := bytes.Clone(composed.StateJSON())
			if tc.from != "" {
				data = bytes.Replace(data, []byte(tc.from), []byte(tc.to), 1)
				if bytes.Equal(data, composed.StateJSON()) {
					t.Fatal("mutation did not change fixture")
				}
			}
			path := filepath.Join(t.TempDir(), "root")
			root, err := installobserve.NewUninstallRoot(composed.RuntimeID(), composed.RootKind(), path)
			if err != nil {
				t.Fatal(err)
			}
			options := installobserve.DefaultOptions()
			options.MaxStateBytes = int64(len(data))
			options.MaxEntries = len(composed.InstalledState().Artifacts())
			// No artifacts are installed here: ID observation reads only state.
			stateDir := filepath.Join(path, ".cortex")
			target := filepath.Join(stateDir, "install-state.json")
			switch tc.name {
			case "root symlink", "state ancestor symlink", "state symlink":
				link := path
				if tc.name == "state ancestor symlink" {
					link = stateDir
				} else if tc.name == "state symlink" {
					link = target
				}
				if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
					t.Fatal(err)
				}
				destination := t.TempDir()
				if tc.name != "state symlink" {
					stateParent := destination
					if tc.name == "root symlink" {
						stateParent = filepath.Join(destination, ".cortex")
					}
					if err := os.MkdirAll(stateParent, 0o700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(stateParent, "install-state.json"), data, 0o600); err != nil {
						t.Fatal(err)
					}
				}
				if tc.name == "state symlink" {
					destination = filepath.Join(destination, "state.json")
					if err := os.WriteFile(destination, data, 0o600); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.Symlink(destination, link); err != nil {
					t.Fatal(err)
				}
			default:
				if err := os.MkdirAll(stateDir, 0o700); err != nil {
					t.Fatal(err)
				}
				if tc.name == "state directory" {
					err = os.Mkdir(target, 0o700)
				} else {
					if tc.name == "noncanonical whitespace" {
						data = append(data, '\n')
					} else if tc.name == "trailing JSON" {
						data = append(data, []byte("{}")...)
					}
					options.MaxStateBytes = int64(len(data))
					err = os.WriteFile(target, data, 0o600)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			switch tc.name {
			case "state byte limit":
				options.MaxStateBytes--
			case "entry limit":
				options.MaxEntries--
			case "invalid options":
				options.MaxFileBytes = 0
			case "invalid root":
				root = installobserve.UninstallRoot{}
			case "foreign trusted root":
				root, err = installobserve.NewUninstallRoot(runtimematrix.RuntimeOpenCode, skilldest.RootKindOpenCodeUserConfig, path)
				if err != nil {
					t.Fatal(err)
				}
			}
			id, found := installobserve.ObserveInstallationID(root, options)
			wantFound := tc.name == "exact limits"
			if found != wantFound || !found && id != "" || found && id != composed.InstalledState().InstallationID() {
				t.Fatalf("identity = (%q, %v), want found %v", id, found, wantFound)
			}
		})
	}
}

func TestObserveInstallationIDV3FeedsActualCandidateBuilder(t *testing.T) {
	_, composed := identityV3Candidate(t)
	writeCandidateFiles(t, composed)
	snapshot, err := catalog.BuildCatalogSnapshot(filepath.Join("..", "..", "catalog"), "catalog.json", catalog.AdmissionPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	candidates, _, err := installcoord.BuildCandidates(installcoord.CandidateRequest{
		Snapshot:    snapshot,
		LocalTarget: runtimematrix.RuntimePi,
		Observations: []runtimematrix.Observation{
			{ID: runtimematrix.RuntimePi, Present: true, Version: "fixture", Compatibility: runtimematrix.Compatible},
			{ID: runtimematrix.RuntimeOpenCode, Compatibility: runtimematrix.CompatibilityUnknown},
			{ID: runtimematrix.RuntimeClaudeCode, Compatibility: runtimematrix.CompatibilityUnknown},
		},
		ResolveRoot: func(plan skilldest.Plan) (skillroot.Plan, error) {
			return skillroot.Resolve(plan, skillroot.Inputs{Home: physicalTempDir(t), PiCodingAgentDir: composed.RootPath()})
		},
		Actors: func(snapshot catalog.CatalogSnapshot) (qaactor.Binding, error) {
			sources, err := qaactor.Sources(snapshot)
			if err != nil {
				return qaactor.Binding{}, err
			}
			set, err := qaactor.Render(sources)
			if err != nil {
				return qaactor.Binding{}, err
			}
			projected, err := qaactor.ProjectPi(set)
			if err != nil {
				return qaactor.Binding{}, err
			}
			return qaactor.Bind(projected)
		},
		// No entropy: minting rather than retaining the v3 ID must fail.
		InstallationID: installcoord.ReuseInstallationID(installstate.NewInstallationIDGenerator(nil)),
	})
	if err != nil || len(candidates) != 1 {
		t.Fatalf("BuildCandidates: %d candidates, %v", len(candidates), err)
	}
	if candidates[0].Plan.InstalledState().InstallationID() != composed.InstalledState().InstallationID() {
		t.Fatal("actual candidate builder lost the v3 installation identity")
	}
}

func identityV3Candidate(t *testing.T) (installplan.Plan, installplan.Plan) {
	t.Helper()
	canonical := makeActorAwareCandidate(t)
	writeCandidateFiles(t, canonical)
	for _, f := range canonical.Files() {
		if f.Role() == "actor" {
			if err := os.WriteFile(f.AbsolutePath(), append(bytes.TrimRight(f.Content(), "\n"), bridgeSuffix...), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	o, err := installobserve.Observe(canonical, installobserve.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	proof, err := installobserve.ClassifyActorGuidance(canonical, o, true)
	if err != nil || proof.Candidate().InstalledState().SchemaVersion() != 3 {
		t.Fatalf("real composed v3 candidate: %v", err)
	}
	return canonical, proof.Candidate()
}

func writeStateBytes(t *testing.T, candidate installplan.Plan, data []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(candidate.RootPath(), ".cortex", "install-state.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}
