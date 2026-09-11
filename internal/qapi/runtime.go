package qapi

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	maxVersionOutputBytes = 64 << 10
	maxPiBinaryBytes      = 64 << 20
)

var (
	versionProbeTimeout = 10 * time.Second
	versionOutput       = regexp.MustCompile(`^(?:pi )?([0-9]+\.[0-9]+\.[0-9]+)$`)
)

// PiPathResolver resolves one Pi candidate for a single binding attempt.
type PiPathResolver interface {
	ResolvePi(context.Context) (string, error)
}

type boundPi struct {
	path, cwd      string
	identity       os.FileInfo
	mode           os.FileMode
	size           int64
	digest         [sha256.Size]byte
	version        string
	versionCapture versionCapture
}

type versionCapture struct {
	started, startFailed, timedOut, waitFailed bool
	exitCode                                   int
	stdout, stderr                             []byte
	stdoutTruncated, stderrTruncated           bool
}

func (capture versionCapture) complete() bool {
	return capture.started && !capture.startFailed && !capture.timedOut && !capture.waitFailed && !capture.stdoutTruncated && !capture.stderrTruncated
}

// executeVersionCommand is a private fixed-command seam for local tests.
var executeVersionCommand = runVersionCommand

func bindPi(ctx context.Context, cwd string, resolver PiPathResolver) (boundPi, error) {
	if ctx == nil || resolver == nil {
		return boundPi{}, errors.New("Pi binding dependencies are unavailable")
	}
	path, err := resolver.ResolvePi(ctx)
	if err != nil {
		return boundPi{}, errors.New("Pi resolution failed")
	}
	canonicalCWD, err := canonicalRuntimeDirectory(cwd)
	if err != nil {
		return boundPi{}, err
	}
	bound, err := inspectPi(path)
	if err != nil {
		return boundPi{}, err
	}
	bound.cwd = canonicalCWD
	capture := executeVersionCommand(ctx, bound.path, bound.cwd)
	version, valid := parseVersion(capture)
	if !valid || version != RuntimeVersion {
		return boundPi{}, errors.New("Pi version is unsupported")
	}
	bound.version, bound.versionCapture = version, capture
	if err := revalidatePi(bound); err != nil {
		return boundPi{}, errors.New("Pi changed during version binding")
	}
	return bound, nil
}

func revalidatePi(bound boundPi) error {
	version, valid := parseVersion(bound.versionCapture)
	if !absolutePaths(bound.path, bound.cwd) || !valid || version != RuntimeVersion || bound.version != version {
		return errors.New("invalid Pi binding")
	}
	current, err := inspectPi(bound.path)
	if err != nil || current.path != bound.path || !samePiFile(bound, current) {
		return errors.New("Pi binding changed")
	}
	return nil
}

func inspectPi(path string) (boundPi, error) {
	absolute, err := filepath.Abs(path)
	if err != nil || !absolutePaths(absolute) {
		return boundPi{}, errors.New("invalid Pi path")
	}
	listed, err := executableFile(absolute)
	if err != nil {
		return boundPi{}, err
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil || !absolutePaths(canonical) {
		return boundPi{}, errors.New("invalid Pi path")
	}
	before, err := executableFile(canonical)
	if err != nil || !os.SameFile(listed, before) {
		return boundPi{}, errors.New("unsafe Pi path")
	}
	file, err := os.Open(canonical)
	if err != nil {
		return boundPi{}, errors.New("Pi open failed")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !samePiInfo(before, opened) {
		return boundPi{}, errors.New("Pi changed before hashing")
	}
	hash := sha256.New()
	size, err := io.Copy(hash, io.LimitReader(file, maxPiBinaryBytes+1))
	after, statErr := executableFile(canonical)
	if err != nil || statErr != nil || size <= 0 || size > maxPiBinaryBytes || !samePiInfo(before, after) || after.Size() != size {
		return boundPi{}, errors.New("Pi hashing failed")
	}
	var digest [sha256.Size]byte
	copy(digest[:], hash.Sum(nil))
	return boundPi{path: canonical, identity: after, mode: after.Mode(), size: size, digest: digest}, nil
}

func executableFile(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 || info.Size() <= 0 || info.Size() > maxPiBinaryBytes {
		return nil, errors.New("Pi is not an executable regular file")
	}
	return info, nil
}

func samePiFile(left, right boundPi) bool {
	return os.SameFile(left.identity, right.identity) && left.mode == right.mode && left.size == right.size && left.digest == right.digest
}

func samePiInfo(left, right os.FileInfo) bool {
	return os.SameFile(left, right) && left.Mode() == right.Mode() && left.Size() == right.Size()
}

func canonicalRuntimeDirectory(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil || !absolutePaths(absolute) {
		return "", errors.New("invalid Pi working directory")
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil || !absolutePaths(canonical) {
		return "", errors.New("invalid Pi working directory")
	}
	info, err := os.Stat(canonical)
	if err != nil || !info.IsDir() {
		return "", errors.New("invalid Pi working directory")
	}
	return canonical, nil
}

func parseVersion(capture versionCapture) (string, bool) {
	if !capture.complete() || capture.exitCode != 0 || len(capture.stdout) == 0 || len(capture.stdout) > maxVersionOutputBytes || len(capture.stderr) > maxVersionOutputBytes {
		return "", false
	}
	match := versionOutput.FindStringSubmatch(strings.TrimSpace(string(capture.stdout)))
	if len(match) != 2 {
		return "", false
	}
	return match[1], true
}

func fixedVersionCommand() ([]string, int, int) {
	return []string{"--version"}, maxVersionOutputBytes, maxVersionOutputBytes
}

// runVersionCommand runs only the fixed Pi identification command once.
func runVersionCommand(ctx context.Context, path, cwd string) versionCapture {
	arguments, stdoutLimit, stderrLimit := fixedVersionCommand()
	if !absolutePaths(path, cwd) {
		return versionCapture{startFailed: true}
	}
	runContext, cancel := context.WithTimeout(ctx, versionProbeTimeout)
	defer cancel()
	stdout := cappedWriter{maximum: stdoutLimit}
	stderr := cappedWriter{maximum: stderrLimit}
	command := exec.CommandContext(runContext, path, arguments...)
	command.Dir = cwd
	command.Env = minimalEnvironment()
	command.Stdout = &stdout
	command.Stderr = &stderr
	command.WaitDelay = pipeWaitDelay
	if err := command.Start(); err != nil {
		return versionCapture{startFailed: true}
	}
	capture := versionCapture{started: true}
	waitErr := command.Wait()
	capture.stdout, capture.stderr = stdout.bytes, stderr.bytes
	capture.stdoutTruncated, capture.stderrTruncated = stdout.truncated, stderr.truncated
	capture.timedOut = runContext.Err() != nil
	var exitError *exec.ExitError
	if errors.As(waitErr, &exitError) {
		capture.exitCode = exitError.ExitCode()
	} else if waitErr != nil {
		capture.waitFailed = true
	}
	return capture
}
