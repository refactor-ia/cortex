package installobserve_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/refactor-ia/cortex/internal/installobserve"
	"github.com/refactor-ia/cortex/internal/installplan"
)

func TestObserveUninstallClassifiesCanonicalPriorState(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, candidate installplan.Plan)
		want  func(installplan.Plan) []installobserve.UninstallRecord
		ready bool
	}{
		{
			name: "absent state is a neutral no-op",
			setup: func(t *testing.T, candidate installplan.Plan) {
				if err := os.MkdirAll(candidate.RootPath(), 0o700); err != nil {
					t.Fatal(err)
				}
			},
			want:  func(installplan.Plan) []installobserve.UninstallRecord { return []installobserve.UninstallRecord{} },
			ready: true,
		},
		{
			name:  "exact prior files are candidates with state last",
			setup: func(t *testing.T, candidate installplan.Plan) { writeCandidateFiles(t, candidate) },
			want: func(candidate installplan.Plan) []installobserve.UninstallRecord {
				return []installobserve.UninstallRecord{
					{LogicalID: "skills/alpha", Status: installobserve.UninstallRemove, SHA256: hashesByID(candidate)["skills/alpha"]},
					{LogicalID: "state/install-state", Status: installobserve.UninstallRemove, SHA256: hashBytes(candidate.StateJSON())},
				}
			},
			ready: true,
		},
		{
			name: "missing prior file is absent",
			setup: func(t *testing.T, candidate installplan.Plan) {
				state := candidate.Files()[len(candidate.Files())-1]
				if err := os.MkdirAll(filepath.Dir(state.AbsolutePath()), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(state.AbsolutePath(), state.Content(), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			want: func(candidate installplan.Plan) []installobserve.UninstallRecord {
				return []installobserve.UninstallRecord{
					{LogicalID: "skills/alpha", Status: installobserve.UninstallAbsent},
					{LogicalID: "state/install-state", Status: installobserve.UninstallRemove, SHA256: hashBytes(candidate.StateJSON())},
				}
			},
			ready: true,
		},
		{
			name: "drift globally disables removal",
			setup: func(t *testing.T, candidate installplan.Plan) {
				writeCandidateFiles(t, candidate)
				if err := os.WriteFile(candidate.Files()[0].AbsolutePath(), []byte("drift"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			want: func(candidate installplan.Plan) []installobserve.UninstallRecord {
				return []installobserve.UninstallRecord{
					{LogicalID: "skills/alpha", Status: installobserve.UninstallConflict, SHA256: hashBytes([]byte("drift"))},
					{LogicalID: "state/install-state", Status: installobserve.UninstallRemove, SHA256: hashBytes(candidate.StateJSON())},
				}
			},
			ready: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate, _ := makeCandidate(t, "one", "alpha")
			tc.setup(t, candidate)
			root := uninstallRoot(t, candidate)
			got, err := installobserve.ObserveUninstall(root, installobserve.DefaultOptions())
			want := tc.want(candidate)
			if err != nil || got.Ready() != tc.ready || !reflect.DeepEqual(got.Records(), want) {
				t.Fatalf("ObserveUninstall() = (%#v, %v)", got, err)
			}
			if tc.ready && len(want) > 0 {
				candidates := make([]installobserve.UninstallRecord, 0, len(want))
				for _, record := range want {
					if record.Status == installobserve.UninstallRemove {
						candidates = append(candidates, record)
					}
				}
				if !reflect.DeepEqual(got.RemovalCandidates(), candidates) {
					t.Fatalf("RemovalCandidates() = %#v", got.RemovalCandidates())
				}
			}
			if !tc.ready && len(got.RemovalCandidates()) != 0 {
				t.Fatalf("conflicted observation exposed removal candidates: %#v", got.RemovalCandidates())
			}
			if tc.name == "exact prior files are candidates with state last" {
				skill, found := got.Exact("skills/alpha")
				if !found || !bytes.Equal(skill.Bytes(), candidate.Files()[0].Content()) || skill.Mode().Perm() != 0o600 {
					t.Fatal("missing exact skill evidence")
				}
				state, found := got.Exact("state/install-state")
				if !found || !bytes.Equal(state.Bytes(), candidate.StateJSON()) || state.Mode().Perm() != 0o600 {
					t.Fatal("missing exact final state evidence")
				}
			}
		})
	}
}

func TestObserveUninstallRejectsUnsafeOrInvalidPriorState(t *testing.T) {
	for _, tc := range []struct {
		name    string
		setup   func(t *testing.T, candidate installplan.Plan)
		options installobserve.Options
	}{
		{
			name: "state symlink",
			setup: func(t *testing.T, candidate installplan.Plan) {
				path := filepath.Join(candidate.RootPath(), ".cortex", "install-state.json")
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("other", path); err != nil {
					t.Fatal(err)
				}
			},
			options: installobserve.DefaultOptions(),
		},
		{
			name: "prior file is a directory",
			setup: func(t *testing.T, candidate installplan.Plan) {
				writeCandidateFiles(t, candidate)
				if err := os.Remove(candidate.Files()[0].AbsolutePath()); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(candidate.Files()[0].AbsolutePath(), 0o700); err != nil {
					t.Fatal(err)
				}
			},
			options: installobserve.DefaultOptions(),
		},
		{
			name:    "state is oversized",
			setup:   func(t *testing.T, candidate installplan.Plan) { writeCandidateFiles(t, candidate) },
			options: installobserve.Options{MaxStateBytes: 1, MaxEntries: 10, MaxFileBytes: 1024},
		},
		{
			name:    "prior file is oversized",
			setup:   func(t *testing.T, candidate installplan.Plan) { writeCandidateFiles(t, candidate) },
			options: installobserve.Options{MaxStateBytes: 1024, MaxEntries: 10, MaxFileBytes: 1},
		},
		{
			name:    "runtime root identity mismatch",
			setup:   func(t *testing.T, candidate installplan.Plan) { writeCandidateFiles(t, candidate) },
			options: installobserve.DefaultOptions(),
		},
		{
			name: "duplicate prior relative paths",
			setup: func(t *testing.T, candidate installplan.Plan) {
				state := []byte(fmt.Sprintf(`{"schemaVersion":1,"owner":"cortex","scope":"user","runtime":%q,"rootKind":%q,"snapshotFingerprint":%q,"artifacts":{"skills/alpha":{"relativePath":"skills/cortex-alpha/SKILL.md","sha256":%q},"skills/beta":{"relativePath":"skills/cortex-alpha/SKILL.md","sha256":%q}}}`,
					candidate.RuntimeID(), candidate.RootKind(), candidate.SnapshotFingerprint(), hashesByID(candidate)["skills/alpha"], hashBytes([]byte("beta"))))
				path := filepath.Join(candidate.RootPath(), ".cortex", "install-state.json")
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, state, 0o600); err != nil {
					t.Fatal(err)
				}
			},
			options: installobserve.DefaultOptions(),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate, _ := makeCandidate(t, "one", "alpha")
			tc.setup(t, candidate)
			root := uninstallRoot(t, candidate)
			if tc.name == "runtime root identity mismatch" {
				var err error
				root, err = installobserve.NewUninstallRoot("opencode", "opencode-user-config", candidate.RootPath())
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, err := installobserve.ObserveUninstall(root, tc.options); err == nil {
				t.Fatal("ObserveUninstall() succeeded")
			}
		})
	}
}

func uninstallRoot(t *testing.T, candidate installplan.Plan) installobserve.UninstallRoot {
	t.Helper()
	root, err := installobserve.NewUninstallRoot(candidate.RuntimeID(), candidate.RootKind(), candidate.RootPath())
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func hashesByID(candidate installplan.Plan) map[string]string {
	out := make(map[string]string)
	for _, file := range candidate.Files() {
		if file.Role() == "skill" {
			out[file.LogicalID()] = file.SHA256()
		}
	}
	return out
}
