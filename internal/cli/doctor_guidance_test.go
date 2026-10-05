package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/refactor-ia/cortex/internal/installplan"
	"github.com/refactor-ia/cortex/internal/runtimematrix"
	"github.com/refactor-ia/cortex/internal/skillroot"
	"github.com/refactor-ia/cortex/internal/uninstalltxn"
)

func TestUninstallGuidanceGroupRefusal(t *testing.T) {
	for _, name := range []string{"composed", "unmarked v3", "stale foreign snapshot", "corrupt", "unknown schema", "noncanonical"} {
		t.Run(name, func(t *testing.T) {
			fixture := newUpdateFixture(t)
			deps := fixture.dependencies(runtimematrix.RuntimePi)
			canonical := must(buildUpdateCandidate(runtimematrix.RuntimePi, fixture.catalog, must(deps.compatibility(nil)), deps))
			var actors []installplan.ActorGuidanceObservation
			for _, f := range canonical.Files() {
				if f.Role() != "actor" {
					continue
				}
				content := f.Content()
				if name != "unmarked v3" {
					content = append(bytes.TrimRight(content, "\n"), updateGuidanceSuffix...)
				}
				actors = append(actors, installplan.ActorGuidanceObservation{LogicalID: f.LogicalID(), RootPath: canonical.RootPath(), RelativePath: f.RelativePath(), AbsolutePath: f.AbsolutePath(), Mode: f.DesiredMode(), Content: content})
			}
			composed, _, err := installplan.BuildActorGuidance(canonical, installplan.ActorGuidancePrior{RootPath: canonical.RootPath(), State: canonical.InstalledState()}, actors, name != "unmarked v3")
			mustOK(err)
			for _, f := range composed.Files() {
				content := f.Content()
				if name == "stale foreign snapshot" && f.Role() == "actor" {
					for _, prior := range canonical.Files() {
						if prior.LogicalID() == f.LogicalID() {
							content = prior.Content()
						}
					}
				}
				if f.Role() == "state" {
					switch name {
					case "corrupt":
						content = []byte("{}")
					case "unknown schema":
						content = bytes.Replace(content, []byte(`"schemaVersion":3`), []byte(`"schemaVersion":99`), 1)
					case "noncanonical":
						content = append(content, '\n')
					}
				}
				mustOK(os.MkdirAll(filepath.Dir(f.AbsolutePath()), 0o700))
				mustOK(os.WriteFile(f.AbsolutePath(), content, 0o600))
				mustOK(os.Chmod(f.AbsolutePath(), 0o640))
			}
			roots := must(skillroot.ResolveUninstallRoots(skillroot.Inputs{Home: fixture.home}))
			if roots[0].RootPath() != canonical.RootPath() {
				t.Fatal("fixture root mismatch")
			}
			for _, root := range roots[1:] {
				writeInstalled(t, root, "ordinary removable skill")
			}
			before := updateGuidanceTree(t, fixture.home)
			var firstOut, firstErr string
			valid := name == "composed" || name == "unmarked v3" || name == "stale foreign snapshot"
			for attempt := 0; attempt < 2; attempt++ {
				code, out, errOut := runUninstallForTest(t, roots, func(d *uninstallDependencies) {
					d.backupName = func() string { t.Fatal("blocked group allocated backup"); return "" }
					d.applyGroup = func([]uninstalltxn.GroupRequest, string, string) error {
						t.Fatal("blocked group reached mutation")
						return nil
					}
				})
				if !reflect.DeepEqual(before, updateGuidanceTree(t, fixture.home)) {
					t.Fatal("refusal changed bytes, modes, state or tree")
				}
				if attempt == 1 && (out != firstOut || errOut != firstErr) {
					t.Fatal("repeat refusal changed diagnostics")
				}
				firstOut, firstErr = out, errOut
				if !valid {
					if code != exitFailure || out != "" || errOut != "error=uninstall_observation_failed\n" {
						t.Fatalf("invalid state = (%d, %q, %q)", code, out, errOut)
					}
					continue
				}
				if code != exitConflict || errOut != "" {
					t.Fatalf("v3 refusal = (%d, %q, %q)", code, out, errOut)
				}
				for _, text := range []string{"runtime=pi uninstall=blocked remove=0", "runtime=opencode uninstall=blocked", "runtime=claude-code uninstall=blocked", "reason=unsupported_installation", "state=validated_canonical_v3", "shared ownership policy", "no files changed", "manual resolution required", "do not confirm external guidance"} {
					if !strings.Contains(out, text) {
						t.Fatalf("diagnostic omitted %q: %q", text, out)
					}
				}
				for _, a := range actors {
					if !strings.Contains(out, a.RelativePath) {
						t.Fatal("missing recorded actor path")
					}
				}
				for _, private := range []string{fixture.home, "external guidance\r", "delete", "sha256", "strip"} {
					if strings.Contains(out+errOut, private) {
						t.Fatalf("unsafe diagnostic included %q", private)
					}
				}
			}
		})
	}
}
