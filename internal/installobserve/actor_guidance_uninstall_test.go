package installobserve_test

import (
	"bytes"
	"os"
	"reflect"
	"testing"

	"github.com/refactor-ia/cortex/internal/installobserve"
	"github.com/refactor-ia/cortex/internal/installstate"
)

func TestActorGuidanceUninstallDiagnostic(t *testing.T) {
	for _, marked := range []bool{true, false} {
		t.Run(map[bool]string{true: "composed", false: "unmarked"}[marked], func(t *testing.T) {
			f := newGuidanceAdmissionFixture(t, marked)
			manifest, err := installstate.Decode(f.state)
			if err != nil {
				t.Fatal(err)
			}
			var want []string
			for _, a := range manifest.Artifacts() {
				if a.Kind() == installstate.KindPiActor {
					want = append(want, a.RelativePath())
				}
			}
			for attempt := 0; attempt < 2; attempt++ {
				got, err := installobserve.ObserveUninstall(uninstallRoot(t, f.candidate), installobserve.DefaultOptions())
				if err == nil || got.Ready() || len(got.Records()) != 0 || len(got.RemovalCandidates()) != 0 {
					t.Fatal("v3 observation granted uninstall authority")
				}
				if !reflect.DeepEqual(got.UnsupportedActorPaths(), want) {
					t.Fatalf("diagnostic paths = %v, want %v", got.UnsupportedActorPaths(), want)
				}
				paths := got.UnsupportedActorPaths()
				paths[0] = "tampered"
				if !reflect.DeepEqual(got.UnsupportedActorPaths(), want) {
					t.Fatal("diagnostic paths alias observation")
				}
				for _, a := range manifest.Artifacts() {
					if _, ok := got.RemovalEvidence(a.LogicalID()); ok {
						t.Fatal("v3 exposed removal evidence")
					}
					if _, ok := got.Exact(a.LogicalID()); ok {
						t.Fatal("diagnostic exposed artifact contents")
					}
				}
				if _, ok := got.RemovalEvidence("state/install-state"); ok {
					t.Fatal("v3 state removal authorized")
				}
			}
		})
	}
}

func TestActorGuidanceUninstallRejectsInvalidDiagnostics(t *testing.T) {
	for _, name := range []string{"corrupt", "noncanonical", "unknown schema", "missing canonical hash", "unsafe actor path", "duplicate actor", "wrong owner", "wrong runtime", "entry bound", "state bound", "state symlink", "root symlink"} {
		t.Run(name, func(t *testing.T) {
			f := newGuidanceAdmissionFixture(t, true)
			state, options := bytes.Clone(f.state), installobserve.DefaultOptions()
			root := uninstallRoot(t, f.candidate)
			switch name {
			case "corrupt":
				state = []byte("{}")
			case "noncanonical":
				state = append(state, '\n')
			case "unknown schema":
				state = bytes.Replace(state, []byte(`"schemaVersion":3`), []byte(`"schemaVersion":99`), 1)
			case "missing canonical hash":
				state = bytes.Replace(state, []byte(`"canonicalSha256":"`+f.expected.ActorSHA256+`",`), nil, 1)
			case "unsafe actor path":
				state = bytes.Replace(state, []byte("agents/cortex-test-designer.md"), []byte("../private.md"), 1)
			case "duplicate actor":
				state = bytes.Replace(state, []byte(`"actors/test-designer":`), []byte(`"actors/requirements-analyst":`), 1)
			case "wrong owner":
				state = bytes.Replace(state, []byte(`"owner":"cortex"`), []byte(`"owner":"other"`), 1)
			case "wrong runtime":
				root, _ = installobserve.NewUninstallRoot("opencode", "opencode-user-config", f.root)
			case "entry bound":
				options.MaxEntries = 1
			case "state bound":
				options.MaxStateBytes = 1
			case "state symlink":
				f.statePath += ".linked"
				writeAdmissionFile(t, f.statePath, state, 0o600)
				// Use a fresh root, without replacing the existing state.
				linked := t.TempDir()
				if err := os.Mkdir(linked+"/.cortex", 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(f.statePath, linked+"/.cortex/install-state.json"); err != nil {
					t.Fatal(err)
				}
				root, _ = installobserve.NewUninstallRoot("pi", "pi-user-agent", linked)
			case "root symlink":
				root, _ = installobserve.NewUninstallRoot("pi", "pi-user-agent", admissionSymlink(t, f.root))
			}
			if name != "state symlink" {
				writeAdmissionFile(t, f.statePath, state, 0o600)
			}
			got, err := installobserve.ObserveUninstall(root, options)
			if err == nil || len(got.UnsupportedActorPaths()) != 0 || got.Ready() || len(got.RemovalCandidates()) != 0 {
				t.Fatal("invalid state was labeled validated or granted authority")
			}
		})
	}
}

func TestActorGuidanceUninstallStaleSnapshot(t *testing.T) {
	f := newGuidanceAdmissionFixture(t, true)
	writeAdmissionFile(t, f.actorPath, f.canonical, 0o600)
	if _, err := installobserve.ObserveAdmissionAssets(f.root, f.cwd, f.expected); err == nil {
		t.Fatal("stale foreign snapshot admitted")
	}
	observation, err := installobserve.Observe(f.candidate, installobserve.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := installobserve.ClassifyActorGuidance(f.candidate, observation, true); err == nil {
		t.Fatal("stale foreign snapshot accepted for update")
	}
	got, err := installobserve.ObserveUninstall(uninstallRoot(t, f.candidate), installobserve.DefaultOptions())
	if err == nil || len(got.UnsupportedActorPaths()) != 6 || got.Ready() || len(got.RemovalCandidates()) != 0 {
		t.Fatal("stale foreign snapshot bypassed uninstall block")
	}
}
