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
	// maxRuntimeBinaryBytes bounds the executable every backend binding
	// hashes. The guard exists because hashing an unbounded file is unbounded
	// work, not because any one runtime is small: it is shared by the pi,
	// claude, and opencode bindings and is named for what it guards.
	//
	// The previous 64 MiB was sized for Pi, which ships as a 660-byte cli.js
	// shim. Real installs of the other two are far larger — claude 217695408
	// bytes (~208 MiB), opencode 144123746 bytes (~137 MiB) — so both were
	// refused before their availability probes ever ran, and doctor reported
	// unsupported_runtime instead of an auth state.
	//
	// The bound is raised to what the work costs rather than to what today's
	// binaries measure. Hashing the 208 MiB claude executable takes ~80ms on
	// an Apple-silicon machine (~2.6 GiB/s with hardware SHA-256); a binding
	// hashes twice, before and after the version capture, so ~160ms per bind,
	// once per report run or per doctor probe and never per availability
	// probe. At 512 MiB the same guard costs ~200ms per hash, which stays a
	// small fraction of a run bounded by qaadmission.DefaultTimeoutSeconds
	// while leaving headroom for runtimes that keep growing.
	//
	// The bound stays a refusal, not a truncation: a file over it is rejected
	// rather than partially read, so Binary.SizeBytes remains the whole
	// observed size and the recorded digest still identifies the whole binary.
	maxRuntimeBinaryBytes = 512 << 20
)

var (
	versionProbeTimeout = 10 * time.Second
	versionOutput       = regexp.MustCompile(`^(?:pi )?([0-9]+\.[0-9]+\.[0-9]+)$`)
)

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

// Path and CWD project the two binding values that cross the backend port.
// The rest of boundPi stays unexported because revalidation, not the report
// sequence, is what needs the executable's identity, mode, size, and digest.
func (bound boundPi) Path() string {
	return bound.path
}

func (bound boundPi) CWD() string {
	return bound.cwd
}

func (capture versionCapture) complete() bool {
	return capture.started && !capture.startFailed && !capture.timedOut && !capture.waitFailed && !capture.stdoutTruncated && !capture.stderrTruncated
}

// executeVersionCommand is a private fixed-command seam for local tests.
var executeVersionCommand = runVersionCommand

// bindPi resolves and verifies Pi once for one run.
//
// It requires a well-formed version line and records it; it does not pin one
// exact build, and it used to. Three adapters carried two different
// runtime-trust contracts — Pi pinned 0.85.1 while the Claude Code and
// OpenCode bindings accepted any well-formed X.Y.Z — while every receipt read
// as equally strong, and the asymmetry was the defect, not the absence of a
// pin: the stream is validated structurally at parse time, so pinning a build
// rejects working installations without gaining evidence. A build whose stream
// does not satisfy the parser still fails closed with a named reason rather
// than producing a report.
func bindPi(ctx context.Context, cwd string, resolver PathResolver) (boundPi, error) {
	if ctx == nil || resolver == nil {
		return boundPi{}, errors.New("Pi binding dependencies are unavailable")
	}
	path, err := resolver.Resolve(ctx)
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
	if err := revalidatePi(bound); err != nil {
		return boundPi{}, errors.New("Pi changed before version binding")
	}
	capture := executeVersionCommand(ctx, bound.path, bound.cwd)
	version, valid := parseVersion(capture)
	if !valid {
		return boundPi{}, errors.New("Pi version is unsupported")
	}
	bound.version, bound.versionCapture = version, capture
	if err := revalidatePi(bound); err != nil {
		return boundPi{}, errors.New("Pi changed during version binding")
	}
	return bound, nil
}

func revalidatePi(bound boundPi) error {
	if err := revalidatePiFile(bound); err != nil {
		return err
	}
	if bound.version == "" && !bound.versionCapture.started {
		return nil
	}
	version, valid := parseVersion(bound.versionCapture)
	if !valid || bound.version != version {
		return errors.New("invalid Pi binding")
	}
	return nil
}

func revalidatePiFile(bound boundPi) error {
	if !absolutePaths(bound.path, bound.cwd) {
		return errors.New("invalid Pi binding")
	}
	current, err := inspectPi(bound.path)
	if err != nil || current.path != bound.path || !samePiFile(bound, current) {
		return errors.New("Pi binding changed")
	}
	return nil
}

// executableIdentity is the verified file identity of one runtime executable:
// its canonical path plus everything needed to prove the same file is still
// there later. It is backend-independent on purpose — every backend binds a
// binary the same way, and only the version contract differs.
type executableIdentity struct {
	path     string
	identity os.FileInfo
	mode     os.FileMode
	size     int64
	digest   [sha256.Size]byte
}

// inspectExecutable canonicalizes, stats, and hashes one runtime executable,
// proving the file did not change between the stat and the hash.
func inspectExecutable(path string) (executableIdentity, error) {
	absolute, err := filepath.Abs(path)
	if err != nil || !absolutePaths(absolute) {
		return executableIdentity{}, errors.New("invalid runtime path")
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil || !absolutePaths(canonical) {
		return executableIdentity{}, errors.New("invalid runtime path")
	}
	before, err := executableFile(canonical)
	if err != nil {
		return executableIdentity{}, err
	}
	file, err := os.Open(canonical)
	if err != nil {
		return executableIdentity{}, errors.New("runtime open failed")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !samePiInfo(before, opened) {
		return executableIdentity{}, errors.New("runtime changed before hashing")
	}
	hash := sha256.New()
	size, err := io.Copy(hash, io.LimitReader(file, maxRuntimeBinaryBytes+1))
	after, statErr := executableFile(canonical)
	if err != nil || statErr != nil || size <= 0 || size > maxRuntimeBinaryBytes || !samePiInfo(before, after) || after.Size() != size {
		return executableIdentity{}, errors.New("runtime hashing failed")
	}
	var digest [sha256.Size]byte
	copy(digest[:], hash.Sum(nil))
	return executableIdentity{path: canonical, identity: after, mode: after.Mode(), size: size, digest: digest}, nil
}

// sameExecutable reports whether two inspections observed the same file.
func sameExecutable(left, right executableIdentity) bool {
	return os.SameFile(left.identity, right.identity) && left.mode == right.mode && left.size == right.size && left.digest == right.digest
}

func inspectPi(path string) (boundPi, error) {
	found, err := inspectExecutable(path)
	if err != nil {
		return boundPi{}, err
	}
	return boundPi{path: found.path, identity: found.identity, mode: found.mode, size: found.size, digest: found.digest}, nil
}

func executableFile(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 || info.Size() <= 0 || info.Size() > maxRuntimeBinaryBytes {
		return nil, errors.New("runtime is not an executable regular file")
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
	return parseVersionWith(capture, versionOutput)
}

// parseVersionWith bounds and matches one fixed version capture against the
// version line contract of a single runtime. The bounds are shared; only the
// expected line shape is backend-specific.
func parseVersionWith(capture versionCapture, contract *regexp.Regexp) (string, bool) {
	if !capture.complete() || capture.exitCode != 0 || len(capture.stdout) == 0 || len(capture.stdout) > maxVersionOutputBytes || len(capture.stderr) > maxVersionOutputBytes {
		return "", false
	}
	match := contract.FindStringSubmatch(strings.TrimSpace(string(capture.stdout)))
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

type availabilityCommand uint8

const (
	availabilityModel availabilityCommand = iota + 1
	availabilityAuth
)

var availabilityProbeTimeout = 10 * time.Second

type probeCapture struct {
	started, startFailed, timedOut, waitFailed bool
	exitCode                                   int
	stdout, stderr                             []byte
	stdoutTruncated, stderrTruncated           bool
}

func (capture probeCapture) complete() bool {
	return capture.started && !capture.startFailed && !capture.timedOut && !capture.waitFailed && !capture.stdoutTruncated && !capture.stderrTruncated
}

type availabilityProbes struct {
	modelCapture probeCapture
	model        ModelProbeResult
	authCapture  probeCapture
	auth         AuthProbeResult
}

// probeAvailability runs the fixed model and no-refresh auth probes once in order.
func probeAvailability(ctx context.Context, bound boundPi, provider, model string) availabilityProbes {
	modelCapture, modelResult := probeModelAvailability(ctx, bound, provider, model)
	authCapture, authResult := probeAuthAvailability(ctx, bound)
	return availabilityProbes{
		modelCapture: modelCapture,
		model:        modelResult,
		authCapture:  authCapture,
		auth:         authResult,
	}
}

func probeModelAvailability(ctx context.Context, bound boundPi, provider, model string) (probeCapture, ModelProbeResult) {
	capture := runAvailabilityCommand(ctx, bound, availabilityModel)
	return capture, ProbeModel(ModelProbeInput{
		Stdout:   capture.stdout,
		ExitCode: capture.exitCode,
		Complete: capture.complete(),
	}, provider, model)
}

func probeAuthAvailability(ctx context.Context, bound boundPi) (probeCapture, AuthProbeResult) {
	capture := runAvailabilityCommand(ctx, bound, availabilityAuth)
	return capture, ProbeAuth(AuthProbeInput{
		Stdout:    capture.stdout,
		ExitCode:  capture.exitCode,
		Complete:  capture.complete(),
		NoRefresh: true,
	})
}

func runAvailabilityCommand(ctx context.Context, bound boundPi, kind availabilityCommand) probeCapture {
	arguments, stdoutLimit, stderrLimit, validKind := fixedAvailabilityCommand(kind)
	if ctx == nil || !validKind || !validProbeBinding(bound) {
		return probeCapture{startFailed: true}
	}
	return executeProbeCommand(ctx, bound.path, bound.cwd, arguments, stdoutLimit, stderrLimit)
}

// executeProbeCommand is a private fixed-command seam for local tests. It
// defaults to the real runner, so every test that exercises a probe against a
// helper process still goes through the same code path production does.
var executeProbeCommand = runProbeCommand

// runProbeCommand runs one fixed, bounded, out-of-band probe command and
// returns its complete capture. It is the shared body every backend's
// availability probe launches through, so no adapter can widen the isolation
// the QA vertical depends on: a minimal environment, one child process, a
// fixed timeout, and independently capped stdout and stderr.
//
// The caller chooses the tokens; it never chooses how they run. That is the
// same split the Backend port makes for the report run itself.
func runProbeCommand(ctx context.Context, binary, cwd string, arguments []string, stdoutLimit, stderrLimit int) probeCapture {
	runContext, cancel := context.WithTimeout(ctx, availabilityProbeTimeout)
	defer cancel()
	stdout := cappedWriter{maximum: stdoutLimit}
	stderr := cappedWriter{maximum: stderrLimit}
	command := exec.CommandContext(runContext, binary, arguments...)
	command.Dir = cwd
	command.Env = minimalEnvironment()
	command.Stdout = &stdout
	command.Stderr = &stderr
	command.WaitDelay = pipeWaitDelay
	if err := command.Start(); err != nil {
		return probeCapture{startFailed: true}
	}
	capture := probeCapture{started: true}
	waitErr := command.Wait()
	capture.stdout, capture.stderr = stdout.bytes, stderr.bytes
	capture.stdoutTruncated, capture.stderrTruncated = stdout.truncated, stderr.truncated
	capture.timedOut = errors.Is(runContext.Err(), context.DeadlineExceeded)
	var exitError *exec.ExitError
	if errors.As(waitErr, &exitError) {
		capture.exitCode = exitError.ExitCode()
	} else if waitErr != nil {
		capture.waitFailed = true
	}
	return capture
}

func fixedAvailabilityCommand(kind availabilityCommand) ([]string, int, int, bool) {
	switch kind {
	case availabilityModel:
		return []string{"--list-models"}, maxModelOutputBytes, maxAuthOutputBytes, true
	case availabilityAuth:
		return []string{"auth", "check", "--provider", "nan", "--json", "--no-refresh"}, maxAuthOutputBytes, maxAuthOutputBytes, true
	default:
		return nil, 0, 0, false
	}
}

func validProbeBinding(bound boundPi) bool {
	version, valid := parseVersion(bound.versionCapture)
	return absolutePaths(bound.path, bound.cwd) && valid && bound.version == version && wellFormedRuntimeVersion(version)
}

// wellFormedRuntimeVersion reports whether a recorded runtime version has the
// shape every backend's receipt requires. No backend pins one exact build; see
// bindPi for why.
func wellFormedRuntimeVersion(version string) bool {
	return runtimeVersionShape.MatchString(version)
}

var runtimeVersionShape = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
