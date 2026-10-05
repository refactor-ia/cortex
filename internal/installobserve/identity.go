package installobserve

import (
	"bytes"

	"github.com/refactor-ia/cortex/internal/installstate"
)

// ObserveInstallationID reads the installation identity already recorded at a
// trusted root, so a later candidate can name the installation it is updating
// instead of inventing a new one.
//
// Only canonical v2/v3 state matching this root's runtime and root kind can
// supply an identity. The closed installstate decoder validates all metadata,
// hashes, and unique artifact paths; encoding must reproduce the exact bytes.
// Absent, unsafe, oversized, invalid, foreign, or v1 state reports no identity.
// This reads only state, not artifact integrity: even actor drift can retain the
// recorded ID. It supplies no ownership, write, removal, or readiness evidence
// and deliberately does not use or widen the uninstall decoder's schema gate.
func ObserveInstallationID(root UninstallRoot, options Options) (installstate.InstallationID, bool) {
	if !validOptions(options) || !validUninstallRoot(root) || !existingRoot(root.rootPath) {
		return "", false
	}
	state, _, present, err := readRegular(root.rootPath, ".cortex/install-state.json", options.MaxStateBytes)
	if err != nil || !present {
		return "", false
	}
	manifest, err := installstate.Decode(state)
	if err != nil {
		return "", false
	}
	switch manifest.SchemaVersion() {
	case 2, 3:
		// Identity-bearing schemas only; future versions require explicit support.
	default:
		return "", false
	}
	if len(manifest.Artifacts()) > options.MaxEntries || manifest.RuntimeID() != root.runtimeID || manifest.RootKind() != root.rootKind {
		return "", false
	}
	encoded, err := installstate.Encode(manifest)
	if err != nil || !bytes.Equal(state, encoded) {
		return "", false
	}
	return manifest.InstallationID(), true
}
