package qapi

import (
	"fmt"
	"path/filepath"
)

// Backends and versions the fixture tree under testdata/ was captured from.
const (
	piFixtureBackend = "pi"
	piFixtureVersion = "0.85.1"

	claudeFixtureBackend = "claude"
	claudeFixtureVersion = "2.1.278"

	opencodeFixtureBackend = "opencode"
	opencodeFixtureVersion = "1.18.25"
)

// backendTestdata builds a path into the fixture tree of one backend at one
// version. Fixtures live under testdata/<backend>-<version>/ so a second
// backend can be added without a second convention.
func backendTestdata(backend, version string, parts ...string) string {
	segments := make([]string, 0, len(parts)+2)
	segments = append(segments, "testdata", fmt.Sprintf("%s-%s", backend, version))
	segments = append(segments, parts...)
	return filepath.Join(segments...)
}
