package qapi

import (
	"bytes"
	"encoding/json"
	"io"
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
