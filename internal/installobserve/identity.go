package installobserve

import (
	"github.com/refactor-ia/cortex/internal/installstate"
)

// ObserveInstallationID reads the installation identity already recorded at a
// trusted root, so a later candidate can name the installation it is updating
// instead of inventing a new one.
//
// It reuses the same canonical decode ObserveUninstall performs: the state file
// must decode, re-encode to the exact bytes on disk, and declare this root's
// runtime and root kind. Anything else — an absent root, no state file, an
// oversized or unreadable file, non-canonical or tampered bytes, a foreign
// runtime, or a v1 skill-only installation — reports no identity rather than an
// approximate one. The prior state is therefore never a second source of truth:
// a caller that gets no identity mints a fresh one, and a state file that
// cannot prove itself canonical can never steer a candidate.
func ObserveInstallationID(root UninstallRoot, options Options) (installstate.InstallationID, bool) {
	if !validOptions(options) || !validUninstallRoot(root) || !existingRoot(root.rootPath) {
		return "", false
	}
	state, _, present, err := readRegular(root.rootPath, ".cortex/install-state.json", options.MaxStateBytes)
	if err != nil || !present {
		return "", false
	}
	manifest, err := decodeCanonicalUninstallState(state, root, options.MaxEntries)
	if err != nil || manifest.SchemaVersion() != 2 {
		return "", false
	}
	return manifest.InstallationID(), true
}
