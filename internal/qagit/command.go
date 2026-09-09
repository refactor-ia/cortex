package qagit

import (
	"context"
	"os"
	"os/exec"
	"strings"
)

type Command struct {
	CWD, Binary       string
	Argv, Environment []string
}
type Result struct {
	ExitCode       int
	Stdout, Stderr []byte
}
type systemRunner struct{}

func (systemRunner) Run(ctx context.Context, command Command) (Result, error) {
	process := exec.CommandContext(ctx, command.Binary, command.Argv...)
	process.Dir, process.Env = command.CWD, command.Environment
	var stdout, stderr strings.Builder
	process.Stdout, process.Stderr = &stdout, &stderr
	err := process.Run()
	result := Result{Stdout: []byte(stdout.String()), Stderr: []byte(stderr.String())}
	if exitError, ok := err.(*exec.ExitError); ok {
		result.ExitCode = exitError.ExitCode()
		return result, nil
	}
	return result, err
}
func fixedCommands(cwd string) []Command {
	return []Command{
		gitCommand(cwd, []string{"-C", cwd, "rev-parse", "--show-toplevel"}),
		gitCommand(cwd, []string{"-C", cwd, "rev-parse", "--path-format=absolute", "--git-common-dir"}),
		gitCommand(cwd, []string{"-C", cwd, "rev-parse", "--show-object-format"}),
		gitCommand(cwd, []string{"-C", cwd, "rev-parse", "--verify", "HEAD^{commit}"}),
		gitCommand(cwd, []string{"-C", cwd, "rev-parse", "--verify", "HEAD^{tree}"}),
		gitCommand(cwd, []string{"-C", cwd, "status", "--porcelain=v2", "-z", "--untracked-files=all"}),
	}
}
func gitCommand(cwd string, argv []string) Command {
	return Command{CWD: cwd, Binary: "git", Argv: argv, Environment: gitEnvironment(os.Environ())}
}
func gitEnvironment(source []string) []string {
	environment := make([]string, 0, 9)
	for _, value := range source {
		if key, _, ok := strings.Cut(value, "="); ok && key == "PATH" {
			environment = append(environment, value)
		}
	}
	return append(environment, "LC_ALL=C", "LANG=C", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_ATTR_NOSYSTEM=1", "GIT_OPTIONAL_LOCKS=0", "GIT_PAGER=cat", "GIT_EXTERNAL_DIFF=")
}
