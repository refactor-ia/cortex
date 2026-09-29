package cli

import (
	"io"
	"runtime/debug"

	"github.com/refactor-ia/cortex/internal/builtinassets"
	"github.com/refactor-ia/cortex/internal/releasecatalog"
)

// Set at build time with -ldflags -X; absent fields use the Go build info.
var buildVersion, buildRevision string

func runVersion(stdout, stderr io.Writer) int {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		info = nil
	}
	report, err := versionReport(buildVersion, buildRevision, info)
	if err != nil {
		writeError(stderr, "embedded_catalog_unavailable")
		return exitFailure
	}
	if _, err := io.WriteString(stdout, report); err != nil {
		writeError(stderr, "output_failed")
		return exitFailure
	}
	return exitOK
}

// versionReport uses only metadata compiled into the running binary. The
// catalog field is reported only after the embedded snapshot is admitted.
func versionReport(explicitVersion, explicitRevision string, info *debug.BuildInfo) (string, error) {
	version, revision, dirty := "unavailable", "unavailable", "unavailable"
	if info != nil {
		version = metadataValue(info.Main.Version)
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				revision = metadataValue(setting.Value)
			case "vcs.modified":
				if setting.Value == "true" || setting.Value == "false" {
					dirty = setting.Value
				}
			}
		}
	}
	if value := metadataValue(explicitVersion); value != "unavailable" {
		version = value
	}
	if value := metadataValue(explicitRevision); value != "unavailable" {
		revision = value
	}
	snapshot, err := builtinassets.Snapshot()
	if err != nil {
		return "", err
	}
	resolution, err := releasecatalog.BuiltInSource().ResolveSnapshot(snapshot)
	if err != nil {
		return "", err
	}
	return "version=" + version + " revision=" + revision + " build_dirty=" + dirty + " embedded_catalog=" + resolution.ID() + "\n", nil
}

func metadataValue(value string) string {
	if value == "" || value == "(devel)" || value == "unavailable" {
		return "unavailable"
	}
	for _, character := range value {
		if character < '!' || character > '~' || character == '=' {
			return "unavailable"
		}
	}
	return value
}
