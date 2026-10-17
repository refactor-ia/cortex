package cli

import (
	"os"
	"path/filepath"
)

// canonicalWorkingDirectory resolves the current working directory to its real
// path so a symlinked invocation directory cannot diverge from the roots the
// shadow scanner validates. QA already canonicalizes its cwd (bindPi); install
// and update align with it here.
//
// Resolution fails closed (issue #306, QA #286 verdicts): if the real path
// cannot be determined, the working directory is not a usable anchor for
// ownership and shadow scanning, so the caller refuses. The earlier best-effort
// fallback to the logical path silently split the two canonicalizers on
// exactly the state where their agreement matters.
func canonicalWorkingDirectory() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return canonicalWorkingDirectoryIn(wd)
}

// canonicalWorkingDirectoryIn canonicalizes one working directory path. It is
// the testable half of canonicalWorkingDirectory: resolution semantics can be
// exercised without rearranging the process's real working directory.
func canonicalWorkingDirectoryIn(wd string) (string, error) {
	canonical, err := filepath.EvalSymlinks(wd)
	if err != nil {
		return "", err
	}
	return canonical, nil
}
