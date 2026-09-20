package qapi

import (
	"bytes"
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/refactor-ia/cortex/internal/qaadmission"
)

const (
	maxModelOutputBytes = 1 << 20
	maxAuthOutputBytes  = 64 << 10
)

const modelTableHeader = "provider      model                context  max-out  thinking  images"

// ModelProbeInput is one complete, bounded model-table capture.
type ModelProbeInput struct {
	Stdout   []byte
	ExitCode int
	Complete bool
}

// AuthProbeInput is one complete, bounded no-refresh auth capture.
type AuthProbeInput struct {
	Stdout    []byte
	ExitCode  int
	Complete  bool
	NoRefresh bool
}

// ModelProbeResult is the closed availability result of a model-table parse.
type ModelProbeResult struct {
	Available bool
	Code      qaadmission.Code
}

// AuthProbeResult is the closed readiness result of an auth-status parse.
type AuthProbeResult struct {
	Ready bool
	Code  qaadmission.Code
}

// ProbeModel parses one complete Pi 0.85.1 model table without launching Pi.
func ProbeModel(input ModelProbeInput, provider, model string) ModelProbeResult {
	if input.ExitCode != 0 || !input.Complete || len(input.Stdout) == 0 || len(input.Stdout) > maxModelOutputBytes || !utf8.Valid(input.Stdout) {
		return ModelProbeResult{Code: qaadmission.CodeNormalizationFailed}
	}

	rows, valid := parseModelTable(string(input.Stdout))
	if !valid {
		return ModelProbeResult{Code: qaadmission.CodeNormalizationFailed}
	}
	if rows[provider+"\x00"+model] {
		return ModelProbeResult{Available: true}
	}
	return ModelProbeResult{Code: qaadmission.CodeModelUnavailable}
}

func parseModelTable(table string) (map[string]bool, bool) {
	if !strings.HasSuffix(table, "\n") {
		return nil, false
	}
	lines := strings.Split(strings.TrimSuffix(table, "\n"), "\n")
	if len(lines) == 0 || lines[0] != modelTableHeader {
		return nil, false
	}

	rows := make(map[string]bool, len(lines)-1)
	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) != 6 {
			return nil, false
		}
		key := fields[0] + "\x00" + fields[1]
		if rows[key] {
			return nil, false
		}
		rows[key] = true
	}
	return rows, true
}

// ProbeAuth parses one complete Pi 0.85.1 no-refresh auth-status document.
func ProbeAuth(input AuthProbeInput) AuthProbeResult {
	if !input.NoRefresh || !input.Complete || len(input.Stdout) == 0 || len(input.Stdout) > maxAuthOutputBytes || !utf8.Valid(input.Stdout) {
		return AuthProbeResult{Code: qaadmission.CodeNormalizationFailed}
	}

	observed, valid := decodeAuth(input.Stdout)
	if !valid {
		return AuthProbeResult{Code: qaadmission.CodeNormalizationFailed}
	}
	switch observed.status {
	case "ready":
		if input.ExitCode == 0 && observed.provider == "nan" && observed.authType == "api_key" && observed.reason == "" {
			return AuthProbeResult{Ready: true}
		}
	case "not_ready":
		if input.ExitCode == 1 && observed.reason != "" && (observed.provider == "" || observed.provider == "nan") && (observed.authType == "" || observed.authType == "api_key") {
			return AuthProbeResult{Code: qaadmission.CodeAuthNotReady}
		}
	}
	return AuthProbeResult{Code: qaadmission.CodeNormalizationFailed}
}

type authObservation struct {
	status   string
	provider string
	reason   string
	authType string
}

func decodeAuth(data []byte) (authObservation, bool) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return authObservation{}, false
	}

	observed := authObservation{}
	seen := make(map[string]bool, 4)
	for decoder.More() {
		token, err = decoder.Token()
		key, ok := token.(string)
		if err != nil || !ok || seen[key] {
			return authObservation{}, false
		}
		seen[key] = true
		token, err = decoder.Token()
		value, ok := token.(string)
		if err != nil || !ok {
			return authObservation{}, false
		}
		switch key {
		case "status":
			observed.status = value
		case "provider":
			observed.provider = value
		case "reason":
			observed.reason = value
		case "authType":
			observed.authType = value
		default:
			return authObservation{}, false
		}
	}
	if token, err = decoder.Token(); err != nil || token != json.Delim('}') {
		return authObservation{}, false
	}
	if token, err = decoder.Token(); err != io.EOF || token != nil {
		return authObservation{}, false
	}
	return observed, observed.status != ""
}

// BackendProbeInput is one complete, bounded capture of one out-of-band
// availability probe command. Unlike the two Pi-shaped inputs above it carries
// no probe-specific flag, because the commands it describes take none: the
// capture is the stdout, the exit code, and whether the capture was complete.
type BackendProbeInput struct {
	Stdout   []byte
	ExitCode int
	Complete bool
}

// ProbeClaudeAuth parses one complete `claude auth status --json` document.
//
// This is the whole of the Claude Code availability probe, and the absence of
// a model probe beside it is deliberate rather than an omission. `claude -p`
// runs Claude: the resolved route's provider and model describe what the role
// asked for, not what this backend will run, so asking Claude Code whether it
// can serve nan/qwen3.6 would be asking a question it has no honest answer to.
// The receipt records that relationship instead of the probe inventing one.
//
// A failing exit with loggedIn false is not a malformed capture: it is the
// backend answering. A failing exit with loggedIn true is a contradiction
// between the two signals, and a probe that cannot be read the same way twice
// fails closed rather than guessing which half to believe.
func ProbeClaudeAuth(input BackendProbeInput) AuthProbeResult {
	if !input.Complete || len(input.Stdout) == 0 || len(input.Stdout) > maxAuthOutputBytes || !utf8.Valid(input.Stdout) {
		return AuthProbeResult{Code: qaadmission.CodeNormalizationFailed}
	}
	var status struct {
		LoggedIn *bool `json:"loggedIn"`
	}
	if json.Unmarshal(input.Stdout, &status) != nil || status.LoggedIn == nil {
		return AuthProbeResult{Code: qaadmission.CodeNormalizationFailed}
	}
	if !*status.LoggedIn {
		return AuthProbeResult{Code: qaadmission.CodeAuthNotReady}
	}
	if input.ExitCode != 0 {
		return AuthProbeResult{Code: qaadmission.CodeNormalizationFailed}
	}
	return AuthProbeResult{Ready: true}
}

// ProbeOpenCodeModel parses one complete `opencode models` listing, which
// prints one `provider/model` identifier per line and nothing else.
//
// It answers the same question Pi's model probe does, and observation shows it
// answers it honestly: on an install with no credentials the listing drops the
// route's provider entirely and keeps only the credential-free models, so a
// resolved route that is not reachable is absent rather than merely unusable.
func ProbeOpenCodeModel(input BackendProbeInput, provider, model string) ModelProbeResult {
	if input.ExitCode != 0 || !input.Complete || len(input.Stdout) == 0 || len(input.Stdout) > maxModelOutputBytes || !utf8.Valid(input.Stdout) {
		return ModelProbeResult{Code: qaadmission.CodeNormalizationFailed}
	}
	rows, valid := parseOpenCodeModelList(string(input.Stdout))
	if !valid {
		return ModelProbeResult{Code: qaadmission.CodeNormalizationFailed}
	}
	if rows[provider+"/"+model] {
		return ModelProbeResult{Available: true}
	}
	return ModelProbeResult{Code: qaadmission.CodeModelUnavailable}
}

func parseOpenCodeModelList(listing string) (map[string]bool, bool) {
	if !strings.HasSuffix(listing, "\n") {
		return nil, false
	}
	lines := strings.Split(strings.TrimSuffix(listing, "\n"), "\n")
	rows := make(map[string]bool, len(lines))
	for _, line := range lines {
		provider, model, qualified := strings.Cut(line, "/")
		if !qualified || provider == "" || model == "" || strings.ContainsAny(line, " \t") || strings.Contains(model, "/") {
			return nil, false
		}
		if rows[line] {
			return nil, false
		}
		rows[line] = true
	}
	return rows, true
}

// ProbeOpenCodeAuth parses one complete `opencode auth list` capture.
//
// The command has no machine format: it draws a box, colours it with ANSI
// escapes even under NO_COLOR, and ends with a credential count. The count is
// the only part this probe reads, and it is the only part that answers the
// question — zero credentials is an install that cannot reach any provider.
//
// What this probe does not claim is as important as what it does. The listing
// names credentials by display label ("OpenAI"), not by the provider token a
// route resolves to, so it cannot confirm that the route's own provider is
// authenticated. That narrower question is the one ProbeOpenCodeModel answers,
// and the two run together for exactly that reason.
func ProbeOpenCodeAuth(input BackendProbeInput) AuthProbeResult {
	if input.ExitCode != 0 || !input.Complete || len(input.Stdout) == 0 || len(input.Stdout) > maxAuthOutputBytes || !utf8.Valid(input.Stdout) {
		return AuthProbeResult{Code: qaadmission.CodeNormalizationFailed}
	}
	count, found := 0, false
	for _, line := range strings.Split(string(input.Stdout), "\n") {
		value, credentials := strings.CutSuffix(strings.TrimSpace(stripANSI(line)), " credentials")
		value = strings.TrimLeft(value, "┌│└● \t")
		if !credentials || !decimalCount(value) {
			continue
		}
		if found {
			return AuthProbeResult{Code: qaadmission.CodeNormalizationFailed}
		}
		parsed, err := strconv.Atoi(value)
		if err != nil {
			return AuthProbeResult{Code: qaadmission.CodeNormalizationFailed}
		}
		count, found = parsed, true
	}
	if !found {
		return AuthProbeResult{Code: qaadmission.CodeNormalizationFailed}
	}
	if count == 0 {
		return AuthProbeResult{Code: qaadmission.CodeAuthNotReady}
	}
	return AuthProbeResult{Ready: true}
}

// decimalCount reports whether a value is a bare non-negative decimal count.
func decimalCount(value string) bool {
	if value == "" || len(value) > 9 {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

// stripANSI removes CSI escape sequences from one captured line. The probes
// run with NO_COLOR and TERM=dumb already; OpenCode colours its output anyway,
// so the sequences are removed here rather than assumed absent.
func stripANSI(line string) string {
	var plain strings.Builder
	for index := 0; index < len(line); index++ {
		if line[index] != 0x1b {
			plain.WriteByte(line[index])
			continue
		}
		index++
		if index < len(line) && line[index] == '[' {
			for index++; index < len(line) && (line[index] < '@' || line[index] > '~'); index++ {
			}
		}
	}
	return plain.String()
}
