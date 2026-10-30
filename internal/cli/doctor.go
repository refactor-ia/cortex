// Package cli provides Cortex command orchestration without process ownership.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/refactor-ia/cortex/internal/installobserve"
	"github.com/refactor-ia/cortex/internal/installstate"
	"github.com/refactor-ia/cortex/internal/qaadmission"
	"github.com/refactor-ia/cortex/internal/qapi"
	"github.com/refactor-ia/cortex/internal/qarole"
	"github.com/refactor-ia/cortex/internal/qaroute"
	"github.com/refactor-ia/cortex/internal/runtimecompat"
	"github.com/refactor-ia/cortex/internal/runtimematrix"
	"github.com/refactor-ia/cortex/internal/runtimeprobe"
	"github.com/refactor-ia/cortex/internal/skilldest"
	"github.com/refactor-ia/cortex/internal/skillroot"
	"github.com/refactor-ia/cortex/internal/uninstalltxn"
)

const (
	exitOK          = 0
	exitUnknown     = 2
	exitConflict    = 3
	exitTransaction = 4
	exitUsage       = 64
	exitFailure     = 70
)

type uninstallDependencies struct {
	resolveRoots func() ([]skillroot.UninstallRoot, error)
	rootExists   func(string) (bool, error)
	observe      func(installobserve.UninstallRoot, installobserve.Options) (installobserve.UninstallObservation, error)
	applyGroup   func([]uninstalltxn.GroupRequest, string, string) error
	backupName   func() string
}

type uninstallPreflight struct {
	root        skillroot.UninstallRoot
	observation installobserve.UninstallObservation
	status      string
}

// Run executes one Cortex command with the supplied runtime probe seam.
// A nil runner selects the constrained production system probe.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer, runner runtimeprobe.Runner) int {
	return runWithDependencies(ctx, args, stdout, stderr, runner, defaultInstallDependencies(), defaultUninstallDependencies())
}

func runWithUninstallDependencies(ctx context.Context, args []string, stdout, stderr io.Writer, runner runtimeprobe.Runner, uninstall uninstallDependencies) int {
	return runWithDependencies(ctx, args, stdout, stderr, runner, defaultInstallDependencies(), uninstall)
}

func runWithDependencies(ctx context.Context, args []string, stdout, stderr io.Writer, runner runtimeprobe.Runner, install installDependencies, uninstall uninstallDependencies) int {
	if len(args) == 0 || len(args) == 1 && (args[0] == "help" || args[0] == "--help") {
		return writeHelp(stdout, stderr, rootHelp)
	}
	if len(args) != 1 && args[0] != "qa" && args[0] != "update" && args[0] != "install" && args[0] != "uninstall" && args[0] != "doctor" {
		writeError(stderr, "invalid_command")
		return exitUsage
	}
	switch args[0] {
	case "--version":
		return runVersion(stdout, stderr)
	case "doctor":
		return runDoctorCommand(ctx, args[1:], stdout, stderr, runner, install.policy, skillroot.ResolveSystemUninstallRoots)
	case "qa":
		return runQA(ctx, args[1:], stdout, stderr)
	case "update":
		// Bare update keeps the all-runtime transaction; any argument selects
		// the explicit single-runtime update, which owns its own flag parsing.
		if len(args) > 1 {
			return runUpdate(ctx, args, stdout, stderr, runner)
		}
		return runInstall(ctx, stdout, stderr, runner, args[0], install)
	case "install":
		return runInstallCommand(ctx, args, stdout, stderr, runner, install)
	case "uninstall":
		return runUninstallCommand(args[1:], stdout, stderr, uninstall)
	default:
		writeError(stderr, "invalid_command")
		return exitUsage
	}
}

// runDoctorCommand dispatches the probed doctor and the directory-target
// doctor (issue #343). A --target-dir report skips the runtime matrix: the
// directory has no host to probe, and its presence, writability, and prior
// ownership are the whole answer to whether an install into it can proceed.
func runDoctorCommand(ctx context.Context, args []string, stdout, stderr io.Writer, runner runtimeprobe.Runner, policy runtimecompat.Policy, resolveRoots func() ([]skillroot.UninstallRoot, error)) int {
	targetDir := ""
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--target-dir":
			if targetDir != "" || index+1 >= len(args) {
				writeError(stderr, "invalid_command")
				return exitUsage
			}
			targetDir = args[index+1]
			index++
		default:
			writeError(stderr, "invalid_command")
			return exitUsage
		}
	}
	if targetDir == "" {
		return runDoctor(ctx, stdout, stderr, runner, policy, resolveRoots)
	}
	return runDoctorDirectoryTarget(stdout, targetDir)
}

// runDoctorDirectoryTarget reports one directory target as distinct explicit
// states: presence, writability, and prior ownership. An unwritable directory
// or an unreadable prior state conflicts exactly the way install would.
func runDoctorDirectoryTarget(stdout io.Writer, targetDir string) int {
	line := "target=" + targetDir
	code := exitOK
	info, err := os.Stat(targetDir)
	switch {
	case os.IsNotExist(err):
		line += " presence=absent"
	case err != nil || !info.IsDir():
		line += " presence=unreadable"
		code = exitConflict
	default:
		line += " presence=present"
		writable := false
		probe, probeErr := os.CreateTemp(targetDir, ".cortex-doctor-*")
		if probeErr == nil {
			writable = true
			name := probe.Name()
			_ = probe.Close()
			_ = os.Remove(name)
		}
		if writable {
			line += " writability=writable"
		} else {
			line += " writability=unwritable"
			code = exitConflict
		}
		line += " ownership=" + doctorTargetOwnership(targetDir)
	}
	if _, err := io.WriteString(stdout, line+"\n"); err != nil {
		return exitFailure
	}
	return code
}

// doctorTargetOwnership classifies the prior state of a directory target:
// unowned (no state file), owned (a canonical state this build decodes), or
// invalid (a state file it cannot classify, which blocks install like the
// canonical prior-state blocker does).
func doctorTargetOwnership(targetDir string) string {
	state, err := os.ReadFile(filepath.Join(targetDir, ".cortex", "install-state.json"))
	if err != nil {
		return "unowned"
	}
	manifest, err := installstate.Decode(state)
	if err != nil || manifest.RuntimeID() != runtimematrix.RuntimeDirectory {
		return "invalid"
	}
	return "owned"
}

func runDoctor(ctx context.Context, stdout, stderr io.Writer, runner runtimeprobe.Runner, policy runtimecompat.Policy, resolveRoots func() ([]skillroot.UninstallRoot, error)) int {
	matrix, err := probeCompatibilityMatrix(ctx, runner, policy)
	if err != nil {
		writeError(stderr, "probe_failed")
		return exitFailure
	}
	if _, err := io.WriteString(stdout, doctorReport(ctx, matrix)); err != nil {
		writeError(stderr, "output_failed")
		return exitFailure
	}
	// The prior install-state probe keeps doctor exit 0 honest: a prior state
	// this build cannot classify blocks install, so the same blocking condition
	// install will hit is named here with its remedy (issue #281).
	notes, blocked := doctorPriorStateBlocker(resolveRoots)
	if notes != "" {
		if _, err := io.WriteString(stdout, notes+"\n"); err != nil {
			writeError(stderr, "output_failed")
			return exitFailure
		}
	}
	// The exit code answers whether an install can proceed, not whether every
	// runtime is certified. An admitted uncertified version is disclosed in the
	// report and does not make the host uncertain.
	for _, decision := range matrix.Decisions {
		switch decision.Outcome {
		case runtimematrix.OutcomeAbsent, runtimematrix.OutcomePresentCompatible, runtimematrix.OutcomePresentUncertified:
		default:
			return exitUnknown
		}
	}
	if blocked {
		return exitConflict
	}
	return exitOK
}

// doctorPriorStateBlocker reads each resolved runtime root's install-state.json
// and reports the schemas this build cannot classify. It parses only the
// schemaVersion field: classification stays in install; doctor only needs the
// schema number to name the same blocking condition install will hit.
func doctorPriorStateBlocker(resolveRoots func() ([]skillroot.UninstallRoot, error)) (string, bool) {
	roots, err := resolveRoots()
	if err != nil {
		return "", false
	}
	notes := make([]string, 0, len(roots))
	for _, root := range roots {
		data, err := os.ReadFile(filepath.Join(root.RootPath(), ".cortex", "install-state.json"))
		if err != nil {
			continue
		}
		var version struct {
			SchemaVersion *int `json:"schemaVersion"`
		}
		if json.Unmarshal(data, &version) != nil || version.SchemaVersion == nil {
			continue
		}
		if installobserve.PriorSchemaSupported(*version.SchemaVersion) {
			continue
		}
		notes = append(notes, fmt.Sprintf(
			"note=install_state_schema_unsupported runtime=%s prior_schema=%d supported="+installobserve.SupportedPriorSchemas+" remedy=adopt the prior state through a guided update or archive it and reinstall",
			root.RuntimeID(), *version.SchemaVersion))
	}
	return strings.Join(notes, "\n"), len(notes) > 0
}

const (
	qaUsage = "usage: cortex qa run --role <role> --request <file> --catalog <dir> [--backend pi|claude|opencode] [--evidence <file.json>] [--profile <id>]\n" +
		"usage: cortex qa setup --catalog <dir> --provider <p> --model <m> [--effort <e>]\n"
	qaHelp = qaUsage + "\nQA evaluates evidence and does not run tests.\n" +
		"Write a short, bounded plain-text request (request.txt):\n" +
		"printf 'R1: Responses must finish within 100 ms.\\nR2: Responses must wait at least 500 ms.\\nIdentify the contradiction; do not edit files.\\n' > request.txt\n" +
		"Use a catalog root directory containing catalog.json (for example, ./catalog):\n" +
		"cortex qa run --role requirements-analyst --request request.txt --catalog ./catalog\n" +
		"Optional evidence sidecar (--evidence evidence.json): a JSON array of execution records\n" +
		"(up to 8 records, <= 48 KiB total; custom top-level shapes like {\"schema\":...} are rejected).\n" +
		"Record fields: id (required); command, exit_code, output_tail (<= 4096 bytes), provenance, truncated.\n" +
		"Example:\n" +
		`[{"id":"selected-test","command":"go test ./...","exit_code":0,"output_tail":"ok example","provenance":"caller fixture","truncated":false}]` + "\n" +
		"Schema reference: docs/operating.md\n" +
		"Optional --profile <id> selects a route profile; without it the file's defaultProfile applies.\n" +
		"A refused preflight names its stage: route_resolution, actor_admission, model_probe or\n" +
		"evidence_admission; a refused profile also lists profile_valid_ids from the profile file.\n" +
		"To audit a code diff, prefix every diff line with '>' (blockquote): quoted lines are content,\n" +
		"so frame directives stay rejected while the quoted diff passes validation (issue #305).\n"
	rootHelp = "usage: cortex <command> [arguments]\ncommands: doctor, install, update, uninstall, qa\noptions: --version, --help\n" +
		"install [--target-dir <dir>]: configure every compatible present runtime, or project the\ncatalog as plain SKILL.md files into an existing directory (Agent Skills layout)\n" +
		"doctor [--target-dir <dir>]: probe runtimes, or report one directory target\n" +
		"uninstall [--target-dir <dir>]: remove owned files from every runtime root, or from <dir>\n\n" + qaHelp
	// qaReportNote surfaces runtime prerequisites on non-catalog failures. It
	// names no route: a refusal happens with a profile route resolved, with a
	// placeholder route, or with no route resolved at all, so claiming one here
	// was wrong for every failure it accompanied (#374 S5). The stage line and
	// the routed pair are printed where the pipeline actually knows them.
	//
	// It names the three backends rather than one runtime, and pins no version
	// on any of them: the route policy admits all three, and decision A of the
	// backend port removed the exact version pin from every binding, so a note
	// that still demanded "Pi 0.85.1" named a prerequisite that no longer
	// exists and hid two that do. `cortex doctor` is named because it is the
	// command that answers, per runtime, which of them can actually run — for
	// the embedded catalog and the policy default route only.
	qaReportNote = "note=prerequisites: one of the pi, claude or opencode runtimes with a usable backend and installed cortex assets; run cortex doctor for per-runtime backend availability; cortex applies no model fallback and owns no automatic configuration\n"
)

func writeHelp(stdout, stderr io.Writer, help string) int {
	if _, err := io.WriteString(stdout, help); err != nil {
		writeError(stderr, "output_failed")
		return exitFailure
	}
	return exitOK
}

// errUnknownQABackend refuses a backend token the route policy does not admit.
var errUnknownQABackend = errors.New("unknown qa backend")

// qaPiResolver is the runtime binary resolution seam; nil selects the
// production constrained lookup for whichever backend was selected.
var qaPiResolver qapi.PathResolver

// runQA executes one local QA report command. It never mutates user
// configuration and never claims admission.
func runQA(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "setup" {
		return runQASetup(ctx, args[1:], stdout, stderr)
	}
	if len(args) == 0 || len(args) == 1 && args[0] == "--help" || len(args) == 2 && args[0] == "run" && args[1] == "--help" {
		return writeHelp(stdout, stderr, qaHelp)
	}
	// The three optional flags are at most one pair each: 7 required arguments
	// plus 3 pairs. Nothing else may be appended.
	if len(args) < 7 || len(args) > 13 || (len(args)-7)%2 != 0 || args[0] != "run" || args[1] != "--role" || args[3] != "--request" || args[4] == "" || args[5] != "--catalog" || args[6] == "" {
		writeError(stderr, "invalid_arguments")
		_, _ = io.WriteString(stderr, qaUsage)
		return exitUsage
	}
	backend, evidencePath, profile := "pi", "", ""
	seen := map[string]bool{}
	for index := 7; index < len(args); index += 2 {
		flag, value := args[index], args[index+1]
		if value == "" || seen[flag] || flag != "--backend" && flag != "--evidence" && flag != "--profile" {
			writeError(stderr, "invalid_arguments")
			_, _ = io.WriteString(stderr, qaUsage)
			return exitUsage
		}
		seen[flag] = true
		// Each optional flag owns its own destination: --profile names a route
		// profile id, which is not an evidence path, and neither one clobbers
		// the other (#374 S6).
		switch flag {
		case "--backend":
			backend = value
		case "--evidence":
			evidencePath = value
		case "--profile":
			profile = value
		}
	}
	if _, known := qaroute.RuntimeFor(backend); !known {
		writeError(stderr, "invalid_arguments")
		_, _ = io.WriteString(stderr, qaUsage)
		return exitUsage
	}
	role := qarole.RoleID(args[2])
	if _, err := qarole.ValidateSquad([]qarole.RoleID{role}); err != nil {
		writeError(stderr, "invalid_arguments")
		_, _ = io.WriteString(stderr, qaUsage)
		return exitUsage
	}
	task, err := readRequestTask(args[4])
	if err != nil {
		return qaFailure(stderr, "invalid_request")
	}
	var evidence []byte
	if evidencePath != "" {
		evidence, err = readQAFile(evidencePath, qapi.MaxEvidenceBytes)
		if err != nil {
			failure := qaFailure(stderr, "invalid_request")
			if os.IsNotExist(err) || errors.Is(err, os.ErrNotExist) {
				_, _ = fmt.Fprintf(stderr, "evidence_field=path evidence_reason=file_not_found\n")
			} else if stat, statErr := os.Stat(evidencePath); statErr == nil && stat.Size() > int64(qapi.MaxEvidenceBytes) {
				_, _ = fmt.Fprintf(stderr, "evidence_field=top-level evidence_reason=exceeds_size\n")
				_, _ = fmt.Fprintf(stderr, "evidence_detail=file size %d exceeds %d bytes\n", stat.Size(), qapi.MaxEvidenceBytes)
			} else if stat, statErr := os.Stat(evidencePath); statErr == nil && stat.IsDir() {
				_, _ = fmt.Fprintf(stderr, "evidence_field=path evidence_reason=not_a_file\n")
			} else {
				_, _ = fmt.Fprintf(stderr, "evidence_field=path evidence_reason=file_unavailable\n")
			}
			return failure
		}
	}
	cwd, err := os.Getwd()
	catalogRoot, absErr := filepath.Abs(args[6])
	if err != nil || absErr != nil {
		return qaFailure(stderr, "invalid_request")
	}
	installRoot, err := qaInstallRoot(backend)
	if err != nil {
		return qaFailure(stderr, "actor_unavailable")
	}
	report, code, err := qapi.RunLocalReportOn(ctx, qapi.ReportRequest{
		Role: role, Profile: profile, CatalogRoot: catalogRoot, InstallRoot: installRoot,
		CurrentDirectory: cwd, Task: task, Evidence: evidence, TimeoutSeconds: qaadmission.DefaultTimeoutSeconds,
	}, backend, qaPiResolver)
	if err != nil || code != "" {
		var catalogError *qapi.CatalogLoadError
		if errors.As(err, &catalogError) {
			writeError(stderr, string(code))
			_, _ = io.WriteString(stderr, "stage=catalog expected=catalog root containing catalog.json next_check=cortex qa --help\n")
			return exitFailure
		}
		failure := qaFailure(stderr, string(code))
		if stage := qaRefusalStage(string(code), err); stage != "" {
			_, _ = fmt.Fprintf(stderr, "stage=%s\n", stage)
		}
		var evidenceErr *qapi.EvidenceValidationError
		if errors.As(err, &evidenceErr) {
			if evidenceErr.Field != "" {
				_, _ = fmt.Fprintf(stderr, "evidence_field=%s evidence_reason=%s\n", sanitizeDiagnosticField(evidenceErr.Field), evidenceErr.Reason)
			} else {
				_, _ = fmt.Fprintf(stderr, "evidence_reason=%s\n", evidenceErr.Reason)
			}
			if evidenceErr.Detail != "" {
				_, _ = fmt.Fprintf(stderr, "evidence_detail=%s\n", evidenceErr.Detail)
			}
		}
		if diagnostic := qaAdmissionDiagnostic(err); diagnostic != "" {
			_, _ = io.WriteString(stderr, diagnostic)
		}
		if err != nil && strings.Contains(err.Error(), "stream_retained=") {
			_, _ = fmt.Fprintf(stderr, "%s\n", err.Error())
		}
		if unavailable := (*qapi.BackendUnavailableError)(nil); errors.As(err, &unavailable) {
			_, _ = fmt.Fprintf(stderr, "route_provider=%s route_model=%s\n", sanitizeRouteIdentity(unavailable.Provider), sanitizeRouteIdentity(unavailable.Model))
		}
		if code == qaadmission.CodeModelUnavailable {
			_, _ = io.WriteString(stderr, qaModelUnavailableNote())
			_, _ = io.WriteString(stderr, qaRouteSetupNote(catalogRoot))
		}
		if code == qaadmission.CodeAuthNotReady {
			_, _ = io.WriteString(stderr, qaAuthNotReadyNote())
		}
		if code == "route_incomplete" {
			_, _ = fmt.Fprintf(stderr, "route_missing_backend=%s remedy=cortex qa setup --catalog %s --backend %s --provider <p> --model <m>\n", backend, catalogRoot, backend)
		}
		if code == qaadmission.CodeProfileInvalid {
			_, _ = io.WriteString(stderr, qaProfileDiagnostic(profile, catalogRoot))
		}
		if code == qaadmission.CodeNormalizationFailed {
			var diagnostic *qapi.ReportNormalizationError
			if errors.As(err, &diagnostic) {
				_, _ = fmt.Fprintf(stderr, "normalization_stage=%s normalization_reason=%s\n", diagnostic.Stage, diagnostic.Reason)
				if diagnostic.Detail != "" {
					_, _ = fmt.Fprintf(stderr, "normalization_detail=%s\n", diagnostic.Detail)
				}
			}
		}
		return failure
	}
	if _, err := io.WriteString(stdout, report+"\n"); err != nil {
		writeError(stderr, "output_failed")
		return exitFailure
	}
	return exitOK
}

// sanitizeDiagnosticField renders a diagnostic field label (which may come from
// caller-supplied JSON key names) safe for terminal output: only printable ASCII
// word characters survive; every other byte collapses to "?" so escape and
// control sequences cannot reach the operator's terminal.
func sanitizeDiagnosticField(field string) string {
	var b strings.Builder
	b.Grow(len(field))
	for _, r := range field {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.' {
			b.WriteRune(r)
			continue
		}
		b.WriteByte('?')
	}
	return b.String()
}

// sanitizeRouteIdentity renders one routed provider or model token for a
// terminal diagnostic. Unlike a field label these are values an operator must
// be able to compare with the runtime's model table, so they are escaped
// rather than flattened: printable ASCII passes through, and every other byte
// becomes \xNN. A profile is a caller-owned file, so this is what keeps an
// escape sequence in a provider or model name out of the operator's terminal
// (#374 S5). The length bound is the same one qaroute applies.
func sanitizeRouteIdentity(value string) string {
	if value == "" || len(value) > 128 {
		return "<redacted>"
	}
	var b strings.Builder
	b.Grow(len(value))
	for index := 0; index < len(value); index++ {
		character := value[index]
		if character >= 0x20 && character < 0x7f && character != '\\' {
			b.WriteByte(character)
			continue
		}
		_, _ = fmt.Fprintf(&b, "\\x%02x", character)
	}
	return b.String()
}

func qaFailure(stderr io.Writer, code string) int {
	writeError(stderr, code)
	_, _ = io.WriteString(stderr, qaReportNote)
	return exitFailure
}

// The four preflight questions a `qa run` refusal can answer (#374 S4). This
// is a fixed vocabulary and not a new set of gates: each token names the
// pipeline step whose existing error or code refused the run, and nothing
// else. evidence_admission covers the evidence sidecar validation, which runs
// before the route; route_resolution covers every qaroute failure code;
// actor_admission covers the typed installed-asset refusal; model_probe covers
// the backend availability verdict. Post-launch outcomes (execution, stream,
// normalization) are not preflight questions and print no stage line.
const (
	qaStageRouteResolution   = "route_resolution"
	qaStageActorAdmission    = "actor_admission"
	qaStageModelProbe        = "model_probe"
	qaStageEvidenceAdmission = "evidence_admission"
)

// qaRefusalStage classifies one refused report run by the existing error and
// code it already carries. It never guesses a root cause: when no preflight
// step refused, it reports no stage at all.
func qaRefusalStage(code string, err error) string {
	var evidence *qapi.EvidenceValidationError
	if errors.As(err, &evidence) {
		return qaStageEvidenceAdmission
	}
	var unavailable *qapi.BackendUnavailableError
	if errors.As(err, &unavailable) {
		return qaStageModelProbe
	}
	var admission *installobserve.AdmissionUnavailableError
	if errors.As(err, &admission) {
		return qaStageActorAdmission
	}
	switch code {
	case "profile_invalid", "route_incomplete", "route_disallowed", "non_nan_route":
		return qaStageRouteResolution
	}
	return ""
}

// qaModelUnavailableNote names the problem behind a refused routed pair and
// its actual remedy (issue #339): qa runs probe pi with --no-extensions, so a
// provider registered only by a Pi extension is invisible to qa forever, and
// the fix is a core models.json entry — not another route profile edit.
func qaModelUnavailableNote() string {
	return "remedy=the routed provider/model was not found in the core pi model table; a provider registered only by a pi extension is invisible to qa (qa probes with --no-extensions); add a core models.json entry (baseUrl, api, apiKey) for it or route to a provider pi lists; pi --no-extensions --list-models shows what qa can see\n"
}

// qaAuthNotReadyNote names the actual remedy for an authenticated provider
// that is not ready (issue #339): complete the login in an interactive pi
// session, then rerun. The no-refresh auth check is the probe that answered.
func qaAuthNotReadyNote() string {
	return "remedy=the routed provider exists but its credentials are not ready; enter pi and complete the login for the provider, then rerun cortex qa run\n"
}

// qaRouteSetupNote names the user-owned profile file and its shape, so an
// unavailable pinned model leads to configuration instead of guessing. The
// fleet ships no models: each owner routes with their own (issue #298).
func qaRouteSetupNote(catalogRoot string) string {
	example := `{"schemaVersion":1,"defaultProfile":"my-route","profiles":{"my-route":{"routes":{"evidence-auditor":{"pi":{"provider":"nan","model":"glm5.3","effort":"high"}}}}}}`
	return fmt.Sprintf("route_setup=%s/cortex/qa-profiles.json example=%s\n", catalogRoot, example)
}

// qaProfileDiagnostic names the profile a refused run selected and what the
// catalog's profile source actually offers (#374 S6), so an unknown id leads to
// the file instead of a guess. It reads the same safe snapshot the route policy
// reads — never a second, weaker path — and every caller-supplied token is
// bounded and escaped for the operator's terminal. The valid ids are the ones
// the route policy itself accepts, so a refusal enumerates what the operator
// can actually select.
func qaProfileDiagnostic(selected, catalogRoot string) string {
	fields := make([]string, 0, 5)
	if selected != "" {
		fields = append(fields, "profile_selected="+sanitizeProfileToken(selected))
	}
	if defaultID, ids, ok := qapi.ProfileSelection(catalogRoot); ok {
		fields = append(fields, "profile_source=readable", "profile_default="+defaultID)
		fields = append(fields, "profile_valid_ids="+strings.Join(ids, ","))
	} else {
		fields = append(fields, "profile_source=unusable")
	}
	fields = append(fields, "route_setup="+filepath.Join(catalogRoot, "cortex", "qa-profiles.json"))
	return strings.Join(fields, " ") + "\n"
}

// sanitizeProfileToken bounds one caller-supplied profile id for a diagnostic
// line: admitted ids are lowercase words joined by dashes, so anything else is
// replaced rather than echoed.
func sanitizeProfileToken(selected string) string {
	if len(selected) == 0 || len(selected) > 64 {
		return "<redacted>"
	}
	for _, character := range selected {
		allowed := character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '-'
		if !allowed {
			return "<redacted>"
		}
	}
	return selected
}

// qaAdmissionDiagnostic renders one bounded diagnostic line for admission
// failures, naming the stage and the expected/found identity (issue #294).
// Digests and the catalog fingerprint are the only values it may carry.
func qaAdmissionDiagnostic(err error) string {
	var unavailable *installobserve.AdmissionUnavailableError
	if !errors.As(err, &unavailable) {
		return ""
	}
	if unavailable.Expected == "" && unavailable.Found == "" {
		return fmt.Sprintf("admission_stage=%s\n", unavailable.Stage)
	}
	return fmt.Sprintf("admission_stage=%s expected=%s found=%s\n", unavailable.Stage, unavailable.Expected, unavailable.Found)
}

func readRequestTask(path string) ([]byte, error) {
	return readQAFile(path, qaadmission.MaxTaskBytes)
}

func readQAFile(path string, maximum int) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > int64(maximum) {
		return nil, fmt.Errorf("request is not a bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("request file unavailable")
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > int64(maximum) {
		return nil, fmt.Errorf("request is not a bounded regular file")
	}
	data, err := io.ReadAll(io.LimitReader(file, int64(maximum)+1))
	if err != nil || len(data) > maximum {
		return nil, fmt.Errorf("request exceeds bound")
	}
	return data, nil
}

func probeCompatibilityMatrix(ctx context.Context, runner runtimeprobe.Runner, policy runtimecompat.Policy) (runtimematrix.Matrix, error) {
	reports, err := probe(ctx, runner)
	if err != nil {
		return runtimematrix.Matrix{}, err
	}
	observations, err := policy.Evaluate(reports)
	if err != nil {
		return runtimematrix.Matrix{}, err
	}
	return runtimematrix.Decide(observations)
}

// The note is separate from the per-runtime records: their machine-readable
// fields and order stay intact, while the scope and safe next check remain
// visible even if every probed backend reports ready.
//
// It states the route scope explicitly (#374 S7). Doctor resolves the policy
// default route against the binary's embedded catalog: it reads no caller
// catalog and no `--profile` route, so a maintainer must not read its answer as
// an evaluation of either. The availability lines name the routed pair only
// when the probe's verdict carried one, which is why the scope belongs here
// rather than guessed per line.
const doctorScopeNote = "note=qa_availability is backend/model readiness for qa_probe_role only (not_probed means no probe); qa_route_scope=embedded_default_route means doctor probes the binary's embedded catalog and the policy default route, never a caller catalog or a --profile route; it does not check asset ownership or install/update conflicts; for a separate read-only ownership plan run cortex update --runtime pi --catalog ./catalog\n"

// doctorReport extends the shared per-runtime line with what doctor alone is
// asked for: whether that runtime's QA backend could actually run a report.
//
// It extends rather than restructures. The presence and compatibility fields
// keep their exact existing spelling and order, and the QA fields are appended
// after them, because presence, compatibility and backend availability are
// three separate facts about one runtime and a maintainer needs to be able to
// read them apart. installRuntimeLine stays untouched and shared, so the
// install and update reports are unchanged: backend availability is doctor's
// question, not a field every command should grow.
//
// It stays read-only. Only the runtimes the matrix reports present are probed,
// and a probe is that backend's own fixed availability commands — never a
// report run, and never a configuration write.
func doctorReport(ctx context.Context, matrix runtimematrix.Matrix) string {
	var output strings.Builder
	for _, decision := range matrix.Decisions {
		line := installRuntimeLine(decision.ID, decision.Outcome, decision.Action, false)
		present := decision.Outcome != runtimematrix.OutcomeAbsent
		output.WriteString(strings.TrimSuffix(line, "\n") + qaBackendLine(ctx, decision.ID, present) + "\n")
	}
	output.WriteString(doctorScopeNote)
	return output.String()
}

func defaultUninstallDependencies() uninstallDependencies {
	return uninstallDependencies{
		resolveRoots: skillroot.ResolveSystemUninstallRoots,
		rootExists:   uninstallRootExists,
		observe:      installobserve.ObserveUninstall,
		applyGroup:   uninstalltxn.ApplyGroup,
		backupName:   nextUninstallBackupName,
	}
}

// runUninstallCommand dispatches the canonical-roots uninstall and the
// directory-target uninstall (issue #342). The directory mode takes an
// explicit path because a directory target has no canonical user location to
// scan: exactly the directory the caller installed into is inspected, and
// only the files its ownership records name are removed.
func runUninstallCommand(args []string, stdout, stderr io.Writer, deps uninstallDependencies) int {
	targetDir := ""
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--target-dir":
			if targetDir != "" || index+1 >= len(args) {
				writeError(stderr, "invalid_command")
				return exitUsage
			}
			targetDir = args[index+1]
			index++
		default:
			writeError(stderr, "invalid_command")
			return exitUsage
		}
	}
	if targetDir == "" {
		return runUninstall(stdout, stderr, deps)
	}
	return runDirectoryUninstall(targetDir, stdout, stderr, deps)
}

// runDirectoryUninstall removes exactly the Cortex-owned files of one
// previously installed directory target and never touches foreign files.
func runDirectoryUninstall(targetDir string, stdout, stderr io.Writer, deps uninstallDependencies) int {
	root, err := skillroot.NewUninstallRoot(runtimematrix.RuntimeDirectory, skilldest.RootKindDirectory, targetDir)
	if err != nil {
		writeError(stderr, "invalid_target")
		return exitUsage
	}
	trusted, err := installobserve.NewUninstallRoot(root.RuntimeID(), root.RootKind(), root.RootPath())
	if err != nil {
		writeError(stderr, "invalid_target")
		return exitUsage
	}
	preflight := make([]uninstallPreflight, 0, 1)
	exists, err := deps.rootExists(root.RootPath())
	if err != nil {
		writeError(stderr, "uninstall_observation_failed")
		return exitFailure
	}
	if !exists {
		preflight = append(preflight, uninstallPreflight{root: root, status: "not_installed"})
		return writeUninstallResult(stdout, stderr, preflight, exitOK)
	}
	observation, err := deps.observe(trusted, installobserve.DefaultOptions())
	if err != nil {
		if len(observation.UnsupportedActorPaths()) > 0 && observation.MatchesRoot(root.RootPath()) && observation.MatchesRuntime(root.RuntimeID()) {
			preflight = append(preflight, uninstallPreflight{root: root, observation: observation, status: "blocked"})
			return writeUninstallResult(stdout, stderr, preflight, exitConflict)
		}
		writeError(stderr, "uninstall_observation_failed")
		return exitFailure
	}
	status := "ready"
	if len(observation.Records()) == 0 {
		status = "not_installed"
	} else if !observation.Ready() {
		status = "conflict"
	}
	preflight = append(preflight, uninstallPreflight{root: root, observation: observation, status: status})
	if status != "ready" {
		return writeUninstallResult(stdout, stderr, preflight, exitConflict)
	}
	backupRoot := filepath.Join(root.RootPath(), ".cortex")
	if err := deps.applyGroup([]uninstalltxn.GroupRequest{{RuntimeID: root.RuntimeID(), Root: root.RootPath(), Observation: observation}}, backupRoot, deps.backupName()); err != nil {
		preflight[0].status = "failed"
		writeError(stderr, uninstallFailureReason(err))
		return writeUninstallResult(stdout, stderr, preflight, exitTransaction)
	}
	// The uninstall backup stays under the target's .cortex directory as
	// rollback evidence; only a directory emptied by the transaction would be
	// removed, and Remove refuses non-empty directories.
	preflight[0].status = "completed"
	return writeUninstallResult(stdout, stderr, preflight, exitOK)
}

func runUninstall(stdout, stderr io.Writer, deps uninstallDependencies) int {
	roots, err := deps.resolveRoots()
	if err != nil || len(roots) != 3 || roots[0].RuntimeID() != runtimematrix.RuntimePi ||
		roots[1].RuntimeID() != runtimematrix.RuntimeOpenCode || roots[2].RuntimeID() != runtimematrix.RuntimeClaudeCode {
		writeError(stderr, "uninstall_root_resolution_failed")
		return exitFailure
	}
	preflight := make([]uninstallPreflight, 0, len(roots))
	for _, root := range roots {
		exists, err := deps.rootExists(root.RootPath())
		if err != nil {
			writeError(stderr, "uninstall_observation_failed")
			return exitFailure
		}
		if !exists {
			preflight = append(preflight, uninstallPreflight{root: root, status: "not_installed"})
			continue
		}
		trusted, err := installobserve.NewUninstallRoot(root.RuntimeID(), root.RootKind(), root.RootPath())
		if err != nil {
			writeError(stderr, "uninstall_observation_failed")
			return exitFailure
		}
		observation, err := deps.observe(trusted, installobserve.DefaultOptions())
		if err != nil {
			if len(observation.UnsupportedActorPaths()) > 0 && observation.MatchesRoot(root.RootPath()) && observation.MatchesRuntime(root.RuntimeID()) {
				preflight = append(preflight, uninstallPreflight{root: root, observation: observation, status: "blocked"})
				continue
			}
			writeError(stderr, "uninstall_observation_failed")
			return exitFailure
		}
		status := "ready"
		if len(observation.Records()) == 0 {
			status = "not_installed"
		} else if !observation.Ready() {
			status = "conflict"
		}
		preflight = append(preflight, uninstallPreflight{root: root, observation: observation, status: status})
	}
	for _, item := range preflight {
		if item.status == "conflict" || len(item.observation.UnsupportedActorPaths()) > 0 {
			for index := range preflight {
				if preflight[index].status == "ready" {
					preflight[index].status = "blocked"
				}
			}
			return writeUninstallResult(stdout, stderr, preflight, exitConflict)
		}
	}
	requests := make([]uninstalltxn.GroupRequest, 0, len(preflight))
	for _, item := range preflight {
		if item.status == "ready" {
			requests = append(requests, uninstalltxn.GroupRequest{
				RuntimeID: item.root.RuntimeID(), Root: item.root.RootPath(), Observation: item.observation,
			})
		}
	}
	if len(requests) == 0 {
		return writeUninstallResult(stdout, stderr, preflight, exitOK)
	}
	backupRoot := filepath.Join(requests[0].Root, ".cortex")
	if err := deps.applyGroup(requests, backupRoot, deps.backupName()); err != nil {
		for index := range preflight {
			if preflight[index].status == "ready" {
				preflight[index].status = "failed"
			}
		}
		writeError(stderr, uninstallFailureReason(err))
		return writeUninstallResult(stdout, stderr, preflight, exitTransaction)
	}
	for index := range preflight {
		if preflight[index].status == "ready" {
			preflight[index].status = "completed"
		}
	}
	return writeUninstallResult(stdout, stderr, preflight, exitOK)
}

func nextUninstallBackupName() string {
	return "uninstall-" + strconv.FormatInt(time.Now().UnixNano(), 36)
}

func uninstallRootExists(root string) (bool, error) {
	_, err := os.Lstat(root)
	if os.IsNotExist(err) {
		return false, nil
	}
	return err == nil, err
}

// uninstallFailureReason classifies a group transaction failure as a stable
// machine-readable code. It never surfaces the underlying error text, which
// may carry private filesystem detail.
func uninstallFailureReason(err error) string {
	switch {
	case errors.Is(err, uninstalltxn.ErrConflict):
		return "ownership_conflict"
	case errors.Is(err, uninstalltxn.ErrInvalid):
		return "unsupported_installation"
	default:
		return "transaction_failed"
	}
}

func writeUninstallResult(stdout, stderr io.Writer, results []uninstallPreflight, code int) int {
	var output strings.Builder
	unsupported := false
	for _, result := range results {
		remove, absent, conflict := uninstallCounts(result.observation)
		_, _ = fmt.Fprintf(&output, "runtime=%s uninstall=%s remove=%d absent=%d conflict=%d", result.root.RuntimeID(), result.status, remove, absent, conflict)
		if paths := result.observation.UnsupportedActorPaths(); len(paths) > 0 {
			unsupported = true
			_, _ = fmt.Fprintf(&output, " reason=unsupported_installation state=validated_canonical_v3 recorded_actor_paths=%s", strings.Join(paths, ","))
		}
		output.WriteByte('\n')
	}
	if unsupported {
		output.WriteString("note=uninstall unsupported for v3 actor state under shared ownership policy; recorded paths do not confirm external guidance; no files changed; manual resolution required\n")
	}
	if _, err := io.WriteString(stdout, output.String()); err != nil {
		writeError(stderr, "output_failed")
		return exitFailure
	}
	return code
}

func uninstallCounts(observation installobserve.UninstallObservation) (remove, absent, conflict int) {
	for _, record := range observation.Records() {
		switch record.Status {
		case installobserve.UninstallRemove:
			remove++
		case installobserve.UninstallAbsent:
			absent++
		case installobserve.UninstallConflict:
			conflict++
		}
	}
	return
}

func probe(ctx context.Context, runner runtimeprobe.Runner) ([]runtimeprobe.Report, error) {
	if runner == nil {
		return runtimeprobe.ProbeSystem(ctx)
	}
	return runtimeprobe.ProbeAll(ctx, runner)
}

func writeError(stderr io.Writer, code string) {
	_, _ = io.WriteString(stderr, "error="+code+"\n")
}
