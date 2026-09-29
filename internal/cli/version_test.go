package cli

import (
	"bytes"
	"context"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/refactor-ia/cortex/internal/builtinassets"
	"github.com/refactor-ia/cortex/internal/releasecatalog"
)

func TestVersionReportMetadata(t *testing.T) {
	cases := []struct {
		name, explicitVersion, explicitRevision string
		info                                    *debug.BuildInfo
		want                                    string
	}{
		{
			name: "missing metadata", want: "version=unavailable revision=unavailable build_dirty=unavailable",
		},
		{
			name: "devel is not a release", explicitVersion: "(devel)", explicitRevision: "(devel)",
			info: &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}},
			want: "version=unavailable revision=unavailable build_dirty=unavailable",
		},
		{
			name: "Go build info fallback", info: &debug.BuildInfo{
				Main:     debug.Module{Version: "v0.2.0"},
				Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "abc123"}, {Key: "vcs.modified", Value: "false"}},
			},
			want: "version=v0.2.0 revision=abc123 build_dirty=false",
		},
		{
			name: "explicit version overrides only version", explicitVersion: "v1.0.0",
			info: &debug.BuildInfo{Main: debug.Module{Version: "v0.2.0"}, Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "abc123"}, {Key: "vcs.modified", Value: "true"}}},
			want: "version=v1.0.0 revision=abc123 build_dirty=true",
		},
		{
			name: "explicit revision overrides only revision", explicitRevision: "release-123",
			info: &debug.BuildInfo{Main: debug.Module{Version: "v0.2.0"}, Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "abc123"}, {Key: "vcs.modified", Value: "true"}}},
			want: "version=v0.2.0 revision=release-123 build_dirty=true",
		},
		{
			name: "explicit release values preserve Go dirty provenance", explicitVersion: "v1.0.0", explicitRevision: "release-123",
			info: &debug.BuildInfo{Main: debug.Module{Version: "v0.2.0"}, Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "abc123"}, {Key: "vcs.modified", Value: "true"}}},
			want: "version=v1.0.0 revision=release-123 build_dirty=true",
		},
		{
			name: "no Go dirty setting cannot claim clean", explicitVersion: "v1.0.0", explicitRevision: "release-123",
			want: "version=v1.0.0 revision=release-123 build_dirty=unavailable",
		},
		{
			name: "unrecognized Go dirty setting cannot claim clean", info: &debug.BuildInfo{Settings: []debug.BuildSetting{{Key: "vcs.modified", Value: "unknown"}}},
			want: "version=unavailable revision=unavailable build_dirty=unavailable",
		},
		{
			name: "malformed explicit values cannot break diagnostic fields", explicitVersion: "v1.0\nstatus=ok", explicitRevision: "rev=abc",
			info: &debug.BuildInfo{Main: debug.Module{Version: "v0.2.0"}, Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "abc123"}}},
			want: "version=v0.2.0 revision=abc123 build_dirty=unavailable",
		},
		{
			name: "invalid Go metadata is unavailable", info: &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}, Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "bad revision"}}},
			want: "version=unavailable revision=unavailable build_dirty=unavailable",
		},
		{
			name: "control characters cannot enter the report", explicitRevision: "rev\x1b[0m",
			want: "version=unavailable revision=unavailable build_dirty=unavailable",
		},
	}
	snapshot, err := builtinassets.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	admitted, err := releasecatalog.BuiltInSource().ResolveSnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := versionReport(tc.explicitVersion, tc.explicitRevision, tc.info)
			if err != nil {
				t.Fatal(err)
			}
			want := tc.want + " embedded_catalog=" + admitted.ID() + "\n"
			if got != want {
				t.Fatalf("versionReport() = %q, want %q", got, want)
			}
		})
	}
}

func TestVersionCommandAndArguments(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"--version"}, &stdout, &stderr, nil); code != exitOK || stderr.Len() != 0 {
		t.Fatalf("--version code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.HasPrefix(stdout.String(), "version=") || !strings.Contains(stdout.String(), " embedded_catalog=catalog.") {
		t.Fatalf("--version output = %q", stdout.String())
	}
	stdout.Reset()
	if code := Run(context.Background(), []string{"--help"}, &stdout, &stderr, nil); code != exitOK || stderr.Len() != 0 || !strings.Contains(stdout.String(), "commands: doctor, install, update, uninstall, qa") || !strings.Contains(stdout.String(), "options: --version, --help") {
		t.Fatalf("--help code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	for _, args := range [][]string{{"--version", "extra"}, {"--version", "--help"}, {"version"}} {
		t.Run(strings.Join(args, "/"), func(t *testing.T) {
			stdout.Reset()
			stderr.Reset()
			if code := Run(context.Background(), args, &stdout, &stderr, nil); code != exitUsage || stdout.Len() != 0 || stderr.String() != "error=invalid_command\n" {
				t.Fatalf("Run(%q) = code %d stdout %q stderr %q", args, code, stdout.String(), stderr.String())
			}
		})
	}
}
