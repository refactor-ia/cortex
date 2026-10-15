package cli

import (
	"os"
	"path/filepath"
)

// canonicalWorkingDirectory resolves the current working directory to its real
// path so a symlinked invocation directory cannot diverge from the roots the
// shadow scanner validates. QA already canonicalizes its cwd (bindPi); install
// and update align with it here. Resolution is best-effort: if the real path
// cannot be determined, the logical path is returned and the existing
// fail-closed validation still guards it (issue #297).
func canonicalWorkingDirectory() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	canonical, err := filepath.EvalSymlinks(wd)
	if err != nil {
		return wd, nil
	}
	return canonical, nil
}
